package normalize

import (
	"regexp"
	"strings"
	"time"

	"sboe/internal/model"
)

// Estados unificados (status_code).
const (
	StatusProxima      = "proxima"              // aún no ha empezado
	StatusEnCurso      = "en_curso"             // se puede pujar
	StatusAdjudicacion = "adjudicacion_directa" // venta directa (Seguridad Social)
	StatusCesion       = "cesion_remate"        // cesión de remate (Procuradores)
	StatusOtro         = "otro"
)

// Subtipos de bien unificados (asset_subtype).
const (
	SubVivienda      = "vivienda"
	SubGaraje        = "garaje"
	SubTrastero      = "trastero"
	SubLocal         = "local"
	SubNave          = "nave"
	SubSolar         = "solar"
	SubFincaRustica  = "finca_rustica"
	SubEdificio      = "edificio"
	SubInmuebleOtros = "inmueble_otros"
	SubVehiculo      = "vehiculo"
	SubEmbarcacion   = "embarcacion"
	SubMaquinaria    = "maquinaria"
	SubMuebleOtros   = "mueble_otros"
)

// Enrich rellena los campos unificados (provincia canónica + código INE, estado y subtipo)
// a partir de los datos crudos de la fuente. Es idempotente.
func Enrich(a *model.Auction, now time.Time) {
	enrichProvince(a)
	a.StatusCode = StatusCode(a.Source, a.Status, a.StartDate, a.EndDate, now)
	if a.AssetSubtype == "" {
		a.AssetSubtype = AssetSubtype(a.AssetType, a.AssetTypeRaw, a.Description)
	}
	// el subtipo manda sobre el tipo (p. ej. "Embarcación" en una fuente que la llama inmueble)
	switch a.AssetSubtype {
	case SubVehiculo:
		a.AssetType = model.AssetVehiculo
	case SubEmbarcacion, SubMaquinaria, SubMuebleOtros:
		a.AssetType = model.AssetMueble
	default:
		a.AssetType = model.AssetInmueble
	}
}

func enrichProvince(a *model.Auction) {
	raw := a.ProvinceRaw
	if raw == "" {
		raw = a.Province
		a.ProvinceRaw = raw
	}
	if p, ok := LookupProvince(raw); ok {
		a.ProvinceCode, a.Province = p.Code, p.Name
		return
	}
	// Sin provincia reconocible: muchas veces la localidad es la capital ("Santa Cruz de Tenerife").
	if p, ok := LookupProvince(a.Locality); ok {
		a.ProvinceCode, a.Province = p.Code, p.Name
		return
	}
	a.ProvinceCode, a.Province = "", ""
}

// StatusCode unifica el estado de cada fuente.
func StatusCode(source, status string, start, end *time.Time, now time.Time) string {
	s := Text(status)
	switch {
	case strings.Contains(s, "adjudicacion"):
		return StatusAdjudicacion
	case strings.Contains(s, "cesion"):
		return StatusCesion
	case strings.Contains(s, "celebrandose"), strings.Contains(s, "abierta"), strings.Contains(s, "en curso"):
		return StatusEnCurso
	case strings.Contains(s, "proxima"):
		return StatusProxima
	case s == "" || s == "subasta":
		// sin estado explícito: se deduce de las fechas
		today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
		if start != nil && start.After(today) {
			return StatusProxima
		}
		if end != nil && end.Before(today) {
			return StatusOtro
		}
		if start != nil || end != nil {
			return StatusEnCurso
		}
	}
	return StatusOtro
}

var reBoeParen = regexp.MustCompile(`\(([^)]+)\)\s*$`)

// palabras clave → subtipo. Se buscan en el tipo de la fuente y, si es genérico, en la descripción.
var subtypeKW = []struct {
	sub string
	kws []string
}{
	{SubVivienda, []string{"vivienda", "piso", "casa", "chalet", "apartamento", "duplex", "atico", "unifamiliar", "adosado", "pareado", "bungalow"}},
	{SubGaraje, []string{"garaje", "aparcamiento", "parking", "cochera"}},
	{SubTrastero, []string{"trastero"}},
	{SubLocal, []string{"local", "oficina", "despacho"}},
	{SubNave, []string{"nave", "almacen", "industrial"}},
	{SubSolar, []string{"solar", "parcela", "terreno urbano", "suelo urbano"}},
	{SubFincaRustica, []string{"finca rustica", "rustica", "rustico", "terreno", "olivar", "vinedo", "huerta", "monte"}},
	{SubEdificio, []string{"edificio"}},
	{SubEmbarcacion, []string{"embarcacion", "barco", "velero", "yate"}},
	{SubVehiculo, []string{"vehiculo", "turismo", "motocicleta", "camion", "furgoneta", "remolque", "tractor", "ciclomotor", "industriales"}},
	{SubMaquinaria, []string{"maquinaria", "herramienta", "instalaciones", "unidad productiva"}},
}

var subtypeRe = func() []*regexp.Regexp {
	out := make([]*regexp.Regexp, len(subtypeKW))
	for i, g := range subtypeKW {
		alts := make([]string, len(g.kws))
		for j, k := range g.kws {
			alts[j] = regexp.QuoteMeta(k)
		}
		// palabra completa, admitiendo plural
		out[i] = regexp.MustCompile(`\b(?:` + strings.Join(alts, "|") + `)(?:es|s)?\b`)
	}
	return out
}()

// matchSubtype devuelve el subtipo cuya palabra clave aparece antes en el texto.
func matchSubtype(text string, allowed func(string) bool) string {
	best, bestPos := "", -1
	for i, g := range subtypeKW {
		if allowed != nil && !allowed(g.sub) {
			continue
		}
		if loc := subtypeRe[i].FindStringIndex(text); loc != nil && (bestPos < 0 || loc[0] < bestPos) {
			best, bestPos = g.sub, loc[0]
		}
	}
	return best
}

func isRealEstateSub(s string) bool {
	switch s {
	case SubVehiculo, SubEmbarcacion, SubMaquinaria, SubMuebleOtros:
		return false
	}
	return true
}

// AssetSubtype clasifica el bien en un subtipo unificado.
func AssetSubtype(assetType, raw, description string) string {
	r := Text(raw)
	// BOE: "Bien 1 - Inmueble (Vivienda)" → lo de dentro del paréntesis
	if m := reBoeParen.FindStringSubmatch(r); m != nil {
		inner := m[1]
		switch {
		case strings.Contains(r, "vehiculo"):
			return SubVehiculo
		case strings.Contains(r, "bien mueble"):
			if strings.Contains(inner, "maquinaria") {
				return SubMaquinaria
			}
			return SubMuebleOtros
		}
		if s := matchSubtype(inner, isRealEstateSub); s != "" {
			return s
		}
		r = "" // "(Otros)": seguimos con la descripción
	}
	// tipos genéricos: mirar la descripción
	generic := r == "" || strings.Contains(r, "finca urbana") || strings.Contains(r, "viviendas y locales") ||
		strings.Contains(r, "otros inmuebles") || r == "lote" || strings.Contains(r, "otros")
	if !generic {
		if s := matchSubtype(r, nil); s != "" {
			return s
		}
	}
	if strings.Contains(r, "rustica") {
		return SubFincaRustica
	}
	if strings.Contains(r, "mueble") || assetType == model.AssetMueble {
		if s := matchSubtype(Text(raw+" "+description), func(s string) bool { return !isRealEstateSub(s) }); s != "" {
			return s
		}
		return SubMuebleOtros
	}
	if assetType == model.AssetVehiculo {
		return SubVehiculo
	}
	if s := matchSubtype(Text(description), isRealEstateSub); s != "" {
		return s
	}
	if strings.Contains(r, "viviendas y locales") {
		return SubVivienda
	}
	return SubInmuebleOtros
}
