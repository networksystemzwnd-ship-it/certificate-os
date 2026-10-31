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
)

// FixtureOpts shapes the records a test needs.
type FixtureOpts struct {
	Incomplete       bool // enrollment is "enrolled" instead of "completed"
	StudentArchived  bool
	TemplateArchived bool
	StudentName      string // default "Asha Verma"
	Grade            string
	Versions         int // number of template versions (default 1)
}

// Refs identifies the records created by a fixture.
type Refs struct{ StudentID, EnrollmentID, TemplateID string }

// TemplateConfig is the configuration the fixture stores for version n (distinct per version).
func TemplateConfig(n int) templates.Config {
	c := templates.Presets()[0].Config
	c.Title = fmt.Sprintf("Certificate v%d", n)
	return c
}

type src struct {
	s       certificates.Source
	configs []templates.Config
}

type tenantData struct {
	sources map[string]*src // by enrollment id
	certs   map[string]*row
	files   map[string][]byte
	counter map[int]int // year -> last number
}

type row struct{ c certificates.Certificate }

type MemStore struct {
	mu    sync.Mutex
	n     int
	data  map[string]*tenantData // keyed by institute id: the in-memory equivalent of RLS
	Audit audit.Writer
}

var _ certificates.Store = (*MemStore)(nil)

func NewMemStore(w audit.Writer) *MemStore {
	return &MemStore{data: map[string]*tenantData{}, Audit: w}
}

func (m *MemStore) tenant(s tenancy.Scope) *tenantData {
	d := m.data[s.InstituteID()]
	if d == nil {
		d = &tenantData{sources: map[string]*src{}, certs: map[string]*row{}, files: map[string][]byte{}, counter: map[int]int{}}
		m.data[s.InstituteID()] = d
	}
	return d
}

// AddFixture registers a student, a completed (or not) enrollment and a template in the institute.
func (m *MemStore) AddFixture(s tenancy.Scope, o FixtureOpts) Refs {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.n++
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
	r := Refs{StudentID: fmt.Sprintf("stu-%d", m.n), EnrollmentID: fmt.Sprintf("enr-%d", m.n), TemplateID: fmt.Sprintf("tpl-%d", m.n)}
	x := &src{s: certificates.Source{
		StudentID: r.StudentID, StudentName: o.StudentName, StudentNumber: fmt.Sprintf("ST-%d", m.n), StudentArchived: o.StudentArchived,
		EnrollmentID: r.EnrollmentID, EnrollmentStatus: status, CompletionDate: completion, Grade: o.Grade,
		BatchName: "Batch A", CourseName: "Web Design", InstituteName: "Test Institute",
		TemplateID: r.TemplateID, TemplateName: "Diploma", TemplateArchived: o.TemplateArchived}}
	for v := 1; v <= o.Versions; v++ {
		x.configs = append(x.configs, TemplateConfig(v))
	}
	m.tenant(s).sources[r.EnrollmentID] = x
	return r
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
	if !ok || x.s.TemplateID != in.TemplateID {
		return certificates.Certificate{}, certificates.ErrNotFound
	}
	version := in.Version
	if version == 0 {
		version = len(x.configs)
	}
	if version < 1 || version > len(x.configs) {
		return certificates.Certificate{}, certificates.ErrNotFound
	}
	source := x.s
	source.TemplateVersion, source.Config = version, x.configs[version-1]
	if err := certificates.CheckEligible(source); err != nil {
		return certificates.Certificate{}, err
	}
	// Abandon stale pending certificates of this enrollment; refuse when a live one remains.
	var abandon []*row
	for _, r := range d.certs {
		if r.c.EnrollmentID != in.EnrollmentID {
			continue
		}
		switch r.c.Status {
		case certificates.Issued:
			return certificates.Certificate{}, certificates.ErrAlreadyIssued
		case certificates.Pending:
			if in.Now.Sub(r.c.CreatedAt) < certificates.StalePendingAfter {
				return certificates.Certificate{}, certificates.ErrAlreadyIssued
			}
			abandon = append(abandon, r)
		}
	}
	year := in.Now.UTC().Year()
	number := certificates.FormatNumber(year, d.counter[year]+1)
	m.n++
	c := certificates.Certificate{ID: fmt.Sprintf("cert-%d", m.n), Number: number, PublicID: in.PublicID, StudentID: source.StudentID,
		EnrollmentID: in.EnrollmentID, TemplateID: in.TemplateID, TemplateVersion: version,
		Snapshot: certificates.BuildSnapshot(source, number, in.Now), Status: certificates.Pending, IssuedBy: in.IssuedBy, CreatedAt: in.Now}
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
	if err := m.record(ctx, s, ev, id, r.c.Number); err != nil {
		return certificates.Certificate{}, err
	}
	sum := sha256.Sum256(pdf)
	now := time.Now()
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
