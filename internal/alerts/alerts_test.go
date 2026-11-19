package alerts

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"sboe/internal/filter"
	"sboe/internal/model"
	"sboe/internal/notify"
	"sboe/internal/store"
)

type fakeMailer struct {
	mu   sync.Mutex
	sent []notify.Message
}

func (f *fakeMailer) Send(_ context.Context, m notify.Message) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, m)
	return nil
}

func setup(t *testing.T) (*store.Store, *fakeMailer, *Service, store.User) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	u, err := st.CreateUser(context.Background(), "a@b.com", "x", store.RoleUser)
	if err != nil {
		t.Fatal(err)
	}
	m := &fakeMailer{}
	loc, _ := time.LoadLocation("Europe/Madrid")
	return st, m, New(st, m, "http://x", loc), u
}

func v(n int64) *int64 { return &n }

func TestOnRunOnlyNew(t *testing.T) {
	ctx := context.Background()
	st, m, svc, u := setup(t)
	f := store.Filter{UserID: u.ID, Name: "Inmuebles", Criteria: filter.Criteria{AssetTypes: []string{"inmueble"}}, AlertMode: store.AlertOnRun, AlertEmail: "a@b.com"}
	if err := st.SaveFilter(ctx, &f); err != nil {
		t.Fatal(err)
	}
	t1 := time.Date(2026, 1, 1, 5, 0, 0, 0, time.UTC)
	st.Sync(ctx, "boe", []model.Auction{{ExternalID: "1", URL: "u", AssetType: "inmueble", ValuationCents: v(100)}}, t1)
	svc.OnRun(ctx, "boe", t1)
	if len(m.sent) != 1 || !strings.Contains(m.sent[0].Subject, "1 subasta nueva") {
		t.Fatalf("primer run: %+v", m.sent)
	}
	t2 := t1.Add(24 * time.Hour)
	st.Sync(ctx, "boe", []model.Auction{
		{ExternalID: "1", URL: "u", AssetType: "inmueble"},
		{ExternalID: "2", URL: "u", AssetType: "vehiculo"},
	}, t2)
	svc.OnRun(ctx, "boe", t2)
	if len(m.sent) != 1 {
		t.Fatalf("no debería enviar (1 ya vista, 2 no cumple): %d", len(m.sent))
	}
	// filtro limitado a otra fuente no recibe
	svc.OnRun(ctx, "procuradores", t2)
	if len(m.sent) != 1 {
		t.Fatal("fuente distinta")
	}
}

func TestDailyOncePerDay(t *testing.T) {
	ctx := context.Background()
	st, m, svc, u := setup(t)
	f := store.Filter{UserID: u.ID, Name: "Todo", AlertMode: store.AlertDaily, AlertTime: "08:00", AlertEmail: "a@b.com"}
	if err := st.SaveFilter(ctx, &f); err != nil {
		t.Fatal(err)
	}
	// subasta posterior a la creación del filtro
	st.Sync(ctx, "boe", []model.Auction{{ExternalID: "1", URL: "u"}}, time.Now().Add(time.Minute))
	loc, _ := time.LoadLocation("Europe/Madrid")
	now := time.Now().In(loc)
	eight := time.Date(now.Year(), now.Month(), now.Day(), 8, 0, 0, 0, loc).AddDate(0, 0, 1)

	svc.Daily(ctx, eight.Add(-time.Minute)) // 07:59 → nada
	if len(m.sent) != 0 {
		t.Fatal("antes de hora")
	}
	svc.Daily(ctx, eight)
	if len(m.sent) != 1 {
		t.Fatalf("a la hora: %d", len(m.sent))
	}
	svc.Daily(ctx, eight) // mismo minuto, mismo día → no repite
	if len(m.sent) != 1 {
		t.Fatal("repetido")
	}
	// al día siguiente sin novedades → nada
	svc.Daily(ctx, eight.AddDate(0, 0, 1))
	if len(m.sent) != 1 {
		t.Fatal("sin novedades no envía")
	}
}

// TestResendEndToEnd: con Resend configurado, los tres envíos (al terminar un job,
// diario y prueba desde la web) llegan a la API de Resend con remitente, destino y asunto.
func TestResendEndToEnd(t *testing.T) {
	type req struct {
		From    string   `json:"from"`
		To      []string `json:"to"`
		Subject string   `json:"subject"`
		HTML    string   `json:"html"`
	}
	var mu sync.Mutex
	var got []req
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer re_test" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var b req
		json.NewDecoder(r.Body).Decode(&b)
		mu.Lock()
		got = append(got, b)
		mu.Unlock()
		w.Write([]byte(`{"id":"x"}`))
	}))
	defer srv.Close()

	ctx := context.Background()
	st, _, _, u := setup(t)
	mailer := notify.New(notify.Config{Resend: notify.ResendConfig{
		APIKey: "re_test", From: "Alertas SBOE <alertas@boe-service.site>", Endpoint: srv.URL,
	}})
	loc, _ := time.LoadLocation("Europe/Madrid")
	svc := New(st, mailer, "http://x", loc)

	onRun := store.Filter{UserID: u.ID, Name: "Al terminar", AlertMode: store.AlertOnRun, AlertEmail: "run@b.com"}
	daily := store.Filter{UserID: u.ID, Name: "Diario", AlertMode: store.AlertDaily, AlertTime: "08:00", AlertEmail: "daily@b.com"}
	for _, f := range []*store.Filter{&onRun, &daily} {
		if err := st.SaveFilter(ctx, f); err != nil {
			t.Fatal(err)
		}
	}
	t1 := time.Now().Add(time.Minute) // posterior a la creación de los filtros
	st.Sync(ctx, "boe", []model.Auction{{ExternalID: "1", URL: "u", AssetType: "inmueble", ValuationCents: v(100)}}, t1)

	svc.OnRun(ctx, "boe", t1)
	at8 := time.Date(t1.Year(), t1.Month(), t1.Day(), 8, 0, 0, 0, loc).AddDate(0, 0, 1)
	svc.Daily(ctx, at8)
	if _, err := svc.SendTest(ctx, onRun); err != nil {
		t.Fatal(err)
	}

	if len(got) != 3 {
		t.Fatalf("Resend recibió %d emails, want 3: %+v", len(got), got)
	}
	want := []struct{ to, subj string }{
		{"run@b.com", "1 subasta nueva · Al terminar"},
		{"daily@b.com", "1 subasta nueva · Diario"},
		{"run@b.com", "[Prueba] 1 subasta nueva · Al terminar"},
	}
	for i, w := range want {
		g := got[i]
		if g.From != "Alertas SBOE <alertas@boe-service.site>" || len(g.To) != 1 || g.To[0] != w.to || g.Subject != w.subj || g.HTML == "" {
			t.Errorf("email %d = %+v, want to=%s subject=%q", i, g, w.to, w.subj)
		}
	}
}
