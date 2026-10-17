// Package tenancytest holds an in-memory tenancy store, a store contract suite, and a
// two-institute fixture that every tenant-owned feature should reuse for cross-tenant tests.
package tenancytest

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/certificate-os/api/internal/audit"
	"github.com/certificate-os/api/internal/auth"
	"github.com/certificate-os/api/internal/tenancy"
)

type member struct {
	tenancy.MemberView
	instituteID string
}

type MemStore struct {
	mu         sync.Mutex
	n          int
	institutes map[string]*tenancy.Institute
	members    []*member
	// Audit, when set, receives the event of ChangeInstituteStatus (nothing is recorded on failure).
	Audit audit.Writer
}

func NewMemStore() *MemStore { return &MemStore{institutes: map[string]*tenancy.Institute{}} }

var (
	_ tenancy.Store      = (*MemStore)(nil)
	_ tenancy.AdminStore = (*MemStore)(nil)
	_ tenancy.OpsStore   = (*MemStore)(nil)
)

func (m *MemStore) ListInstitutes(_ context.Context, q string, limit, offset int) ([]tenancy.Institute, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	q = strings.ToLower(q)
	var all []tenancy.Institute
	for _, i := range m.institutes {
		if q == "" || strings.Contains(strings.ToLower(i.Name), q) || strings.Contains(i.Slug, q) {
			all = append(all, *i)
		}
	}
	sort.Slice(all, func(a, b int) bool { return all[a].Slug < all[b].Slug })
	if offset > len(all) {
		offset = len(all)
	}
	all = all[offset:]
	if limit > 0 && limit < len(all) {
		all = all[:limit]
	}
	return all, nil
}

func (m *MemStore) ChangeInstituteStatus(ctx context.Context, id string, from []tenancy.InstituteStatus, to tenancy.InstituteStatus, ev audit.Event) (tenancy.Institute, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	i, ok := m.institutes[id]
	if !ok {
		return tenancy.Institute{}, tenancy.ErrNotFound
	}
	allowed := false
	for _, f := range from {
		allowed = allowed || f == i.Status
	}
	if !allowed || !to.Valid() {
		return tenancy.Institute{}, tenancy.ErrInvalidStatusChange
	}
	if m.Audit != nil {
		if err := m.Audit.Record(ctx, ev); err != nil {
			return tenancy.Institute{}, err
		}
	}
	i.Status = to
	return *i, nil
}

func (m *MemStore) next(prefix string) string { m.n++; return fmt.Sprintf("%s-%d", prefix, m.n) }

func (m *MemStore) CreateInstitute(_ context.Context, slug, name string) (tenancy.Institute, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := tenancy.ValidateSlug(slug); err != nil {
		return tenancy.Institute{}, err
	}
	for _, i := range m.institutes {
		if i.Slug == slug {
			return tenancy.Institute{}, tenancy.ErrSlugTaken
		}
	}
	i := &tenancy.Institute{ID: m.next("inst"), Slug: slug, Name: name, Status: tenancy.InstituteActive, CreatedAt: time.Now()}
	m.institutes[i.ID] = i
	return *i, nil
}

func (m *MemStore) InstituteBySlug(_ context.Context, slug string) (tenancy.Institute, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, i := range m.institutes {
		if i.Slug == slug {
			return *i, nil
		}
	}
	return tenancy.Institute{}, tenancy.ErrNotFound
}

func (m *MemStore) InstituteByID(_ context.Context, id string) (tenancy.Institute, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if i, ok := m.institutes[id]; ok {
		return *i, nil
	}
	return tenancy.Institute{}, tenancy.ErrNotFound
}

func (m *MemStore) SetInstituteStatus(_ context.Context, id string, st tenancy.InstituteStatus) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	i, ok := m.institutes[id]
	if !ok || !st.Valid() {
		return tenancy.ErrNotFound
	}
	i.Status = st
	return nil
}

func (m *MemStore) AddMembership(_ context.Context, instituteID, userID string, role auth.InstituteRole, grants []auth.Permission) (tenancy.MemberView, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.institutes[instituteID]; !ok {
		return tenancy.MemberView{}, tenancy.ErrNotFound
	}
	if !role.Valid() {
		return tenancy.MemberView{}, fmt.Errorf("invalid role %q", role)
	}
	if err := auth.ValidateGrants(grants); err != nil {
		return tenancy.MemberView{}, err
	}
	for _, x := range m.members {
		if x.instituteID == instituteID && x.UserID == userID {
			return tenancy.MemberView{}, tenancy.ErrMembershipExists
		}
	}
	x := &member{instituteID: instituteID, MemberView: tenancy.MemberView{
		MembershipID: m.next("mem"), UserID: userID, Role: role, Grants: append([]auth.Permission(nil), grants...), Status: tenancy.MembershipActive}}
	m.members = append(m.members, x)
	return x.MemberView, nil
}

func (m *MemStore) RemoveMembership(_ context.Context, instituteID, userID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, x := range m.members {
		if x.instituteID == instituteID && x.UserID == userID {
			x.Status = tenancy.MembershipRemoved
			return nil
		}
	}
	return tenancy.ErrNotFound
}

func (m *MemStore) MembershipsForUser(_ context.Context, userID string) ([]tenancy.MembershipView, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []tenancy.MembershipView
	for _, x := range m.members {
		i := m.institutes[x.instituteID]
		if x.UserID != userID || x.Status != tenancy.MembershipActive || i.Status == tenancy.InstituteClosed {
			continue
		}
		out = append(out, tenancy.MembershipView{MembershipID: x.MembershipID, Institute: *i, Role: x.Role, Grants: x.Grants})
	}
	sort.Slice(out, func(a, b int) bool { return out[a].Institute.Name < out[b].Institute.Name })
	return out, nil
}

func (m *MemStore) GetInstitute(_ context.Context, s tenancy.Scope) (tenancy.Institute, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if i, ok := m.institutes[s.InstituteID()]; ok {
		return *i, nil
	}
	return tenancy.Institute{}, tenancy.ErrNotFound
}

func (m *MemStore) ListMembers(_ context.Context, s tenancy.Scope) ([]tenancy.MemberView, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []tenancy.MemberView
	for _, x := range m.members {
		if x.instituteID == s.InstituteID() {
			out = append(out, x.MemberView)
		}
	}
	return out, nil
}
