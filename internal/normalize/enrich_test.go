package normalize

import (
	"testing"
	"time"

	"sboe/internal/model"
)

func TestLookupProvince(t *testing.T) {
	cases := map[string]string{
		"BARCELONA": "08", "Barcelona": "08", "ALACANT/ALICANTE": "03", "Alicante / Alacant": "03",
		"ARABA/ÁLAVA": "01", "Vizcaya / Bizkaia": "48", "BIZKAIA": "48", "S.C. DE TENERIFE": "38",
		"Santa Cruz de Tenerife": "38", "VALÈNCIA/VALENCIA": "46", "CASTELLÓ/CASTELLÓN": "12",
		"Castellón/Castelló": "12", "A CORUÑA": "15", "ILLES BALEARS": "07", "Illes Balears": "07",
		"LAS PALMAS": "35", "MÁLAGA": "29", "Ávila": "05", "8": "08", "28": "28", "CEUTA": "51",
	}
	for in, want := range cases {
		p, ok := LookupProvince(in)
		if !ok || p.Code != want {
			t.Errorf("LookupProvince(%q) = %+v %v, want %s", in, p, ok, want)
		}
	}
	for _, bad := range []string{"", "el", "l'", "Candelaria"} {
		if p, ok := LookupProvince(bad); ok {
			t.Errorf("LookupProvince(%q) = %+v, want none", bad, p)
		}
	}
	if len(Provinces()) != 52 || Provinces()[0].Name != "A Coruña" {
		t.Errorf("catálogo: %d %v", len(Provinces()), Provinces()[0])
	}
}

func TestAssetSubtype(t *testing.T) {
	cases := []struct{ typ, raw, desc, want string }{
		{"inmueble", "Bien 1 - Inmueble (Vivienda)", "", SubVivienda},
		{"inmueble", "Bien 0 - Inmueble (Nave industrial)", "", SubNave},
		{"inmueble", "Bien 1 - Inmueble (Local comercial)", "", SubLocal},
		{"inmueble", "Bien 1 - Inmueble (Finca rústica)", "", SubFincaRustica},
		{"inmueble", "Bien 1 - Inmueble (Otros)", "PLAZA DE GARAJE Nº 4 en la localidad de Vigo", SubGaraje},
		{"inmueble", "Bien 1 - Inmueble (Otros)", "Derechos sobre la localidad", SubInmuebleOtros},
		{"vehiculo", "Bien 1 - Vehículo (Industriales)", "", SubVehiculo},
		{"mueble", "Bien 0 - Bien mueble (Maquinaria)", "", SubMaquinaria},
		{"mueble", "Bien 1 - Bien mueble (Joyas, obras de arte y antigüedades)", "", SubMuebleOtros},
		{"inmueble", "Piso", "", SubVivienda},
		{"inmueble", "Casa / Chalet", "", SubVivienda},
		{"inmueble", "Lote", "Lote de dos garajes y un trastero", SubGaraje},
		{"vehiculo", "Turismo", "", SubVehiculo},
		{"mueble", "Embarcación", "", SubEmbarcacion},
		{"mueble", "Maquinaria, herramientas e instalaciones", "", SubMaquinaria},
		{"inmueble", "Finca Urbana", "CL CANELONES (BADALONA)", SubInmuebleOtros},
		{"inmueble", "Finca Urbana", "VIVIENDA EN CL MAYOR 3", SubVivienda},
		{"inmueble", "Finca Rústica", "PARCELA 771 (VEDRA)", SubFincaRustica},
		{"mueble", "Resto de Bienes Muebles", "", SubMuebleOtros},
		{"inmueble", "Viviendas y Locales", "Local situado en planta baja", SubLocal},
		{"inmueble", "Viviendas y Locales", "Vivienda y trastero (único lote)", SubVivienda},
		{"inmueble", "Viviendas y Locales", "Inmueble en c/ Mayor", SubVivienda},
		{"inmueble", "Solares", "", SubSolar},
		{"inmueble", "Edificios Singulares", "", SubEdificio},
	}
	for _, c := range cases {
		if got := AssetSubtype(c.typ, c.raw, c.desc); got != c.want {
			t.Errorf("AssetSubtype(%q, %q, %q) = %s, want %s", c.typ, c.raw, c.desc, got, c.want)
		}
	}
}

func TestEnrich(t *testing.T) {
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	future := now.AddDate(0, 0, 10)
	past := now.AddDate(0, 0, -3)

	a := model.Auction{Source: "procuradores", Status: "Subasta", Locality: "Campello", Province: "el", AssetType: "inmueble", AssetTypeRaw: "Piso", StartDate: &future}
	Enrich(&a, now)
	if a.ProvinceCode != "" || a.Province != "" || a.ProvinceRaw != "el" || a.StatusCode != StatusProxima || a.AssetSubtype != SubVivienda {
		t.Errorf("procuradores: %+v", a)
	}
	b := model.Auction{Source: "procuradores", Status: "Subasta", Locality: "Santa Cruz de Tenerife", StartDate: &past, AssetTypeRaw: "Garaje"}
	Enrich(&b, now)
	if b.ProvinceCode != "38" || b.StatusCode != StatusEnCurso || b.AssetSubtype != SubGaraje {
		t.Errorf("provincia desde localidad: %+v", b)
	}
	c := model.Auction{Source: "boe", Status: "Celebrándose", Province: "Alicante/Alacant", AssetType: "mueble", AssetTypeRaw: "Bien 1 - Vehículo (Turismos)"}
	Enrich(&c, now)
	if c.Province != "Alicante" || c.StatusCode != StatusEnCurso || c.AssetType != model.AssetVehiculo {
		t.Errorf("boe: %+v", c)
	}
	// idempotente
	Enrich(&c, now)
	if c.Province != "Alicante" || c.ProvinceRaw != "Alicante/Alacant" {
		t.Errorf("idempotencia: %+v", c)
	}
	for in, want := range map[string]string{"Próxima apertura": StatusProxima, "Cesión de remate": StatusCesion, "Adjudicación directa": StatusAdjudicacion, "Suspendida": StatusOtro} {
		if got := StatusCode("x", in, nil, nil, now); got != want {
			t.Errorf("StatusCode(%q) = %s, want %s", in, got, want)
		}
	}
}
