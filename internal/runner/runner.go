// Package runner registra los jobs y los ejecuta guardando el resultado en el store.
package runner

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"sboe/internal/config"
	"sboe/internal/httpx"
	"sboe/internal/model"
	"sboe/internal/sources"
	"sboe/internal/sources/boe"
	"sboe/internal/sources/procuradores"
	"sboe/internal/sources/segsocial"
	"sboe/internal/store"
)

// Quién lanzó un run.
const (
	TriggerCLI      = "cli"
	TriggerSchedule = "schedule"
	TriggerManual   = "manual"
)

// ErrRunning: el job ya se está ejecutando.
var ErrRunning = errors.New("el job ya se está ejecutando")

// ErrUnknownJob: no hay un job con ese nombre.
var ErrUnknownJob = errors.New("job desconocido")

// Registry devuelve los jobs disponibles por nombre (cada uno con su propio cliente/cookies).
func Registry(cfg config.Config, dumpDir string) map[string]sources.Source {
	client := func(name string) *httpx.Client {
		return httpx.New(httpx.Options{Name: name, MinDelay: cfg.MinDelay, MaxDelay: cfg.MaxDelay, DumpDir: dumpDir})
	}
	all := []sources.Source{
		boe.New(client("boe")),
		procuradores.New(client("procuradores")),
		segsocial.NewRegulares(client("ss_regulares")),
		segsocial.NewAdjudicacion(client("ss_adjudicacion")),
	}
	m := make(map[string]sources.Source, len(all))
	for _, s := range all {
		m[s.Name()] = s
	}
	return m
}

// Names devuelve los nombres ordenados del registro.
func Names(reg map[string]sources.Source) []string {
	names := make([]string, 0, len(reg))
	for n := range reg {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// FinishedFunc se llama cuando un job guardó subastas nuevas; runStart es su first_seen.
// También se llama si el job falló a mitad pero alcanzó a guardar subastas nuevas.
type FinishedFunc func(ctx context.Context, source string, runStart time.Time)

// Options de una ejecución.
type Options struct {
	Trigger string // cli | schedule | manual
	UserID  int64  // quién la lanzó (0 = nadie)
	Full    bool   // descargar el detalle de todo, aunque no haya cambiado
}

// Manager ejecuta jobs: el mismo job nunca corre dos veces a la vez, y los jobs del mismo
// grupo (mismo servidor) esperan su turno.
type Manager struct {
	st         *store.Store
	reg        map[string]sources.Source
	onFinished FinishedFunc
	baseCtx    context.Context

	mu      sync.Mutex
	running map[string]bool
	groups  map[string]*sync.Mutex
	wg      sync.WaitGroup
}

// NewManager crea un Manager. baseCtx es el contexto de los runs lanzados con Start
// (al cancelarlo se interrumpen). onFinished puede ser nil.
func NewManager(baseCtx context.Context, st *store.Store, reg map[string]sources.Source, onFinished FinishedFunc) *Manager {
	m := &Manager{st: st, reg: reg, onFinished: onFinished, baseCtx: baseCtx, running: map[string]bool{}, groups: map[string]*sync.Mutex{}}
	for _, src := range reg {
		g := sources.GroupOf(src)
		if m.groups[g] == nil {
			m.groups[g] = &sync.Mutex{}
		}
	}
	return m
}

func (m *Manager) Names() []string { return Names(m.reg) }

// IsRunning indica si el job se está ejecutando (o esperando turno en su grupo).
func (m *Manager) IsRunning(name string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.running[name]
}

func (m *Manager) acquire(name string) (sources.Source, error) {
	job, ok := m.reg[name]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownJob, name)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.running[name] {
		return nil, ErrRunning
	}
	m.running[name] = true
	return job, nil
}

func (m *Manager) release(name string) {
	m.mu.Lock()
	delete(m.running, name)
	m.mu.Unlock()
}

// Run ejecuta un job y espera a que termine.
func (m *Manager) Run(ctx context.Context, name string, o Options) error {
	job, err := m.acquire(name)
	if err != nil {
		return err
	}
	defer m.release(name)
	g := m.groups[sources.GroupOf(job)]
	g.Lock()
	defer g.Unlock()
	return m.runOne(ctx, job, o)
}

// Start lanza el job en segundo plano y vuelve enseguida.
func (m *Manager) Start(name string, o Options) error {
	job, err := m.acquire(name)
	if err != nil {
		return err
	}
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		defer m.release(name)
		g := m.groups[sources.GroupOf(job)]
		g.Lock()
		defer g.Unlock()
		if err := m.runOne(m.baseCtx, job, o); err != nil {
			slog.Error("job terminado con error", "job", name, "err", err)
		}
	}()
	return nil
}

// Wait espera a los jobs lanzados con Start.
func (m *Manager) Wait() { m.wg.Wait() }

// RunAll ejecuta los jobs indicados: en paralelo entre grupos distintos y en secuencia dentro
// de cada grupo. Un job que falla no detiene a los demás.
func (m *Manager) RunAll(ctx context.Context, names []string, o Options) error {
	byGroup := map[string][]string{}
	var order []string
	for _, n := range names {
		src, ok := m.reg[n]
		if !ok {
			return fmt.Errorf("%w: %q", ErrUnknownJob, n)
		}
		g := sources.GroupOf(src)
		if _, ok := byGroup[g]; !ok {
			order = append(order, g)
		}
		byGroup[g] = append(byGroup[g], n)
	}
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		errs []error
	)
	for _, g := range order {
		wg.Add(1)
		go func(jobs []string) {
			defer wg.Done()
			for _, n := range jobs {
				if ctx.Err() != nil {
					return
				}
				if err := m.Run(ctx, n, o); err != nil {
					mu.Lock()
					errs = append(errs, fmt.Errorf("%s: %w", n, err))
					mu.Unlock()
				}
			}
		}(byGroup[g])
	}
	wg.Wait()
	if ctx.Err() != nil {
		errs = append(errs, ctx.Err())
	}
	return errors.Join(errs...)
}

// batchSize: cada cuántas subastas se escribe en la base.
const batchSize = 50

// sink guarda en la base lo que entrega la fuente, por lotes.
type sink struct {
	st      *store.Store
	source  string
	start   time.Time
	full    bool
	known   map[string]store.KnownAuction
	pending []model.Auction

	items, newItems int
}

func (s *sink) Known(id string) (sources.Known, bool) {
	k, ok := s.known[id]
	return sources.Known{Status: k.Status, DetailedAt: k.DetailedAt}, ok
}

func (s *sink) Full() bool { return s.full }

func (s *sink) Save(ctx context.Context, auctions ...model.Auction) error {
	s.pending = append(s.pending, auctions...)
	if len(s.pending) >= batchSize {
		return s.flush(ctx)
	}
	return nil
}

func (s *sink) Touch(ctx context.Context, ids ...string) error {
	if len(ids) == 0 {
		return nil
	}
	n, err := s.st.Touch(ctx, s.source, ids, s.start)
	s.items += n
	return err
}

func (s *sink) flush(ctx context.Context) error {
	if len(s.pending) == 0 {
		return nil
	}
	n, err := s.st.Upsert(ctx, s.source, s.pending, s.start)
	if err != nil {
		return err
	}
	s.items += len(s.pending)
	s.newItems += n
	s.pending = s.pending[:0]
	return nil
}

func (m *Manager) runOne(ctx context.Context, job sources.Source, o Options) (err error) {
	// Segundos exactos: first_seen de las subastas nuevas = este instante.
	start := time.Now().UTC().Truncate(time.Second)
	log := slog.With("job", job.Name(), "trigger", o.Trigger)
	log.Info("comenzando job", "completo", o.Full)

	runID, err := m.st.StartRun(ctx, job.Name(), start, o.Trigger, o.UserID)
	if err != nil {
		return err
	}
	known, err := m.st.Known(ctx, job.Name())
	if err != nil {
		return err
	}
	sk := &sink{st: m.st, source: job.Name(), start: start, full: o.Full, known: known}
	defer func() {
		// contexto propio: registrar el fin y avisar aunque ctx esté cancelado
		bg := context.Background()
		if ferr := m.st.FinishRun(bg, runID, sk.items, sk.newItems, err); ferr != nil {
			log.Error("registrando fin del job", "err", ferr)
		}
		if m.onFinished != nil && sk.newItems > 0 {
			m.onFinished(bg, job.Name(), start)
		}
	}()

	fetchErr := job.Fetch(ctx, sk)
	// lo descargado hasta el fallo también se guarda
	if ferr := sk.flush(context.Background()); ferr != nil && fetchErr == nil {
		fetchErr = ferr
	}
	if fetchErr != nil {
		log.Error("job falló", "err", fetchErr, "guardadas", sk.items, "nuevas", sk.newItems)
		return fetchErr
	}
	if sk.items == 0 {
		log.Warn("sin resultados; no se marca nada como retirado")
		return httpx.ErrEmpty
	}
	retired, err := m.st.Deactivate(ctx, job.Name(), start)
	if err != nil {
		return err
	}
	log.Info("job terminado", "subastas", sk.items, "nuevas", sk.newItems, "retiradas", retired, "duracion", time.Since(start).Round(time.Second))
	return nil
}
