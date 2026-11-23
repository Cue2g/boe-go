// Package web sirve la interfaz: platform (/app) para usuarios y admin (/admin).
package web

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"sboe/internal/alerts"
	"sboe/internal/auth"
	"sboe/internal/filter"
	"sboe/internal/normalize"
	"sboe/internal/runner"
	"sboe/internal/scheduler"
	"sboe/internal/store"
)

//go:embed templates/*.html
var templatesFS embed.FS

//go:embed static
var staticFS embed.FS

// Deps son las dependencias del servidor.
type Deps struct {
	Store     *store.Store
	Auth      *auth.Auth
	Manager   *runner.Manager
	Scheduler *scheduler.Scheduler
	Alerts    *alerts.Service
	Location  *time.Location
}

// Server es el handler HTTP de la aplicación.
type Server struct {
	Deps
	pages  map[string]*template.Template
	mux    *http.ServeMux
	static map[string]*staticAsset
}

// Page son los datos comunes a todas las páginas.
type Page struct {
	Title, Nav   string
	User         store.User
	CSRF         string
	Flash, Error string
	D            any
}

const pageSize = 50

// Version se muestra junto al logo. Se puede fijar al compilar con
// -ldflags "-X sboe/internal/web.Version=1.1".
var Version = "1.3"

// New construye el servidor.
func New(d Deps) (*Server, error) {
	s := &Server{Deps: d, pages: map[string]*template.Template{}, mux: http.NewServeMux()}
	sub, _ := fs.Sub(staticFS, "static")
	assets, err := loadStatic(sub)
	if err != nil {
		return nil, fmt.Errorf("estáticos: %w", err)
	}
	s.static = assets
	if err := s.parseTemplates(); err != nil {
		return nil, err
	}
	s.routes()
	return s, nil
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Referrer-Policy", "same-origin")
	s.Auth.Load(s.mux).ServeHTTP(w, r)
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /static/", s.serveStatic)

	s.mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/app/subastas", http.StatusSeeOther)
	})
	s.mux.HandleFunc("GET /login", s.loginPage)
	s.mux.HandleFunc("POST /login", s.loginPost)
	s.mux.HandleFunc("POST /logout", s.logout)

	user := func(p string, h http.HandlerFunc) { s.mux.Handle(p, auth.RequireUser(h)) }
	admin := func(p string, h http.HandlerFunc) { s.mux.Handle(p, auth.RequireAdmin(h)) }

	user("GET /app/subastas", s.auctions)
	user("GET /app/subastas/export.xlsx", s.auctionsExport)
	user("GET /app/filtros", s.filters)
	user("GET /app/filtros/nuevo", s.filterNew)
	user("POST /app/filtros", s.filterSave)
	user("GET /app/filtros/{id}/editar", s.filterEdit)
	user("POST /app/filtros/{id}", s.filterSave)
	user("POST /app/filtros/{id}/borrar", s.filterDelete)
	user("POST /app/filtros/{id}/probar", s.filterTest)
	user("GET /app/cuenta", s.account)
	user("POST /app/cuenta/password", s.accountPassword)

	admin("GET /admin/jobs", s.jobs)
	admin("GET /admin/jobs/tabla", s.jobsTable)
	admin("GET /admin/runs/tabla", s.runsTable)
	admin("GET /admin/jobs/{name}", s.job)
	admin("POST /admin/jobs/{name}", s.jobSave)
	admin("POST /admin/jobs/{name}/ejecutar", s.jobRun)
	admin("GET /admin/usuarios", s.users)
	admin("POST /admin/usuarios", s.userCreate)
	admin("POST /admin/usuarios/{id}/activo", s.userActive)
	admin("POST /admin/usuarios/{id}/rol", s.userRole)
	admin("POST /admin/usuarios/{id}/password", s.userPassword)
}

// ---------- plantillas ----------

func (s *Server) funcs() template.FuncMap {
	f := template.FuncMap{
		"has": func(list []string, v string) bool { return slices.Contains(list, v) },
		"hasNorm": func(list []string, v string) bool {
			n := normalize.Text(v)
			return slices.ContainsFunc(list, func(x string) bool { return normalize.Text(x) == n })
		},
		"asset":   s.assetURL,
		"version": func() string { return Version },
		"seq":     func(n int) []int { return make([]int, n) },
		"hasInt":  func(list []int, v int) bool { return slices.Contains(list, v) },
		"numv": func(f *float64) string {
			if f == nil {
				return ""
			}
			return strconv.FormatFloat(*f, 'f', -1, 64)
		},
		"isTrue":  func(b *bool) bool { return b != nil && *b },
		"isFalse": func(b *bool) bool { return b != nil && !*b },
		"isNew":   func(t time.Time) bool { return time.Since(t) < 24*time.Hour },
		"add":     func(a, b int) int { return a + b },
		"sub":     func(a, b int) int { return a - b },
		"qs":      func(c filter.Criteria) template.URL { return template.URL(c.Values().Encode()) },
		"fmtTime": func(v any) string {
			var t time.Time
			switch x := v.(type) {
			case time.Time:
				t = x
			case *time.Time:
				if x == nil {
					return ""
				}
				t = *x
			}
			if t.IsZero() {
				return ""
			}
			return t.In(s.Location).Format("02/01/2006 15:04")
		},
		"describeCron": scheduler.Describe,
		"triggerLabel": func(t string) string {
			switch t {
			case runner.TriggerManual:
				return "Manual"
			case runner.TriggerSchedule:
				return "Programado"
			case runner.TriggerCLI:
				return "CLI"
			}
			return t
		},
		"alertDesc": func(f store.Filter) string {
			switch f.AlertMode {
			case store.AlertOnRun:
				return "Cuando corran los jobs → " + f.AlertEmail
			case store.AlertDaily:
				return "Diaria a las " + f.AlertTime + " → " + f.AlertEmail
			}
			return "Sin alerta"
		},
	}
	for k, v := range alerts.Funcs {
		f[k] = v
	}
	return f
}

func (s *Server) parseTemplates() error {
	// Cada página = layout + sus ficheros. admin_job reutiliza runs_table/run_status de
	// admin_jobs.html y redefine "content": el último fichero parseado gana.
	pages := map[string][]string{
		"login":       {"login.html"},
		"auctions":    {"auctions.html"},
		"filters":     {"filters.html"},
		"filter_edit": {"filter_edit.html"},
		"account":     {"account.html"},
		"admin_jobs":  {"admin_jobs.html"},
		"admin_job":   {"admin_jobs.html", "admin_job.html"},
		"admin_users": {"admin_users.html"},
	}
	for name, files := range pages {
		paths := []string{"templates/layout.html"}
		for _, f := range files {
			paths = append(paths, "templates/"+f)
		}
		t, err := template.New(name).Funcs(s.funcs()).ParseFS(templatesFS, paths...)
		if err != nil {
			return fmt.Errorf("plantilla %s: %w", name, err)
		}
		s.pages[name] = t
	}
	return nil
}

func (s *Server) page(r *http.Request, title, nav string, d any) Page {
	p := Page{Title: title, Nav: nav, D: d}
	if u, ok := auth.UserFrom(r.Context()); ok {
		p.User = u
	}
	if sess, ok := auth.SessionFrom(r.Context()); ok {
		p.CSRF = sess.CSRFToken
	}
	p.Flash = flashMessages[r.URL.Query().Get("ok")]
	p.Error = errorMessages[r.URL.Query().Get("err")]
	return p
}

var flashMessages = map[string]string{
	"guardado":     "Cambios guardados.",
	"borrado":      "Filtro borrado.",
	"creado":       "Usuario creado.",
	"password":     "Contraseña actualizada.",
	"prueba":       "Email de prueba enviado.",
	"ejecutando":   "Job lanzado. Se está ejecutando en segundo plano.",
	"usuario":      "Usuario actualizado.",
	"filtro_nuevo": "Filtro guardado.",
}

var errorMessages = map[string]string{
	"sin_datos":  "El filtro no tiene subastas que enviar ahora mismo; prueba con otros criterios.",
	"envio":      "No se pudo enviar el email. Revisa la configuración de email (Resend/SMTP) en el servidor.",
	"no_existe":  "No encontrado.",
	"duplicado":  "Ya existe un usuario con ese email.",
	"corriendo":  "Ese job ya se está ejecutando.",
	"password":   "La contraseña debe tener al menos 8 caracteres y las dos deben coincidir.",
	"actual":     "La contraseña actual no es correcta.",
	"email":      "Email no válido.",
	"datos":      "Revisa los datos del formulario.",
	"mismo":      "No puedes cambiar tu propio rol ni desactivarte.",
	"horario":    "Horario inválido.",
	"alerta_sin": "Para la alerta indica un email válido y, si es diaria, una hora.",
}

func (s *Server) render(w http.ResponseWriter, page, tmpl string, data any) {
	t, ok := s.pages[page]
	if !ok {
		http.Error(w, "plantilla desconocida", http.StatusInternalServerError)
		return
	}
	var buf bytes.Buffer
	if err := t.ExecuteTemplate(&buf, tmpl, data); err != nil {
		slog.Error("render", "page", page, "tmpl", tmpl, "err", err)
		http.Error(w, "error interno", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	buf.WriteTo(w)
}

func (s *Server) fail(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	slog.Error("handler", "path", r.URL.Path, "err", err)
	http.Error(w, "error interno", http.StatusInternalServerError)
}

func redirect(w http.ResponseWriter, r *http.Request, to string) {
	http.Redirect(w, r, to, http.StatusSeeOther)
}

func isHX(r *http.Request) bool { return r.Header.Get("HX-Request") != "" }

func pathID(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	return id, err == nil && id > 0
}

func validEmail(e string) bool {
	e = strings.TrimSpace(e)
	at := strings.LastIndex(e, "@")
	return at > 0 && at < len(e)-3 && strings.Contains(e[at:], ".") && !strings.ContainsAny(e, " \r\n,;<>")
}

// ---------- login ----------

func safeNext(n string) string {
	if strings.HasPrefix(n, "/") && !strings.HasPrefix(n, "//") && !strings.HasPrefix(n, "/\\") {
		return n
	}
	return "/app/subastas"
}

func (s *Server) loginPage(w http.ResponseWriter, r *http.Request) {
	if _, ok := auth.UserFrom(r.Context()); ok {
		redirect(w, r, "/app/subastas")
		return
	}
	s.render(w, "login", "layout", s.page(r, "Entrar", "", map[string]string{"Next": r.URL.Query().Get("next")}))
}

func (s *Server) loginPost(w http.ResponseWriter, r *http.Request) {
	email, pw := r.PostFormValue("email"), r.PostFormValue("password")
	_, err := s.Auth.Login(w, r, email, pw)
	if err != nil {
		p := s.page(r, "Entrar", "", map[string]string{"Next": r.PostFormValue("next"), "Email": email})
		if errors.Is(err, auth.ErrBadCredentials) || errors.Is(err, auth.ErrTooManyTries) {
			p.Error = err.Error()
		} else {
			slog.Error("login", "err", err)
			p.Error = "Error interno."
		}
		w.WriteHeader(http.StatusUnauthorized)
		s.render(w, "login", "layout", p)
		return
	}
	redirect(w, r, safeNext(r.PostFormValue("next")))
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	s.Auth.Logout(w, r)
	redirect(w, r, "/login")
}

// ---------- platform: subastas ----------

type auctionsData struct {
	C             filter.Criteria
	Items         []store.AuctionRow
	Total         int
	Page          int
	Pages         int
	Sort          string
	Sorts         any
	QS            template.URL
	Sources       []string
	SubtypeGroups any
	StatusCodes   []string
	Provinces     []provinceOpt
	Localities    []store.LocalityOption
	FilterID      int64
	FilterName    string
	View          string // "cards" o "table"
}

type provinceOpt struct {
	Code, Name string
	Count      int
	Top        bool // se muestra sin pulsar "Ver todas"
}

// topProvinces es cuántas provincias (las de más subastas) se muestran de entrada.
const topProvinces = 12

// viewOf elige la vista de resultados: query ?view=, si no la cookie sboe_view (la pone el front).
func viewOf(r *http.Request) string {
	v := r.URL.Query().Get("view")
	if v == "" {
		if c, err := r.Cookie("sboe_view"); err == nil {
			v = c.Value
		}
	}
	if v == "table" {
		return "table"
	}
	return "cards"
}

func (s *Server) auctions(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	u, _ := auth.UserFrom(r.Context())
	c := filter.FromValues(q)
	d := auctionsData{C: c, Sort: q.Get("sort"), Sorts: filter.Sorts, Sources: filter.Sources,
		SubtypeGroups: filter.SubtypeGroups, StatusCodes: filter.StatusCodes, View: viewOf(r)}
	if d.Sort == "" {
		d.Sort = filter.Sorts[0].Key
	}
	if id, err := strconv.ParseInt(q.Get("filter_id"), 10, 64); err == nil && id > 0 {
		if f, err := s.Store.GetFilter(r.Context(), u.ID, id); err == nil {
			d.FilterID, d.FilterName = f.ID, f.Name
		}
	}
	d.Page, _ = strconv.Atoi(q.Get("page"))
	if d.Page < 1 {
		d.Page = 1
	}
	where, args := filter.Build(c)
	items, total, err := s.Store.QueryAuctions(r.Context(), where, args, filter.OrderBy(d.Sort), pageSize, (d.Page-1)*pageSize)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	d.Items, d.Total = items, total
	d.Pages = (total + pageSize - 1) / pageSize
	qv := c.Values()
	if d.Sort != filter.Sorts[0].Key {
		qv.Set("sort", d.Sort)
	}
	d.QS = template.URL(qv.Encode())

	p := s.page(r, "Subastas", "subastas", d)
	if isHX(r) && r.Header.Get("HX-Target") == "results" {
		s.render(w, "auctions", "results", p)
		return
	}
	counts, err := s.Store.ProvinceCounts(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	for _, pr := range normalize.Provinces() {
		d.Provinces = append(d.Provinces, provinceOpt{Code: pr.Code, Name: pr.Name, Count: counts[pr.Code],
			Top: slices.Contains(c.Provinces, pr.Code)})
	}
	byCount := make([]int, len(d.Provinces))
	for i := range byCount {
		byCount[i] = i
	}
	slices.SortStableFunc(byCount, func(a, b int) int { return d.Provinces[b].Count - d.Provinces[a].Count })
	for _, i := range byCount[:min(topProvinces, len(byCount))] {
		if d.Provinces[i].Count > 0 {
			d.Provinces[i].Top = true
		}
	}
	locs, err := s.Store.LocalityOptions(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	for i := range locs {
		locs[i].Name = normalize.DisplayName(locs[i].Name)
	}
	d.Localities = locs
	p.D = d
	s.render(w, "auctions", "layout", p)
}

// ---------- platform: filtros ----------

func (s *Server) filters(w http.ResponseWriter, r *http.Request) {
	u, _ := auth.UserFrom(r.Context())
	fs, err := s.Store.ListFilters(r.Context(), u.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.render(w, "filters", "layout", s.page(r, "Mis filtros", "filtros", map[string]any{"Filters": fs}))
}

type filterEditData struct {
	F          store.Filter
	CriteriaQS string
	Matches    int
}

func (s *Server) renderFilterEdit(w http.ResponseWriter, r *http.Request, f store.Filter, errKey string) {
	where, args := filter.Build(f.Criteria)
	_, total, err := s.Store.QueryAuctions(r.Context(), where, args, filter.OrderBy(""), 1, 0)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	title := "Nuevo filtro"
	if f.ID != 0 {
		title = "Editar filtro"
	}
	p := s.page(r, title, "filtros", filterEditData{F: f, CriteriaQS: f.Criteria.Values().Encode(), Matches: total})
	if errKey != "" {
		p.Error = errorMessages[errKey]
		w.WriteHeader(http.StatusUnprocessableEntity)
	}
	s.render(w, "filter_edit", "layout", p)
}

func (s *Server) filterNew(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	q.Del("sort")
	q.Del("page")
	q.Del("filter_id")
	s.renderFilterEdit(w, r, store.Filter{Criteria: filter.FromValues(q), AlertMode: store.AlertNone}, "")
}

func (s *Server) filterEdit(w http.ResponseWriter, r *http.Request) {
	u, _ := auth.UserFrom(r.Context())
	id, ok := pathID(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	f, err := s.Store.GetFilter(r.Context(), u.ID, id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if q := r.URL.Query(); q.Get("use_qs") != "" {
		f.Criteria = filter.FromValues(q)
	}
	s.renderFilterEdit(w, r, f, "")
}

func (s *Server) filterSave(w http.ResponseWriter, r *http.Request) {
	u, _ := auth.UserFrom(r.Context())
	f := store.Filter{UserID: u.ID}
	if r.PathValue("id") != "" {
		id, ok := pathID(r)
		if !ok {
			http.NotFound(w, r)
			return
		}
		existing, err := s.Store.GetFilter(r.Context(), u.ID, id)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		f = existing
	}
	cq, _ := url.ParseQuery(r.PostFormValue("criteria_qs"))
	f.Criteria = filter.FromValues(cq)
	f.Name = strings.TrimSpace(r.PostFormValue("name"))
	f.AlertMode = r.PostFormValue("alert_mode")
	f.AlertTime = strings.TrimSpace(r.PostFormValue("alert_time"))
	f.AlertEmail = strings.TrimSpace(r.PostFormValue("alert_email"))

	if f.Name == "" || len(f.Name) > 100 {
		s.renderFilterEdit(w, r, f, "datos")
		return
	}
	switch f.AlertMode {
	case store.AlertNone:
	case store.AlertOnRun, store.AlertDaily:
		if !validEmail(f.AlertEmail) {
			s.renderFilterEdit(w, r, f, "alerta_sin")
			return
		}
		if f.AlertMode == store.AlertDaily {
			if _, err := time.Parse("15:04", f.AlertTime); err != nil {
				s.renderFilterEdit(w, r, f, "alerta_sin")
				return
			}
		}
	default:
		s.renderFilterEdit(w, r, f, "datos")
		return
	}
	if f.AlertMode != store.AlertDaily {
		f.AlertTime = ""
	}
	isNew := f.ID == 0
	if err := s.Store.SaveFilter(r.Context(), &f); err != nil {
		s.fail(w, r, err)
		return
	}
	if isNew {
		redirect(w, r, "/app/filtros?ok=filtro_nuevo")
		return
	}
	redirect(w, r, "/app/filtros?ok=guardado")
}

func (s *Server) filterDelete(w http.ResponseWriter, r *http.Request) {
	u, _ := auth.UserFrom(r.Context())
	id, ok := pathID(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if err := s.Store.DeleteFilter(r.Context(), u.ID, id); err != nil {
		s.fail(w, r, err)
		return
	}
	redirect(w, r, "/app/filtros?ok=borrado")
}

func (s *Server) filterTest(w http.ResponseWriter, r *http.Request) {
	u, _ := auth.UserFrom(r.Context())
	id, ok := pathID(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	f, err := s.Store.GetFilter(r.Context(), u.ID, id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if f.AlertEmail == "" {
		f.AlertEmail = u.Email
	}
	if _, err := s.Alerts.SendTest(r.Context(), f); err != nil {
		if errors.Is(err, alerts.ErrNoResults) {
			redirect(w, r, "/app/filtros?err=sin_datos")
			return
		}
		slog.Error("email de prueba", "filtro", f.ID, "err", err)
		redirect(w, r, "/app/filtros?err=envio")
		return
	}
	redirect(w, r, "/app/filtros?ok=prueba")
}

// ---------- platform: cuenta ----------

func (s *Server) account(w http.ResponseWriter, r *http.Request) {
	s.render(w, "account", "layout", s.page(r, "Mi cuenta", "cuenta", nil))
}

func (s *Server) accountPassword(w http.ResponseWriter, r *http.Request) {
	u, _ := auth.UserFrom(r.Context())
	sess, _ := auth.SessionFrom(r.Context())
	cur, nw, conf := r.PostFormValue("current"), r.PostFormValue("new"), r.PostFormValue("confirm")
	if !auth.CheckPassword(u.PasswordHash, cur) {
		redirect(w, r, "/app/cuenta?err=actual")
		return
	}
	if nw != conf {
		redirect(w, r, "/app/cuenta?err=password")
		return
	}
	hash, err := auth.HashPassword(nw)
	if err != nil {
		redirect(w, r, "/app/cuenta?err=password")
		return
	}
	if err := s.Store.SetUserPassword(r.Context(), u.ID, hash, sess.TokenHash); err != nil {
		s.fail(w, r, err)
		return
	}
	redirect(w, r, "/app/cuenta?ok=password")
}

// ---------- admin: jobs ----------

type jobRow struct {
	store.Job
	Next    time.Time
	Last    store.Run
	Running bool
}

func (s *Server) jobRows(r *http.Request) ([]jobRow, error) {
	jobs, err := s.Store.ListJobs(r.Context())
	if err != nil {
		return nil, err
	}
	last, err := s.Store.LastRuns(r.Context())
	if err != nil {
		return nil, err
	}
	known := s.Manager.Names()
	var rows []jobRow
	for _, j := range jobs {
		if !slices.Contains(known, j.Name) {
			continue
		}
		rows = append(rows, jobRow{Job: j, Next: s.Scheduler.NextRun(j.Name), Last: last[j.Name], Running: s.Manager.IsRunning(j.Name)})
	}
	return rows, nil
}

func (s *Server) jobs(w http.ResponseWriter, r *http.Request) {
	rows, err := s.jobRows(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	runs, err := s.Store.ListRuns(r.Context(), "", 20)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	stats, err := s.Store.AuctionStats(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.render(w, "admin_jobs", "layout", s.page(r, "Jobs", "jobs", map[string]any{"Jobs": rows, "Runs": runs, "Stats": stats}))
}

func (s *Server) jobsTable(w http.ResponseWriter, r *http.Request) {
	rows, err := s.jobRows(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.render(w, "admin_jobs", "jobs_table", s.page(r, "", "", map[string]any{"Jobs": rows}))
}

func (s *Server) runsTable(w http.ResponseWriter, r *http.Request) {
	source := r.URL.Query().Get("source")
	limit := 20
	if source != "" {
		limit = 50
	}
	runs, err := s.Store.ListRuns(r.Context(), source, limit)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.render(w, "admin_jobs", "runs_table", runs)
}

type jobData struct {
	Job      store.Job
	Preset   scheduler.Preset
	Next     time.Time
	Runs     []store.Run
	Running  bool
	DayNames []string
	TZ       string
}

func (s *Server) jobData(r *http.Request, name string) (jobData, error) {
	j, err := s.Store.GetJob(r.Context(), name)
	if err != nil {
		return jobData{}, err
	}
	if !slices.Contains(s.Manager.Names(), name) {
		return jobData{}, store.ErrNotFound
	}
	runs, err := s.Store.ListRuns(r.Context(), name, 50)
	if err != nil {
		return jobData{}, err
	}
	return jobData{
		Job: j, Preset: scheduler.FromCron(j.Schedule), Next: s.Scheduler.NextRun(name), Runs: runs,
		Running:  s.Manager.IsRunning(name),
		DayNames: []string{"dom", "lun", "mar", "mié", "jue", "vie", "sáb"},
		TZ:       s.Location.String(),
	}, nil
}

func (s *Server) job(w http.ResponseWriter, r *http.Request) {
	d, err := s.jobData(r, r.PathValue("name"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.render(w, "admin_job", "layout", s.page(r, d.Job.Name, "jobs", d))
}

func (s *Server) jobSave(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	d, err := s.jobData(r, name)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	p := scheduler.Preset{Kind: r.PostFormValue("kind"), Cron: r.PostFormValue("cron")}
	switch p.Kind {
	case "daily":
		p.Time = r.PostFormValue("daily_time")
	case "weekdays":
		p.Time = r.PostFormValue("weekdays_time")
		for _, v := range r.PostForm["days"] {
			if n, err := strconv.Atoi(v); err == nil {
				p.Days = append(p.Days, n)
			}
		}
	case "hours":
		p.Every, _ = strconv.Atoi(r.PostFormValue("every"))
	}
	spec, err := p.ToCron()
	if err != nil {
		page := s.page(r, name, "jobs", d)
		page.Error = "Horario inválido: " + err.Error()
		w.WriteHeader(http.StatusUnprocessableEntity)
		s.render(w, "admin_job", "layout", page)
		return
	}
	j := store.Job{Name: name, Schedule: spec, Enabled: r.PostFormValue("enabled") != ""}
	if err := s.Store.UpdateJob(r.Context(), j); err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.Scheduler.Reload(); err != nil {
		s.fail(w, r, err)
		return
	}
	redirect(w, r, "/admin/jobs/"+name+"?ok=guardado")
}

func (s *Server) jobRun(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	u, _ := auth.UserFrom(r.Context())
	err := s.Manager.Start(name, runner.Options{Trigger: runner.TriggerManual, UserID: u.ID, Full: r.URL.Query().Get("full") == "1"})
	if errors.Is(err, runner.ErrUnknownJob) {
		http.NotFound(w, r)
		return
	}
	if err != nil && !errors.Is(err, runner.ErrRunning) {
		s.fail(w, r, err)
		return
	}
	if !isHX(r) {
		if errors.Is(err, runner.ErrRunning) {
			redirect(w, r, "/admin/jobs?err=corriendo")
			return
		}
		redirect(w, r, "/admin/jobs?ok=ejecutando")
		return
	}
	if r.URL.Query().Get("view") == "detail" {
		d, err := s.jobData(r, name)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		d.Running = true
		s.render(w, "admin_job", "job_run", d)
		return
	}
	s.jobsTable(w, r)
}

// ---------- admin: usuarios ----------

func (s *Server) users(w http.ResponseWriter, r *http.Request) {
	us, err := s.Store.ListUsers(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.render(w, "admin_users", "layout", s.page(r, "Usuarios", "usuarios", map[string]any{"Users": us}))
}

func (s *Server) userCreate(w http.ResponseWriter, r *http.Request) {
	email := strings.TrimSpace(r.PostFormValue("email"))
	role := r.PostFormValue("role")
	if !validEmail(email) {
		redirect(w, r, "/admin/usuarios?err=email")
		return
	}
	if role != store.RoleAdmin && role != store.RoleUser {
		redirect(w, r, "/admin/usuarios?err=datos")
		return
	}
	hash, err := auth.HashPassword(r.PostFormValue("password"))
	if err != nil {
		redirect(w, r, "/admin/usuarios?err=password")
		return
	}
	if _, err := s.Store.CreateUser(r.Context(), email, hash, role); err != nil {
		if errors.Is(err, store.ErrDuplicate) {
			redirect(w, r, "/admin/usuarios?err=duplicado")
			return
		}
		s.fail(w, r, err)
		return
	}
	redirect(w, r, "/admin/usuarios?ok=creado")
}

// targetUser carga el usuario del path y evita que un admin se modifique a sí mismo.
func (s *Server) targetUser(w http.ResponseWriter, r *http.Request) (store.User, bool) {
	me, _ := auth.UserFrom(r.Context())
	id, ok := pathID(r)
	if !ok {
		http.NotFound(w, r)
		return store.User{}, false
	}
	if id == me.ID {
		redirect(w, r, "/admin/usuarios?err=mismo")
		return store.User{}, false
	}
	u, err := s.Store.UserByID(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return store.User{}, false
	}
	return u, true
}

func (s *Server) userActive(w http.ResponseWriter, r *http.Request) {
	u, ok := s.targetUser(w, r)
	if !ok {
		return
	}
	if err := s.Store.SetUserActive(r.Context(), u.ID, r.PostFormValue("active") == "1"); err != nil {
		s.fail(w, r, err)
		return
	}
	redirect(w, r, "/admin/usuarios?ok=usuario")
}

func (s *Server) userRole(w http.ResponseWriter, r *http.Request) {
	u, ok := s.targetUser(w, r)
	if !ok {
		return
	}
	role := r.PostFormValue("role")
	if role != store.RoleAdmin && role != store.RoleUser {
		redirect(w, r, "/admin/usuarios?err=datos")
		return
	}
	if err := s.Store.SetUserRole(r.Context(), u.ID, role); err != nil {
		s.fail(w, r, err)
		return
	}
	redirect(w, r, "/admin/usuarios?ok=usuario")
}

func (s *Server) userPassword(w http.ResponseWriter, r *http.Request) {
	u, ok := s.targetUser(w, r)
	if !ok {
		return
	}
	hash, err := auth.HashPassword(r.PostFormValue("password"))
	if err != nil {
		redirect(w, r, "/admin/usuarios?err=password")
		return
	}
	if err := s.Store.SetUserPassword(r.Context(), u.ID, hash, ""); err != nil {
		s.fail(w, r, err)
		return
	}
	redirect(w, r, "/admin/usuarios?ok=password")
}
