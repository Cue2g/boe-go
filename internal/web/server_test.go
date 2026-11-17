package web

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/xuri/excelize/v2"

	"sboe/internal/alerts"
	"sboe/internal/auth"
	"sboe/internal/config"
	"sboe/internal/model"
	"sboe/internal/notify"
	"sboe/internal/runner"
	"sboe/internal/scheduler"
	"sboe/internal/store"
)

type env struct {
	srv *httptest.Server
	st  *store.Store
}

func newEnv(t *testing.T) *env {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	for _, u := range []struct{ e, r string }{{"admin@x.com", store.RoleAdmin}, {"user@x.com", store.RoleUser}} {
		h, _ := auth.HashPassword("password123")
		if _, err := st.CreateUser(ctx, u.e, h, u.r); err != nil {
			t.Fatal(err)
		}
	}
	yes := true
	v := int64(12345600)
	st.Sync(ctx, "boe", []model.Auction{{ExternalID: "SUB-1", URL: "https://example.com", AssetType: "inmueble",
		AssetTypeRaw: "Bien 1 - Inmueble (Vivienda)", Province: "Barcelona", Locality: "Sant Celoni", ValuationCents: &v, MainResidence: &yes}}, time.Now().UTC())

	loc, _ := time.LoadLocation("Europe/Madrid")
	cfg := config.Load()
	reg := runner.Registry(cfg, "")
	mgr := runner.NewManager(ctx, st, reg, nil)
	h, err := New(Deps{
		Store: st, Auth: auth.New(st, false), Manager: mgr, Scheduler: scheduler.New(ctx, st, mgr, loc, nil),
		Alerts: alerts.New(st, notify.LogMailer{}, "", loc), Location: loc,
	})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return &env{srv: srv, st: st}
}

type client struct {
	t    *testing.T
	base string
	c    *http.Client
	csrf string
}

func (e *env) client(t *testing.T) *client {
	jar, _ := cookiejar.New(nil)
	return &client{t: t, base: e.srv.URL, c: &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

var csrfRe = regexp.MustCompile(`name="csrf_token" value="([^"]+)"`)

func (c *client) do(method, path string, form url.Values) (int, string, http.Header) {
	c.t.Helper()
	var body io.Reader
	if form != nil {
		if c.csrf != "" && form.Get("csrf_token") == "" && form.Get("_nocsrf") == "" {
			form.Set("csrf_token", c.csrf)
		}
		form.Del("_nocsrf")
		body = strings.NewReader(form.Encode())
	}
	req, _ := http.NewRequest(method, c.base+path, body)
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	resp, err := c.c.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if m := csrfRe.FindSubmatch(b); m != nil {
		c.csrf = string(m[1])
	}
	return resp.StatusCode, string(b), resp.Header
}

func (c *client) login(email string) {
	c.t.Helper()
	code, _, h := c.do("POST", "/login", url.Values{"email": {email}, "password": {"password123"}})
	if code != http.StatusSeeOther || h.Get("Location") != "/app/subastas" {
		c.t.Fatalf("login %s: %d %s", email, code, h.Get("Location"))
	}
	c.do("GET", "/app/cuenta", nil) // carga el token CSRF
}

func TestAuthAndRoles(t *testing.T) {
	e := newEnv(t)
	anon := e.client(t)
	if code, _, h := anon.do("GET", "/app/subastas", nil); code != http.StatusSeeOther || !strings.HasPrefix(h.Get("Location"), "/login") {
		t.Fatalf("anónimo debe ir al login: %d", code)
	}
	if code, _, _ := anon.do("POST", "/login", url.Values{"email": {"user@x.com"}, "password": {"mala"}}); code != http.StatusUnauthorized {
		t.Fatalf("password mala: %d", code)
	}

	u := e.client(t)
	u.login("user@x.com")
	if code, body, _ := u.do("GET", "/app/subastas", nil); code != 200 || !strings.Contains(body, "Sant Celoni") || strings.Contains(body, "/admin/jobs") {
		t.Fatalf("subastas para usuario: %d", code)
	}
	if code, _, _ := u.do("GET", "/admin/jobs", nil); code != http.StatusForbidden {
		t.Fatalf("usuario en admin: %d", code)
	}
	if code, _, _ := u.do("POST", "/app/filtros", url.Values{"name": {"x"}, "alert_mode": {"none"}, "csrf_token": {"malo"}}); code != http.StatusForbidden {
		t.Fatalf("CSRF inválido: %d", code)
	}

	a := e.client(t)
	a.login("admin@x.com")
	for _, p := range []string{"/admin/jobs", "/admin/jobs/boe", "/admin/usuarios", "/admin/jobs/tabla", "/admin/runs/tabla", "/app/filtros", "/app/cuenta"} {
		if code, body, _ := a.do("GET", p, nil); code != 200 {
			t.Errorf("GET %s: %d %s", p, code, body[:min(200, len(body))])
		}
	}
	if code, _, h := a.do("POST", "/admin/usuarios", url.Values{"email": {"nuevo@x.com"}, "password": {"password123"}, "role": {"user"}}); code != http.StatusSeeOther || !strings.Contains(h.Get("Location"), "ok=creado") {
		t.Fatalf("crear usuario: %d %s", code, h.Get("Location"))
	}
	n := e.client(t)
	n.login("nuevo@x.com")

	// horario del job
	if code, _, h := a.do("POST", "/admin/jobs/boe", url.Values{"enabled": {"1"}, "kind": {"daily"}, "daily_time": {"06:30"}}); code != http.StatusSeeOther || !strings.Contains(h.Get("Location"), "ok=guardado") {
		t.Fatalf("guardar horario: %d", code)
	}
	j, _ := e.st.GetJob(context.Background(), "boe")
	if j.Schedule != "30 6 * * *" || !j.Enabled {
		t.Fatalf("job: %+v", j)
	}
	if code, _, _ := a.do("POST", "/admin/jobs/boe", url.Values{"kind": {"custom"}, "cron": {"nope"}}); code != http.StatusUnprocessableEntity {
		t.Fatalf("cron inválido: %d", code)
	}
}

func TestFilterFlow(t *testing.T) {
	e := newEnv(t)
	u := e.client(t)
	u.login("user@x.com")
	code, body, _ := u.do("GET", "/app/filtros/nuevo?asset_type=inmueble&province=barcelona", nil)
	if code != 200 || !strings.Contains(body, "coinciden ahora <strong>1</strong>") {
		t.Fatalf("nuevo filtro: %d", code)
	}
	code, _, h := u.do("POST", "/app/filtros", url.Values{"name": {"BCN"}, "criteria_qs": {"asset_type=inmueble&province=barcelona"},
		"alert_mode": {"daily"}, "alert_time": {"08:00"}, "alert_email": {"user@x.com"}})
	if code != http.StatusSeeOther {
		t.Fatalf("guardar: %d", code)
	}
	_ = h
	code, body, _ = u.do("GET", "/app/filtros", nil)
	if code != 200 || !strings.Contains(body, "BCN") || !strings.Contains(body, "Diaria a las 08:00") {
		t.Fatalf("listado: %s", body)
	}
	if code, _, h := u.do("POST", "/app/filtros/1/probar", url.Values{}); code != http.StatusSeeOther || !strings.Contains(h.Get("Location"), "ok=prueba") {
		t.Fatalf("prueba: %d %s", code, h.Get("Location"))
	}
	// otro usuario no ve ni borra el filtro
	a := e.client(t)
	a.login("admin@x.com")
	if code, _, _ := a.do("GET", "/app/filtros/1/editar", nil); code != http.StatusNotFound {
		t.Fatalf("filtro ajeno: %d", code)
	}
	// htmx parcial
	req, _ := http.NewRequest("GET", e.srv.URL+"/app/subastas?q=vivienda", nil)
	req.Header.Set("HX-Request", "true")
	req.Header.Set("HX-Target", "results")
	resp, err := u.c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if strings.Contains(string(b), "<html") || !strings.Contains(string(b), "Sant Celoni") {
		t.Fatalf("parcial htmx: %s", b)
	}
}

func TestExportAndViews(t *testing.T) {
	e := newEnv(t)
	u := e.client(t)
	u.login("user@x.com")

	// exportación .xlsx con los filtros actuales
	req, _ := http.NewRequest("GET", e.srv.URL+"/app/subastas/export.xlsx?province=barcelona&sort=valor_desc", nil)
	resp, err := u.c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet" ||
		!strings.Contains(resp.Header.Get("Content-Disposition"), ".xlsx") {
		t.Fatalf("export: %d %v", resp.StatusCode, resp.Header)
	}
	f, err := excelize.OpenReader(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	rows, err := f.GetRows("Subastas")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0][0] != "Plataforma" || rows[1][0] != "BOE" || rows[1][6] != "Sant Celoni" {
		t.Fatalf("filas: %v", rows)
	}
	if v, _ := f.GetCellValue("Subastas", "K2", excelize.Options{RawCellValue: true}); v != "123456" {
		t.Fatalf("valor numérico: %q", v)
	}
	// anónimo no puede exportar
	if code, _, _ := e.client(t).do("GET", "/app/subastas/export.xlsx", nil); code != http.StatusSeeOther {
		t.Fatalf("export anónimo: %d", code)
	}

	// vista tabla por query y por cookie; plataforma visible
	if _, body, _ := u.do("GET", "/app/subastas?view=table", nil); !strings.Contains(body, `<table class="data grid">`) || !strings.Contains(body, "src-boe") {
		t.Fatal("vista tabla por query")
	}
	u.c.Jar.SetCookies(mustURL(e.srv.URL), []*http.Cookie{{Name: "sboe_view", Value: "table"}})
	if _, body, _ := u.do("GET", "/app/subastas", nil); !strings.Contains(body, `<table class="data grid">`) {
		t.Fatal("vista tabla por cookie")
	}
	if _, body, _ := u.do("GET", "/app/subastas?view=cards", nil); strings.Contains(body, `<table class="data grid">`) || !strings.Contains(body, `class="acard`) {
		t.Fatal("vista tarjetas")
	}
}

func TestStaticCache(t *testing.T) {
	e := newEnv(t)
	_, body, _ := e.client(t).do("GET", "/login", nil)
	m := regexp.MustCompile(`/static/app\.css\?v=([0-9a-f]+)`).FindStringSubmatch(body)
	if m == nil {
		t.Fatal("sin URL versionada")
	}
	req, _ := http.NewRequest("GET", e.srv.URL+m[0], nil)
	req.Header.Set("Accept-Encoding", "gzip")
	resp, err := http.DefaultTransport.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(resp.Header.Get("Cache-Control"), "immutable") || resp.Header.Get("Content-Encoding") != "gzip" ||
		!strings.HasPrefix(resp.Header.Get("Content-Type"), "text/css") {
		t.Fatalf("static: %d %v", resp.StatusCode, resp.Header)
	}
	req, _ = http.NewRequest("GET", e.srv.URL+"/static/app.css", nil)
	req.Header.Set("If-None-Match", resp.Header.Get("ETag"))
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotModified {
		t.Fatalf("ETag: %d", resp.StatusCode)
	}
}

func mustURL(s string) *url.URL {
	u, err := url.Parse(s)
	if err != nil {
		panic(err)
	}
	return u
}
