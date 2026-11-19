package filter_test

import (
	"context"
	"encoding/json"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"sboe/internal/filter"
	"sboe/internal/model"
	"sboe/internal/store"
)

func i64(v int64) *int64     { return &v }
func f64(v float64) *float64 { return &v }

func seed(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	yes := true
	at := time.Date(2026, 1, 1, 5, 0, 0, 0, time.UTC)
	_, err = st.Sync(context.Background(), "boe", []model.Auction{
		{ExternalID: "1", URL: "u", AssetType: "inmueble", AssetTypeRaw: "Bien 1 - Inmueble (Vivienda)", Province: "Barcelona", Locality: "Sant Celoni", Status: "Celebrándose",
			ValuationCents: i64(10000000), ClaimedCents: i64(2000000), MainResidence: &yes, Description: "Piso con terraza"},
		{ExternalID: "2", URL: "u", AssetType: "vehiculo", Province: "Illes Balears", Locality: "Maó", ValuationCents: i64(500000)},
		{ExternalID: "3", URL: "u", AssetType: "inmueble", Province: "BARCELONA", Locality: "Badalona", ValuationCents: i64(30000000), ClaimedCents: i64(25000000)},
	}, at)
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func ids(t *testing.T, st *store.Store, c filter.Criteria) string {
	t.Helper()
	where, args := filter.Build(c)
	rows, total, err := st.QueryAuctions(context.Background(), where, args, "external_id", 100, 0)
	if err != nil {
		t.Fatalf("%s: %v", where, err)
	}
	var out []string
	for _, r := range rows {
		out = append(out, r.ExternalID)
	}
	if total != len(out) {
		t.Fatalf("total %d != %d", total, len(out))
	}
	return strings.Join(out, ",")
}

func TestBuild(t *testing.T) {
	st := seed(t)
	cases := []struct {
		name string
		c    filter.Criteria
		want string
	}{
		{"todo", filter.Criteria{}, "1,2,3"},
		{"tipo", filter.Criteria{AssetTypes: []string{"inmueble"}}, "1,3"},
		{"provincia sin tildes ni mayúsculas", filter.Criteria{Provinces: []string{"barcelona"}}, "1,3"},
		{"localidad con tilde", filter.Criteria{Localities: []string{"mao"}}, "2"},
		{"varias localidades", filter.Criteria{Localities: []string{"Maó", "badalona"}}, "2,3"},
		{"texto", filter.Criteria{Text: "TERRAZA"}, "1"},
		{"valor máx", filter.Criteria{ValuationMax: f64(10000)}, "2"},
		{"pct reclamado", filter.Criteria{PctClaimedMax: f64(33)}, "1"},
		{"reclamado máx", filter.Criteria{ClaimedMax: f64(50000)}, "1"},
		{"vivienda habitual", filter.Criteria{MainResidence: func() *bool { b := true; return &b }()}, "1"},
		{"fuente inválida se ignora", filter.Criteria{Sources: []string{"x'; DROP TABLE auctions;--"}}, "1,2,3"},
		{"like escapado", filter.Criteria{Text: "%"}, ""},
		{"id público", filter.Criteria{Text: "SB-000002"}, "2"},
		{"id público sin ceros", filter.Criteria{Text: "sb3"}, "3"},
		{"id de origen", filter.Criteria{Text: "2"}, "2"},
		{"subtipo", filter.Criteria{Subtypes: []string{"vivienda"}}, "1"},
		{"subtipo inválido se ignora", filter.Criteria{Subtypes: []string{"nope"}}, "1,2,3"},
		{"estado", filter.Criteria{StatusCodes: []string{"en_curso"}}, "1"},
		{"provincia por código", filter.Criteria{Provinces: []string{"07"}}, "2"},
		{"provincia bilingüe", filter.Criteria{Provinces: []string{"Illes Balears"}}, "2"},
		{"provincia desconocida", filter.Criteria{Provinces: []string{"Narnia"}}, ""},
	}
	for _, c := range cases {
		if got := ids(t, st, c.c); got != c.want {
			t.Errorf("%s: got %q want %q", c.name, got, c.want)
		}
	}
}

func TestValuesRoundTrip(t *testing.T) {
	yes := true
	c := filter.Criteria{Sources: []string{"boe"}, Provinces: []string{"Madrid", "Toledo"}, Text: "piso", ValuationMax: f64(150000),
		PctClaimedMax: f64(33.5), MainResidence: &yes, EndDateFrom: "2026-01-01", IncludeClosed: true}
	back := filter.FromValues(c.Values())
	if back.Summary() != c.Summary() {
		t.Errorf("roundtrip:\n%s\n%s", c.Summary(), back.Summary())
	}
	// formatos de número del usuario
	v := url.Values{"valuation_max": {"50.000"}, "pct_claimed_max": {"33,5"}, "claimed_max": {"1.234,50"}}
	got := filter.FromValues(v)
	if *got.ValuationMax != 50000 || *got.PctClaimedMax != 33.5 || *got.ClaimedMax != 1234.5 {
		t.Errorf("números: %v %v %v", *got.ValuationMax, *got.PctClaimedMax, *got.ClaimedMax)
	}
	if filter.FormatEuros(123456789) != "1.234.567,89 €" {
		t.Error(filter.FormatEuros(123456789))
	}
}

func TestUnmarshalLegacyLocality(t *testing.T) {
	var c filter.Criteria
	if err := json.Unmarshal([]byte(`{"locality":" Maó ","provinces":["07"]}`), &c); err != nil {
		t.Fatal(err)
	}
	if len(c.Localities) != 1 || c.Localities[0] != "Maó" || len(c.Provinces) != 1 {
		t.Fatalf("criterios: %+v", c)
	}
	b, _ := json.Marshal(c)
	if strings.Contains(string(b), `"locality"`) {
		t.Fatalf("no debería volver a escribir locality: %s", b)
	}
	v := filter.Criteria{Localities: []string{"Maó", "Badalona"}}.Values()
	if got := filter.FromValues(v).Localities; len(got) != 2 {
		t.Fatalf("ida y vuelta por query string: %v", got)
	}
}
