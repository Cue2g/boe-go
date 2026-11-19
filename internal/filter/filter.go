// Package filter traduce los criterios de búsqueda de un usuario a SQL sobre auctions_v.
// Lo usan tanto el explorador de la platform como las alertas por email.
package filter

import (
	"fmt"
	"math"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"sboe/internal/normalize"
)

// Criteria son los criterios de un filtro. Todos son opcionales; los importes van en euros.
type Criteria struct {
	Sources       []string `json:"sources,omitempty"`
	AssetTypes    []string `json:"asset_types,omitempty"`
	Provinces     []string `json:"provinces,omitempty"`
	Locality      string   `json:"locality,omitempty"`
	Text          string   `json:"text,omitempty"`
	Subtypes      []string `json:"subtypes,omitempty"`
	StatusCodes   []string `json:"status_codes,omitempty"`
	Status        string   `json:"status,omitempty"` // texto libre (filtros antiguos)
	ValuationMin  *float64 `json:"valuation_min,omitempty"`
	ValuationMax  *float64 `json:"valuation_max,omitempty"`
	AppraisalMin  *float64 `json:"appraisal_min,omitempty"`
	AppraisalMax  *float64 `json:"appraisal_max,omitempty"`
	MinBidMax     *float64 `json:"min_bid_max,omitempty"`
	ClaimedMax    *float64 `json:"claimed_max,omitempty"`
	PctClaimedMax *float64 `json:"pct_claimed_valuation_max,omitempty"`
	MainResidence *bool    `json:"main_residence,omitempty"`
	EndDateFrom   string   `json:"end_date_from,omitempty"` // YYYY-MM-DD
	EndDateTo     string   `json:"end_date_to,omitempty"`
	IncludeClosed bool     `json:"include_closed,omitempty"` // por defecto solo activas
}

// Valores permitidos (los mismos que producen las fuentes y normalize.Enrich).
var (
	Sources     = []string{"boe", "procuradores", "ss_regulares", "ss_adjudicacion"}
	AssetTypes  = []string{"inmueble", "vehiculo", "mueble"}
	StatusCodes = []string{normalize.StatusProxima, normalize.StatusEnCurso, normalize.StatusAdjudicacion, normalize.StatusCesion, normalize.StatusOtro}
	// Subtipos agrupados por tipo, en el orden en que se muestran.
	SubtypeGroups = []struct {
		AssetType string
		Subtypes  []string
	}{
		{"inmueble", []string{normalize.SubVivienda, normalize.SubGaraje, normalize.SubTrastero, normalize.SubLocal, normalize.SubNave,
			normalize.SubSolar, normalize.SubFincaRustica, normalize.SubEdificio, normalize.SubInmuebleOtros}},
		{"vehiculo", []string{normalize.SubVehiculo}},
		{"mueble", []string{normalize.SubEmbarcacion, normalize.SubMaquinaria, normalize.SubMuebleOtros}},
	}
	Subtypes = func() []string {
		var out []string
		for _, g := range SubtypeGroups {
			out = append(out, g.Subtypes...)
		}
		return out
	}()
)

// SourceLabel y AssetLabel dan nombres legibles para la UI y los emails.
var (
	SourceLabel = map[string]string{
		"boe": "BOE", "procuradores": "Subastas Procuradores",
		"ss_regulares": "Seg. Social (embargos)", "ss_adjudicacion": "Seg. Social (adjudicación)",
	}
	AssetLabel  = map[string]string{"inmueble": "Inmueble", "vehiculo": "Vehículo", "mueble": "Bien mueble"}
	StatusLabel = map[string]string{
		normalize.StatusProxima: "Próxima", normalize.StatusEnCurso: "En curso (se puede pujar)",
		normalize.StatusAdjudicacion: "Adjudicación directa", normalize.StatusCesion: "Cesión de remate", normalize.StatusOtro: "Otro",
	}
	SubtypeLabel = map[string]string{
		normalize.SubVivienda: "Vivienda", normalize.SubGaraje: "Garaje", normalize.SubTrastero: "Trastero",
		normalize.SubLocal: "Local / oficina", normalize.SubNave: "Nave / almacén", normalize.SubSolar: "Solar / parcela",
		normalize.SubFincaRustica: "Finca rústica", normalize.SubEdificio: "Edificio", normalize.SubInmuebleOtros: "Otros inmuebles",
		normalize.SubVehiculo: "Vehículo", normalize.SubEmbarcacion: "Embarcación", normalize.SubMaquinaria: "Maquinaria",
		normalize.SubMuebleOtros: "Otros bienes muebles",
	}
)

var rePublicID = regexp.MustCompile(`(?i)^\s*sb-?0*(\d+)\s*$`)

// PublicID formatea el ID público de una subasta.
func PublicID(id int64) string { return fmt.Sprintf("SB-%06d", id) }

// ProvinceCodes convierte provincias (código INE o nombre en cualquier forma) a códigos INE.
func ProvinceCodes(vals []string) []string {
	var out []string
	for _, v := range vals {
		if p, ok := normalize.LookupProvince(v); ok && !slices.Contains(out, p.Code) {
			out = append(out, p.Code)
		}
	}
	return out
}

func cents(euros float64) int64 { return int64(math.Round(euros * 100)) }

// Build devuelve la cláusula WHERE (sin la palabra WHERE) y sus argumentos.
// Solo se usan columnas fijas: nada del usuario se concatena en el SQL.
func Build(c Criteria) (string, []any) {
	var conds []string
	var args []any
	add := func(cond string, a ...any) {
		conds = append(conds, cond)
		args = append(args, a...)
	}
	in := func(col string, vals, allowed []string, transform func(string) string) {
		var ok []any
		for _, v := range vals {
			if allowed != nil && !slices.Contains(allowed, v) {
				continue
			}
			if transform != nil {
				v = transform(v)
			}
			ok = append(ok, v)
		}
		if len(ok) == 0 {
			return
		}
		add(col+" IN ("+strings.TrimSuffix(strings.Repeat("?,", len(ok)), ",")+")", ok...)
	}

	if !c.IncludeClosed {
		add("active = 1")
	}
	in("source", c.Sources, Sources, nil)
	in("asset_type", c.AssetTypes, AssetTypes, nil)
	in("asset_subtype", c.Subtypes, Subtypes, nil)
	in("status_code", c.StatusCodes, StatusCodes, nil)
	if len(c.Provinces) > 0 {
		codes := ProvinceCodes(c.Provinces)
		if len(codes) == 0 {
			add("0 = 1") // provincias desconocidas: nada coincide
		} else {
			in("province_code", codes, nil, nil)
		}
	}
	if v := normalize.Text(c.Locality); v != "" {
		// lo escrito, tal cual…
		cond := `norm(locality) LIKE ? ESCAPE '\'`
		args := []any{"%" + escapeLike(v) + "%"}
		if k, ok := normalize.LookupKnownLocality(c.Locality); ok {
			// …y, si es una localidad del catálogo, cualquiera de sus nombres (Mahón = Maó),
			// pero solo en su provincia (La Granada, Barcelona ≠ Granada)
			var ors []string
			for _, key := range k.Keys() {
				ors = append(ors, `norm(locality) LIKE ? ESCAPE '\'`)
				args = append(args, "%"+escapeLike(key)+"%")
			}
			args = append(args, k.ProvinceCode)
			cond = "(" + cond + " OR ((" + strings.Join(ors, " OR ") + ") AND (province_code = ? OR province_code IS NULL)))"
		}
		add(cond, args...)
	}
	if m := rePublicID.FindStringSubmatch(c.Text); m != nil {
		// búsqueda por ID público: SB-000123
		id, _ := strconv.ParseInt(m[1], 10, 64)
		add("id = ?", id)
	} else if v := normalize.Text(c.Text); v != "" {
		like := "%" + escapeLike(v) + "%"
		add(`(norm(description) LIKE ? ESCAPE '\' OR norm(asset_type_raw) LIKE ? ESCAPE '\' OR norm(locality) LIKE ? ESCAPE '\' OR norm(external_id) LIKE ? ESCAPE '\')`,
			like, like, like, like)
	}
	if v := normalize.Text(c.Status); v != "" {
		add(`norm(status) LIKE ? ESCAPE '\'`, "%"+escapeLike(v)+"%")
	}
	rng := func(col string, min, max *float64) {
		if min != nil {
			add(col+" >= ?", cents(*min))
		}
		if max != nil {
			add(col+" <= ?", cents(*max))
		}
	}
	rng("valuation_cents", c.ValuationMin, c.ValuationMax)
	rng("appraisal_cents", c.AppraisalMin, c.AppraisalMax)
	rng("min_bid_cents", nil, c.MinBidMax)
	rng("claimed_cents", nil, c.ClaimedMax)
	if c.PctClaimedMax != nil {
		add("pct_claimed_valuation <= ?", *c.PctClaimedMax)
	}
	if c.MainResidence != nil {
		add("main_residence = ?", *c.MainResidence)
	}
	if validDate(c.EndDateFrom) {
		add("end_date >= ?", c.EndDateFrom)
	}
	if validDate(c.EndDateTo) {
		add("end_date <= ?", c.EndDateTo)
	}
	if len(conds) == 0 {
		return "1 = 1", nil
	}
	return strings.Join(conds, " AND "), args
}

func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

func validDate(s string) bool {
	_, err := time.Parse("2006-01-02", s)
	return err == nil
}

// Sorts permitidos → ORDER BY.
var Sorts = []struct{ Key, Label, SQL string }{
	{"recientes", "Más recientes", "first_seen DESC, id DESC"},
	{"valor_asc", "Valor ↑", "valuation_cents IS NULL, valuation_cents ASC"},
	{"valor_desc", "Valor ↓", "valuation_cents IS NULL, valuation_cents DESC"},
	{"puja_asc", "Puja mínima ↑", "min_bid_cents IS NULL, min_bid_cents ASC"},
	{"pct_asc", "% reclamado ↑", "pct_claimed_valuation IS NULL, pct_claimed_valuation ASC"},
	{"fin_asc", "Fecha fin ↑", "end_date IS NULL, end_date ASC"},
}

// OrderBy devuelve el ORDER BY de un sort (por defecto, recientes).
func OrderBy(key string) string {
	for _, s := range Sorts {
		if s.Key == key {
			return s.SQL
		}
	}
	return Sorts[0].SQL
}

// FromValues lee los criterios desde un formulario / query string.
func FromValues(v url.Values) Criteria {
	c := Criteria{
		Sources:       nonEmpty(v["source"]),
		AssetTypes:    nonEmpty(v["asset_type"]),
		Provinces:     ProvinceCodes(nonEmpty(v["province"])),
		Subtypes:      nonEmpty(v["subtype"]),
		StatusCodes:   nonEmpty(v["status_code"]),
		Locality:      strings.TrimSpace(v.Get("locality")),
		Text:          strings.TrimSpace(v.Get("q")),
		Status:        strings.TrimSpace(v.Get("status")),
		ValuationMin:  num(v.Get("valuation_min")),
		ValuationMax:  num(v.Get("valuation_max")),
		AppraisalMin:  num(v.Get("appraisal_min")),
		AppraisalMax:  num(v.Get("appraisal_max")),
		MinBidMax:     num(v.Get("min_bid_max")),
		ClaimedMax:    num(v.Get("claimed_max")),
		PctClaimedMax: num(v.Get("pct_claimed_max")),
		EndDateFrom:   v.Get("end_from"),
		EndDateTo:     v.Get("end_to"),
		IncludeClosed: v.Get("include_closed") != "",
	}
	switch v.Get("main_residence") {
	case "si":
		t := true
		c.MainResidence = &t
	case "no":
		f := false
		c.MainResidence = &f
	}
	if !validDate(c.EndDateFrom) {
		c.EndDateFrom = ""
	}
	if !validDate(c.EndDateTo) {
		c.EndDateTo = ""
	}
	return c
}

// Values es la inversa de FromValues (para abrir un filtro guardado en el explorador).
func (c Criteria) Values() url.Values {
	v := url.Values{}
	v["source"] = c.Sources
	v["asset_type"] = c.AssetTypes
	v["province"] = ProvinceCodes(c.Provinces)
	v["subtype"] = c.Subtypes
	v["status_code"] = c.StatusCodes
	set := func(k, s string) {
		if s != "" {
			v.Set(k, s)
		}
	}
	setNum := func(k string, f *float64) {
		if f != nil {
			v.Set(k, strconv.FormatFloat(*f, 'f', -1, 64))
		}
	}
	set("locality", c.Locality)
	set("q", c.Text)
	set("status", c.Status)
	setNum("valuation_min", c.ValuationMin)
	setNum("valuation_max", c.ValuationMax)
	setNum("appraisal_min", c.AppraisalMin)
	setNum("appraisal_max", c.AppraisalMax)
	setNum("min_bid_max", c.MinBidMax)
	setNum("claimed_max", c.ClaimedMax)
	setNum("pct_claimed_max", c.PctClaimedMax)
	set("end_from", c.EndDateFrom)
	set("end_to", c.EndDateTo)
	if c.IncludeClosed {
		v.Set("include_closed", "1")
	}
	if c.MainResidence != nil {
		if *c.MainResidence {
			v.Set("main_residence", "si")
		} else {
			v.Set("main_residence", "no")
		}
	}
	for k, vs := range v {
		if len(vs) == 0 {
			delete(v, k)
		}
	}
	return v
}

// Summary describe el filtro en texto (para la UI y el email).
func (c Criteria) Summary() string {
	var parts []string
	label := func(m map[string]string, vals []string) string {
		out := make([]string, len(vals))
		for i, v := range vals {
			if l, ok := m[v]; ok {
				out[i] = l
			} else {
				out[i] = v
			}
		}
		return strings.Join(out, ", ")
	}
	if len(c.Sources) > 0 {
		parts = append(parts, "Fuente: "+label(SourceLabel, c.Sources))
	}
	if len(c.AssetTypes) > 0 {
		parts = append(parts, "Tipo: "+label(AssetLabel, c.AssetTypes))
	}
	if len(c.Subtypes) > 0 {
		parts = append(parts, "Subtipo: "+label(SubtypeLabel, c.Subtypes))
	}
	if len(c.Provinces) > 0 {
		var names []string
		for _, code := range ProvinceCodes(c.Provinces) {
			p, _ := normalize.ProvinceByCode(code)
			names = append(names, p.Name)
		}
		parts = append(parts, "Provincia: "+strings.Join(names, ", "))
	}
	if len(c.StatusCodes) > 0 {
		parts = append(parts, "Estado: "+label(StatusLabel, c.StatusCodes))
	}
	if c.Locality != "" {
		parts = append(parts, "Localidad contiene “"+c.Locality+"”")
	}
	if c.Text != "" {
		parts = append(parts, "Texto “"+c.Text+"”")
	}
	if c.Status != "" {
		parts = append(parts, "Estado “"+c.Status+"”")
	}
	eur := func(f float64) string { return FormatEuros(cents(f)) }
	if c.ValuationMin != nil {
		parts = append(parts, "Valor ≥ "+eur(*c.ValuationMin))
	}
	if c.ValuationMax != nil {
		parts = append(parts, "Valor ≤ "+eur(*c.ValuationMax))
	}
	if c.AppraisalMin != nil {
		parts = append(parts, "Tasación ≥ "+eur(*c.AppraisalMin))
	}
	if c.AppraisalMax != nil {
		parts = append(parts, "Tasación ≤ "+eur(*c.AppraisalMax))
	}
	if c.MinBidMax != nil {
		parts = append(parts, "Puja mínima ≤ "+eur(*c.MinBidMax))
	}
	if c.ClaimedMax != nil {
		parts = append(parts, "Reclamado ≤ "+eur(*c.ClaimedMax))
	}
	if c.PctClaimedMax != nil {
		parts = append(parts, fmt.Sprintf("Reclamado ≤ %s%% del valor", strconv.FormatFloat(*c.PctClaimedMax, 'f', -1, 64)))
	}
	if c.MainResidence != nil {
		if *c.MainResidence {
			parts = append(parts, "Vivienda habitual")
		} else {
			parts = append(parts, "No vivienda habitual")
		}
	}
	if c.EndDateFrom != "" {
		parts = append(parts, "Fin desde "+c.EndDateFrom)
	}
	if c.EndDateTo != "" {
		parts = append(parts, "Fin hasta "+c.EndDateTo)
	}
	if c.IncludeClosed {
		parts = append(parts, "Incluye no activas")
	}
	if len(parts) == 0 {
		return "Todas las subastas activas"
	}
	return strings.Join(parts, " · ")
}

// FormatEuros formatea céntimos al estilo español: 1.234,56 €.
func FormatEuros(c int64) string {
	neg := c < 0
	if neg {
		c = -c
	}
	ent, dec := c/100, c%100
	s := strconv.FormatInt(ent, 10)
	var b strings.Builder
	for i, r := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte('.')
		}
		b.WriteRune(r)
	}
	out := fmt.Sprintf("%s,%02d €", b.String(), dec)
	if neg {
		out = "-" + out
	}
	return out
}

func nonEmpty(in []string) []string {
	var out []string
	for _, s := range in {
		if s = strings.TrimSpace(s); s != "" && !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	return out
}

func num(s string) *float64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	// acepta "50.000", "50000", "33,5"
	if strings.Contains(s, ",") {
		s = strings.ReplaceAll(s, ".", "")
		s = strings.Replace(s, ",", ".", 1)
	} else if strings.Count(s, ".") > 1 || (strings.Count(s, ".") == 1 && len(s)-strings.Index(s, ".") == 4) {
		s = strings.ReplaceAll(s, ".", "")
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
		return nil
	}
	return &f
}
