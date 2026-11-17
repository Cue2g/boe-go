package normalize

import (
	"strings"
)

// Province es una provincia española con su código INE.
type Province struct {
	Code string // INE, 2 dígitos
	Name string // nombre canónico
}

// provinces: catálogo INE. Los alias se comparan normalizados (sin tildes, minúsculas, sin puntos).
var provinces = []struct {
	Province
	aliases []string
}{
	{Province{"01", "Álava"}, []string{"araba", "alava", "araba/alava"}},
	{Province{"02", "Albacete"}, nil},
	{Province{"03", "Alicante"}, []string{"alacant", "alicante/alacant"}},
	{Province{"04", "Almería"}, nil},
	{Province{"05", "Ávila"}, nil},
	{Province{"06", "Badajoz"}, nil},
	{Province{"07", "Illes Balears"}, []string{"baleares", "islas baleares", "balears", "illes balears", "mallorca", "menorca", "ibiza", "eivissa"}},
	{Province{"08", "Barcelona"}, nil},
	{Province{"09", "Burgos"}, nil},
	{Province{"10", "Cáceres"}, nil},
	{Province{"11", "Cádiz"}, nil},
	{Province{"12", "Castellón"}, []string{"castello", "castellon de la plana", "castello de la plana"}},
	{Province{"13", "Ciudad Real"}, nil},
	{Province{"14", "Córdoba"}, nil},
	{Province{"15", "A Coruña"}, []string{"la coruna", "coruna", "a coruna"}},
	{Province{"16", "Cuenca"}, nil},
	{Province{"17", "Girona"}, []string{"gerona"}},
	{Province{"18", "Granada"}, nil},
	{Province{"19", "Guadalajara"}, nil},
	{Province{"20", "Gipuzkoa"}, []string{"guipuzcoa"}},
	{Province{"21", "Huelva"}, nil},
	{Province{"22", "Huesca"}, nil},
	{Province{"23", "Jaén"}, nil},
	{Province{"24", "León"}, nil},
	{Province{"25", "Lleida"}, []string{"lerida"}},
	{Province{"26", "La Rioja"}, []string{"rioja", "logrono"}},
	{Province{"27", "Lugo"}, nil},
	{Province{"28", "Madrid"}, []string{"comunidad de madrid"}},
	{Province{"29", "Málaga"}, nil},
	{Province{"30", "Murcia"}, []string{"region de murcia"}},
	{Province{"31", "Navarra"}, []string{"nafarroa", "pamplona", "iruna"}},
	{Province{"32", "Ourense"}, []string{"orense"}},
	{Province{"33", "Asturias"}, []string{"principado de asturias", "oviedo"}},
	{Province{"34", "Palencia"}, nil},
	{Province{"35", "Las Palmas"}, []string{"palmas", "las palmas de gran canaria", "gran canaria"}},
	{Province{"36", "Pontevedra"}, nil},
	{Province{"37", "Salamanca"}, nil},
	{Province{"38", "Santa Cruz de Tenerife"}, []string{"sc de tenerife", "s c de tenerife", "sc tenerife", "tenerife", "santa cruz tenerife"}},
	{Province{"39", "Cantabria"}, []string{"santander"}},
	{Province{"40", "Segovia"}, nil},
	{Province{"41", "Sevilla"}, nil},
	{Province{"42", "Soria"}, nil},
	{Province{"43", "Tarragona"}, nil},
	{Province{"44", "Teruel"}, nil},
	{Province{"45", "Toledo"}, nil},
	{Province{"46", "Valencia"}, []string{"valencia/valencia"}},
	{Province{"47", "Valladolid"}, nil},
	{Province{"48", "Bizkaia"}, []string{"vizcaya", "bilbao"}},
	{Province{"49", "Zamora"}, nil},
	{Province{"50", "Zaragoza"}, nil},
	{Province{"51", "Ceuta"}, nil},
	{Province{"52", "Melilla"}, nil},
}

var (
	provByKey  = map[string]Province{}
	provByCode = map[string]Province{}
)

func init() {
	for _, p := range provinces {
		provByCode[p.Code] = p.Province
		provByKey[provKey(p.Name)] = p.Province
		for _, a := range p.aliases {
			provByKey[provKey(a)] = p.Province
		}
	}
}

func provKey(s string) string {
	s = Text(strings.NewReplacer(".", " ", ",", " ", "-", " ").Replace(s))
	s = strings.Join(strings.Fields(strings.ReplaceAll(s, " / ", "/")), " ")
	return s
}

// Provinces devuelve el catálogo ordenado por nombre.
func Provinces() []Province {
	out := make([]Province, 0, len(provinces))
	for _, p := range provinces {
		out = append(out, p.Province)
	}
	// ordenar por nombre sin tildes
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && Text(out[j].Name) < Text(out[j-1].Name); j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// ProvinceByCode devuelve la provincia de un código INE.
func ProvinceByCode(code string) (Province, bool) {
	p, ok := provByCode[code]
	return p, ok
}

// LookupProvince reconoce una provincia escrita de cualquier forma ("ALACANT/ALICANTE",
// "S.C. DE TENERIFE", "Vizcaya / Bizkaia") o por su código INE.
func LookupProvince(raw string) (Province, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return Province{}, false
	}
	if p, ok := provByCode[raw]; ok {
		return p, true
	}
	if len(raw) == 1 && raw[0] >= '1' && raw[0] <= '9' {
		if p, ok := provByCode["0"+raw]; ok {
			return p, true
		}
	}
	k := provKey(raw)
	if p, ok := provByKey[k]; ok {
		return p, true
	}
	for _, part := range strings.Split(k, "/") {
		if p, ok := provByKey[strings.TrimSpace(part)]; ok {
			return p, true
		}
	}
	return Province{}, false
}
