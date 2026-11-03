package auth

import (
	"context"
	"errors"
	"log/slog"
	"net/mail"
	"strings"
	"time"
)

type Service struct {
	Store      Store
	Hasher     Hasher
	SessionTTL time.Duration
	Now        func() time.Time

	dummyHash string // verified against when the email is unknown, to equalize timing
}

func NewService(store Store, hasher Hasher, sessionTTL time.Duration) (*Service, error) {
	dummy, err := hasher.Hash("dummy-password-for-timing-equalization")
	if err != nil {
		return nil, err
	}
	return &Service{Store: store, Hasher: hasher, SessionTTL: sessionTTL, Now: time.Now, dummyHash: dummy}, nil
}

// NormalizeEmail lowercases and trims; returns ok=false if it is not a plain address.
func NormalizeEmail(raw string) (string, bool) {
	e := strings.ToLower(strings.TrimSpace(raw))
	if e == "" || len(e) > 254 {
		return "", false
	}
	addr, err := mail.ParseAddress(e)
	if err != nil || addr.Address != e || addr.Name != "" { // reject "Name <a@b>" forms
		return "", false
	}
	return e, true
}

// Register creates a normal user. It never accepts a role: platform roles are granted
// out-of-band (CLI), and institute roles through memberships.
func (s *Service) Register(ctx context.Context, email, password string) (User, error) {
	hash, err := s.Hasher.Hash(password)
	if err != nil {
		return User{}, err
	}
	return s.Store.CreateUser(ctx, email, hash)
}

// Login verifies credentials and opens a session. All failure causes (unknown email, wrong
// password, suspended account) return the same ErrInvalidLogin.
func (s *Service) Login(ctx context.Context, email, password string) (token string, p Principal, err error) {
	u, err := s.Store.UserByEmail(ctx, email)
	switch {
	case errors.Is(err, ErrNotFound):
		s.Hasher.Verify(password, s.dummyHash)
		return "", Principal{}, ErrInvalidLogin
	case err != nil:
		return "", Principal{}, err
	}
	if !s.Hasher.Verify(password, u.PasswordHash) || u.Status != StatusActive {
		return "", Principal{}, ErrInvalidLogin
	}
	if s.Hasher.NeedsRehash(u.PasswordHash) {
		if h, herr := s.Hasher.Hash(password); herr == nil {
			_ = s.Store.UpdatePasswordHash(ctx, u.ID, h) // best effort
		}
	}
	token, hash, err := NewSessionToken()
	if err != nil {
		return "", Principal{}, err
	}
	if err := s.Store.CreateSession(ctx, u.ID, hash, s.Now().Add(s.SessionTTL)); err != nil {
		return "", Principal{}, err
	}
	return token, Principal{UserID: u.ID, Email: u.Email, PlatformRole: u.PlatformRole, Status: u.Status}, nil
}

// Authenticate resolves a session token to a principal. Suspended users are rejected
// immediately, even with a previously valid session.
func (s *Service) Authenticate(ctx context.Context, token string) (Principal, error) {
	if token == "" {
		return Principal{}, ErrUnauthenticated
	}
	p, err := s.Store.PrincipalBySession(ctx, HashToken(token), s.Now())
	if errors.Is(err, ErrNotFound) {
		return Principal{}, ErrUnauthenticated
	}
	if err != nil {
		return Principal{}, err
	}
	if p.Status != StatusActive {
		return Principal{}, ErrUnauthenticated
	}
	return p, nil
}

func (s *Service) Logout(ctx context.Context, token string) error {
	if token == "" {
		return nil
	}
	return s.Store.RevokeSession(ctx, HashToken(token))
}

// SuspendUser blocks the user and revokes all their sessions.
func (s *Service) SuspendUser(ctx context.Context, userID string) error {
	if err := s.Store.SetUserStatus(ctx, userID, StatusSuspended); err != nil {
		return err
	}
	return s.Store.RevokeUserSessions(ctx, userID)
}

func (s *Service) ReactivateUser(ctx context.Context, userID string) error {
	return s.Store.SetUserStatus(ctx, userID, StatusActive)
}

// DeadSessionGrace is how long an expired or revoked session row is kept before it is deleted.
const DeadSessionGrace = 7 * 24 * time.Hour

// PurgeDeadSessions deletes sessions that expired or were revoked more than DeadSessionGrace ago.
func (s *Service) PurgeDeadSessions(ctx context.Context) (int, error) {
	return s.Store.DeleteDeadSessions(ctx, s.Now().Add(-DeadSessionGrace))
}

// RunPurge purges once at start and then every interval until ctx ends; failures are logged and retried.
func (s *Service) RunPurge(ctx context.Context, every time.Duration, log *slog.Logger) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		if n, err := s.PurgeDeadSessions(ctx); err != nil && ctx.Err() == nil {
			log.Error("session purge failed", "err", err)
		} else if n > 0 {
			log.Info("dead sessions purged", "count", n)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
