// Package sources define el contrato común de las fuentes de subastas.
package sources

import (
	"context"
	"time"

	"sboe/internal/model"
)

// Source es un job que obtiene subastas de una web y las va entregando al Sink.
type Source interface {
	Name() string
	Fetch(ctx context.Context, sink Sink) error
}

// Grouper es opcional: las fuentes con el mismo grupo (mismo servidor) nunca se ejecutan
// a la vez. Sin Group(), el grupo es el propio nombre.
type Grouper interface {
	Group() string
}

// GroupOf devuelve el grupo de una fuente.
func GroupOf(s Source) string {
	if g, ok := s.(Grouper); ok {
		return g.Group()
	}
	return s.Name()
}

// Known es lo que ya sabemos de una subasta guardada.
type Known struct {
	Status     string
	DetailedAt time.Time
}

// Sink recibe las subastas mientras se descargan. Lo implementa el runner, que las guarda
// en la base por lotes.
type Sink interface {
	// Known indica si la subasta ya está guardada y cómo.
	Known(externalID string) (Known, bool)
	// Save guarda (o actualiza) subastas. Con Detailed=false se conservan los campos de
	// detalle que ya hubiera en la base.
	Save(ctx context.Context, auctions ...model.Auction) error
	// Touch marca como vistas subastas ya guardadas que no hizo falta volver a descargar.
	Touch(ctx context.Context, externalIDs ...string) error
	// Full indica que se pidió una actualización completa (descargar todo el detalle).
	Full() bool
}

// RefreshAfter: aunque no cambie, el detalle de una subasta se vuelve a descargar pasado este tiempo.
const RefreshAfter = 7 * 24 * time.Hour

// NeedsDetail decide si hay que descargar el detalle de una subasta: si es nueva, si cambió
// de estado en el listado, si su detalle es antiguo o si se pidió actualización completa.
func NeedsDetail(s Sink, externalID, listStatus string) bool {
	if s.Full() {
		return true
	}
	k, ok := s.Known(externalID)
	if !ok || k.DetailedAt.IsZero() {
		return true
	}
	if listStatus != "" && k.Status != listStatus {
		return true
	}
	return time.Since(k.DetailedAt) > RefreshAfter
}
