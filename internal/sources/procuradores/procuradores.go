// Package procuradores obtiene subastas de subastasprocuradores.com.
package procuradores

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"

	"sboe/internal/httpx"
	"sboe/internal/model"
	"sboe/internal/normalize"
	"sboe/internal/sources"
)

const (
	baseURL  = "https://www.subastasprocuradores.com"
	listURL  = baseURL + "/subastas"
	maxPages = 1000
)

type Source struct{ c *httpx.Client }

func New(c *httpx.Client) *Source { return &Source{c: c} }

func (s *Source) Name() string { return "procuradores" }

func (s *Source) Fetch(ctx context.Context, sink sources.Sink) error {
	seen := map[string]bool{}
	var detailed, listOnly int
	for page := 1; page <= maxPages; page++ {
		html, err := s.c.Do(ctx, httpx.Request{URL: listURL, Query: url.Values{
			"SaleOrigin": {"Spain"}, "OrderBy": {"PublishDateDesc"}, "Page": {strconv.Itoa(page)},
		}})
		if err != nil {
			return fmt.Errorf("listado página %d: %w", page, err)
		}
		items, err := ParseList(html)
		if err != nil {
			return err
		}
		if len(items) == 0 {
			break
		}
		for _, a := range items {
			if seen[a.ExternalID] {
				continue
			}
			seen[a.ExternalID] = true
			if !sources.NeedsDetail(sink, a.ExternalID, a.Status) {
				// el listado trae casi todo; se actualiza sin pedir el detalle
				if err := sink.Save(ctx, a); err != nil {
					return err
				}
				listOnly++
				continue
			}
			if html, err := s.c.Do(ctx, httpx.Request{URL: a.URL}); err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				slog.Warn("procuradores detalle", "id", a.ExternalID, "err", err)
			} else if err := ParseDetail(html, &a); err != nil {
				slog.Warn("procuradores detalle", "id", a.ExternalID, "err", err)
			} else {
				a.Detailed = true
			}
			if err := sink.Save(ctx, a); err != nil {
				return err
			}
			detailed++
			if err := s.c.Pace(ctx); err != nil {
				return err
			}
		}
		if page%10 == 0 {
			slog.Info("procuradores listado", "pagina", page, "vistas", len(seen))
		}
		if err := s.c.Pace(ctx); err != nil {
			return err
		}
	}
	slog.Info("procuradores resumen", "con_detalle", detailed, "solo_listado", listOnly)
	return nil
}

var reProvince = regexp.MustCompile(`^(.*?)\s*\(([^)]+)\)\s*$`)

// splitLocation separa "Localidad (Provincia)". Si lo del paréntesis no es una provincia
// ("Campello (el)"), se deja todo como localidad.
func splitLocation(s string) (locality, province string) {
	s = normalize.Clean(s)
	if m := reProvince.FindStringSubmatch(s); m != nil {
		if _, ok := normalize.LookupProvince(m[2]); ok {
			return m[1], m[2]
		}
	}
	return s, ""
}

// ParseList obtiene los datos disponibles en el listado.
func ParseList(html string) ([]model.Auction, error) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return nil, err
	}
	var out []model.Auction
	doc.Find(".sp_sales__list .sp_sales__item").Each(func(_ int, it *goquery.Selection) {
		id, _ := it.Attr("data-saleid")
		if id == "" {
			return
		}
		href, _ := it.Find("a.sp_sales__link").First().Attr("href")
		u, err := url.Parse(href)
		if err != nil || href == "" {
			u, _ = url.Parse("/subastas/" + id)
		}
		u.RawQuery = ""
		rawType := normalize.Clean(it.Find(".sp_sales__asset_type").Text())
		loc, prov := splitLocation(it.Find(".sp_sales__location").Text())
		a := model.Auction{
			Source:         "procuradores",
			ExternalID:     id,
			AssetTypeRaw:   rawType,
			AssetType:      normalize.AssetType(rawType),
			Locality:       loc,
			Province:       prov,
			Description:    normalize.Clean(it.Find(".sp_sales__title").Text()),
			StartDate:      normalize.Date(it.Find(".sp_sales__date").Text()),
			ValuationCents: normalize.Euros(strings.TrimPrefix(normalize.Clean(it.Find(".sp_sales__appraisal").Text()), "Valoración:")),
			MinBidCents:    normalize.Euros(it.Find(".sp_sales__bid").Text()),
			Status:         normalize.Clean(it.Find(".sp_sales__type").Text()),
			URL:            baseURL + u.Path,
			Extra:          map[string]string{},
		}
		if end, ok := it.Attr("data-bidenddate"); ok && len(end) >= 10 {
			if t, err := time.Parse("2006-01-02", end[:10]); err == nil {
				a.EndDate = &t
			}
		}
		if ref := strings.TrimSpace(strings.TrimPrefix(normalize.Clean(it.Find(".sp_sales__reference").Text()), "Ref.")); ref != "" {
			a.Extra["referencia"] = ref
		}
		out = append(out, a)
	})
	return out, nil
}

// ParseDetail completa la subasta con el depósito, la dirección y las fechas exactas.
func ParseDetail(html string, a *model.Auction) error {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return err
	}
	deposit := doc.Find(".sp_sale__deposit_featured")
	if v := normalize.Euros(deposit.Find("strong").First().Text()); v != nil {
		a.DepositCents = v
	} else if strings.Contains(normalize.Text(deposit.Text()), "no requiere deposito") {
		zero := int64(0)
		a.DepositCents = &zero
	}
	if v := normalize.Euros(doc.Find(".sp_sale__box_minimum_bid .sp_sale__info_value").Text()); v != nil {
		a.MinBidCents = v
	}
	if v := normalize.Euros(doc.Find(".sp_sale__box_appraisal .sp_sale__info_value").Text()); v != nil {
		a.ValuationCents = v
	}
	if addr := normalize.Clean(doc.Find(".sp_sale__location").Text()); addr != "" {
		a.Extra["direccion"] = addr
	}
	doc.Find(".sp_sale__extra_data span").Each(func(_ int, sp *goquery.Selection) {
		alt, _ := sp.Find("img").Attr("alt")
		var txt string
		sp.Find("strong").EachWithBreak(func(_ int, st *goquery.Selection) bool {
			t := normalize.Clean(st.Text())
			if t != "" && !strings.Contains(t, "{{") {
				txt = t
				return false
			}
			return true
		})
		switch alt {
		case "Inicio":
			if d := normalize.Date(txt); d != nil {
				a.StartDate = d
			}
		case "Fin":
			if d := normalize.Date(txt); d != nil {
				a.EndDate = d
			}
		}
	})
	return nil
}
