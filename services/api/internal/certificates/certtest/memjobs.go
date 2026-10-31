package certtest

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/certificate-os/api/internal/audit"
	"github.com/certificate-os/api/internal/certificates"
	"github.com/certificate-os/api/internal/tenancy"
)

type jobRec struct {
	j     certificates.Job // counts are derived on read
	seq   int
	token string
	lease time.Time
	items []*certificates.JobItem
}

func (r *jobRec) view() certificates.Job {
	j := r.j
	j.Total = len(r.items)
	j.Succeeded, j.Failed, j.Remaining = 0, 0, 0
	for _, it := range r.items {
		switch it.Status {
		case certificates.ItemSucceeded:
			j.Succeeded++
		case certificates.ItemFailed:
			j.Failed++
		default:
			j.Remaining++
		}
	}
	return j
}

func (m *MemStore) job(d *tenantData, id string) (*jobRec, error) {
	for _, r := range d.jobs {
		if r.j.ID == id {
			return r, nil
		}
	}
	return nil, certificates.ErrNotFound
}

func (m *MemStore) activeJob(d *tenantData) *jobRec {
	for _, r := range d.jobs {
		if r.j.Status.Active() {
			return r
		}
	}
	return nil
}

func (m *MemStore) batchName(d *tenantData, batchID string) string {
	for _, x := range d.sources {
		if x.batchID == batchID {
			return x.s.BatchName
		}
	}
	return ""
}

func (m *MemStore) Preview(_ context.Context, s tenancy.Scope, batchID, templateID string) (certificates.Preview, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d := m.tenant(s)
	t := d.templates[templateID]
	el, issued, not, found := m.eligibleLocked(s, batchID)
	if !found || t == nil {
		return certificates.Preview{}, certificates.ErrNotFound
	}
	if t.archived {
		return certificates.Preview{}, &certificates.EligibilityError{Field: "template_id", Reason: "This template is archived. Restore it or choose another."}
	}
	return certificates.Preview{BatchName: m.batchName(d, batchID), Eligible: len(el), AlreadyIssued: issued, NotEligible: not}, nil
}

func (m *MemStore) CreateJob(ctx context.Context, s tenancy.Scope, in certificates.JobInput, ev audit.Event) (certificates.Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d := m.tenant(s)
	t := d.templates[in.TemplateID]
	el, issued, not, found := m.eligibleLocked(s, in.BatchID)
	if !found || t == nil {
		return certificates.Job{}, certificates.ErrNotFound
	}
	version := in.Version
	if version == 0 {
		version = len(t.configs)
	}
	if version < 1 || version > len(t.configs) {
		return certificates.Job{}, certificates.ErrNotFound
	}
	if t.archived {
		return certificates.Job{}, &certificates.EligibilityError{Field: "template_id", Reason: "This template is archived. Restore it or choose another."}
	}
	if len(el) == 0 {
		return certificates.Job{}, certificates.ErrNothingToIssue
	}
	if len(el) > certificates.MaxJobItems {
		return certificates.Job{}, &certificates.EligibilityError{Field: "batch_id", Reason: fmt.Sprintf("A job can hold at most %d certificates. Issue the batch in smaller groups.", certificates.MaxJobItems)}
	}
	if m.activeJob(d) != nil {
		return certificates.Job{}, certificates.ErrJobActive
	}
	m.n++
	rec := &jobRec{seq: m.n, j: certificates.Job{ID: fmt.Sprintf("job-%d", m.n), BatchID: in.BatchID, BatchName: m.batchName(d, in.BatchID),
		TemplateID: in.TemplateID, TemplateName: t.name, TemplateVersion: version, Status: certificates.JobQueued,
		SkippedIssued: issued, SkippedIneligible: not, CreatedBy: in.CreatedBy, CreatedAt: in.Now}}
	for i, id := range el {
		rec.items = append(rec.items, &certificates.JobItem{EnrollmentID: id, StudentName: d.sources[id].s.StudentName, Position: i + 1, Status: certificates.ItemQueued})
	}
	ev.Metadata = withMeta(ev.Metadata, map[string]any{"total": len(el), "template_version": version, "skipped_issued": issued, "skipped_ineligible": not})
	if err := m.record(ctx, s, ev, rec.j.ID, ""); err != nil {
		return certificates.Job{}, err
	}
	d.jobs = append(d.jobs, rec)
	return rec.view(), nil
}

func withMeta(a, b map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		out[k] = v
	}
	return out
}

func (m *MemStore) GetJob(_ context.Context, s tenancy.Scope, id string) (certificates.Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, err := m.job(m.tenant(s), id)
	if err != nil {
		return certificates.Job{}, err
	}
	return r.view(), nil
}

func (m *MemStore) ListJobs(_ context.Context, s tenancy.Scope, limit, offset int) ([]certificates.Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d := m.tenant(s)
	recs := append([]*jobRec(nil), d.jobs...)
	sort.Slice(recs, func(i, j int) bool { return recs[i].seq > recs[j].seq })
	var out []certificates.Job
	for i := offset; i < len(recs) && len(out) < limit+1; i++ {
		out = append(out, recs[i].view())
	}
	return out, nil
}

func (m *MemStore) ListItems(_ context.Context, s tenancy.Scope, jobID string, status certificates.ItemStatus, limit, offset int) ([]certificates.JobItem, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d := m.tenant(s)
	r, err := m.job(d, jobID)
	if err != nil {
		return nil, err
	}
	var out []certificates.JobItem
	skipped := 0
	for _, it := range r.items {
		if status != "" && it.Status != status {
			continue
		}
		if skipped < offset {
			skipped++
			continue
		}
		if len(out) >= limit+1 {
			break
		}
		c := *it
		if c.CertificateID != "" {
			c.CertificateNumber = d.certs[c.CertificateID].c.Number
		}
		out = append(out, c)
	}
	return out, nil
}

func (m *MemStore) ClaimJob(ctx context.Context, s tenancy.Scope, id, token string, now time.Time, lease time.Duration, ev audit.Event) (certificates.Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d := m.tenant(s)
	r, err := m.job(d, id)
	if err != nil {
		return certificates.Job{}, err
	}
	stale := r.j.Status == certificates.JobProcessing && r.lease.Before(now)
	if m.inactive[s.InstituteID()] || (r.j.Status != certificates.JobQueued && !stale) {
		return certificates.Job{}, certificates.ErrState
	}
	if err := m.record(ctx, s, ev, r.j.ID, ""); err != nil {
		return certificates.Job{}, err
	}
	r.j.Status, r.token, r.lease = certificates.JobProcessing, token, now.Add(lease)
	if r.j.StartedAt == nil {
		t := now
		r.j.StartedAt = &t
	}
	for _, it := range r.items {
		if it.Status == certificates.ItemProcessing {
			it.Status = certificates.ItemQueued
		}
	}
	return r.view(), nil
}

func (m *MemStore) owned(d *tenantData, s tenancy.Scope, jobID, token string) (*jobRec, error) {
	r, err := m.job(d, jobID)
	if err != nil {
		return nil, err
	}
	if token == "" || r.token != token {
		return nil, certificates.ErrState
	}
	return r, nil
}

func (m *MemStore) NextItem(_ context.Context, s tenancy.Scope, jobID, token string, now time.Time, lease time.Duration) (certificates.JobItem, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, err := m.owned(m.tenant(s), s, jobID, token)
	if err != nil {
		return certificates.JobItem{}, false, err
	}
	if r.j.Status != certificates.JobProcessing || m.inactive[s.InstituteID()] {
		return certificates.JobItem{}, false, certificates.ErrState
	}
	r.lease = now.Add(lease)
	for _, it := range r.items {
		if it.Status == certificates.ItemQueued {
			it.Status = certificates.ItemProcessing
			it.Attempts++
			return *it, true, nil
		}
	}
	return certificates.JobItem{}, false, nil
}

func (m *MemStore) RecordItem(_ context.Context, s tenancy.Scope, jobID, enrollmentID, token string, res certificates.ItemResult, _ time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	d := m.tenant(s)
	r, err := m.owned(d, s, jobID, token)
	if err != nil {
		return err
	}
	for _, it := range r.items {
		if it.EnrollmentID != enrollmentID {
			continue
		}
		if it.Status != certificates.ItemProcessing {
			return certificates.ErrState
		}
		if res.CertificateID != "" {
			if _, ok := d.certs[res.CertificateID]; !ok {
				return certificates.ErrNotFound
			}
			it.Status, it.CertificateID, it.Error = certificates.ItemSucceeded, res.CertificateID, ""
		} else {
			if res.Error == "" {
				return certificates.ErrState
			}
			it.Status, it.Error = certificates.ItemFailed, res.Error
		}
		return nil
	}
	return certificates.ErrNotFound
}

func (m *MemStore) FinishJob(ctx context.Context, s tenancy.Scope, jobID, token string, now time.Time, ev audit.Event) (certificates.Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, err := m.owned(m.tenant(s), s, jobID, token)
	if err != nil {
		return certificates.Job{}, err
	}
	v := r.view()
	if r.j.Status != certificates.JobProcessing || v.Remaining > 0 {
		return certificates.Job{}, certificates.ErrState
	}
	status := certificates.JobCompletedWithErrors
	switch {
	case v.Failed == 0:
		status = certificates.JobCompleted
	case v.Succeeded == 0:
		status = certificates.JobFailed
	}
	ev.Metadata = withMeta(ev.Metadata, map[string]any{"status": string(status), "total": v.Total, "succeeded": v.Succeeded, "failed": v.Failed})
	if err := m.record(ctx, s, ev, r.j.ID, ""); err != nil {
		return certificates.Job{}, err
	}
	t := now
	r.j.Status, r.j.FinishedAt = status, &t
	return r.view(), nil
}

func (m *MemStore) CancelJob(ctx context.Context, s tenancy.Scope, id string, now time.Time, ev audit.Event) (certificates.Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, err := m.job(m.tenant(s), id)
	if err != nil {
		return certificates.Job{}, err
	}
	if !r.j.Status.Active() {
		return certificates.Job{}, certificates.ErrState
	}
	v := r.view()
	ev.Metadata = withMeta(ev.Metadata, map[string]any{"succeeded": v.Succeeded, "failed": v.Failed, "remaining": v.Remaining})
	if err := m.record(ctx, s, ev, r.j.ID, ""); err != nil {
		return certificates.Job{}, err
	}
	t := now
	r.j.Status, r.j.FinishedAt = certificates.JobCancelled, &t
	return r.view(), nil
}

func (m *MemStore) RetryFailed(ctx context.Context, s tenancy.Scope, id string, now time.Time, ev audit.Event) (certificates.Job, int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d := m.tenant(s)
	r, err := m.job(d, id)
	if err != nil {
		return certificates.Job{}, 0, err
	}
	if r.j.Status != certificates.JobCompletedWithErrors && r.j.Status != certificates.JobFailed {
		return certificates.Job{}, 0, certificates.ErrState
	}
	if m.activeJob(d) != nil {
		return certificates.Job{}, 0, certificates.ErrJobActive
	}
	n := 0
	for _, it := range r.items {
		if it.Status == certificates.ItemFailed {
			n++
		}
	}
	ev.Metadata = withMeta(ev.Metadata, map[string]any{"requeued": n})
	if err := m.record(ctx, s, ev, r.j.ID, ""); err != nil {
		return certificates.Job{}, 0, err
	}
	for _, it := range r.items {
		if it.Status == certificates.ItemFailed {
			it.Status, it.Error = certificates.ItemQueued, ""
		}
	}
	r.j.Status, r.j.FinishedAt = certificates.JobQueued, nil
	return r.view(), n, nil
}

func (m *MemStore) FindLive(_ context.Context, s tenancy.Scope, enrollmentID string) (certificates.Certificate, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, r := range m.tenant(s).certs {
		if r.c.EnrollmentID == enrollmentID && (r.c.Status == certificates.Issued || r.c.Status == certificates.Pending) {
			return r.c, nil
		}
	}
	return certificates.Certificate{}, certificates.ErrNotFound
}
