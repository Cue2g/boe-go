package segsocial

import (
	"context"
	"net/url"
	"strconv"
	"strings"

	"sboe/internal/httpx"
	"sboe/internal/model"
	"sboe/internal/normalize"
	"sboe/internal/sources"
)

const regularesURL = baseURL + "/subastas/SubaSeControladorInter"

// Filtros de la búsqueda avanzada (copiados de la versión TS).
var (
	regularesTipos      = []string{"0101", "0102", "0211", "0212", "0215"}
	regularesProvincias = []string{
		"04", "11", "14", "18", "21", "23", "29", "41", "22", "44", "50", "33", "35", "38", "39", "02", "13", "16",
		"19", "45", "05", "09", "24", "34", "37", "40", "42", "47", "49", "08", "17", "25", "43", "51", "28", "03",
		"12", "46", "06", "10", "15", "27", "32", "36", "07", "26", "52", "31", "01", "20", "48", "30",
	}
)

// Regulares: subastas de bienes embargados.
type Regulares struct{ c *httpx.Client }

func NewRegulares(c *httpx.Client) *Regulares { return &Regulares{c: c} }

func (s *Regulares) Name() string { return "ss_regulares" }

func (s *Regulares) Fetch(ctx context.Context, sink sources.Sink) error {
	form := url.Values{"opcion": {"7"}}
	for _, k := range []string{"TASACION", "CARGAS", "FSUBASTA", "LICITACION"} {
		form.Set("CAMPO_"+k+"_DESDE", "")
		form.Set("CAMPO_"+k+"_HASTA", "")
	}
	form["EMB_TIPOBIEN"] = regularesTipos
	form["provincia"] = regularesProvincias

	headers := map[string]string{"Referer": sessionURL}
	return paginate(ctx, s.c, s.Name(),
		httpx.Request{URL: regularesURL, Form: form, Headers: headers},
		func(page int) httpx.Request {
			return httpx.Request{URL: regularesURL, Query: url.Values{"pagina": {strconv.Itoa(page)}, "opcion": {"7"}}, Headers: headers}
		},
		saver(ctx, sink, BuildRegular))
}

// BuildRegular convierte una fila en Auction.
// Caption: "<tipo de bien> - <PROVINCIA> - (<fecha de consulta>)".
func BuildRegular(r Row) model.Auction {
	id, link := linkParam(r.Link, "EMB_ID")
	parts := splitCaption(r.Caption)
	a := model.Auction{
		Source:         "ss_regulares",
		ExternalID:     id,
		Description:    r.Cells["razon"],
		AppraisalCents: normalize.Euros(r.Cells["importe tasacion"]),
		ChargesCents:   normalize.Euros(r.Cells["importe cargas"]),
		ValuationCents: normalize.Euros(r.Cells["tipo enajenac."]), // tipo de salida
		StartDate:      normalize.Date(r.Cells["fecha subasta"]),
		URL:            link,
		Extra:          map[string]string{},
	}
	if len(parts) > 0 {
		a.AssetTypeRaw = parts[0]
		a.AssetType = normalize.AssetType(parts[0])
	}
	if len(parts) > 1 {
		a.Province = parts[1]
	}
	if v := strings.TrimSpace(strings.TrimPrefix(r.Cells["lote no"], "Lote:")); v != "" {
		a.Extra["lote"] = v
	}
	if v := r.Cells["tipo"]; v != "" && v != "-" {
		a.Extra["tipo"] = v
	}
	if v := r.Cells["fecha subasta"]; v != "" {
		a.Extra["fecha subasta"] = v
	}
	return a
}

func (s *Regulares) Group() string { return Group }
