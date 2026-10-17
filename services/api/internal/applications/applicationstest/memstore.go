// Package applicationstest provides an in-memory applications store and the shared store contract.
package applicationstest

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/certificate-os/api/internal/applications"
	"github.com/certificate-os/api/internal/audit"
	"github.com/certificate-os/api/internal/auth"
	"github.com/certificate-os/api/internal/tenancy"
)

type MemStore struct {
	mu      sync.Mutex
	n       int
	apps    map[string]*applications.Application
	Tenants tenancy.AdminStore
	Audit   audit.Writer
	Now     func() time.Time
}

var _ applications.Store = (*MemStore)(nil)

func NewMemStore(tenants tenancy.AdminStore, w audit.Writer) *MemStore {
	return &MemStore{apps: map[string]*applications.Application{}, Tenants: tenants, Audit: w, Now: time.Now}
}

func contains(ss []applications.Status, s applications.Status) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

func (m *MemStore) Create(ctx context.Context, applicantID string, d applications.Details, ev audit.Event) (applications.Application, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, a := range m.apps {
		if a.ApplicantUserID == applicantID && a.Status.Open() {
			return applications.Application{}, applications.ErrOpenApplicationExists
		}
	}
	m.n++
	now := m.Now()
	a := &applications.Application{ID: fmt.Sprintf("app-%d", m.n), ApplicantUserID: applicantID, Status: applications.Draft,
		Details: d, Version: 1, CreatedAt: now, UpdatedAt: now}
	ev.EntityID = a.ID
	if err := m.Audit.Record(ctx, ev); err != nil {
		return applications.Application{}, err
	}
	m.apps[a.ID] = a
	return *a, nil
}

func (m *MemStore) GetForApplicant(_ context.Context, applicantID, id string) (applications.Application, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if a, ok := m.apps[id]; ok && a.ApplicantUserID == applicantID {
		return *a, nil
	}
	return applications.Application{}, applications.ErrNotFound
}

func (m *MemStore) ListForApplicant(_ context.Context, applicantID string) ([]applications.Application, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []applications.Application
	for _, a := range m.apps {
		if a.ApplicantUserID == applicantID {
			out = append(out, *a)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt.After(out[j].CreatedAt) || (out[i].CreatedAt.Equal(out[j].CreatedAt) && out[i].ID > out[j].ID)
	})
	return out, nil
}

func (m *MemStore) UpdateDetails(ctx context.Context, applicantID, id string, d applications.Details, ev audit.Event) (applications.Application, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.apps[id]
	if !ok || a.ApplicantUserID != applicantID {
		return applications.Application{}, applications.ErrNotFound
	}
	if !a.Status.Editable() {
		return applications.Application{}, applications.ErrNotEditable
	}
	ev.EntityID = id
	if err := m.Audit.Record(ctx, ev); err != nil {
		return applications.Application{}, err
	}
	a.Details, a.Version, a.UpdatedAt = d, a.Version+1, m.Now()
	return *a, nil
}

func (m *MemStore) GetForReview(_ context.Context, id string) (applications.Application, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if a, ok := m.apps[id]; ok && a.Status != applications.Draft {
		return *a, nil
	}
	return applications.Application{}, applications.ErrNotFound
}

func (m *MemStore) ListForReview(_ context.Context, f applications.ListFilter) ([]applications.Application, error) {
	f = f.Normalized()
	m.mu.Lock()
	defer m.mu.Unlock()
	var all []applications.Application
	for _, a := range m.apps {
		if a.Status == applications.Draft || (len(f.Statuses) > 0 && !contains(f.Statuses, a.Status)) {
			continue
		}
		all = append(all, *a)
	}
	// Oldest submission first (review queue order); ID breaks ties deterministically.
	sort.Slice(all, func(i, j int) bool {
		ti, tj := all[i].SubmittedAt, all[j].SubmittedAt
		if ti != nil && tj != nil && !ti.Equal(*tj) {
			return ti.Before(*tj)
		}
		return all[i].ID < all[j].ID
	})
	if f.Offset >= len(all) {
		return nil, nil
	}
	all = all[f.Offset:]
	if len(all) > f.Limit {
		all = all[:f.Limit]
	}
	return all, nil
}

func (m *MemStore) slugClaimedLocked(slug, exceptID string) bool {
	for _, a := range m.apps {
		if a.ID != exceptID && a.Status.ClaimsSlug() && a.Details.Slug == slug {
			return true
		}
	}
	return false
}

func (m *MemStore) Transition(ctx context.Context, t applications.Transition) (applications.Application, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.apps[t.ID]
	if !ok || (t.ApplicantID != "" && a.ApplicantUserID != t.ApplicantID) || (t.ApplicantID == "" && a.Status == applications.Draft) {
		return applications.Application{}, applications.ErrNotFound
	}
	if !contains(t.From, a.Status) {
		return applications.Application{}, applications.ErrInvalidTransition
	}
	next := *a
	if t.Details != nil {
		next.Details = *t.Details
	}
	if t.To == applications.Submitted {
		if m.slugClaimedLocked(next.Details.Slug, a.ID) {
			return applications.Application{}, applications.ErrSlugTaken
		}
		if _, err := m.Tenants.InstituteBySlug(ctx, next.Details.Slug); err == nil {
			return applications.Application{}, applications.ErrSlugTaken
		}
		now := m.Now()
		next.SubmittedAt = &now
	}
	now := m.Now()
	switch t.To {
	case applications.UnderReview:
		next.ReviewedBy = t.ActorUserID
	case applications.Rejected, applications.ChangesRequested:
		next.ReviewedBy, next.ReviewedAt, next.DecisionReason = t.ActorUserID, &now, t.Reason
	}
	next.Status, next.Version, next.UpdatedAt = t.To, a.Version+1, now
	t.Event.EntityID = a.ID
	if err := m.Audit.Record(ctx, t.Event); err != nil {
		return applications.Application{}, err
	}
	*a = next
	return *a, nil
}

func (m *MemStore) Approve(ctx context.Context, in applications.ApproveInput) (applications.Application, tenancy.Institute, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.apps[in.ID]
	if !ok || a.Status == applications.Draft {
		return applications.Application{}, tenancy.Institute{}, applications.ErrNotFound
	}
	if a.Status != applications.UnderReview {
		return applications.Application{}, tenancy.Institute{}, applications.ErrInvalidTransition
	}
	inst, err := m.Tenants.CreateInstitute(ctx, a.Details.Slug, a.Details.Name)
	if err != nil {
		return applications.Application{}, tenancy.Institute{}, err // ErrSlugTaken: nothing changed
	}
	if _, err := m.Tenants.AddMembership(ctx, inst.ID, a.ApplicantUserID, auth.InstituteOwner, nil); err != nil {
		return applications.Application{}, tenancy.Institute{}, err
	}
	now := m.Now()
	a.Status, a.ReviewedBy, a.ReviewedAt, a.InstituteID, a.Version, a.UpdatedAt = applications.Approved, in.ReviewerID, &now, inst.ID, a.Version+1, now
	for _, ev := range applications.ApprovalEvents(in, *a, inst) {
		if err := m.Audit.Record(ctx, ev); err != nil {
			return applications.Application{}, tenancy.Institute{}, err
		}
	}
	return *a, inst, nil
}
