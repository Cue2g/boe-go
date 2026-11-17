// Package store persiste las subastas en SQLite.
package store

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"modernc.org/sqlite"

	"sboe/internal/model"
	"sboe/internal/normalize"
)

func init() {
	// norm(x): minúsculas y sin tildes, para comparar provincias/localidades entre fuentes.
	err := sqlite.RegisterDeterministicScalarFunction("norm", 1, func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
		switch v := args[0].(type) {
		case string:
			return normalize.Text(v), nil
		case []byte:
			return normalize.Text(string(v)), nil
		case nil:
			return nil, nil
		default:
			return v, nil
		}
	})
	if err != nil {
		panic(err)
	}
}

//go:embed migrations/*.sql
var migrations embed.FS

// TSFormat es el formato de las marcas de tiempo guardadas (UTC).
const TSFormat = time.RFC3339

type Store struct{ db *sql.DB }

// Open abre (o crea) la base de datos y aplica las migraciones pendientes.
func Open(path string) (*Store, error) {
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}
	dsn := "file:" + path + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(10000)&_pragma=foreign_keys(1)&_txlock=immediate"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(4)
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) DB() *sql.DB { return s.db }

func (s *Store) migrate() error {
	var version int
	if err := s.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	entries, err := migrations.ReadDir("migrations")
	if err != nil {
		return err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, e := range entries {
		n, err := strconv.Atoi(strings.SplitN(e.Name(), "_", 2)[0])
		if err != nil {
			return fmt.Errorf("migración con nombre inválido: %s", e.Name())
		}
		if n <= version {
			continue
		}
		sqlText, err := migrations.ReadFile("migrations/" + e.Name())
		if err != nil {
			return err
		}
		tx, err := s.db.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(string(sqlText)); err != nil {
			tx.Rollback()
			return fmt.Errorf("migración %s: %w", e.Name(), err)
		}
		if hook, ok := migrationHooks[n]; ok {
			if err := hook(tx); err != nil {
				tx.Rollback()
				return fmt.Errorf("migración %s (datos): %w", e.Name(), err)
			}
		}
		if _, err := tx.Exec(fmt.Sprintf("PRAGMA user_version = %d", n)); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

// StartRun registra el inicio de un job. triggeredBy: cli | schedule | manual; userID 0 = ninguno.
func (s *Store) StartRun(ctx context.Context, source string, at time.Time, triggeredBy string, userID int64) (int64, error) {
	res, err := s.db.ExecContext(ctx, `INSERT INTO runs (source, started_at, status, triggered_by, user_id) VALUES (?, ?, 'running', ?, NULLIF(?, 0))`,
		source, at.UTC().Format(TSFormat), triggeredBy, userID)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// FinishRun cierra el registro del job.
func (s *Store) FinishRun(ctx context.Context, id int64, items, newItems int, runErr error) error {
	status, msg := "ok", ""
	if runErr != nil {
		status, msg = "error", runErr.Error()
	}
	_, err := s.db.ExecContext(ctx, `UPDATE runs SET finished_at=?, status=?, items=?, new_items=?, error=NULLIF(?, '') WHERE id=?`,
		time.Now().UTC().Format(TSFormat), status, items, newItems, msg, id)
	return err
}

// columnas que escribe el upsert, en orden.
const upsertCols = `source, external_id, asset_type, asset_subtype, asset_type_raw, status, status_code,
  locality, province, province_code, province_raw, start_date, end_date,
  valuation_cents, appraisal_cents, min_bid_cents, deposit_cents, claimed_cents, charges_cents,
  cadastral_ref, description, main_residence, url, extra, detailed_at, first_seen, last_seen, active`

// Los campos de detalle usan COALESCE: si un guardado parcial (sin detalle) no los trae,
// se conserva lo que ya había. extra se mezcla con json_patch.
const upsertSQL = `
INSERT INTO auctions (` + upsertCols + `)
VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,1)
ON CONFLICT (source, external_id) DO UPDATE SET
  asset_type = excluded.asset_type,
  asset_subtype = excluded.asset_subtype,
  asset_type_raw = COALESCE(excluded.asset_type_raw, asset_type_raw),
  status = COALESCE(excluded.status, status),
  status_code = excluded.status_code,
  locality = COALESCE(excluded.locality, locality),
  province = COALESCE(excluded.province, province),
  province_code = COALESCE(excluded.province_code, province_code),
  province_raw = COALESCE(excluded.province_raw, province_raw),
  start_date = COALESCE(excluded.start_date, start_date),
  end_date = COALESCE(excluded.end_date, end_date),
  valuation_cents = COALESCE(excluded.valuation_cents, valuation_cents),
  appraisal_cents = COALESCE(excluded.appraisal_cents, appraisal_cents),
  min_bid_cents = COALESCE(excluded.min_bid_cents, min_bid_cents),
  deposit_cents = COALESCE(excluded.deposit_cents, deposit_cents),
  claimed_cents = COALESCE(excluded.claimed_cents, claimed_cents),
  charges_cents = COALESCE(excluded.charges_cents, charges_cents),
  cadastral_ref = COALESCE(excluded.cadastral_ref, cadastral_ref),
  description = COALESCE(excluded.description, description),
  main_residence = COALESCE(excluded.main_residence, main_residence),
  url = excluded.url,
  extra = json_patch(COALESCE(extra, '{}'), COALESCE(excluded.extra, '{}')),
  detailed_at = COALESCE(excluded.detailed_at, detailed_at),
  last_seen = excluded.last_seen,
  active = 1
RETURNING first_seen = last_seen`

// Upsert guarda un lote de subastas de una fuente (normalizándolas) en una transacción.
// runStart se usa como first_seen de las nuevas y last_seen de todas. Devuelve cuántas son nuevas.
func (s *Store) Upsert(ctx context.Context, source string, auctions []model.Auction, runStart time.Time) (newItems int, err error) {
	if len(auctions) == 0 {
		return 0, nil
	}
	ts := runStart.UTC().Format(TSFormat)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() {
		if err != nil {
			tx.Rollback()
		}
	}()
	stmt, err := tx.PrepareContext(ctx, upsertSQL)
	if err != nil {
		return 0, err
	}
	defer stmt.Close()

	for _, a := range auctions {
		a.Source = source
		normalize.Enrich(&a, runStart)
		var extra any
		if len(a.Extra) > 0 {
			b, err := json.Marshal(a.Extra)
			if err != nil {
				return 0, err
			}
			extra = string(b)
		}
		var detailedAt any
		if a.Detailed {
			detailedAt = ts
		}
		var isNew bool
		err = stmt.QueryRowContext(ctx,
			source, a.ExternalID, a.AssetType, nullStr(a.AssetSubtype), nullStr(a.AssetTypeRaw), nullStr(a.Status), nullStr(a.StatusCode),
			nullStr(a.Locality), nullStr(a.Province), nullStr(a.ProvinceCode), nullStr(a.ProvinceRaw), date(a.StartDate), date(a.EndDate),
			a.ValuationCents, a.AppraisalCents, a.MinBidCents, a.DepositCents, a.ClaimedCents, a.ChargesCents,
			nullStr(a.CadastralRef), nullStr(a.Description), boolInt(a.MainResidence), a.URL, extra, detailedAt,
			ts, ts,
		).Scan(&isNew)
		if err != nil {
			return 0, fmt.Errorf("guardando %s/%s: %w", source, a.ExternalID, err)
		}
		if isNew {
			newItems++
		}
	}
	return newItems, tx.Commit()
}

// Touch marca como vistas (last_seen = runStart, activas) subastas ya guardadas que no hizo
// falta volver a descargar. Devuelve cuántas existían.
func (s *Store) Touch(ctx context.Context, source string, externalIDs []string, runStart time.Time) (int, error) {
	ts := runStart.UTC().Format(TSFormat)
	total := 0
	for len(externalIDs) > 0 {
		n := min(len(externalIDs), 500)
		chunk := externalIDs[:n]
		externalIDs = externalIDs[n:]
		args := []any{ts, source}
		for _, id := range chunk {
			args = append(args, id)
		}
		res, err := s.db.ExecContext(ctx, `UPDATE auctions SET last_seen = ?, active = 1 WHERE source = ? AND external_id IN (`+
			strings.TrimSuffix(strings.Repeat("?,", len(chunk)), ",")+`)`, args...)
		if err != nil {
			return total, err
		}
		c, _ := res.RowsAffected()
		total += int(c)
	}
	return total, nil
}

// Deactivate marca como no activas las subastas de la fuente que no se vieron en este run.
// Solo debe llamarse cuando el run terminó bien.
func (s *Store) Deactivate(ctx context.Context, source string, runStart time.Time) (int, error) {
	res, err := s.db.ExecContext(ctx, `UPDATE auctions SET active = 0 WHERE source = ? AND last_seen < ? AND active = 1`,
		source, runStart.UTC().Format(TSFormat))
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// KnownAuction es lo que una fuente necesita saber de una subasta ya guardada.
type KnownAuction struct {
	Status     string
	DetailedAt time.Time // zero si nunca se descargó el detalle
}

// Known devuelve las subastas guardadas de una fuente, por external_id.
func (s *Store) Known(ctx context.Context, source string) (map[string]KnownAuction, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT external_id, COALESCE(status, ''), COALESCE(detailed_at, '') FROM auctions WHERE source = ?`, source)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]KnownAuction{}
	for rows.Next() {
		var id, st, det string
		if err := rows.Scan(&id, &st, &det); err != nil {
			return nil, err
		}
		k := KnownAuction{Status: st}
		k.DetailedAt, _ = time.Parse(TSFormat, det)
		out[id] = k
	}
	return out, rows.Err()
}

// Sync guarda todas las subastas de una fuente de una vez y desactiva las que no aparecen.
// Si auctions está vacío no desactiva nada (posible fallo de la web).
func (s *Store) Sync(ctx context.Context, source string, auctions []model.Auction, at time.Time) (int, error) {
	for i := range auctions {
		auctions[i].Detailed = true
	}
	n, err := s.Upsert(ctx, source, auctions, at)
	if err != nil || len(auctions) == 0 {
		return n, err
	}
	_, err = s.Deactivate(ctx, source, at)
	return n, err
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func date(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.Format("2006-01-02")
}

func boolInt(b *bool) any {
	if b == nil {
		return nil
	}
	if *b {
		return 1
	}
	return 0
}
