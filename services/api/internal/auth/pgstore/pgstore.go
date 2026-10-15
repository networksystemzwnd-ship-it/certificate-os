// Package pgstore is the PostgreSQL implementation of auth.Store.
package pgstore

import (
	"context"
	"errors"
	"time"

	"github.com/certificate-os/api/internal/auth"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct{ Pool *pgxpool.Pool }

func New(pool *pgxpool.Pool) *Store { return &Store{Pool: pool} }

func (s *Store) CreateUser(ctx context.Context, email, hash string) (auth.User, error) {
	u := auth.User{Email: email, PasswordHash: hash}
	var status, role string
	err := s.Pool.QueryRow(ctx,
		`INSERT INTO users (email, password_hash) VALUES ($1, $2)
		 RETURNING id::text, status, coalesce(platform_role, ''), created_at`,
		email, hash).Scan(&u.ID, &status, &role, &u.CreatedAt)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return auth.User{}, auth.ErrEmailTaken
		}
		return auth.User{}, err
	}
	u.Status, u.PlatformRole = auth.UserStatus(status), auth.PlatformRole(role)
	return u, nil
}

func (s *Store) UserByEmail(ctx context.Context, email string) (auth.User, error) {
	var u auth.User
	var status, role string
	err := s.Pool.QueryRow(ctx,
		`SELECT id::text, email, password_hash, status, coalesce(platform_role, ''), created_at
		 FROM users WHERE email = $1`, email).
		Scan(&u.ID, &u.Email, &u.PasswordHash, &status, &role, &u.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return auth.User{}, auth.ErrNotFound
	}
	if err != nil {
		return auth.User{}, err
	}
	u.Status, u.PlatformRole = auth.UserStatus(status), auth.PlatformRole(role)
	return u, nil
}

func (s *Store) exec(ctx context.Context, sql string, args ...any) error {
	tag, err := s.Pool.Exec(ctx, sql, args...)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return auth.ErrNotFound
	}
	return nil
}

func (s *Store) UpdatePasswordHash(ctx context.Context, userID, hash string) error {
	return s.exec(ctx, `UPDATE users SET password_hash = $2 WHERE id = $1::uuid`, userID, hash)
}

func (s *Store) CreateSession(ctx context.Context, userID string, tokenHash []byte, expiresAt time.Time) error {
	_, err := s.Pool.Exec(ctx,
		`INSERT INTO sessions (user_id, token_hash, expires_at) VALUES ($1::uuid, $2, $3)`,
		userID, tokenHash, expiresAt)
	return err
}

// PrincipalBySession reads the user's current status/role in the same query, so suspensions and
// role changes take effect on the next request.
func (s *Store) PrincipalBySession(ctx context.Context, tokenHash []byte, now time.Time) (auth.Principal, error) {
	var p auth.Principal
	var status, role string
	err := s.Pool.QueryRow(ctx,
		`SELECT u.id::text, u.email, u.status, coalesce(u.platform_role, '')
		 FROM sessions s JOIN users u ON u.id = s.user_id
		 WHERE s.token_hash = $1 AND s.revoked_at IS NULL AND s.expires_at > $2`,
		tokenHash, now).Scan(&p.UserID, &p.Email, &status, &role)
	if errors.Is(err, pgx.ErrNoRows) {
		return auth.Principal{}, auth.ErrNotFound
	}
	if err != nil {
		return auth.Principal{}, err
	}
	p.Status, p.PlatformRole = auth.UserStatus(status), auth.PlatformRole(role)
	return p, nil
}

func (s *Store) RevokeSession(ctx context.Context, tokenHash []byte) error {
	_, err := s.Pool.Exec(ctx, `UPDATE sessions SET revoked_at = now() WHERE token_hash = $1 AND revoked_at IS NULL`, tokenHash)
	return err
}

func (s *Store) RevokeUserSessions(ctx context.Context, userID string) error {
	_, err := s.Pool.Exec(ctx, `UPDATE sessions SET revoked_at = now() WHERE user_id = $1::uuid AND revoked_at IS NULL`, userID)
	return err
}

func (s *Store) SetUserStatus(ctx context.Context, userID string, status auth.UserStatus) error {
	return s.exec(ctx, `UPDATE users SET status = $2 WHERE id = $1::uuid`, userID, string(status))
}

func (s *Store) SetPlatformRole(ctx context.Context, userID string, role auth.PlatformRole) error {
	if !role.Valid() {
		return errors.New("invalid platform role")
	}
	return s.exec(ctx, `UPDATE users SET platform_role = nullif($2, '') WHERE id = $1::uuid`, userID, string(role))
}
