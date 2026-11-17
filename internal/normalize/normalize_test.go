package normalize

import (
	"testing"

	"sboe/internal/model"
)

func TestEuros(t *testing.T) {
	cases := map[string]int64{
		"1.234,56 €":   123456,
		"12.000,00 €":  1200000,
		"350 €":        35000,
		"0,99":         99,
		"Importe: 5,5": 550,
	}
	for in, want := range cases {
		got := Euros(in)
		if got == nil || *got != want {
			t.Errorf("Euros(%q) = %v, want %d", in, got, want)
		}
	}
	for _, in := range []string{"", "No consta", "-"} {
		if got := Euros(in); got != nil {
			t.Errorf("Euros(%q) = %d, want nil", in, *got)
		}
	}
}

func TestDate(t *testing.T) {
	cases := map[string]string{
		"05/03/2025": "2025-03-05",
		"14-02-2025 18:00:00 CET  (ISO: 2025-02-14T18:00:00+01:00)": "2025-02-14",
		"(12 enero 2025)":         "2025-01-12",
		"3 de Septiembre de 2024": "2024-09-03",
	}
	for in, want := range cases {
		got := Date(in)
		if got == nil || got.Format("2006-01-02") != want {
			t.Errorf("Date(%q) = %v, want %s", in, got, want)
		}
	}
	if Date("sin fecha") != nil {
		t.Error("expected nil")
	}
}

func TestAssetType(t *testing.T) {
	cases := map[string]string{
		"Bien 1 - Inmueble (Vivienda)": model.AssetInmueble,
		"VEHÍCULO TURISMO":             model.AssetVehiculo,
		"Veh�culo":                     model.AssetMueble, // mojibake ya no debería ocurrir (decodificamos charset)
		"Finca rústica":                model.AssetInmueble,
		"Joya":                         model.AssetMueble,
		"Motocicleta":                  model.AssetVehiculo,
	}
	for in, want := range cases {
		if got := AssetType(in); got != want {
			t.Errorf("AssetType(%q) = %s, want %s", in, got, want)
		}
	}
}
