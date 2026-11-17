// Package normalize contiene el parseo común de importes, fechas y tipos de bien.
package normalize

import (
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"

	"sboe/internal/model"
)

var madrid = func() *time.Location {
	loc, err := time.LoadLocation("Europe/Madrid")
	if err != nil {
		return time.UTC
	}
	return loc
}()

// Text pasa a minúsculas, quita tildes y espacios sobrantes.
func Text(s string) string {
	t := transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC)
	out, _, err := transform.String(t, s)
	if err != nil {
		out = s
	}
	return strings.Join(strings.Fields(strings.ToLower(out)), " ")
}

var nonMoney = regexp.MustCompile(`[^\d,.\-]`)

// Euros convierte un importe en formato español ("1.234,56 €") a céntimos.
// Devuelve nil si no hay número.
func Euros(s string) *int64 {
	c := nonMoney.ReplaceAllString(s, "")
	if c == "" || c == "-" {
		return nil
	}
	c = strings.ReplaceAll(c, ".", "")
	c = strings.Replace(c, ",", ".", 1)
	f, err := strconv.ParseFloat(c, 64)
	if err != nil {
		return nil
	}
	v := int64(f*100 + 0.5)
	if f < 0 {
		v = int64(f*100 - 0.5)
	}
	return &v
}

var (
	reNumericDate = regexp.MustCompile(`(\d{1,2})[/\-](\d{1,2})[/\-](\d{4})`)
	reTextDate    = regexp.MustCompile(`(\d{1,2})\s+(?:de\s+)?([a-z]+)\s+(?:de\s+)?(\d{4})`)
	months        = map[string]time.Month{
		"enero": 1, "febrero": 2, "marzo": 3, "abril": 4, "mayo": 5, "junio": 6, "julio": 7,
		"agosto": 8, "septiembre": 9, "setiembre": 9, "octubre": 10, "noviembre": 11, "diciembre": 12,
	}
)

// Date reconoce "dd/mm/yyyy", "dd-mm-yyyy hh:mm:ss (CET)" y "12 enero 2025".
// Solo se conserva el día (como hacía la versión TS).
func Date(s string) *time.Time {
	if m := reNumericDate.FindStringSubmatch(s); m != nil {
		d, _ := strconv.Atoi(m[1])
		mo, _ := strconv.Atoi(m[2])
		y, _ := strconv.Atoi(m[3])
		if mo >= 1 && mo <= 12 && d >= 1 && d <= 31 {
			t := time.Date(y, time.Month(mo), d, 0, 0, 0, 0, madrid)
			return &t
		}
	}
	if m := reTextDate.FindStringSubmatch(Text(s)); m != nil {
		mo, ok := months[m[2]]
		if !ok {
			return nil
		}
		d, _ := strconv.Atoi(m[1])
		y, _ := strconv.Atoi(m[3])
		t := time.Date(y, mo, d, 0, 0, 0, 0, madrid)
		return &t
	}
	return nil
}

var (
	vehicleKeywords = []string{"vehiculo", "motocicleta", "turismo", "camion", "furgoneta", "remolque"}
	realEstateKW    = []string{
		"inmueble", "vivienda", "casa", "chalet", "piso", "solar", "finca", "nave industrial",
		"local comercial", "local", "garaje", "plaza de aparcamiento", "trastero", "terreno", "parcela", "lote",
	}
)

// AssetType clasifica el texto de tipo de bien en inmueble / vehiculo / mueble.
func AssetType(raw string) string {
	t := Text(raw)
	for _, k := range vehicleKeywords {
		if strings.Contains(t, k) {
			return model.AssetVehiculo
		}
	}
	for _, k := range realEstateKW {
		if strings.Contains(t, k) {
			return model.AssetInmueble
		}
	}
	return model.AssetMueble
}

// Clean colapsa espacios en blanco.
func Clean(s string) string { return strings.Join(strings.Fields(s), " ") }
