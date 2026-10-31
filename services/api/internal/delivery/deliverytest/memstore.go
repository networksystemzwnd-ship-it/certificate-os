// Package deliverytest holds the in-memory delivery.Store and the contract every store must satisfy.
package deliverytest

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/certificate-os/api/internal/audit"
	"github.com/certificate-os/api/internal/certificates"
	"github.com/certificate-os/api/internal/delivery"
	"github.com/certificate-os/api/internal/tenancy"
)

type rec struct {
	d        delivery.Delivery
	token    string
	lease    time.Time
	hasLease bool
	due      time.Time
	attempts []delivery.Attempt
}

// MemStore is the in-memory equivalent of the Postgres store (and of its row-level security: data is keyed by institute).
type MemStore struct {
	mu       sync.Mutex
	n        int
	Certs    certificates.Store
	Audit    audit.Writer
	data     map[string]map[string]*rec // institute -> delivery id
	emails   map[string]string          // institute|certificate -> email
	inactive map[string]bool
	now      func() time.Time
}

var _ delivery.Store = (*MemStore)(nil)

func NewMemStore(certs certificates.Store, w audit.Writer) *MemStore {
	return &MemStore{Certs: certs, Audit: w, data: map[string]map[string]*rec{}, emails: map[string]string{}, inactive: map[string]bool{}}
}

// SetEmail sets the address of the student of a certificate (the Postgres store reads students.email).
func (m *MemStore) SetEmail(s tenancy.Scope, certID, email string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.emails[s.InstituteID()+"|"+certID] = email
}

// SetActive marks an institute active or suspended.
func (m *MemStore) SetActive(instituteID string, active bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.inactive[instituteID] = !active
}

func (m *MemStore) tenant(s tenancy.Scope) map[string]*rec {
	d := m.data[s.InstituteID()]
	if d == nil {
		d = map[string]*rec{}
		m.data[s.InstituteID()] = d
	}
	return d
}

func (m *MemStore) record(ctx context.Context, s tenancy.Scope, ev audit.Event, d delivery.Delivery) error {
	ev.EntityID, ev.InstituteID = d.ID, s.InstituteID()
	meta := map[string]any{"certificate_id": d.CertificateID, "number": d.CertificateNumber}
	for k, v := range ev.Metadata {
		meta[k] = v
	}
	ev.Metadata = meta
	if m.Audit == nil {
		return nil
	}
	return m.Audit.Record(ctx, ev)
}

func (m *MemStore) Request(ctx context.Context, s tenancy.Scope, in delivery.RequestInput, ev audit.Event) (delivery.Delivery, error) {
	c, err := m.Certs.Get(ctx, s, in.CertificateID)
	if err != nil || c.Status != certificates.Issued {
		return delivery.Delivery{}, delivery.ErrNotFound
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	email := m.emails[s.InstituteID()+"|"+in.CertificateID]
	if email == "" {
		return delivery.Delivery{}, delivery.ErrNoRecipient
	}
	total, active, accepted := 0, false, false
	for _, r := range m.tenant(s) {
		if r.d.CertificateID != in.CertificateID {
			continue
		}
		total++
		active = active || r.d.Status.Active()
		accepted = accepted || r.d.Status == delivery.Accepted
	}
	switch {
	case active:
		return delivery.Delivery{}, delivery.ErrActive
	case accepted && in.SkipAccepted:
		return delivery.Delivery{}, delivery.ErrAlreadyAccepted
	case total >= delivery.MaxPerCertificate:
		return delivery.Delivery{}, delivery.ErrLimit
	}
	m.n++
	next := in.Now
	d := delivery.Delivery{ID: fmt.Sprintf("dlv-%d", m.n), CertificateID: c.ID, CertificateNumber: c.Number, Channel: "email", Recipient: email,
		Status: delivery.Queued, MaxAttempts: delivery.MaxAttempts, NextAttemptAt: &next, CreatedBy: in.CreatedBy, CreatedAt: in.Now, UpdatedAt: in.Now}
	if err := m.record(ctx, s, ev, d); err != nil {
		return delivery.Delivery{}, err
	}
	m.tenant(s)[d.ID] = &rec{d: d, due: in.Now}
	return d, nil
}

func (m *MemStore) Get(_ context.Context, s tenancy.Scope, id string) (delivery.Delivery, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r := m.tenant(s)[id]
	if r == nil {
		return delivery.Delivery{}, delivery.ErrNotFound
	}
	return r.d, nil
}

func (m *MemStore) List(_ context.Context, s tenancy.Scope, f delivery.Filter) ([]delivery.Delivery, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []delivery.Delivery
	for _, r := range m.tenant(s) {
		if f.CertificateID != "" && r.d.CertificateID != f.CertificateID {
			continue
		}
		if f.Status != "" && r.d.Status != f.Status {
			continue
		}
		out = append(out, r.d)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.After(out[j].CreatedAt)
		}
		return out[i].ID > out[j].ID
	})
	if f.Limit > 0 && len(out) > f.Limit+1 {
		out = out[:f.Limit+1]
	}
	return out, nil
}

func (m *MemStore) Attempts(_ context.Context, s tenancy.Scope, id string) ([]delivery.Attempt, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r := m.tenant(s)[id]
	if r == nil {
		return nil, delivery.ErrNotFound
	}
	return append([]delivery.Attempt(nil), r.attempts...), nil
}

func (m *MemStore) Retry(ctx context.Context, s tenancy.Scope, id string, now time.Time, ev audit.Event) (delivery.Delivery, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r := m.tenant(s)[id]
	if r == nil {
		return delivery.Delivery{}, delivery.ErrNotFound
	}
	if r.d.Status != delivery.Failed {
		return delivery.Delivery{}, delivery.ErrState
	}
	c, err := m.Certs.Get(ctx, s, r.d.CertificateID)
	if err != nil || c.Status != certificates.Issued {
		return delivery.Delivery{}, delivery.ErrNotFound
	}
	email := m.emails[s.InstituteID()+"|"+r.d.CertificateID]
	if email == "" {
		return delivery.Delivery{}, delivery.ErrNoRecipient
	}
	for _, o := range m.tenant(s) {
		if o != r && o.d.CertificateID == r.d.CertificateID && o.d.Status.Active() {
			return delivery.Delivery{}, delivery.ErrActive
		}
	}
	d := r.d
	d.Recipient, d.Status, d.FinishedAt, d.UpdatedAt = email, delivery.Queued, nil, now
	d.MaxAttempts = d.Attempts + delivery.MaxAttempts
	next := now
	d.NextAttemptAt = &next
	if err := m.record(ctx, s, ev, d); err != nil {
		return delivery.Delivery{}, err
	}
	r.d, r.due, r.token, r.hasLease = d, now, "", false
	return d, nil
}

func (m *MemStore) Due(_ context.Context, now time.Time, limit int) ([]delivery.DueRef, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	type item struct {
		ref delivery.DueRef
		due time.Time
	}
	var items []item
	for inst, d := range m.data {
		for id, r := range d {
			if !r.d.Status.Active() || r.due.After(now) || (r.hasLease && r.lease.After(now)) {
				continue
			}
			items = append(items, item{delivery.DueRef{InstituteID: inst, DeliveryID: id}, r.due})
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if !items[i].due.Equal(items[j].due) {
			return items[i].due.Before(items[j].due)
		}
		return items[i].ref.DeliveryID < items[j].ref.DeliveryID
	})
	var out []delivery.DueRef
	for _, it := range items {
		if limit > 0 && len(out) >= limit {
			break
		}
		out = append(out, it.ref)
	}
	return out, nil
}

func (m *MemStore) Claim(ctx context.Context, s tenancy.Scope, id, token string, now time.Time, lease time.Duration) (delivery.Claim, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r := m.tenant(s)[id]
	if r == nil {
		return delivery.Claim{}, delivery.ErrNotFound
	}
	if m.inactive[s.InstituteID()] {
		return delivery.Claim{}, delivery.ErrSuspended
	}
	stale := r.d.Status == delivery.Sending && r.hasLease && !r.lease.After(now)
	if !(r.d.Status == delivery.Queued && !r.due.After(now)) && !stale {
		return delivery.Claim{}, delivery.ErrState
	}
	if stale && r.d.Attempts >= r.d.MaxAttempts {
		// A worker keeps dying on this delivery: stop instead of sending forever.
		r.d.Status, r.d.LastError, r.d.UpdatedAt, r.d.NextAttemptAt = delivery.Failed, delivery.CodeInterrupted, now, nil
		fin := now
		r.d.FinishedAt = &fin
		r.hasLease, r.token = false, ""
		_ = m.record(ctx, s, audit.Event{ActorType: audit.ActorSystem, Action: "delivery.failed", EntityType: "delivery", Metadata: map[string]any{"code": delivery.CodeInterrupted}}, r.d)
		return delivery.Claim{}, delivery.ErrState
	}
	r.d.Status, r.d.Attempts, r.d.NextAttemptAt, r.d.UpdatedAt = delivery.Sending, r.d.Attempts+1, nil, now
	r.token, r.lease, r.hasLease = token, now.Add(lease), true
	return delivery.Claim{Delivery: r.d, Token: token}, nil
}

func (m *MemStore) Finish(ctx context.Context, s tenancy.Scope, id, token string, in delivery.FinishInput, ev audit.Event) (delivery.Delivery, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r := m.tenant(s)[id]
	if r == nil {
		return delivery.Delivery{}, delivery.ErrNotFound
	}
	if r.d.Status != delivery.Sending || r.token == "" || r.token != token {
		return delivery.Delivery{}, delivery.ErrState
	}
	d := r.d
	d.UpdatedAt = in.At
	a := delivery.Attempt{No: d.Attempts, Provider: in.Provider, StartedAt: in.StartedAt, FinishedAt: in.At}
	switch in.Outcome {
	case delivery.OutcomeAccepted:
		a.Status = "accepted"
		fin := in.At
		d.Status, d.LastError, d.FinishedAt = delivery.Accepted, "", &fin
	case delivery.OutcomeRetry:
		a.Status, a.ErrorCode = "failed", in.Code
		next := in.NextAttemptAt
		d.Status, d.LastError, d.NextAttemptAt = delivery.Queued, in.Code, &next
	default:
		a.Status, a.ErrorCode = "failed", in.Code
		fin := in.At
		d.Status, d.LastError, d.FinishedAt = delivery.Failed, in.Code, &fin
	}
	if err := m.record(ctx, s, ev, d); err != nil {
		return delivery.Delivery{}, err
	}
	r.d, r.token, r.hasLease = d, "", false
	r.attempts = append(r.attempts, a)
	if d.Status == delivery.Queued {
		r.due = in.NextAttemptAt
	}
	return d, nil
}

func (m *MemStore) Postpone(_ context.Context, s tenancy.Scope, id string, until time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	r := m.tenant(s)[id]
	if r == nil {
		return delivery.ErrNotFound
	}
	if !r.d.Status.Active() {
		return delivery.ErrState
	}
	r.due = until
	if r.d.Status == delivery.Queued {
		u := until
		r.d.NextAttemptAt = &u
	}
	return nil
}

func (m *MemStore) Depth(_ context.Context, now time.Time) (delivery.Depth, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var d delivery.Depth
	var oldest time.Time
	for _, t := range m.data {
		for _, r := range t {
			if !r.d.Status.Active() {
				continue
			}
			d.Active++
			if !r.due.After(now) {
				d.Overdue++
				if oldest.IsZero() || r.due.Before(oldest) {
					oldest = r.due
				}
			}
		}
	}
	if !oldest.IsZero() {
		d.OldestOverdue = now.Sub(oldest)
	}
	return d, nil
}
