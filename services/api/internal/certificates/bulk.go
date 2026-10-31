package certificates

import (
	"archive/zip"
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/certificate-os/api/internal/audit"
	"github.com/certificate-os/api/internal/templates"
	"github.com/certificate-os/api/internal/tenancy"
)

// Bulk issuing (M4c). A job issues one template version to every eligible enrollment of one batch. The
// eligible enrollments are fixed when the job is created (one item each). Items go through the normal
// Service.Issue path one at a time, so every certificate is rendered, stored and numbered exactly like a
// single one, and the one-live-certificate-per-enrollment rule makes duplicates impossible even if a job
// is resumed or two requests race. At most one job per institute is active at a time.

var (
	// ErrJobActive: the institute already has a queued or processing job.
	ErrJobActive = errors.New("another bulk job is already running")
	// ErrNothingToIssue: no enrollment of the batch is eligible.
	ErrNothingToIssue = errors.New("nothing to issue")
)

// MaxJobItems bounds one job (a larger batch is issued in several jobs).
const MaxJobItems = 500

// JobLease is how long a runner owns a job without making progress (renewed with every item).
const JobLease = 2 * time.Minute

type JobStatus string

const (
	JobQueued              JobStatus = "queued"
	JobProcessing          JobStatus = "processing"
	JobCompleted           JobStatus = "completed"
	JobCompletedWithErrors JobStatus = "completed_with_errors"
	JobFailed              JobStatus = "failed"
	JobCancelled           JobStatus = "cancelled"
)

func (s JobStatus) Active() bool   { return s == JobQueued || s == JobProcessing }
func (s JobStatus) Finished() bool { return !s.Active() }
func (s JobStatus) Valid() bool {
	switch s {
	case JobQueued, JobProcessing, JobCompleted, JobCompletedWithErrors, JobFailed, JobCancelled:
		return true
	}
	return false
}

type ItemStatus string

const (
	ItemQueued     ItemStatus = "queued"
	ItemProcessing ItemStatus = "processing"
	ItemSucceeded  ItemStatus = "succeeded"
	ItemFailed     ItemStatus = "failed"
)

func (s ItemStatus) Valid() bool {
	return s == ItemQueued || s == ItemProcessing || s == ItemSucceeded || s == ItemFailed
}

// Job is a bulk issuing job with live counts (derived from its items).
type Job struct {
	ID, BatchID, BatchName           string
	TemplateID, TemplateName         string
	TemplateVersion                  int
	Status                           JobStatus
	Total, Succeeded, Failed         int
	Remaining                        int // queued + processing
	SkippedIssued, SkippedIneligible int
	CreatedBy                        string
	CreatedAt                        time.Time
	StartedAt, FinishedAt            *time.Time
}

// JobItem is one enrollment of a job.
type JobItem struct {
	EnrollmentID, StudentName        string
	Position, Attempts               int
	Status                           ItemStatus
	CertificateID, CertificateNumber string
	Error                            string // short, no personal data
}

// Preview counts what a job for a batch would do, before anything is created.
type Preview struct {
	BatchName     string
	Eligible      int // would be issued
	AlreadyIssued int // already hold a live certificate
	NotEligible   int // not completed, or the student is archived
}

type JobInput struct {
	BatchID, TemplateID string
	Version             int // 0 = the template's current version, pinned at creation
	CreatedBy           string
	Now                 time.Time
}

// ItemResult records how one item ended: a certificate id, or a short error.
type ItemResult struct{ CertificateID, Error string }

// JobStore persists bulk jobs. Same rules as Store: every method takes the Scope, another institute's id is
// indistinguishable from a missing one, mutations are audited in the same transaction.
type JobStore interface {
	// Preview counts the batch's enrollments by outcome (ErrNotFound for an unknown batch or template; an
	// archived template is an *EligibilityError).
	Preview(ctx context.Context, s tenancy.Scope, batchID, templateID string) (Preview, error)
	// CreateJob fixes the eligible enrollments as items (ErrNothingToIssue when none, ErrJobActive when the
	// institute already has an active job, ErrNotFound, *EligibilityError for an archived template).
	CreateJob(ctx context.Context, s tenancy.Scope, in JobInput, ev audit.Event) (Job, error)
	GetJob(ctx context.Context, s tenancy.Scope, id string) (Job, error)
	// ListJobs returns up to limit+1 jobs, newest first.
	ListJobs(ctx context.Context, s tenancy.Scope, limit, offset int) ([]Job, error)
	// ListItems returns up to limit+1 items by position; status "" means all.
	ListItems(ctx context.Context, s tenancy.Scope, jobID string, status ItemStatus, limit, offset int) ([]JobItem, error)
	// ClaimJob gives a runner the job: allowed when queued, or processing with an expired lease. Items left
	// "processing" by a dead runner go back to queued. ErrState otherwise (including a suspended institute).
	ClaimJob(ctx context.Context, s tenancy.Scope, id, token string, now time.Time, lease time.Duration, ev audit.Event) (Job, error)
	// NextItem marks the next queued item processing and renews the lease. ok=false when none is left.
	// ErrState when the token no longer owns a processing job, or the institute is not active.
	NextItem(ctx context.Context, s tenancy.Scope, jobID, token string, now time.Time, lease time.Duration) (item JobItem, ok bool, err error)
	// RecordItem stores an item's result. Requires the runner's token (even if the job was cancelled meanwhile).
	RecordItem(ctx context.Context, s tenancy.Scope, jobID, enrollmentID, token string, res ItemResult, now time.Time) error
	// FinishJob sets completed / completed_with_errors / failed from the item counts. ErrState unless the token owns a processing job with nothing left.
	FinishJob(ctx context.Context, s tenancy.Scope, jobID, token string, now time.Time, ev audit.Event) (Job, error)
	// CancelJob stops an active job; queued items stay queued. ErrState if already finished.
	CancelJob(ctx context.Context, s tenancy.Scope, id string, now time.Time, ev audit.Event) (Job, error)
	// RetryFailed puts the failed items of a completed_with_errors/failed job back in the queue and the job
	// back to queued; it returns how many. ErrJobActive when another job is active, ErrState otherwise.
	RetryFailed(ctx context.Context, s tenancy.Scope, id string, now time.Time, ev audit.Event) (Job, int, error)
	// FindLive returns the pending/issued certificate of an enrollment (ErrNotFound when none).
	FindLive(ctx context.Context, s tenancy.Scope, enrollmentID string) (Certificate, error)
}

func jobEvent(t tenancy.Tenant, action, reqID string, meta map[string]any) audit.Event {
	return audit.Event{ActorType: audit.ActorUser, ActorUserID: t.UserID, Action: action, EntityType: "bulk_job", RequestID: reqID, Metadata: meta}
}

// Jobs is the bulk issuing service. It owns the background runners.
type Jobs struct {
	Svc   *Service
	Store JobStore
	Log   func(msg string, args ...any)
	// Audit records exports (optional; without it nothing is recorded).
	Audit audit.Writer
	// Go starts a runner. The default runs it in a goroutine tracked by Wait; tests may run it inline.
	Go func(func())

	baseCtx context.Context
	wg      sync.WaitGroup
	mu      sync.Mutex
	seq     int
}

// NewJobs creates the service. baseCtx ends when the process shuts down: runners stop between items and the
// job stays resumable.
func NewJobs(baseCtx context.Context, svc *Service, st JobStore, log func(msg string, args ...any)) *Jobs {
	if log == nil {
		log = func(string, ...any) {}
	}
	j := &Jobs{Svc: svc, Store: st, Log: log, baseCtx: baseCtx}
	j.Go = func(f func()) {
		j.wg.Add(1)
		go func() { defer j.wg.Done(); f() }()
	}
	return j
}

// Wait blocks until the running runners return (or ctx ends).
func (j *Jobs) Wait(ctx context.Context) {
	done := make(chan struct{})
	go func() { j.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
	}
}

func (j *Jobs) token() (string, error) { return NewPublicID() }

type BulkRequest struct {
	BatchID, TemplateID string
	TemplateVersion     int
}

func (r BulkRequest) validate() error {
	p := map[string]string{}
	if strings.TrimSpace(r.BatchID) == "" {
		p["batch_id"] = "Choose a batch."
	}
	if strings.TrimSpace(r.TemplateID) == "" {
		p["template_id"] = "Choose a template."
	}
	if r.TemplateVersion < 0 || r.TemplateVersion > templates.MaxVersions {
		p["template_version"] = "Use a version number from the template's history."
	}
	if len(p) > 0 {
		return &ValidationError{Problems: p}
	}
	return nil
}

// Preview shows what a job would do.
func (j *Jobs) Preview(ctx context.Context, t tenancy.Tenant, batchID, templateID string) (Preview, error) {
	if err := (BulkRequest{BatchID: batchID, TemplateID: templateID}).validate(); err != nil {
		return Preview{}, err
	}
	return j.Store.Preview(ctx, t.Scope(), batchID, templateID)
}

// Start creates the job and begins processing it in the background.
func (j *Jobs) Start(ctx context.Context, t tenancy.Tenant, req BulkRequest, reqID string) (Job, error) {
	if err := req.validate(); err != nil {
		return Job{}, err
	}
	if j.Svc.renderOff() {
		return Job{}, ErrRenderUnavailable // nothing written
	}
	job, err := j.Store.CreateJob(ctx, t.Scope(), JobInput{BatchID: req.BatchID, TemplateID: req.TemplateID, Version: req.TemplateVersion,
		CreatedBy: t.UserID, Now: j.Svc.Now()}, jobEvent(t, "certificate.bulk_created", reqID, map[string]any{
		"batch_id": req.BatchID, "template_id": req.TemplateID}))
	if err != nil {
		return Job{}, err
	}
	return j.launch(ctx, t, job.ID, reqID)
}

// Resume restarts a stalled job (its runner died, e.g. after a restart) or a queued one.
func (j *Jobs) Resume(ctx context.Context, t tenancy.Tenant, id, reqID string) (Job, error) {
	if j.Svc.renderOff() {
		return Job{}, ErrRenderUnavailable
	}
	return j.launch(ctx, t, id, reqID)
}

// RetryFailed requeues the failed items of a finished job and runs it again.
func (j *Jobs) RetryFailed(ctx context.Context, t tenancy.Tenant, id, reqID string) (Job, error) {
	if j.Svc.renderOff() {
		return Job{}, ErrRenderUnavailable
	}
	_, n, err := j.Store.RetryFailed(ctx, t.Scope(), id, j.Svc.Now(), jobEvent(t, "certificate.bulk_retry", reqID, map[string]any{}))
	if err != nil {
		return Job{}, err
	}
	_ = n
	return j.launch(ctx, t, id, reqID)
}

func (j *Jobs) Cancel(ctx context.Context, t tenancy.Tenant, id, reqID string) (Job, error) {
	return j.Store.CancelJob(ctx, t.Scope(), id, j.Svc.Now(), jobEvent(t, "certificate.bulk_cancelled", reqID, map[string]any{}))
}

func (j *Jobs) Get(ctx context.Context, t tenancy.Tenant, id string) (Job, error) {
	return j.Store.GetJob(ctx, t.Scope(), id)
}

func (j *Jobs) List(ctx context.Context, t tenancy.Tenant, limit, offset int) ([]Job, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}
	return j.Store.ListJobs(ctx, t.Scope(), limit, offset)
}

func (j *Jobs) Items(ctx context.Context, t tenancy.Tenant, id string, status ItemStatus, limit, offset int) ([]JobItem, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}
	return j.Store.ListItems(ctx, t.Scope(), id, status, limit, offset)
}

func (j *Jobs) launch(ctx context.Context, t tenancy.Tenant, id, reqID string) (Job, error) {
	tok, err := j.token()
	if err != nil {
		return Job{}, err
	}
	job, err := j.Store.ClaimJob(ctx, t.Scope(), id, tok, j.Svc.Now(), JobLease, jobEvent(t, "certificate.bulk_started", reqID, map[string]any{}))
	if err != nil {
		return Job{}, err
	}
	// The runner acts as the job's creator; permissions were checked when the request came in.
	rt := tenancy.Tenant{Institute: t.Institute, UserID: job.CreatedBy}
	j.Go(func() { j.run(rt, job, tok, reqID) })
	return job, nil
}

// run processes the queued items one by one until none is left, the job is cancelled or taken over, or
// the process shuts down. Whatever happens, finished items stay finished and the rest stays resumable.
func (j *Jobs) run(t tenancy.Tenant, job Job, tok, reqID string) {
	ctx := j.baseCtx
	s := t.Scope()
	for {
		if ctx.Err() != nil {
			return
		}
		item, ok, err := j.Store.NextItem(ctx, s, job.ID, tok, j.Svc.Now(), JobLease)
		if err != nil {
			if !errors.Is(err, ErrState) && ctx.Err() == nil {
				j.Log("bulk job: next item failed", "job", job.ID, "err", err)
			}
			return
		}
		if !ok {
			break
		}
		res, abort := j.process(ctx, t, job, item, reqID)
		if abort {
			return // shutting down: the item goes back to the queue on resume
		}
		if err := j.Store.RecordItem(context.WithoutCancel(ctx), s, job.ID, item.EnrollmentID, tok, res, j.Svc.Now()); err != nil {
			j.Log("bulk job: could not record an item", "job", job.ID, "err", err)
			return
		}
	}
	if _, err := j.Store.FinishJob(context.WithoutCancel(ctx), s, job.ID, tok, j.Svc.Now(),
		audit.Event{ActorType: audit.ActorSystem, Action: "certificate.bulk_finished", EntityType: "bulk_job", RequestID: reqID}); err != nil && !errors.Is(err, ErrState) {
		j.Log("bulk job: could not finish", "job", job.ID, "err", err)
	}
}

func (j *Jobs) process(ctx context.Context, t tenancy.Tenant, job Job, item JobItem, reqID string) (res ItemResult, abort bool) {
	c, err := j.Svc.Issue(ctx, t, IssueRequest{EnrollmentID: item.EnrollmentID, TemplateID: job.TemplateID, TemplateVersion: job.TemplateVersion}, reqID)
	if err == nil {
		return ItemResult{CertificateID: c.ID}, false
	}
	if ctx.Err() != nil {
		return ItemResult{}, true
	}
	var ee *EligibilityError
	switch {
	case errors.Is(err, ErrAlreadyIssued):
		// A previous attempt of THIS job may have finished the certificate just before it died: adopt it.
		live, ferr := j.Store.FindLive(ctx, t.Scope(), item.EnrollmentID)
		if ferr == nil && live.Status == Issued && live.TemplateID == job.TemplateID && live.TemplateVersion == job.TemplateVersion &&
			live.IssuedBy == job.CreatedBy && !live.CreatedAt.Before(job.CreatedAt) {
			return ItemResult{CertificateID: live.ID}, false
		}
		return ItemResult{Error: "This enrollment already has a certificate."}, false
	case errors.As(err, &ee):
		return ItemResult{Error: truncate(ee.Reason)}, false
	case errors.Is(err, ErrNotFound):
		return ItemResult{Error: "The enrollment or template no longer exists."}, false
	case errors.Is(err, ErrRenderUnavailable):
		return ItemResult{Error: "PDF rendering is not available."}, false
	case errors.Is(err, ErrRenderFailed):
		j.Log("bulk job: rendering failed", "job", job.ID, "err", err)
		return ItemResult{Error: "The PDF could not be created. Retry this item."}, false
	}
	j.Log("bulk job: item failed", "job", job.ID, "err", fmt.Sprint(err))
	return ItemResult{Error: "Unexpected error. Retry this item."}, false
}

func truncate(s string) string {
	r := []rune(s)
	if len(r) > 200 {
		return string(r[:200])
	}
	return s
}

// csvSafe stops spreadsheet programs from running a cell as a formula.
func csvSafe(v string) string {
	if v != "" && strings.ContainsAny(v[:1], "=+-@\t\r") {
		return "'" + v
	}
	return v
}

// ExportZIP streams the PDFs of a finished job's issued certificates (revoked ones are left out) plus a
// manifest.csv. It checks everything it can BEFORE writing the first byte; once streaming has begun a
// failure returns an error and the caller must abort the response, so a truncated archive is never
// mistaken for a complete one.
func (j *Jobs) ExportZIP(ctx context.Context, t tenancy.Tenant, id string, begin func(filename string) io.Writer, reqID string) error {
	job, err := j.Store.GetJob(ctx, t.Scope(), id)
	if err != nil {
		return err
	}
	if job.Status.Active() || job.Succeeded == 0 {
		return ErrState
	}
	type entry struct {
		c       Certificate
		student string
	}
	var entries []entry
	for off := 0; ; off += 100 {
		items, err := j.Store.ListItems(ctx, t.Scope(), id, ItemSucceeded, 100, off)
		if err != nil {
			return err
		}
		more := len(items) > 100
		if more {
			items = items[:100]
		}
		for _, it := range items {
			c, err := j.Svc.Store.Get(ctx, t.Scope(), it.CertificateID)
			if err != nil {
				return err
			}
			if c.Status == Issued {
				entries = append(entries, entry{c, it.StudentName})
			}
		}
		if !more {
			break
		}
	}
	if len(entries) == 0 {
		return ErrState // everything in the job has been revoked since
	}
	if j.Audit != nil {
		if err := j.Audit.Record(ctx, audit.Event{ActorType: audit.ActorUser, ActorUserID: t.UserID, Action: "certificate.bulk_exported",
			EntityType: "bulk_job", EntityID: id, InstituteID: t.Institute.ID, RequestID: reqID, Metadata: map[string]any{"files": len(entries)}}); err != nil {
			return err
		}
	}
	zw := zip.NewWriter(begin("certificates-" + strings.ToLower(shortID(id)) + ".zip"))
	rows := [][]string{{"certificate_number", "student_name", "status", "issue_date", "pdf_sha256", "verify_url"}}
	for _, e := range entries {
		p, err := j.Svc.Store.GetPDF(ctx, t.Scope(), e.c.ID)
		if err != nil {
			return err
		}
		w, err := zw.CreateHeader(&zip.FileHeader{Name: p.Number + ".pdf", Method: zip.Store, Modified: e.c.CreatedAt})
		if err != nil {
			return err
		}
		if _, err := w.Write(p.Bytes); err != nil {
			return err
		}
		rows = append(rows, []string{e.c.Number, csvSafe(e.student), string(e.c.Status), e.c.Snapshot.IssueDate, p.SHA256, j.Svc.VerifyURL(e.c.PublicID)})
	}
	w, err := zw.CreateHeader(&zip.FileHeader{Name: "manifest.csv", Method: zip.Deflate, Modified: j.Svc.Now()})
	if err != nil {
		return err
	}
	if err := csv.NewWriter(w).WriteAll(rows); err != nil {
		return err
	}
	return zw.Close()
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}
