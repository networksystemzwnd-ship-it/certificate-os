// Package certtest provides an in-memory certificates.Store and the shared store contract.
package certtest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/certificate-os/api/internal/audit"
	"github.com/certificate-os/api/internal/certificates"
	"github.com/certificate-os/api/internal/templates"
	"github.com/certificate-os/api/internal/tenancy"
	"github.com/certificate-os/api/internal/verification"
)

// FixtureOpts shapes the records a test needs.
type FixtureOpts struct {
	Incomplete       bool // enrollment is "enrolled" instead of "completed"
	StudentArchived  bool
	TemplateArchived bool
	StudentName      string // default "Asha Verma"
	Grade            string
	Versions         int    // number of template versions (default 1)
	BatchID          string // reuse a batch created by an earlier fixture of the same institute
	TemplateID       string // reuse a template created by an earlier fixture of the same institute
}

// Refs identifies the records created by a fixture.
type Refs struct{ StudentID, EnrollmentID, TemplateID, BatchID string }

// TemplateConfig is the configuration the fixture stores for version n (distinct per version).
func TemplateConfig(n int) templates.Config {
	c := templates.Presets()[0].Config
	c.Title = fmt.Sprintf("Certificate v%d", n)
	return c
}

type src struct {
	s       certificates.Source
	batchID string
	tplID   string
}

type tpl struct {
	name     string
	archived bool
	configs  []templates.Config
}

type tenantData struct {
	templates map[string]*tpl
	sources   map[string]*src // by enrollment id
	certs     map[string]*row
	files     map[string][]byte
	counter   map[int]int // year -> last number
	jobs      []*jobRec
}

type row struct{ c certificates.Certificate }

type MemStore struct {
	mu       sync.Mutex
	n        int
	inactive map[string]bool // institutes that are not active (suspended)
	events   []VerificationEvent
	data     map[string]*tenantData // keyed by institute id: the in-memory equivalent of RLS
	Audit    audit.Writer
}

var (
	_ certificates.Store    = (*MemStore)(nil)
	_ certificates.JobStore = (*MemStore)(nil)
)

func NewMemStore(w audit.Writer) *MemStore {
	return &MemStore{data: map[string]*tenantData{}, inactive: map[string]bool{}, Audit: w}
}

func (m *MemStore) tenant(s tenancy.Scope) *tenantData {
	d := m.data[s.InstituteID()]
	if d == nil {
		d = &tenantData{templates: map[string]*tpl{}, sources: map[string]*src{}, certs: map[string]*row{}, files: map[string][]byte{}, counter: map[int]int{}}
		m.data[s.InstituteID()] = d
	}
	return d
}

// AddFixture registers a student, a completed (or not) enrollment and a template in the institute.
// BatchID / TemplateID in the options reuse records of an earlier fixture.
func (m *MemStore) AddFixture(s tenancy.Scope, o FixtureOpts) Refs {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.n++
	d := m.tenant(s)
	if o.StudentName == "" {
		o.StudentName = "Asha Verma"
	}
	if o.Versions < 1 {
		o.Versions = 1
	}
	status, completion := "completed", "2026-03-15"
	if o.Incomplete {
		status, completion = "enrolled", ""
	}
	r := Refs{StudentID: fmt.Sprintf("stu-%d", m.n), EnrollmentID: fmt.Sprintf("enr-%d", m.n), TemplateID: o.TemplateID, BatchID: o.BatchID}
	if r.TemplateID == "" {
		r.TemplateID = fmt.Sprintf("tpl-%d", m.n)
		t := &tpl{name: "Diploma", archived: o.TemplateArchived}
		for v := 1; v <= o.Versions; v++ {
			t.configs = append(t.configs, TemplateConfig(v))
		}
		d.templates[r.TemplateID] = t
	}
	if r.BatchID == "" {
		r.BatchID = fmt.Sprintf("bat-%d", m.n)
	}
	d.sources[r.EnrollmentID] = &src{batchID: r.BatchID, tplID: r.TemplateID, s: certificates.Source{
		StudentID: r.StudentID, StudentName: o.StudentName, StudentNumber: fmt.Sprintf("ST-%d", m.n), StudentArchived: o.StudentArchived,
		EnrollmentID: r.EnrollmentID, EnrollmentStatus: status, CompletionDate: completion, Grade: o.Grade,
		BatchName: "Batch A", CourseName: "Web Design", InstituteName: "Test Institute"}}
	return r
}

// SetTemplateArchived flips the archived flag of a fixture template.
func (m *MemStore) SetTemplateArchived(s tenancy.Scope, templateID string, archived bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if t := m.tenant(s).templates[templateID]; t != nil {
		t.archived = archived
	}
}

// EligibleInBatch lists the enrollments of a batch that could be issued now, plus counts of the ones left out.
func (m *MemStore) EligibleInBatch(s tenancy.Scope, batchID string) (eligible []string, alreadyIssued, notEligible int, found bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.eligibleLocked(s, batchID)
}

func (m *MemStore) eligibleLocked(s tenancy.Scope, batchID string) (eligible []string, alreadyIssued, notEligible int, found bool) {
	d := m.tenant(s)
	type row struct{ id, name string }
	var rows []row
	for id, x := range d.sources {
		if x.batchID != batchID {
			continue
		}
		found = true
		live := false
		for _, r := range d.certs {
			if r.c.EnrollmentID == id && (r.c.Status == certificates.Issued || r.c.Status == certificates.Pending) {
				live = true
			}
		}
		switch {
		case live:
			alreadyIssued++
		case x.s.EnrollmentStatus != "completed" || x.s.StudentArchived:
			notEligible++
		default:
			rows = append(rows, row{id, x.s.StudentName + "|" + id})
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].name < rows[j].name })
	for _, r := range rows {
		eligible = append(eligible, r.id)
	}
	return
}

// SetInstituteActive marks the institute active (default) or suspended for bulk runners.
func (m *MemStore) SetInstituteActive(s tenancy.Scope, active bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.inactive[s.InstituteID()] = !active
}

// Template reports a fixture template for tests of other packages.
func (m *MemStore) Template(s tenancy.Scope, id string) (name string, archived bool, versions int, ok bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t := m.tenant(s).templates[id]
	if t == nil {
		return "", false, 0, false
	}
	return t.name, t.archived, len(t.configs), true
}

func (m *MemStore) record(ctx context.Context, s tenancy.Scope, ev audit.Event, id, number string) error {
	ev.EntityID, ev.InstituteID = id, s.InstituteID()
	meta := map[string]any{}
	for k, v := range ev.Metadata {
		meta[k] = v
	}
	if number != "" {
		meta["number"] = number
	}
	ev.Metadata = meta
	if m.Audit == nil {
		return nil
	}
	return m.Audit.Record(ctx, ev)
}

func (m *MemStore) BeginIssue(ctx context.Context, s tenancy.Scope, in certificates.BeginInput, ev audit.Event) (certificates.Certificate, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d := m.tenant(s)
	x, ok := d.sources[in.EnrollmentID]
	t := d.templates[in.TemplateID]
	if !ok || t == nil {
		return certificates.Certificate{}, certificates.ErrNotFound
	}
	version := in.Version
	if version == 0 {
		version = len(t.configs)
	}
	if version < 1 || version > len(t.configs) {
		return certificates.Certificate{}, certificates.ErrNotFound
	}
	source := x.s
	source.TemplateID, source.TemplateName, source.TemplateArchived = in.TemplateID, t.name, t.archived
	source.TemplateVersion, source.Config = version, t.configs[version-1]
	if err := certificates.CheckEligible(source); err != nil {
		return certificates.Certificate{}, err
	}
	if in.Replaces != "" {
		old, ok := d.certs[in.Replaces]
		if !ok {
			return certificates.Certificate{}, certificates.ErrNotFound
		}
		if old.c.Status != certificates.Issued || old.c.EnrollmentID != in.EnrollmentID {
			return certificates.Certificate{}, certificates.ErrState
		}
	}
	// Abandon stale pending certificates of this enrollment; refuse when a live one remains.
	var abandon []*row
	for _, r := range d.certs {
		if r.c.EnrollmentID != in.EnrollmentID {
			continue
		}
		switch r.c.Status {
		case certificates.Issued:
			if in.Replaces != "" && r.c.ID == in.Replaces {
				continue // the certificate being replaced stays valid meanwhile
			}
			return certificates.Certificate{}, certificates.ErrAlreadyIssued
		case certificates.Pending:
			if in.Now.Sub(r.c.CreatedAt) >= certificates.StalePendingAfter {
				abandon = append(abandon, r)
			} else if r.c.ReplacesID != "" && in.Replaces == r.c.ReplacesID {
				return certificates.Certificate{}, certificates.ErrReplacing
			} else if r.c.ReplacesID == "" || in.Replaces == "" {
				return certificates.Certificate{}, certificates.ErrAlreadyIssued
			} else {
				return certificates.Certificate{}, certificates.ErrReplacing
			}
		}
	}
	year := in.Now.UTC().Year()
	number := certificates.FormatNumber(year, d.counter[year]+1)
	m.n++
	c := certificates.Certificate{ID: fmt.Sprintf("cert-%d", m.n), Number: number, PublicID: in.PublicID, StudentID: source.StudentID,
		EnrollmentID: in.EnrollmentID, TemplateID: in.TemplateID, TemplateVersion: version,
		Snapshot: certificates.BuildSnapshot(source, number, in.Now), Status: certificates.Pending, IssuedBy: in.IssuedBy, CreatedAt: in.Now,
		ReplacesID: in.Replaces, ReplaceReason: in.ReplaceReason}
	if err := m.record(ctx, s, ev, c.ID, number); err != nil {
		return certificates.Certificate{}, err
	}
	for _, r := range abandon {
		t := in.Now
		r.c.Status, r.c.FailedAt, r.c.Failure = certificates.Failed, &t, "abandoned"
	}
	d.counter[year]++
	d.certs[c.ID] = &row{c}
	return c, nil
}

func (m *MemStore) pending(d *tenantData, id string) (*row, error) {
	r, ok := d.certs[id]
	if !ok {
		return nil, certificates.ErrNotFound
	}
	if r.c.Status != certificates.Pending {
		return nil, certificates.ErrState
	}
	return r, nil
}

func (m *MemStore) Complete(ctx context.Context, s tenancy.Scope, id string, pdf []byte, ev audit.Event) (certificates.Certificate, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d := m.tenant(s)
	r, err := m.pending(d, id)
	if err != nil {
		return certificates.Certificate{}, err
	}
	if len(pdf) == 0 {
		return certificates.Certificate{}, errors.New("empty pdf")
	}
	var old *row
	if r.c.ReplacesID != "" {
		old = d.certs[r.c.ReplacesID]
		if old == nil || old.c.Status != certificates.Issued {
			return certificates.Certificate{}, certificates.ErrState // the certificate to replace is no longer valid
		}
	}
	if err := m.record(ctx, s, ev, id, r.c.Number); err != nil {
		return certificates.Certificate{}, err
	}
	sum := sha256.Sum256(pdf)
	now := time.Now()
	if old != nil {
		oe := audit.Event{ActorType: ev.ActorType, ActorUserID: ev.ActorUserID, RequestID: ev.RequestID, Action: "certificate.replaced",
			EntityType: "certificate", Metadata: map[string]any{"replaced_by": id}}
		if err := m.record(ctx, s, oe, old.c.ID, old.c.Number); err != nil {
			return certificates.Certificate{}, err
		}
		t := now
		old.c.Status, old.c.RevokedAt, old.c.RevokeReason = certificates.Revoked, &t, r.c.ReplaceReason
		old.c.ReplacedByID, old.c.ReplacedByPublicID = id, r.c.PublicID
	}
	r.c.Status, r.c.PDFSHA256, r.c.PDFSize, r.c.IssuedAt = certificates.Issued, hex.EncodeToString(sum[:]), len(pdf), &now
	d.files[id] = append([]byte(nil), pdf...)
	return r.c, nil
}

func (m *MemStore) Fail(ctx context.Context, s tenancy.Scope, id, reason string, ev audit.Event) (certificates.Certificate, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, err := m.pending(m.tenant(s), id)
	if err != nil {
		return certificates.Certificate{}, err
	}
	if err := m.record(ctx, s, ev, id, r.c.Number); err != nil {
		return certificates.Certificate{}, err
	}
	now := time.Now()
	r.c.Status, r.c.FailedAt, r.c.Failure = certificates.Failed, &now, reason
	return r.c, nil
}

func (m *MemStore) Get(_ context.Context, s tenancy.Scope, id string) (certificates.Certificate, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if r, ok := m.tenant(s).certs[id]; ok {
		return r.c, nil
	}
	return certificates.Certificate{}, certificates.ErrNotFound
}

func (m *MemStore) List(_ context.Context, s tenancy.Scope, f certificates.Filter) ([]certificates.Certificate, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	f = f.Normalized()
	var all []certificates.Certificate
	q := strings.ToLower(f.Q)
	for _, r := range m.tenant(s).certs {
		c := r.c
		if (f.Status != "" && c.Status != f.Status) || (f.StudentID != "" && c.StudentID != f.StudentID) {
			continue
		}
		if q != "" && !strings.Contains(strings.ToLower(c.Number), q) && !strings.Contains(strings.ToLower(c.Snapshot.StudentName), q) {
			continue
		}
		all = append(all, c)
	}
	sort.Slice(all, func(i, j int) bool {
		if !all[i].CreatedAt.Equal(all[j].CreatedAt) {
			return all[i].CreatedAt.After(all[j].CreatedAt)
		}
		return all[i].ID > all[j].ID
	})
	if f.Offset >= len(all) {
		return nil, nil
	}
	all = all[f.Offset:]
	if len(all) > f.Limit+1 {
		all = all[:f.Limit+1]
	}
	return all, nil
}

func (m *MemStore) GetPDF(_ context.Context, s tenancy.Scope, id string) (certificates.PDF, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d := m.tenant(s)
	r, ok := d.certs[id]
	if !ok || (r.c.Status != certificates.Issued && r.c.Status != certificates.Revoked) {
		return certificates.PDF{}, certificates.ErrNotFound
	}
	return certificates.PDF{Bytes: append([]byte(nil), d.files[id]...), SHA256: r.c.PDFSHA256, Number: r.c.Number}, nil
}

func (m *MemStore) Revoke(ctx context.Context, s tenancy.Scope, id, reason, by string, ev audit.Event) (certificates.Certificate, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.tenant(s).certs[id]
	if !ok {
		return certificates.Certificate{}, certificates.ErrNotFound
	}
	if r.c.Status != certificates.Issued {
		return certificates.Certificate{}, certificates.ErrState
	}
	if err := m.record(ctx, s, ev, id, r.c.Number); err != nil {
		return certificates.Certificate{}, err
	}
	now := time.Now()
	r.c.Status, r.c.RevokedAt, r.c.RevokeReason = certificates.Revoked, &now, reason
	_ = by
	return r.c, nil
}

// VerificationEvent is what the memory store records for a public verification.
type VerificationEvent struct{ InstituteID, CertificateID, Result string }

// Verifications lists the recorded public verifications, oldest first.
func (m *MemStore) Verifications() []VerificationEvent {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]VerificationEvent(nil), m.events...)
}

var _ verification.Store = (*MemStore)(nil)

// Lookup implements verification.Store across all institutes: only issued and revoked certificates exist publicly.
func (m *MemStore) Lookup(_ context.Context, publicID string) (verification.Record, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for inst, d := range m.data {
		for _, r := range d.certs {
			c := r.c
			if c.PublicID != publicID || (c.Status != certificates.Issued && c.Status != certificates.Revoked) {
				continue
			}
			rec := verification.Record{Number: c.Number, InstituteName: c.Snapshot.InstituteName, StudentName: c.Snapshot.StudentName,
				CourseName: c.Snapshot.CourseName, IssueDate: c.Snapshot.IssueDate, Revoked: c.Status == certificates.Revoked,
				RevokedAt: c.RevokedAt, ReplacedByPublicID: c.ReplacedByPublicID}
			m.events = append(m.events, VerificationEvent{inst, c.ID, string(verification.StatusOf(rec))})
			return rec, nil
		}
	}
	return verification.Record{}, verification.ErrNotFound
}
