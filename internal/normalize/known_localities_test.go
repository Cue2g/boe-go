package normalize

import "testing"

func TestLookupKnownLocality(t *testing.T) {
	for _, s := range []string{"Mahón", "MAÓ", "mao", "Maó-Mahón"} {
		k, ok := LookupKnownLocality(s)
		if !ok || k.Names[0] != "Mahón" {
			t.Errorf("LookupKnownLocality(%q) = %v, %v", s, k.Names, ok)
		}
	}
	if k, ok := LookupKnownLocality("Hospitalet de Llobregat"); !ok || k.Names[0] != "L'Hospitalet de Llobregat" {
		t.Errorf("Hospitalet: %v %v", k.Names, ok)
	}
	if n := len(KnownLocalities()); n != 14+305 {
		t.Errorf("catálogo: %d localidades", n)
	}
	if _, ok := LookupKnownLocality("Madrid"); ok {
		t.Error("Madrid no está en el catálogo")
	}
}

func TestKnownLocalityIn(t *testing.T) {
	cases := []struct {
		text, prov, want string
	}{
		{"Porción de terreno en el término municipal de Ciutadella de Menorca, camino", "07", "Ciutadella de Menorca"},
		{"Vivienda en Sant Celoni (Barcelona)", "08", "Sant Celoni"},
		{"Vivienda en Sant Celoni (Barcelona)", "", ""}, // sin provincia no se busca
		{"Nave en el polígono de La Garriga", "08", "La Garriga"},
		{"Vivienda en CAÑADA MERCADAL 30 en Bellpuig", "25", ""},
		{"Vivienda en UR MONTSENY 157", "", ""},
		{"Vivienda en Sant Celoni", "07", ""}, // otra provincia
	}
	for _, c := range cases {
		k, ok := KnownLocalityIn(c.text, c.prov)
		got := ""
		if ok {
			got = k.Names[0]
		}
		if got != c.want {
			t.Errorf("KnownLocalityIn(%q, %q) = %q, want %q", c.text, c.prov, got, c.want)
		}
	}
}
