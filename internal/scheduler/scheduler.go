// Package scheduler programa los jobs según la tabla `jobs` y dispara las alertas diarias.
package scheduler

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/robfig/cron/v3"

	"sboe/internal/runner"
	"sboe/internal/store"
)

var parser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)

// Validate comprueba una expresión cron de 5 campos.
func Validate(spec string) error {
	if strings.TrimSpace(spec) == "" {
		return fmt.Errorf("horario vacío")
	}
	_, err := parser.Parse(spec)
	return err
}

// Next devuelve la próxima ejecución de spec después de t (zero si es inválida).
func Next(spec string, t time.Time, loc *time.Location) time.Time {
	s, err := parser.Parse(spec)
	if err != nil {
		return time.Time{}
	}
	return s.Next(t.In(loc))
}

// Scheduler ejecuta los jobs habilitados y un tick por minuto.
type Scheduler struct {
	st   *store.Store
	mgr  *runner.Manager
	loc  *time.Location
	tick func(ctx context.Context, now time.Time)
	ctx  context.Context

	mu      sync.Mutex
	c       *cron.Cron
	entries map[string]cron.EntryID
}

// New crea el scheduler. tick (puede ser nil) se llama cada minuto (alertas diarias).
func New(ctx context.Context, st *store.Store, mgr *runner.Manager, loc *time.Location, tick func(context.Context, time.Time)) *Scheduler {
	return &Scheduler{
		st: st, mgr: mgr, loc: loc, tick: tick, ctx: ctx,
		c:       cron.New(cron.WithParser(parser), cron.WithLocation(loc)),
		entries: map[string]cron.EntryID{},
	}
}

// Start carga los jobs y arranca el cron.
func (s *Scheduler) Start() error {
	if s.tick != nil {
		if _, err := s.c.AddFunc("* * * * *", func() { s.tick(s.ctx, time.Now()) }); err != nil {
			return err
		}
	}
	if err := s.Reload(); err != nil {
		return err
	}
	s.c.Start()
	return nil
}

// Stop detiene el cron (no espera a los jobs en curso).
func (s *Scheduler) Stop() { s.c.Stop() }

// Reload vuelve a leer la tabla jobs.
func (s *Scheduler) Reload() error {
	jobs, err := s.st.ListJobs(s.ctx)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for name, id := range s.entries {
		s.c.Remove(id)
		delete(s.entries, name)
	}
	for _, j := range jobs {
		if !j.Enabled {
			continue
		}
		name := j.Name
		id, err := s.c.AddFunc(j.Schedule, func() {
			if err := s.mgr.Start(name, runner.Options{Trigger: runner.TriggerSchedule}); err != nil {
				slog.Warn("ejecución programada omitida", "job", name, "err", err)
			}
		})
		if err != nil {
			slog.Error("horario inválido", "job", name, "schedule", j.Schedule, "err", err)
			continue
		}
		s.entries[name] = id
	}
	slog.Info("horarios cargados", "jobs_programados", len(s.entries))
	return nil
}

// NextRun devuelve la próxima ejecución programada del job (zero si no está programado).
func (s *Scheduler) NextRun(name string) time.Time {
	s.mu.Lock()
	id, ok := s.entries[name]
	s.mu.Unlock()
	if !ok {
		return time.Time{}
	}
	return s.c.Entry(id).Next
}

// Preset es un horario expresado de forma amigable para la UI.
type Preset struct {
	Kind  string // daily | hours | weekdays | custom
	Time  string // HH:MM (daily, weekdays)
	Every int    // horas (hours)
	Days  []int  // 0=domingo..6 (weekdays)
	Cron  string // custom
}

// ToCron convierte un preset a expresión cron.
func (p Preset) ToCron() (string, error) {
	hm := func() (int, int, error) {
		t, err := time.Parse("15:04", p.Time)
		if err != nil {
			return 0, 0, fmt.Errorf("hora inválida %q (usa HH:MM)", p.Time)
		}
		return t.Hour(), t.Minute(), nil
	}
	var spec string
	switch p.Kind {
	case "daily":
		h, m, err := hm()
		if err != nil {
			return "", err
		}
		spec = fmt.Sprintf("%d %d * * *", m, h)
	case "hours":
		if p.Every < 1 || p.Every > 23 {
			return "", fmt.Errorf("cada cuántas horas: entre 1 y 23")
		}
		spec = fmt.Sprintf("0 */%d * * *", p.Every)
	case "weekdays":
		h, m, err := hm()
		if err != nil {
			return "", err
		}
		if len(p.Days) == 0 {
			return "", fmt.Errorf("elige al menos un día")
		}
		ds := make([]string, len(p.Days))
		for i, d := range p.Days {
			if d < 0 || d > 6 {
				return "", fmt.Errorf("día inválido")
			}
			ds[i] = strconv.Itoa(d)
		}
		spec = fmt.Sprintf("%d %d * * %s", m, h, strings.Join(ds, ","))
	case "custom":
		spec = strings.TrimSpace(p.Cron)
	default:
		return "", fmt.Errorf("tipo de horario desconocido")
	}
	return spec, Validate(spec)
}

// FromCron intenta reconocer un preset a partir de una expresión cron.
func FromCron(spec string) Preset {
	f := strings.Fields(spec)
	if len(f) == 5 && f[2] == "*" && f[3] == "*" {
		m, errM := strconv.Atoi(f[0])
		h, errH := strconv.Atoi(f[1])
		if errM == nil && errH == nil {
			t := fmt.Sprintf("%02d:%02d", h, m)
			if f[4] == "*" {
				return Preset{Kind: "daily", Time: t, Every: 6}
			}
			var days []int
			ok := true
			for _, d := range strings.Split(f[4], ",") {
				n, err := strconv.Atoi(d)
				if err != nil || n < 0 || n > 6 {
					ok = false
					break
				}
				days = append(days, n)
			}
			if ok {
				return Preset{Kind: "weekdays", Time: t, Days: days, Every: 6}
			}
		}
		if f[0] == "0" && strings.HasPrefix(f[1], "*/") && f[4] == "*" {
			if n, err := strconv.Atoi(f[1][2:]); err == nil {
				return Preset{Kind: "hours", Every: n, Time: "05:00"}
			}
		}
	}
	return Preset{Kind: "custom", Cron: spec, Time: "05:00", Every: 6}
}

// Describe da una descripción legible de una expresión cron.
func Describe(spec string) string {
	p := FromCron(spec)
	names := []string{"dom", "lun", "mar", "mié", "jue", "vie", "sáb"}
	switch p.Kind {
	case "daily":
		return "Todos los días a las " + p.Time
	case "hours":
		return fmt.Sprintf("Cada %d horas", p.Every)
	case "weekdays":
		ds := make([]string, len(p.Days))
		for i, d := range p.Days {
			ds[i] = names[d]
		}
		return strings.Join(ds, ", ") + " a las " + p.Time
	}
	return "cron: " + spec
}
