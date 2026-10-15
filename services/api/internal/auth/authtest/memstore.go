// Package authtest provides an in-memory auth.Store for tests in any package.
package authtest

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/certificate-os/api/internal/auth"
)

type session struct {
	userID  string
	expires time.Time
	revoked bool
}

type MemStore struct {
	mu       sync.Mutex
	n        int
	users    map[string]*auth.User // by id
	sessions map[string]*session   // by hex(tokenHash)
}

func NewMemStore() *MemStore {
	return &MemStore{users: map[string]*auth.User{}, sessions: map[string]*session{}}
}

func (m *MemStore) CreateUser(_ context.Context, email, hash string) (auth.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, u := range m.users {
		if u.Email == email {
			return auth.User{}, auth.ErrEmailTaken
		}
	}
	m.n++
	u := &auth.User{ID: fmt.Sprintf("user-%d", m.n), Email: email, PasswordHash: hash, Status: auth.StatusActive, CreatedAt: time.Now()}
	m.users[u.ID] = u
	return *u, nil
}

func (m *MemStore) UserByEmail(_ context.Context, email string) (auth.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, u := range m.users {
		if u.Email == email {
			return *u, nil
		}
	}
	return auth.User{}, auth.ErrNotFound
}

func (m *MemStore) UpdatePasswordHash(_ context.Context, id, hash string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if u, ok := m.users[id]; ok {
		u.PasswordHash = hash
		return nil
	}
	return auth.ErrNotFound
}

func key(h []byte) string { return fmt.Sprintf("%x", h) }

func (m *MemStore) CreateSession(_ context.Context, userID string, h []byte, exp time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessions[key(h)] = &session{userID: userID, expires: exp}
	return nil
}

func (m *MemStore) PrincipalBySession(_ context.Context, h []byte, now time.Time) (auth.Principal, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.sessions[key(h)]
	if s == nil || s.revoked || !now.Before(s.expires) {
		return auth.Principal{}, auth.ErrNotFound
	}
	u := m.users[s.userID]
	if u == nil {
		return auth.Principal{}, auth.ErrNotFound
	}
	return auth.Principal{UserID: u.ID, Email: u.Email, PlatformRole: u.PlatformRole, Status: u.Status}, nil
}

func (m *MemStore) RevokeSession(_ context.Context, h []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s := m.sessions[key(h)]; s != nil {
		s.revoked = true
	}
	return nil
}

func (m *MemStore) RevokeUserSessions(_ context.Context, userID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, s := range m.sessions {
		if s.userID == userID {
			s.revoked = true
		}
	}
	return nil
}

func (m *MemStore) SetUserStatus(_ context.Context, id string, st auth.UserStatus) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if u, ok := m.users[id]; ok {
		u.Status = st
		return nil
	}
	return auth.ErrNotFound
}

func (m *MemStore) SetPlatformRole(_ context.Context, id string, r auth.PlatformRole) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if u, ok := m.users[id]; ok {
		u.PlatformRole = r
		return nil
	}
	return auth.ErrNotFound
}
