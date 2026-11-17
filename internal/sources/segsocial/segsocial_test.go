package segsocial

import (
	"os"
	"testing"

	"sboe/internal/model"
)

func rows(t *testing.T, p string) []Row {
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	r, err := ParseTables(string(b))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestRegulares(t *testing.T) {
	r := rows(t, "testdata/regulares.html")
	if len(r) != 20 {
		t.Fatalf("filas=%d", len(r))
	}
	a := BuildRegular(r[0])
	if a.ExternalID != "174" || a.Province != "A CORUÑA" || a.AssetType != model.AssetInmueble {
		t.Errorf("%+v", a)
	}
	if a.AppraisalCents == nil || *a.AppraisalCents != 978994 || a.ChargesCents != nil || a.StartDate == nil {
		t.Errorf("importes/fecha: %+v", a)
	}
	if a.URL != "https://w6.seg-social.es/subastas/SubaSeControladorInter?opcion=13&EMB_ID=174&opcion2=1&tipoOperacion=0" {
		t.Errorf("url %s", a.URL)
	}
	t.Logf("%+v", a)
}

func TestAdjudicacion(t *testing.T) {
	r := rows(t, "testdata/adjudicacion.html")
	if len(r) != 20 {
		t.Fatalf("filas=%d", len(r))
	}
	a := BuildAdjudicacion(r[0])
	if a.ExternalID != "13266" || a.Locality != "Oviedo" || a.Province != "ASTURIAS" {
		t.Errorf("%+v", a)
	}
	if a.MinBidCents == nil || *a.MinBidCents != 5754000 {
		t.Errorf("precio %v", a.MinBidCents)
	}
	t.Logf("%+v", a)
}
