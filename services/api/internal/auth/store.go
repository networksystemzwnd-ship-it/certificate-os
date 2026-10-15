package auth

import (
	"context"
	"errors"
	"time"
)

var (
	ErrNotFound        = errors.New("not found")
	ErrEmailTaken      = errors.New("email already registered")
	ErrInvalidLogin    = errors.New("invalid credentials")
	ErrUnauthenticated = errors.New("unauthenticated")
)

type UserStatus string

const (
	StatusActive    UserStatus = "active"
	StatusSuspended UserStatus = "suspended"
)

type User struct {
	ID           string
	Email        string
	PasswordHash string
	Status       UserStatus
	PlatformRole PlatformRole
	CreatedAt    time.Time
}

// Principal is the authenticated caller attached to a request context.
type Principal struct {
	UserID       string
	Email        string
	PlatformRole PlatformRole
	Status       UserStatus
}

func (p Principal) HasPlatformPermission(perm Permission) bool {
	return p.Status == StatusActive && HasPlatformPermission(p.PlatformRole, perm)
}

// Store is the persistence port. Implementations: pgstore (Postgres), authtest.MemStore (tests).
type Store interface {
	// CreateUser must return ErrEmailTaken on a duplicate (normalized) email.
	CreateUser(ctx context.Context, email, passwordHash string) (User, error)
	// UserByEmail returns ErrNotFound when absent.
	UserByEmail(ctx context.Context, email string) (User, error)
	UpdatePasswordHash(ctx context.Context, userID, hash string) error
	CreateSession(ctx context.Context, userID string, tokenHash []byte, expiresAt time.Time) error
	// PrincipalBySession returns ErrNotFound for unknown, revoked or expired sessions.
	// It must reflect the user's CURRENT status and role (no caching in the store).
	PrincipalBySession(ctx context.Context, tokenHash []byte, now time.Time) (Principal, error)
	RevokeSession(ctx context.Context, tokenHash []byte) error
	RevokeUserSessions(ctx context.Context, userID string) error
	SetUserStatus(ctx context.Context, userID string, status UserStatus) error
	SetPlatformRole(ctx context.Context, userID string, role PlatformRole) error
}
