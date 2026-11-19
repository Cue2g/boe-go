package normalize

import (
	"regexp"
	"strings"
)

// KnownLocality es una localidad que siempre aparece en la lista del filtro, tenga o no
// subastas ahora mismo. Names son los nombres que se muestran (el primero es el principal);
// Aliases son otras grafías (castellano/catalán) que también cuentan como esa localidad.
type KnownLocality struct {
	Names        []string
	Aliases      []string
	ProvinceCode string
}

// knownLocalities: zonas que se siguen de cerca. Para añadir una, una línea más.
var knownLocalities = []KnownLocality{
	// Menorca (Illes Balears)
	{Names: []string{"Mahón", "Maó"}, Aliases: []string{"Maó-Mahón", "Mahón-Maó", "Mao", "Mahon"}, ProvinceCode: "07"},
	{Names: []string{"Es Castell"}, Aliases: []string{"Castell, Es", "Villacarlos"}, ProvinceCode: "07"},
	{Names: []string{"Sant Lluís"}, Aliases: []string{"San Luis"}, ProvinceCode: "07"},
	{Names: []string{"Alaior"}, Aliases: []string{"Alayor"}, ProvinceCode: "07"},
	{Names: []string{"Es Mercadal"}, Aliases: []string{"Mercadal, Es", "Mercadal"}, ProvinceCode: "07"},
	{Names: []string{"Ferreries"}, Aliases: []string{"Ferrerías"}, ProvinceCode: "07"},
	{Names: []string{"Es Migjorn Gran"}, Aliases: []string{"Migjorn Gran, Es", "Migjorn Gran", "San Cristóbal"}, ProvinceCode: "07"},
	{Names: []string{"Ciutadella de Menorca"}, Aliases: []string{"Ciutadella", "Ciudadela", "Ciudadela de Menorca"}, ProvinceCode: "07"},
	// Montseny (Barcelona)
	{Names: []string{"Sant Esteve de Palautordera"}, Aliases: []string{"San Esteban de Palautordera"}, ProvinceCode: "08"},
	{Names: []string{"Santa Maria de Palautordera"}, Aliases: []string{"Santa María de Palautordera"}, ProvinceCode: "08"},
	{Names: []string{"Sant Pere de Vilamajor"}, Aliases: []string{"San Pedro de Vilamajor"}, ProvinceCode: "08"},
	{Names: []string{"Sant Antoni de Vilamajor"}, Aliases: []string{"San Antonio de Vilamajor"}, ProvinceCode: "08"},
	{Names: []string{"Montseny"}, ProvinceCode: "08"},
	{Names: []string{"Sant Celoni"}, Aliases: []string{"San Celoni"}, ProvinceCode: "08"},
}

func init() { knownLocalities = append(knownLocalities, barcelonaLocalities...) }

// KnownLocalities devuelve el catálogo fijo de localidades.
func KnownLocalities() []KnownLocality { return knownLocalities }

// Keys: todas sus grafías normalizadas (Text), sin repetir.
func (k KnownLocality) Keys() []string {
	seen := map[string]bool{}
	var out []string
	for _, n := range append(append([]string{}, k.Names...), k.Aliases...) {
		if t := Text(n); !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	return out
}

// LookupKnownLocality busca una localidad del catálogo por cualquiera de sus grafías.
func LookupKnownLocality(s string) (KnownLocality, bool) {
	t := Text(s)
	if t == "" {
		return KnownLocality{}, false
	}
	for _, k := range knownLocalities {
		for _, key := range k.Keys() {
			if key == t {
				return k, true
			}
		}
	}
	return KnownLocality{}, false
}

var reWords = regexp.MustCompile(`[^\p{L}\p{N}']+`)

// KnownLocalityIn busca en un texto libre (p. ej. la descripción) el nombre completo de una
// localidad del catálogo de la provincia indicada (sin provincia no se busca: con tantos
// municipios habría falsos positivos). Solo se usa cuando la fuente no trae localidad. Se ignoran grafías de una sola palabra genérica para evitar
// falsos positivos ("Mercadal" como nombre de calle).
func KnownLocalityIn(text, provinceCode string) (KnownLocality, bool) {
	if provinceCode == "" {
		return KnownLocality{}, false
	}
	t := " " + strings.Join(strings.Fields(reWords.ReplaceAllString(Text(text), " ")), " ") + " "
	for _, k := range knownLocalities {
		if provinceCode != k.ProvinceCode {
			continue
		}
		for _, n := range append(append([]string{}, k.Names...), k.Aliases...) {
			key := strings.Join(strings.Fields(reWords.ReplaceAllString(Text(n), " ")), " ")
			if !strings.Contains(key, " ") && !distinctive[key] {
				continue
			}
			if strings.Contains(t, " "+key+" ") {
				return k, true
			}
		}
	}
	return KnownLocality{}, false
}

// distinctive: nombres de una palabra que no se confunden con calles o marcas.
var distinctive = map[string]bool{"mahon": true, "alaior": true, "alayor": true, "ferreries": true,
	"ferrerias": true, "ciutadella": true, "ciudadela": true}
