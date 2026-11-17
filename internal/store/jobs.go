package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

type Job struct {
	Name     string
	Schedule string // expresión cron (5 campos)
	Enabled  bool
}

func (s *Store) ListJobs(ctx context.Context) ([]Job, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT name, schedule, enabled FROM jobs ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Job
	for rows.Next() {
		var j Job
		if err := rows.Scan(&j.Name, &j.Schedule, &j.Enabled); err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

func (s *Store) GetJob(ctx context.Context, name string) (Job, error) {
	var j Job
	err := s.db.QueryRowContext(ctx, `SELECT name, schedule, enabled FROM jobs WHERE name = ?`, name).Scan(&j.Name, &j.Schedule, &j.Enabled)
	if errors.Is(err, sql.ErrNoRows) {
		return j, ErrNotFound
	}
	return j, err
}

// EnsureJobs crea filas para jobs registrados que no estén en la tabla.
func (s *Store) EnsureJobs(ctx context.Context, names []string) error {
	for _, n := range names {
		if _, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO jobs (name, schedule, enabled) VALUES (?, '9 5 * * *', 0)`, n); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) UpdateJob(ctx context.Context, j Job) error {
	_, err := s.db.ExecContext(ctx, `UPDATE jobs SET schedule = ?, enabled = ?, updated_at = ? WHERE name = ?`,
		j.Schedule, j.Enabled, now(), j.Name)
	return err
}

type Run struct {
	ID          int64
	Source      string
	StartedAt   time.Time
	FinishedAt  *time.Time
	Status      string
	Items       int
	NewItems    int
	Error       string
	TriggeredBy string
	UserEmail   string
}

func (r Run) Duration() time.Duration {
	end := time.Now()
	if r.FinishedAt != nil {
		end = *r.FinishedAt
	}
	return end.Sub(r.StartedAt).Round(time.Second)
}

// ListRuns devuelve los últimos runs (de una fuente, o de todas si source == "").
func (s *Store) ListRuns(ctx context.Context, source string, limit int) ([]Run, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT r.id, r.source, r.started_at, r.finished_at, r.status, COALESCE(r.items, 0), COALESCE(r.new_items, 0),
		       COALESCE(r.error, ''), COALESCE(r.triggered_by, ''), COALESCE(u.email, '')
		FROM runs r LEFT JOIN users u ON u.id = r.user_id
		WHERE (? = '' OR r.source = ?) ORDER BY r.id DESC LIMIT ?`, source, source, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Run
	for rows.Next() {
		var r Run
		var started string
		var finished sql.NullString
		if err := rows.Scan(&r.ID, &r.Source, &started, &finished, &r.Status, &r.Items, &r.NewItems, &r.Error, &r.TriggeredBy, &r.UserEmail); err != nil {
			return nil, err
		}
		r.StartedAt, _ = time.Parse(TSFormat, started)
		if finished.Valid {
			t, _ := time.Parse(TSFormat, finished.String)
			r.FinishedAt = &t
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// LastRuns devuelve el último run de cada fuente.
func (s *Store) LastRuns(ctx context.Context) (map[string]Run, error) {
	runs, err := s.ListRuns(ctx, "", 200)
	if err != nil {
		return nil, err
	}
	out := map[string]Run{}
	for _, r := range runs {
		if _, ok := out[r.Source]; !ok {
			out[r.Source] = r
		}
	}
	return out, nil
}

// MarkInterruptedRuns cierra los runs que quedaron en 'running' (p. ej. tras reiniciar el servidor).
func (s *Store) MarkInterruptedRuns(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `UPDATE runs SET status = 'error', error = 'interrumpido (reinicio del servidor)', finished_at = ? WHERE status = 'running'`, now())
	return err
}
