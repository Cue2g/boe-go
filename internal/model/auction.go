// Package model define el modelo común de subasta que producen todas las fuentes.
package model

import "time"

// Tipos de bien normalizados.
const (
	AssetInmueble = "inmueble"
	AssetVehiculo = "vehiculo"
	AssetMueble   = "mueble"
)

// Auction es una subasta normalizada, independiente de la fuente.
// Los importes van en céntimos de euro; nil = dato no disponible.
type Auction struct {
	Source       string
	ExternalID   string
	AssetType    string // inmueble | vehiculo | mueble
	AssetTypeRaw string // texto original de la fuente
	Status       string // estado tal como lo da la fuente
	Locality     string
	Province     string // nombre canónico (normalize.Enrich); la fuente puede ponerlo crudo
	StartDate    *time.Time
	EndDate      *time.Time

	ValuationCents *int64 // valor subasta
	AppraisalCents *int64 // tasación
	MinBidCents    *int64 // puja mínima
	DepositCents   *int64 // depósito
	ClaimedCents   *int64 // cantidad reclamada
	ChargesCents   *int64 // cargas

	CadastralRef  string
	Description   string
	MainResidence *bool
	URL           string
	Extra         map[string]string // campos crudos adicionales de la fuente

	// Campos unificados (los rellena normalize.Enrich al guardar).
	ProvinceRaw  string // provincia tal como venía
	ProvinceCode string // código INE (2 dígitos)
	StatusCode   string // proxima | en_curso | adjudicacion_directa | cesion_remate | otro
	AssetSubtype string // vivienda | garaje | trastero | local | nave | solar | ...

	// Detailed indica que se descargó el detalle completo. Si es false, al guardar
	// se conservan los campos de detalle que ya había en la base.
	Detailed bool
}
