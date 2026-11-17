package procuradores

import (
	"os"
	"testing"

	"sboe/internal/model"
)

func TestParse(t *testing.T) {
	b, _ := os.ReadFile("testdata/list.html")
	items, err := ParseList(string(b))
	if err != nil || len(items) != 10 {
		t.Fatalf("items=%d err=%v", len(items), err)
	}
	a := items[0]
	if a.ExternalID != "gjrsdacdntah4eo9" || a.AssetType != model.AssetInmueble || a.Province != "Barcelona" {
		t.Errorf("listado: %+v", a)
	}
	if a.ValuationCents == nil || *a.ValuationCents != 4192150 || a.MinBidCents == nil || *a.MinBidCents != 2934505 {
		t.Errorf("importes: %v %v", a.ValuationCents, a.MinBidCents)
	}
	if a.StartDate == nil || a.EndDate == nil {
		t.Errorf("fechas: %v %v", a.StartDate, a.EndDate)
	}
	d, _ := os.ReadFile("testdata/detail.html")
	if err := ParseDetail(string(d), &a); err != nil {
		t.Fatal(err)
	}
	if a.DepositCents == nil || *a.DepositCents != 125765 {
		t.Errorf("deposito: %v", a.DepositCents)
	}
	if a.EndDate.Format("2006-01-02") != "2026-12-16" || a.URL != "https://www.subastasprocuradores.com/subastas/gjrsdacdntah4eo9" {
		t.Errorf("fin/url: %v %s", a.EndDate, a.URL)
	}
	t.Logf("%+v", a)
}
