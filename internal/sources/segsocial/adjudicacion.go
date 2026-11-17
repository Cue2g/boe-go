package segsocial

import (
	"context"
	"net/url"
	"regexp"
	"strconv"

	"sboe/internal/httpx"
	"sboe/internal/model"
	"sboe/internal/normalize"
	"sboe/internal/sources"
)

const adjudicacionURL = baseURL + "/vinmWeb/VinmSeControladorInter"

// Filtros de la búsqueda (copiados de la versión TS).
var (
	adjProvincias = []string{
		"4", "11", "14", "18", "21", "23", "29", "41", "22", "44", "50", "2", "13", "16", "19", "45", "5", "9", "24",
		"34", "37", "40", "42", "47", "49", "8", "17", "25", "43", "6", "10", "15", "27", "32", "36", "1", "20", "48",
		"3", "46", "12",
	}
	adjAutonomas = []string{"33", "39", "7", "26", "28", "31", "30", "51", "52"}
)

// Adjudicacion: venta por adjudicación directa de inmuebles de la Seguridad Social.
type Adjudicacion struct{ c *httpx.Client }

func NewAdjudicacion(c *httpx.Client) *Adjudicacion { return &Adjudicacion{c: c} }

func (s *Adjudicacion) Name() string { return "ss_adjudicacion" }

func (s *Adjudicacion) Fetch(ctx context.Context, sink sources.Sink) error {
	form := url.Values{"opcion": {"43"}, "pulsado": {"S"}, "T": {"4"}}
	for i := 1; i <= 5; i++ {
		form.Set("inmueble"+strconv.Itoa(i), strconv.Itoa(i))
	}
	for i := 1; i <= 3; i++ {
		form.Set("subasta"+strconv.Itoa(i), strconv.Itoa(i))
	}
	form["provincia[]"] = adjProvincias
	form["autonoma[]"] = adjAutonomas

	return paginate(ctx, s.c, s.Name(),
		httpx.Request{
			URL: adjudicacionURL + "?opcion=53", Form: form,
			Headers: map[string]string{"Referer": adjudicacionURL + "?opcion=33&T=4"},
		},
		func(page int) httpx.Request {
			return httpx.Request{URL: adjudicacionURL, Query: url.Values{"opcion": {"44"}, "pagina": {strconv.Itoa(page)}}}
		},
		saver(ctx, sink, BuildAdjudicacion))
}

var reLocProv = regexp.MustCompile(`^(.*?)\s*\(([^)]+)\)\s*$`)

// BuildAdjudicacion convierte una fila en Auction.
// Caption: "<tipo de inmueble> - (<fecha de consulta>)"; la fecha no es de la subasta y se ignora.
func BuildAdjudicacion(r Row) model.Auction {
	id, link := linkParam(r.Link, "CN_INMNCODIGO")
	parts := splitCaption(r.Caption)
	a := model.Auction{
		Source:       "ss_adjudicacion",
		ExternalID:   id,
		Status:       "Adjudicación directa",
		Description:  r.Cells["descripcion"],
		MinBidCents:  normalize.Euros(r.Cells["precio"]),
		URL:          link,
		AssetType:    model.AssetInmueble,
		AssetTypeRaw: parts[0],
		Extra:        map[string]string{},
	}
	if t := normalize.AssetType(parts[0]); t != model.AssetMueble {
		a.AssetType = t
	}
	loc := r.Cells["ubicacion"]
	if m := reLocProv.FindStringSubmatch(loc); m != nil {
		a.Locality, a.Province = m[1], m[2]
	} else {
		a.Locality = loc
	}
	if v := r.Cells["objeto de la subasta"]; v != "" {
		a.Extra["objeto"] = v
	}
	return a
}

func (s *Adjudicacion) Group() string { return Group }
