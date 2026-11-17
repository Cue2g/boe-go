// Package boe obtiene subastas de subastas.boe.es.
package boe

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"strconv"
	"strings"

	"github.com/PuerkitoBio/goquery"

	"sboe/internal/httpx"
	"sboe/internal/model"
	"sboe/internal/normalize"
	"sboe/internal/sources"
)

const (
	baseURL   = "https://subastas.boe.es/"
	listURL   = baseURL + "subastas_ava.php"
	detailURL = baseURL + "detalleSubasta.php"
	pageSize  = 500
	maxPages  = 10
)

// Búsquedas guardadas del portal (copiadas de la versión TS).
// Si dejan de funcionar hay que regenerarlas desde la búsqueda avanzada del BOE.
var searches = []struct{ name, id string }{
	{"Próxima apertura", "_V2RjQVJkQzdWMU91WnFsTzk1aW9mN0w1VldDMkIyeHE5dG8yVG9YVHpWazVsL2RlamhCYjJtNzJ0SFRjV3lnbG45aXBKd1JvcG8wNForMjFzQzIvS1ZQS3BLOW1OdjhwZWRLMi82YkRjaVQvL2lHdkJrSWR2Z2U4eEVnVXgxcytsdHE3a1Q2ZG1JZmFRNzQrWFIra2kvcStIbHhnVEZXcnczNXM5b1RXSVpvVWdKL2k1dXZmektNTUNXUHlXZE91RndmMkhtTm5VV0s0TmVZeC9GNEpqOStaZTExbStrWkZ1aTlUMFdYY25PUVE2SEZMa3Bha2w2WFVzTG4vS2pvYUFtMXFqM2o4YmFNbmpzeUhhb2dZOW4xalBPeUEwbHU4dmNqSXVvTGlZVGc9-"},
	{"Celebrándose", "_V2RjQVJkQzdWMU91WnFsTzk1aW9meWpPTFVpQmhSWE1wVTk0RkVyeFRqTXdWU3h3VzJFdTNYRGMyWHFQYkpGRTBHMURZSlAvUWVwaTZ6ckd2VEI5Ri9tTzRFeWd0UWRUOW5CMjkwUUR6YXVIZkp1ckZWNGMzcTQyOTIwNnlaWFlyNG1IeUpzRlljSlljbnhZM205NDQrWWMyK09lSHk5ZXk0S25LQkZtMjJnMzRxY1F3RlJtSnp1LzlBanQ5aUpsYUZlL2lEalFoU1BZNUtPeExjRUtiQmtQYU56enhwRVBxaThMY3BtRW1BUmNpVTgvQXMxLzJKVEJtN0xGSWRvSjFRZHBNSWdWSDlyMXlZVkl4alRrU1lQK1Qxb014ZVpzMUEyOWplUyszWm89-"},
}

// Source es la fuente BOE.
type Source struct{ c *httpx.Client }

func New(c *httpx.Client) *Source { return &Source{c: c} }

func (s *Source) Name() string { return "boe" }

type ListItem struct {
	ID, Status string
}

func (s *Source) Fetch(ctx context.Context, sink sources.Sink) error {
	seen := map[string]bool{}
	var stats struct{ detail, touched, failed int }
	for _, search := range searches {
		for page := 0; page < maxPages; page++ {
			html, err := s.c.Do(ctx, httpx.Request{URL: listURL, Query: url.Values{
				"accion":      {"Mas"},
				"id_busqueda": {fmt.Sprintf("%s%d-%d", search.id, page*pageSize, pageSize)},
			}})
			if err != nil {
				return fmt.Errorf("listado %q página %d: %w", search.name, page, err)
			}
			items, err := ParseList(html)
			if err != nil {
				return err
			}
			slog.Info("boe listado", "busqueda", search.name, "pagina", page, "items", len(items))

			var unchanged []string
			for _, it := range items {
				if seen[it.ID] {
					continue
				}
				seen[it.ID] = true
				if !sources.NeedsDetail(sink, it.ID, it.Status) {
					unchanged = append(unchanged, it.ID)
					continue
				}
				a, err := s.detail(ctx, it)
				if err != nil {
					if ctx.Err() != nil {
						return ctx.Err()
					}
					slog.Warn("boe detalle", "id", it.ID, "err", err)
					stats.failed++
					// la seguimos viendo en el listado: que no se marque como retirada
					unchanged = append(unchanged, it.ID)
					continue
				}
				if err := sink.Save(ctx, a); err != nil {
					return err
				}
				stats.detail++
				if stats.detail%100 == 0 {
					slog.Info("boe detalles", "descargados", stats.detail)
				}
				if err := s.c.Pace(ctx); err != nil {
					return err
				}
			}
			if err := sink.Touch(ctx, unchanged...); err != nil {
				return err
			}
			stats.touched += len(unchanged)

			if len(items) < pageSize {
				break
			}
			if err := s.c.Pace(ctx); err != nil {
				return err
			}
		}
	}
	slog.Info("boe resumen", "con_detalle", stats.detail, "sin_cambios", stats.touched, "fallidas", stats.failed)
	return nil
}

// ParseList extrae id y estado de una página de resultados.
func ParseList(html string) ([]ListItem, error) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return nil, err
	}
	var items []ListItem
	doc.Find(".listadoResult .resultado-busqueda").Each(func(_ int, el *goquery.Selection) {
		href, _ := el.Find("a.resultado-busqueda-link-defecto").Attr("href")
		u, err := url.Parse(href)
		if err != nil {
			return
		}
		id := u.Query().Get("idSub")
		if id == "" {
			return
		}
		status := ""
		el.Find("p").EachWithBreak(func(_ int, p *goquery.Selection) bool {
			t := strings.TrimSpace(p.Text())
			if strings.HasPrefix(t, "Estado:") {
				status = strings.TrimSpace(strings.SplitN(strings.TrimPrefix(t, "Estado:"), " - ", 2)[0])
				return false
			}
			return true
		})
		items = append(items, ListItem{ID: id, Status: status})
	})
	return items, nil
}

func (s *Source) detail(ctx context.Context, it ListItem) (model.Auction, error) {
	fields := map[string]string{}
	var assetRaw string
	for _, view := range []int{1, 3} { // 2 = autoridad gestora, 4/5 vienen vacías sin sesión
		html, err := s.c.Do(ctx, httpx.Request{URL: detailURL, Query: url.Values{
			"idSub": {it.ID}, "ver": {strconv.Itoa(view)},
		}})
		if err != nil {
			return model.Auction{}, err
		}
		raw, err := ParseDetail(html, view, fields)
		if err != nil {
			return model.Auction{}, err
		}
		if raw != "" {
			assetRaw = raw
		}
	}
	a := Build(it.ID, it.Status, assetRaw, fields)
	a.Detailed = true
	return a, nil
}

// ParseDetail vuelca las filas th/td del bloque `ver` en fields (gana el primer valor:
// en subastas por lotes nos quedamos con el primer bien). Devuelve el h4 del bien (ver=3).
func ParseDetail(html string, view int, fields map[string]string) (string, error) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return "", err
	}
	block := doc.Find(fmt.Sprintf("#idBloqueDatos%d", view))
	block.Find("table tr").Each(func(_ int, tr *goquery.Selection) {
		k := normalize.Text(tr.Find("th").Text())
		v := normalize.Clean(tr.Find("td").Text())
		if k == "" || v == "" {
			return
		}
		if k == "valor de tasacion" { // así aparece dentro de cada lote
			k = "tasacion"
		}
		// Gana el primer valor, salvo que fuera un texto sin cifras ("Ver valor de subasta en
		// cada lote", "No consta"): en subastas por lotes el importe real está en el lote 1.
		if old, ok := fields[k]; !ok || (!hasDigit(old) && hasDigit(v)) {
			fields[k] = v
		}
	})
	if view == 3 {
		return normalize.Clean(block.Find("h4").First().Text()), nil
	}
	return "", nil
}

// Build convierte los campos crudos en una Auction.
func Build(id, status, assetRaw string, f map[string]string) model.Auction {
	a := model.Auction{
		Source:         "boe",
		ExternalID:     id,
		AssetTypeRaw:   assetRaw,
		AssetType:      normalize.AssetType(assetRaw),
		Status:         status,
		Locality:       f["localidad"],
		Province:       f["provincia"],
		StartDate:      normalize.Date(f["fecha de inicio"]),
		EndDate:        normalize.Date(f["fecha de conclusion"]),
		ValuationCents: normalize.Euros(f["valor subasta"]),
		AppraisalCents: normalize.Euros(f["tasacion"]),
		MinBidCents:    normalize.Euros(f["puja minima"]),
		DepositCents:   normalize.Euros(f["importe del deposito"]),
		ClaimedCents:   normalize.Euros(f["cantidad reclamada"]),
		ChargesCents:   normalize.Euros(f["cargas"]),
		CadastralRef:   f["referencia catastral"],
		Description:    f["descripcion"],
		URL:            detailURL + "?idSub=" + url.QueryEscape(id),
		Extra:          map[string]string{},
	}
	if v, ok := f["vivienda habitual"]; ok {
		b := strings.HasPrefix(normalize.Text(v), "si")
		a.MainResidence = &b
	}
	for _, k := range []string{"tipo de subasta", "anuncio boe", "lotes", "situacion posesoria", "direccion", "codigo postal", "idufir", "visitable", "cuenta expediente"} {
		if v := f[k]; v != "" {
			a.Extra[k] = v
		}
	}
	return a
}

func hasDigit(s string) bool {
	return strings.ContainsAny(s, "0123456789")
}
