// Package segsocial obtiene subastas de la Seguridad Social (w6.seg-social.es):
// subastas regulares de bienes embargados y adjudicación directa de inmuebles.
package segsocial

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"strings"

	"github.com/PuerkitoBio/goquery"

	"sboe/internal/httpx"
	"sboe/internal/model"
	"sboe/internal/normalize"
	"sboe/internal/sources"
)

const (
	baseURL    = "https://w6.seg-social.es"
	sessionURL = baseURL + "/subastas/SubaSeControladorInter?opcion=6&avanzada=1"
	maxPages   = 500
)

// Row es una fila de las tablas .tablas-resultados.
type Row struct {
	Caption string            // título de la tabla ("Finca Urbana - A CORUÑA - (16/11/2026)")
	Cells   map[string]string // cabecera normalizada → texto
	Link    string            // href del primer enlace de la fila
}

// ParseTables extrae todas las filas de las tablas de resultados.
func ParseTables(html string) ([]Row, error) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return nil, err
	}
	var rows []Row
	doc.Find(".tablas-resultados table").Each(func(_ int, table *goquery.Selection) {
		caption := normalize.Clean(table.Find("caption").Text())
		var headers []string
		table.Find("thead th").Each(func(_ int, th *goquery.Selection) {
			headers = append(headers, normalize.Text(th.Text()))
		})
		table.Find("tr").Each(func(_ int, tr *goquery.Selection) {
			tds := tr.Find("td")
			if tds.Length() == 0 {
				return
			}
			r := Row{Caption: caption, Cells: map[string]string{}}
			tds.Each(func(i int, td *goquery.Selection) {
				if i < len(headers) {
					r.Cells[headers[i]] = normalize.Clean(td.Text())
				}
				if href, ok := td.Find("a").Attr("href"); ok && r.Link == "" {
					r.Link = href
				}
			})
			rows = append(rows, r)
		})
	})
	return rows, nil
}

// Group: los dos jobs de la Seguridad Social usan el mismo servidor y no corren a la vez.
const Group = "seg-social"

// paginate abre sesión, hace la búsqueda inicial (POST) y recorre las páginas siguientes
// hasta que una venga vacía, entregando cada página a onPage.
func paginate(ctx context.Context, c *httpx.Client, name string, first httpx.Request, next func(page int) httpx.Request, onPage func([]Row) error) error {
	if _, err := c.Do(ctx, httpx.Request{URL: sessionURL}); err != nil {
		return fmt.Errorf("abriendo sesión: %w", err)
	}
	for page := 1; page <= maxPages; page++ {
		if err := c.Pace(ctx); err != nil {
			return err
		}
		req := first
		if page > 1 {
			req = next(page)
		}
		html, err := c.Do(ctx, req)
		if err != nil {
			return fmt.Errorf("página %d: %w", page, err)
		}
		rows, err := ParseTables(html)
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			return nil
		}
		slog.Debug(name+" listado", "pagina", page, "filas", len(rows))
		if err := onPage(rows); err != nil {
			return err
		}
	}
	return nil
}

// saver convierte filas en subastas, descarta duplicados y las guarda.
func saver(ctx context.Context, sink sources.Sink, build func(Row) model.Auction) func([]Row) error {
	seen := map[string]bool{}
	return func(rows []Row) error {
		var batch []model.Auction
		for _, r := range rows {
			a := build(r)
			if a.ExternalID == "" || seen[a.ExternalID] {
				continue
			}
			seen[a.ExternalID] = true
			a.Detailed = true
			batch = append(batch, a)
		}
		return sink.Save(ctx, batch...)
	}
}

func linkParam(link, key string) (string, string) {
	u, err := url.Parse(link)
	if err != nil {
		return "", ""
	}
	abs := u
	if !u.IsAbs() {
		base, _ := url.Parse(baseURL)
		abs = base.ResolveReference(u)
	}
	return u.Query().Get(key), abs.String()
}

func splitCaption(caption string) []string {
	parts := strings.Split(caption, " - ")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}
