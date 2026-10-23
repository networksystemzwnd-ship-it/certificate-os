// Package stafftest provides an in-memory staff store and the shared store contract.
package stafftest

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/certificate-os/api/internal/audit"
	"github.com/certificate-os/api/internal/auth"
	"github.com/certificate-os/api/internal/staff"
	"github.com/certificate-os/api/internal/tenancy"
	"github.com/certificate-os/api/internal/tenancy/tenancytest"
)

type rec struct {
	staff.Invitation
	instituteID string
	tokenHash   string
}

// MemStore keeps invitations itself and members in the shared tenancytest.MemStore, so a membership
// created by accepting an invitation is visible through the ordinary tenancy store.
type MemStore struct {
	mu    sync.Mutex
	n     int
	T     *tenancytest.MemStore
	invs  []*rec
	Audit audit.Writer
	// UserIDByEmail resolves an email to a user id (the Postgres store joins the users table).
	UserIDByEmail func(email string) string
}

var (
	_ staff.Store    = (*MemStore)(nil)
	_ staff.Acceptor = (*MemStore)(nil)
)

func NewMemStore(t *tenancytest.MemStore, w audit.Writer) *MemStore {
	return &MemStore{T: t, Audit: w, UserIDByEmail: func(string) string { return "" }}
}

func (m *MemStore) record(ctx context.Context, instituteID string, ev audit.Event, entityID string) error {
	ev.EntityID, ev.InstituteID = entityID, instituteID
	if m.Audit == nil {
		return nil
	}
	return m.Audit.Record(ctx, ev)
}

func cp(i staff.Invitation) staff.Invitation {
	i.Grants = append([]auth.Permission(nil), i.Grants...)
	return i
}

func live(r *rec, now time.Time) bool {
	return r.Status == staff.InvitationPending && now.Before(r.ExpiresAt)
}

func (m *MemStore) CreateInvitation(ctx context.Context, s tenancy.Scope, in staff.NewInvitation, now time.Time, ev audit.Event) (staff.Invitation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	inst := s.InstituteID()
	pending := 0
	for _, r := range m.invs {
		if r.instituteID == inst && r.Email == in.Email && live(r, now) {
			return staff.Invitation{}, staff.ErrAlreadyInvited
		}
		if r.instituteID == inst && live(r, now) {
			pending++
		}
	}
	if uid := m.UserIDByEmail(in.Email); uid != "" {
		if mv, ok := m.T.MemberByUser(inst, uid); ok && mv.Status == tenancy.MembershipActive {
			return staff.Invitation{}, staff.ErrAlreadyMember
		}
	}
	if pending >= staff.MaxPendingInvites {
		return staff.Invitation{}, staff.ErrTooManyPending
	}
	m.n++
	r := &rec{instituteID: inst, tokenHash: in.TokenHash, Invitation: staff.Invitation{ID: fmt.Sprintf("invite-%d", m.n), Email: in.Email, Role: in.Role,
		Grants: append([]auth.Permission(nil), in.Grants...), Status: staff.InvitationPending, InvitedBy: in.InvitedBy, CreatedAt: now, ExpiresAt: in.ExpiresAt}}
	if err := m.record(ctx, inst, ev, r.ID); err != nil {
		return staff.Invitation{}, err
	}
	for _, o := range m.invs { // an expired pending invitation for the same email gives way
		if o.instituteID == inst && o.Email == in.Email && o.Status == staff.InvitationPending {
			t := now
			o.Status, o.RevokedAt = staff.InvitationRevoked, &t
		}
	}
	m.invs = append(m.invs, r)
	return cp(r.Invitation), nil
}

func (m *MemStore) ListInvitations(_ context.Context, s tenancy.Scope, limit int) ([]staff.Invitation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []staff.Invitation{}
	for i := len(m.invs) - 1; i >= 0; i-- {
		if m.invs[i].instituteID == s.InstituteID() {
			out = append(out, cp(m.invs[i].Invitation))
		}
	}
	sort.SliceStable(out, func(a, b int) bool { return out[a].CreatedAt.After(out[b].CreatedAt) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *MemStore) RevokeInvitation(ctx context.Context, s tenancy.Scope, id string, now time.Time, ev audit.Event) (staff.Invitation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, r := range m.invs {
		if r.instituteID == s.InstituteID() && r.ID == id {
			if r.Status != staff.InvitationPending {
				return staff.Invitation{}, staff.ErrState
			}
			if err := m.record(ctx, r.instituteID, ev, r.ID); err != nil {
				return staff.Invitation{}, err
			}
			t := now
			r.Status, r.RevokedAt = staff.InvitationRevoked, &t
			return cp(r.Invitation), nil
		}
	}
	return staff.Invitation{}, staff.ErrNotFound
}

func (m *MemStore) ChangeMember(ctx context.Context, s tenancy.Scope, membershipID string, role auth.InstituteRole, grants []auth.Permission, ev audit.Event) (tenancy.MemberView, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, ok := m.T.MemberByID(s.InstituteID(), membershipID)
	if !ok || cur.Status != tenancy.MembershipActive {
		return tenancy.MemberView{}, staff.ErrNotFound
	}
	if cur.Role == auth.InstituteOwner {
		return tenancy.MemberView{}, staff.ErrProtected
	}
	meta := map[string]any{}
	for k, v := range ev.Metadata {
		meta[k] = v
	}
	meta["from_role"] = string(cur.Role)
	ev.Metadata = meta
	if err := m.record(ctx, s.InstituteID(), ev, membershipID); err != nil {
		return tenancy.MemberView{}, err
	}
	return m.T.UpdateMember(s.InstituteID(), membershipID, role, grants, tenancy.MembershipActive)
}

func (m *MemStore) RemoveMember(ctx context.Context, s tenancy.Scope, membershipID string, ev audit.Event) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, ok := m.T.MemberByID(s.InstituteID(), membershipID)
	if !ok || cur.Status != tenancy.MembershipActive {
		return staff.ErrNotFound
	}
	if cur.Role == auth.InstituteOwner {
		return staff.ErrProtected
	}
	if err := m.record(ctx, s.InstituteID(), ev, membershipID); err != nil {
		return err
	}
	_, err := m.T.UpdateMember(s.InstituteID(), membershipID, cur.Role, cur.Grants, tenancy.MembershipRemoved)
	return err
}

func (m *MemStore) AcceptInvitation(ctx context.Context, tokenHash, userID, userEmail string, now time.Time, ev audit.Event) (staff.Accepted, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var r *rec
	for _, x := range m.invs {
		if x.tokenHash == tokenHash {
			r = x
		}
	}
	if r == nil || !live(r, now) || !strings.EqualFold(r.Email, userEmail) {
		return staff.Accepted{}, staff.ErrInvalidInvitation
	}
	inst, err := m.T.InstituteByID(ctx, r.instituteID)
	if err != nil || inst.Status != tenancy.InstituteActive {
		return staff.Accepted{}, staff.ErrInvalidInvitation
	}
	existing, has := m.T.MemberByUser(r.instituteID, userID)
	if has && existing.Status == tenancy.MembershipActive {
		return staff.Accepted{}, staff.ErrInvalidInvitation
	}
	meta := map[string]any{}
	for k, v := range ev.Metadata {
		meta[k] = v
	}
	meta["role"] = string(r.Role)
	ev.Metadata = meta
	if err := m.record(ctx, r.instituteID, ev, r.ID); err != nil {
		return staff.Accepted{}, err
	}
	var mv tenancy.MemberView
	if has {
		mv, err = m.T.UpdateMember(r.instituteID, existing.MembershipID, r.Role, r.Grants, tenancy.MembershipActive)
	} else {
		mv, err = m.T.AddMembership(ctx, r.instituteID, userID, r.Role, r.Grants)
	}
	if err != nil {
		return staff.Accepted{}, err
	}
	t := now
	r.Status, r.AcceptedAt = staff.InvitationAccepted, &t
	return staff.Accepted{Institute: inst, Member: mv}, nil
}
