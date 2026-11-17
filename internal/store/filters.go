package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"sboe/internal/filter"
)

const (
	AlertNone  = "none"
	AlertOnRun = "on_run"
	AlertDaily = "daily"
)

type Filter struct {
	ID          int64
	UserID      int64
	Name        string
	Criteria    filter.Criteria
	AlertMode   string
	AlertTime   string // HH:MM
	AlertEmail  string
	LastAlertAt *time.Time
	CreatedAt   time.Time
	UpdatedAt   time.Time

	// Datos de la última alerta (solo en listados).
	LastSentItems int
	LastSentError string
}

const filterCols = `f.id, f.user_id, f.name, f.criteria, f.alert_mode, COALESCE(f.alert_time,''), COALESCE(f.alert_email,''),
	f.last_alert_at, f.created_at, f.updated_at`

func scanFilter(sc interface{ Scan(...any) error }, extra ...any) (Filter, error) {
	var f Filter
	var crit, created, updated string
	var last sql.NullString
	dest := append([]any{&f.ID, &f.UserID, &f.Name, &crit, &f.AlertMode, &f.AlertTime, &f.AlertEmail, &last, &created, &updated}, extra...)
	if err := sc.Scan(dest...); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return f, ErrNotFound
		}
		return f, err
	}
	_ = json.Unmarshal([]byte(crit), &f.Criteria)
	if last.Valid {
		t, _ := time.Parse(TSFormat, last.String)
		f.LastAlertAt = &t
	}
	f.CreatedAt, _ = time.Parse(TSFormat, created)
	f.UpdatedAt, _ = time.Parse(TSFormat, updated)
	return f, nil
}

func (s *Store) SaveFilter(ctx context.Context, f *Filter) error {
	crit, err := json.Marshal(f.Criteria)
	if err != nil {
		return err
	}
	ts := now()
	if f.ID == 0 {
		res, err := s.db.ExecContext(ctx, `INSERT INTO filters (user_id, name, criteria, alert_mode, alert_time, alert_email, created_at, updated_at)
			VALUES (?, ?, ?, ?, NULLIF(?, ''), NULLIF(?, ''), ?, ?)`,
			f.UserID, f.Name, string(crit), f.AlertMode, f.AlertTime, f.AlertEmail, ts, ts)
		if err != nil {
			return err
		}
		f.ID, _ = res.LastInsertId()
		return nil
	}
	res, err := s.db.ExecContext(ctx, `UPDATE filters SET name = ?, criteria = ?, alert_mode = ?, alert_time = NULLIF(?, ''),
		alert_email = NULLIF(?, ''), updated_at = ? WHERE id = ? AND user_id = ?`,
		f.Name, string(crit), f.AlertMode, f.AlertTime, f.AlertEmail, ts, f.ID, f.UserID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// GetFilter devuelve un filtro del usuario.
func (s *Store) GetFilter(ctx context.Context, userID, id int64) (Filter, error) {
	return scanFilter(s.db.QueryRowContext(ctx, `SELECT `+filterCols+` FROM filters f WHERE f.id = ? AND f.user_id = ?`, id, userID))
}

func (s *Store) DeleteFilter(ctx context.Context, userID, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM filters WHERE id = ? AND user_id = ?`, id, userID)
	return err
}

func (s *Store) ListFilters(ctx context.Context, userID int64) ([]Filter, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+filterCols+`, COALESCE(a.items, 0), COALESCE(a.error, '')
		FROM filters f
		LEFT JOIN alerts_sent a ON a.id = (SELECT max(id) FROM alerts_sent WHERE filter_id = f.id)
		WHERE f.user_id = ? ORDER BY f.name`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Filter
	for rows.Next() {
		var f Filter
		f, err := scanFilter(rows, new(int), new(string))
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// AlertFilters devuelve los filtros con un modo de alerta dado, de usuarios activos.
func (s *Store) AlertFilters(ctx context.Context, mode string) ([]Filter, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+filterCols+` FROM filters f JOIN users u ON u.id = f.user_id
		WHERE f.alert_mode = ? AND u.active = 1 AND COALESCE(f.alert_email, '') <> ''`, mode)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Filter
	for rows.Next() {
		f, err := scanFilter(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// RecordAlert guarda el resultado de un envío y, si se indica, actualiza last_alert_at.
func (s *Store) RecordAlert(ctx context.Context, filterID int64, at time.Time, items int, sendErr error, touchLast bool) error {
	var msg any
	if sendErr != nil {
		msg = sendErr.Error()
	}
	ts := at.UTC().Format(TSFormat)
	if _, err := s.db.ExecContext(ctx, `INSERT INTO alerts_sent (filter_id, sent_at, items, error) VALUES (?, ?, ?, ?)`, filterID, ts, items, msg); err != nil {
		return err
	}
	if touchLast {
		_, err := s.db.ExecContext(ctx, `UPDATE filters SET last_alert_at = ? WHERE id = ?`, ts, filterID)
		return err
	}
	return nil
}

// TouchFilterAlert actualiza last_alert_at sin registrar envío (no había nada que enviar).
func (s *Store) TouchFilterAlert(ctx context.Context, filterID int64, at time.Time) error {
	_, err := s.db.ExecContext(ctx, `UPDATE filters SET last_alert_at = ? WHERE id = ?`, at.UTC().Format(TSFormat), filterID)
	return err
}
