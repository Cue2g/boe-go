package boe

import (
	"os"
	"testing"

	"sboe/internal/model"
)

func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestParseList(t *testing.T) {
	items, err := ParseList(read(t, "testdata/list.html"))
	if err != nil {
		t.Fatal(err)
	}
	if len(items) < 100 {
		t.Fatalf("esperaba muchos items, got %d", len(items))
	}
	for _, it := range items {
		if it.ID == "" || it.Status == "" {
			t.Fatalf("item incompleto: %+v", it)
		}
	}
}

func TestDetail(t *testing.T) {
	f := map[string]string{}
	if _, err := ParseDetail(read(t, "testdata/detail_1.html"), 1, f); err != nil {
		t.Fatal(err)
	}
	raw, err := ParseDetail(read(t, "testdata/detail_3.html"), 3, f)
	if err != nil {
		t.Fatal(err)
	}
	a := Build("SUB-JA-2024-209095", "Próxima apertura", raw, f)
	if a.AssetType != model.AssetInmueble || a.AssetTypeRaw != "Bien 1 - Inmueble (Vivienda)" {
		t.Errorf("tipo: %q %q", a.AssetType, a.AssetTypeRaw)
	}
	if a.ValuationCents == nil || a.ClaimedCents == nil || a.DepositCents == nil {
		t.Errorf("faltan importes: %+v", a)
	}
	if a.Locality == "" || a.Province == "" || a.MainResidence == nil {
		t.Errorf("faltan datos de ubicación: %+v", a)
	}
	t.Logf("%+v valor=%d reclamado=%d", a, *a.ValuationCents, *a.ClaimedCents)
}

func TestDetailLotes(t *testing.T) {
	f := map[string]string{}
	ParseDetail(read(t, "testdata/lotes_1.html"), 1, f)
	raw, _ := ParseDetail(read(t, "testdata/lotes_3.html"), 3, f)
	a := Build("SUB-JV-2026-267979", "", raw, f)
	if a.ValuationCents == nil || *a.ValuationCents != 21203300 || a.AppraisalCents == nil || a.MinBidCents == nil || a.DepositCents == nil {
		t.Errorf("importes del lote 1 no tomados: val=%v tas=%v puja=%v dep=%v", a.ValuationCents, a.AppraisalCents, a.MinBidCents, a.DepositCents)
	}
}
