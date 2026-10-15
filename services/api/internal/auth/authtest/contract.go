package authtest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/certificate-os/api/internal/auth"
)

// RunStoreContract verifies any auth.Store implementation. newStore must return an EMPTY store.
// It runs against MemStore always and against pgstore when TEST_DATABASE_URL is set.
func RunStoreContract(t *testing.T, newStore func(t *testing.T) auth.Store) {
	ctx := context.Background()
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	hash := func(s string) []byte { return auth.HashToken(s) }

	t.Run("users", func(t *testing.T) {
		s := newStore(t)
		u, err := s.CreateUser(ctx, "a@x.com", "h1")
		if err != nil || u.ID == "" || u.Status != auth.StatusActive || u.PlatformRole != auth.PlatformNone {
			t.Fatalf("create: %+v %v", u, err)
		}
		if _, err := s.CreateUser(ctx, "a@x.com", "h2"); !errors.Is(err, auth.ErrEmailTaken) {
			t.Fatalf("duplicate: %v", err)
		}
		got, err := s.UserByEmail(ctx, "a@x.com")
		if err != nil || got.ID != u.ID || got.PasswordHash != "h1" {
			t.Fatalf("lookup: %+v %v", got, err)
		}
		if _, err := s.UserByEmail(ctx, "none@x.com"); !errors.Is(err, auth.ErrNotFound) {
			t.Fatalf("missing: %v", err)
		}
		if err := s.UpdatePasswordHash(ctx, u.ID, "h3"); err != nil {
			t.Fatal(err)
		}
		if got, _ := s.UserByEmail(ctx, "a@x.com"); got.PasswordHash != "h3" {
			t.Fatal("hash not updated")
		}
	})

	t.Run("sessions", func(t *testing.T) {
		s := newStore(t)
		u, _ := s.CreateUser(ctx, "a@x.com", "h")
		if err := s.CreateSession(ctx, u.ID, hash("t1"), now.Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
		p, err := s.PrincipalBySession(ctx, hash("t1"), now)
		if err != nil || p.UserID != u.ID || p.Email != "a@x.com" {
			t.Fatalf("principal: %+v %v", p, err)
		}
		if _, err := s.PrincipalBySession(ctx, hash("unknown"), now); !errors.Is(err, auth.ErrNotFound) {
			t.Fatalf("unknown token: %v", err)
		}
		if _, err := s.PrincipalBySession(ctx, hash("t1"), now.Add(time.Hour)); !errors.Is(err, auth.ErrNotFound) {
			t.Fatalf("expired at boundary: %v", err)
		}
		_ = s.RevokeSession(ctx, hash("t1"))
		if _, err := s.PrincipalBySession(ctx, hash("t1"), now); !errors.Is(err, auth.ErrNotFound) {
			t.Fatal("revoked session still valid")
		}
		_ = s.CreateSession(ctx, u.ID, hash("t2"), now.Add(time.Hour))
		_ = s.CreateSession(ctx, u.ID, hash("t3"), now.Add(time.Hour))
		_ = s.RevokeUserSessions(ctx, u.ID)
		for _, tk := range []string{"t2", "t3"} {
			if _, err := s.PrincipalBySession(ctx, hash(tk), now); !errors.Is(err, auth.ErrNotFound) {
				t.Fatalf("%s survived RevokeUserSessions", tk)
			}
		}
	})

	t.Run("status and role are read live", func(t *testing.T) {
		s := newStore(t)
		u, _ := s.CreateUser(ctx, "a@x.com", "h")
		_ = s.CreateSession(ctx, u.ID, hash("t"), now.Add(time.Hour))
		if err := s.SetPlatformRole(ctx, u.ID, auth.PlatformOps); err != nil {
			t.Fatal(err)
		}
		if err := s.SetUserStatus(ctx, u.ID, auth.StatusSuspended); err != nil {
			t.Fatal(err)
		}
		p, err := s.PrincipalBySession(ctx, hash("t"), now)
		if err != nil || p.PlatformRole != auth.PlatformOps || p.Status != auth.StatusSuspended {
			t.Fatalf("live read: %+v %v", p, err)
		}
		if err := s.SetPlatformRole(ctx, u.ID, auth.PlatformNone); err != nil {
			t.Fatal(err)
		}
		if p, _ := s.PrincipalBySession(ctx, hash("t"), now); p.PlatformRole != auth.PlatformNone {
			t.Fatal("role not cleared")
		}
	})
}
