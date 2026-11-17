package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// ErrNotFound se devuelve cuando no existe el registro.
var ErrNotFound = errors.New("no encontrado")

// ErrDuplicate se devuelve al violar una restricción UNIQUE.
var ErrDuplicate = errors.New("ya existe")

const (
	RoleAdmin = "admin"
	RoleUser  = "user"
)

type User struct {
	ID           int64
	Email        string
	PasswordHash string
	Role         string
	Active       bool
	CreatedAt    time.Time
}

func (u User) IsAdmin() bool { return u.Role == RoleAdmin }

const userCols = `id, email, password_hash, role, active, created_at`

func scanUser(sc interface{ Scan(...any) error }) (User, error) {
	var u User
	var created string
	if err := sc.Scan(&u.ID, &u.Email, &u.PasswordHash, &u.Role, &u.Active, &created); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return u, ErrNotFound
		}
		return u, err
	}
	u.CreatedAt, _ = time.Parse(TSFormat, created)
	return u, nil
}

func isUnique(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}

func now() string { return time.Now().UTC().Format(TSFormat) }

func (s *Store) CreateUser(ctx context.Context, email, passwordHash, role string) (User, error) {
	res, err := s.db.ExecContext(ctx, `INSERT INTO users (email, password_hash, role, created_at) VALUES (?, ?, ?, ?)`,
		strings.TrimSpace(email), passwordHash, role, now())
	if isUnique(err) {
		return User{}, ErrDuplicate
	}
	if err != nil {
		return User{}, err
	}
	id, _ := res.LastInsertId()
	return s.UserByID(ctx, id)
}

func (s *Store) UserByID(ctx context.Context, id int64) (User, error) {
	return scanUser(s.db.QueryRowContext(ctx, `SELECT `+userCols+` FROM users WHERE id = ?`, id))
}

func (s *Store) UserByEmail(ctx context.Context, email string) (User, error) {
	return scanUser(s.db.QueryRowContext(ctx, `SELECT `+userCols+` FROM users WHERE email = ?`, strings.TrimSpace(email)))
}

func (s *Store) ListUsers(ctx context.Context) ([]User, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+userCols+` FROM users ORDER BY email`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (s *Store) CountUsers(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM users`).Scan(&n)
	return n, err
}

func (s *Store) SetUserActive(ctx context.Context, id int64, active bool) error {
	if _, err := s.db.ExecContext(ctx, `UPDATE users SET active = ? WHERE id = ?`, active, id); err != nil {
		return err
	}
	if !active {
		_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ?`, id)
		return err
	}
	return nil
}

func (s *Store) SetUserRole(ctx context.Context, id int64, role string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE users SET role = ? WHERE id = ?`, role, id)
	return err
}

// SetUserPassword cambia la contraseña y cierra todas sus sesiones salvo keepSession (hash).
func (s *Store) SetUserPassword(ctx context.Context, id int64, passwordHash, keepSession string) error {
	if _, err := s.db.ExecContext(ctx, `UPDATE users SET password_hash = ? WHERE id = ?`, passwordHash, id); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ? AND token_hash <> ?`, id, keepSession)
	return err
}
