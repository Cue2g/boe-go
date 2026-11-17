package runner

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"sboe/internal/model"
	"sboe/internal/sources"
	"sboe/internal/store"
)

// fake: lista n subastas; pide "detalle" solo si NeedsDetail; puede fallar tras failAfter.
type fake struct {
	name, group string
	n           int
	failAfter   int
	details     atomic.Int32
	delay       time.Duration
	active      *atomic.Int32
	maxActive   *atomic.Int32
}

func (f *fake) Name() string  { return f.name }
func (f *fake) Group() string { return f.group }
func (f *fake) Fetch(ctx context.Context, s sources.Sink) error {
	if f.active != nil {
		cur := f.active.Add(1)
		defer f.active.Add(-1)
		for {
			m := f.maxActive.Load()
			if cur <= m || f.maxActive.CompareAndSwap(m, cur) {
				break
			}
		}
	}
	time.Sleep(f.delay)
	var touch []string
	for i := 0; i < f.n; i++ {
		if f.failAfter > 0 && i == f.failAfter {
			return errors.New("web caída")
		}
		id := fmt.Sprint(i)
		if !sources.NeedsDetail(s, id, "abierta") {
			touch = append(touch, id)
			continue
		}
		f.details.Add(1)
		if err := s.Save(ctx, model.Auction{ExternalID: id, URL: "u", Status: "abierta", Detailed: true}); err != nil {
			return err
		}
	}
	return s.Touch(ctx, touch...)
}

func newStore(t *testing.T) *store.Store {
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func count(t *testing.T, st *store.Store, q string) int {
	var n int
	if err := st.DB().QueryRow(q).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestIncrementalAndPartial(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	var alerts []int
	var mu sync.Mutex
	f := &fake{name: "x", n: 120, failAfter: 80}
	m := NewManager(ctx, st, map[string]sources.Source{"x": f}, func(context.Context, string, time.Time) {
		mu.Lock()
		alerts = append(alerts, 1)
		mu.Unlock()
	})

	// 1) falla a mitad: lo descargado queda guardado y se avisa de las nuevas
	if err := m.Run(ctx, "x", Options{Trigger: TriggerCLI}); err == nil {
		t.Fatal("esperaba error")
	}
	if n := count(t, st, "SELECT count(*) FROM auctions"); n != 80 {
		t.Fatalf("parcial: %d guardadas", n)
	}
	if len(alerts) != 1 {
		t.Fatalf("alertas tras fallo parcial: %d", len(alerts))
	}

	// 2) completa: solo pide detalle de las 40 que faltan
	time.Sleep(1100 * time.Millisecond) // otro segundo → otro runStart
	f.failAfter, f.details = 0, atomic.Int32{}
	if err := m.Run(ctx, "x", Options{Trigger: TriggerCLI}); err != nil {
		t.Fatal(err)
	}
	if d := f.details.Load(); d != 40 {
		t.Fatalf("detalles en segunda pasada: %d", d)
	}
	if n := count(t, st, "SELECT count(*) FROM auctions WHERE active = 1"); n != 120 {
		t.Fatalf("activas: %d", n)
	}

	// 3) sin cambios: ningún detalle; las que desaparecen se retiran
	time.Sleep(1100 * time.Millisecond)
	f.n, f.details = 100, atomic.Int32{}
	if err := m.Run(ctx, "x", Options{Trigger: TriggerCLI}); err != nil {
		t.Fatal(err)
	}
	if d := f.details.Load(); d != 0 {
		t.Fatalf("detalles sin cambios: %d", d)
	}
	if n := count(t, st, "SELECT count(*) FROM auctions WHERE active = 1"); n != 100 {
		t.Fatalf("activas tras retirar: %d", n)
	}
	var items, newItems int
	st.DB().QueryRow("SELECT items, new_items FROM runs ORDER BY id DESC LIMIT 1").Scan(&items, &newItems)
	if items != 100 || newItems != 0 {
		t.Fatalf("run: items=%d nuevas=%d", items, newItems)
	}

	// 4) completo: vuelve a pedir todo
	f.details = atomic.Int32{}
	if err := m.Run(ctx, "x", Options{Trigger: TriggerCLI, Full: true}); err != nil {
		t.Fatal(err)
	}
	if d := f.details.Load(); d != 100 {
		t.Fatalf("completo: %d", d)
	}
}

func TestGroupsRunInParallel(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	var activeAll, maxAll atomic.Int32
	reg := map[string]sources.Source{
		"a1": &fake{name: "a1", group: "A", n: 1, delay: 300 * time.Millisecond, active: &activeAll, maxActive: &maxAll},
		"a2": &fake{name: "a2", group: "A", n: 1, delay: 300 * time.Millisecond, active: &activeAll, maxActive: &maxAll},
		"b":  &fake{name: "b", group: "B", n: 1, delay: 300 * time.Millisecond, active: &activeAll, maxActive: &maxAll},
		"c":  &fake{name: "c", group: "C", n: 1, delay: 300 * time.Millisecond, active: &activeAll, maxActive: &maxAll},
	}
	m := NewManager(ctx, st, reg, nil)
	t0 := time.Now()
	if err := m.RunAll(ctx, []string{"a1", "a2", "b", "c"}, Options{Trigger: TriggerCLI}); err != nil {
		t.Fatal(err)
	}
	el := time.Since(t0)
	// grupos en paralelo (máx 3 a la vez), a1 y a2 en serie → ~600 ms, no 1200 ms
	if maxAll.Load() != 3 || el > 1100*time.Millisecond {
		t.Fatalf("max simultáneos=%d duración=%v", maxAll.Load(), el)
	}
	// el mismo grupo nunca a la vez aunque se lancen por separado
	if err := m.Start("a1", Options{}); err != nil {
		t.Fatal(err)
	}
	if err := m.Start("a1", Options{}); !errors.Is(err, ErrRunning) {
		t.Fatalf("doble ejecución: %v", err)
	}
	m.Wait()
}
