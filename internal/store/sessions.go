package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

type Session struct {
	TokenHash string
	UserID    int64
	CSRFToken string
	ExpiresAt time.Time
}

func (s *Store) CreateSession(ctx context.Context, sess Session) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO sessions (token_hash, user_id, csrf_token, expires_at) VALUES (?, ?, ?, ?)`,
		sess.TokenHash, sess.UserID, sess.CSRFToken, sess.ExpiresAt.UTC().Format(TSFormat))
	return err
}

// SessionUser devuelve la sesión vigente y su usuario activo.
func (s *Store) SessionUser(ctx context.Context, tokenHash string) (Session, User, error) {
	var sess Session
	var exp string
	err := s.db.QueryRowContext(ctx, `SELECT token_hash, user_id, csrf_token, expires_at FROM sessions WHERE token_hash = ?`, tokenHash).
		Scan(&sess.TokenHash, &sess.UserID, &sess.CSRFToken, &exp)
	if errors.Is(err, sql.ErrNoRows) {
		return sess, User{}, ErrNotFound
	}
	if err != nil {
		return sess, User{}, err
	}
	sess.ExpiresAt, _ = time.Parse(TSFormat, exp)
	if time.Now().After(sess.ExpiresAt) {
		s.DeleteSession(ctx, tokenHash)
		return sess, User{}, ErrNotFound
	}
	u, err := s.UserByID(ctx, sess.UserID)
	if err != nil {
		return sess, u, err
	}
	if !u.Active {
		return sess, u, ErrNotFound
	}
	return sess, u, nil
}

func (s *Store) DeleteSession(ctx context.Context, tokenHash string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash = ?`, tokenHash)
	return err
}

func (s *Store) DeleteExpiredSessions(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at < ?`, now())
	return err
}
