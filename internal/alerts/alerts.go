// Package alerts envía por email las subastas nuevas que cumplen los filtros de los usuarios.
package alerts

import (
	"bytes"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"slices"
	"strings"
	"time"

	"sboe/internal/filter"
	"sboe/internal/notify"
	"sboe/internal/store"
)

// MaxItems por email.
const MaxItems = 200

//go:embed email.html
var emailHTML string

// Funcs para plantillas (también las usa la web).
var Funcs = template.FuncMap{
	"euros": func(c *int64) string {
		if c == nil {
			return "—"
		}
		return filter.FormatEuros(*c)
	},
	"pct": func(p *float64) string {
		if p == nil {
			return "—"
		}
		return strings.Replace(fmt.Sprintf("%.1f %%", *p), ".", ",", 1)
	},
	"sourceLabel": func(s string) string {
		if l, ok := filter.SourceLabel[s]; ok {
			return l
		}
		return s
	},
	"subtypeLabel": func(s string) string {
		if l, ok := filter.SubtypeLabel[s]; ok {
			return l
		}
		return s
	},
	"statusLabel": func(s string) string {
		if l, ok := filter.StatusLabel[s]; ok {
			return l
		}
		return s
	},
	"assetLabel": func(s string) string {
		if l, ok := filter.AssetLabel[s]; ok {
			return l
		}
		return s
	},
}

var emailTmpl = template.Must(template.New("email").Funcs(Funcs).Parse(emailHTML))

// Service envía alertas.
type Service struct {
	st      *store.Store
	mailer  notify.Mailer
	baseURL string
	loc     *time.Location
}

func New(st *store.Store, mailer notify.Mailer, baseURL string, loc *time.Location) *Service {
	return &Service{st: st, mailer: mailer, baseURL: strings.TrimRight(baseURL, "/"), loc: loc}
}

// OnRun se llama al terminar un job: alerta de las subastas nuevas de ese run (first_seen = runStart).
func (s *Service) OnRun(ctx context.Context, source string, runStart time.Time) {
	filters, err := s.st.AlertFilters(ctx, store.AlertOnRun)
	if err != nil {
		slog.Error("alertas on_run", "err", err)
		return
	}
	for _, f := range filters {
		if len(f.Criteria.Sources) > 0 && !slices.Contains(f.Criteria.Sources, source) {
			continue
		}
		where, args := filter.Build(f.Criteria)
		where = "source = ? AND first_seen = ? AND " + where
		args = append([]any{source, runStart.UTC().Format(store.TSFormat)}, args...)
		s.send(ctx, f, where, args, time.Now())
	}
}

// Daily se llama cada minuto: envía las alertas diarias cuya hora coincide con now.
func (s *Service) Daily(ctx context.Context, now time.Time) {
	local := now.In(s.loc)
	hhmm := local.Format("15:04")
	filters, err := s.st.AlertFilters(ctx, store.AlertDaily)
	if err != nil {
		slog.Error("alertas diarias", "err", err)
		return
	}
	for _, f := range filters {
		if f.AlertTime != hhmm {
			continue
		}
		if f.LastAlertAt != nil && sameDay(f.LastAlertAt.In(s.loc), local) {
			continue // ya se procesó hoy
		}
		since := f.CreatedAt
		if f.LastAlertAt != nil {
			since = *f.LastAlertAt
		}
		where, args := filter.Build(f.Criteria)
		where = "first_seen > ? AND " + where
		args = append([]any{since.UTC().Format(store.TSFormat)}, args...)
		if !s.send(ctx, f, where, args, now) {
			// sin novedades: marcar igualmente para no reprocesar en el mismo día
			if err := s.st.TouchFilterAlert(ctx, f.ID, now); err != nil {
				slog.Error("alertas diarias", "filtro", f.ID, "err", err)
			}
		}
	}
}

// ErrNoResults: la prueba no encontró subastas.
var ErrNoResults = errors.New("el filtro no tiene subastas que enviar")

// SendTest envía un email de prueba con las subastas actuales que cumplen el filtro.
func (s *Service) SendTest(ctx context.Context, f store.Filter) (int, error) {
	where, args := filter.Build(f.Criteria)
	items, total, err := s.st.QueryAuctions(ctx, where, args, filter.OrderBy(""), MaxItems, 0)
	if err != nil {
		return 0, err
	}
	if total == 0 {
		return 0, ErrNoResults
	}
	return total, s.deliver(ctx, f, items, total, "[Prueba] ")
}

// send busca y envía; devuelve true si había subastas. at se guarda como last_alert_at.
func (s *Service) send(ctx context.Context, f store.Filter, where string, args []any, at time.Time) bool {
	items, total, err := s.st.QueryAuctions(ctx, where, args, filter.OrderBy(""), MaxItems, 0)
	if err != nil {
		slog.Error("alerta: consulta", "filtro", f.ID, "err", err)
		return false
	}
	if total == 0 {
		return false
	}
	sendErr := s.deliver(ctx, f, items, total, "")
	if sendErr != nil {
		slog.Error("alerta: envío", "filtro", f.ID, "to", f.AlertEmail, "err", sendErr)
	} else {
		slog.Info("alerta enviada", "filtro", f.ID, "to", f.AlertEmail, "subastas", total)
	}
	if err := s.st.RecordAlert(ctx, f.ID, at, total, sendErr, true); err != nil {
		slog.Error("alerta: registro", "filtro", f.ID, "err", err)
	}
	return true
}

func (s *Service) deliver(ctx context.Context, f store.Filter, items []store.AuctionRow, total int, prefix string) error {
	platform := ""
	if s.baseURL != "" {
		platform = s.baseURL + "/app/subastas?" + f.Criteria.Values().Encode()
	}
	data := struct {
		FilterName, Summary, PlatformURL string
		Count, More                      int
		Items                            []store.AuctionRow
	}{f.Name, f.Criteria.Summary(), platform, total, total - len(items), items}
	var buf bytes.Buffer
	if err := emailTmpl.Execute(&buf, data); err != nil {
		return err
	}
	subject := fmt.Sprintf("%s%d subasta%s nueva%s · %s", prefix, total, plural(total), plural(total), f.Name)
	return s.mailer.Send(ctx, notify.Message{To: f.AlertEmail, Subject: subject, HTML: buf.String()})
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func sameDay(a, b time.Time) bool {
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()
	return ay == by && am == bm && ad == bd
}

// PlatformLink construye un link al explorador con los criterios (para la UI).
func PlatformLink(c filter.Criteria) string { return "/app/subastas?" + c.Values().Encode() }
