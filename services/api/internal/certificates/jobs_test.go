package certificates_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/certificate-os/api/internal/audit"
	"github.com/certificate-os/api/internal/audit/audittest"
	"github.com/certificate-os/api/internal/certificates"
	"github.com/certificate-os/api/internal/certificates/certtest"
	"github.com/certificate-os/api/internal/tenancy"
)

// scriptRenderer renders like pdf.Fake but can fail or react per call.
type scriptRenderer struct {
	mu     sync.Mutex
	calls  int
	before func(n int, html string) error // called before rendering; a non-nil error fails the render
}

func (r *scriptRenderer) HTMLToPDF(ctx context.Context, html string) ([]byte, error) {
	r.mu.Lock()
	r.calls++
	n := r.calls
	hook := r.before
	r.mu.Unlock()
	if hook != nil {
		if err := hook(n, html); err != nil {
			return nil, err
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return []byte("%PDF-1.4\n" + html + "\n%%EOF\n"), nil
}

type bulkEnv struct {
	store  *certtest.MemStore
	w      *audittest.MemWriter
	rend   *scriptRenderer
	svc    *certificates.Service
	jobs   *certificates.Jobs
	scope  tenancy.Scope
	tenant tenancy.Tenant
	refs   []certtest.Refs
	clock  time.Time
	cancel context.CancelFunc
}

func newBulk(t *testing.T, names ...string) *bulkEnv {
	t.Helper()
	w := audittest.NewMemWriter()
	e := &bulkEnv{store: certtest.NewMemStore(w), w: w, rend: &scriptRenderer{}, clock: time.Date(2026, 10, 26, 9, 0, 0, 0, time.UTC)}
	e.scope = tenancy.TrustedScope("inst-1")
	e.tenant = tenancy.Tenant{Institute: tenancy.Institute{ID: "inst-1"}, UserID: "user-1"}
	e.svc = certificates.NewService(e.store, e.rend, "https://certs.example.com")
	e.svc.Now = func() time.Time { return e.clock }
	e.newJobs(t)
	for i, n := range names {
		o := certtest.FixtureOpts{StudentName: n}
		if i > 0 {
			o.BatchID, o.TemplateID = e.refs[0].BatchID, e.refs[0].TemplateID
		}
		e.refs = append(e.refs, e.store.AddFixture(e.scope, o))
	}
	return e
}

// newJobs creates the job service (a "process") whose runners run inline, so tests are deterministic.
func (e *bulkEnv) newJobs(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	e.cancel = cancel
	t.Cleanup(cancel)
	e.jobs = certificates.NewJobs(ctx, e.svc, e.store, func(msg string, args ...any) { t.Logf("%s %v", msg, args) })
	e.jobs.Go = func(f func()) { f() }
}

func (e *bulkEnv) start(t *testing.T) certificates.Job {
	t.Helper()
	j, err := e.jobs.Start(context.Background(), e.tenant, certificates.BulkRequest{BatchID: e.refs[0].BatchID, TemplateID: e.refs[0].TemplateID}, "req-1")
	if err != nil {
		t.Fatal(err)
	}
	return j
}

func (e *bulkEnv) get(t *testing.T, id string) certificates.Job {
	t.Helper()
	j, err := e.jobs.Get(context.Background(), e.tenant, id)
	if err != nil {
		t.Fatal(err)
	}
	return j
}

func (e *bulkEnv) allCerts(t *testing.T) []certificates.Certificate {
	t.Helper()
	var out []certificates.Certificate
	for off := 0; ; off += 100 {
		page, err := e.svc.List(context.Background(), e.tenant, certificates.Filter{Limit: 100, Offset: off})
		if err != nil {
			t.Fatal(err)
		}
		more := len(page) > 100
		if more {
			page = page[:100]
		}
		out = append(out, page...)
		if !more {
			return out
		}
	}
}

// issuedPerEnrollment fails the test when an enrollment holds more than one live certificate.
func (e *bulkEnv) issuedPerEnrollment(t *testing.T) map[string]certificates.Certificate {
	t.Helper()
	live := map[string]certificates.Certificate{}
	for _, c := range e.allCerts(t) {
		if c.Status != certificates.Issued {
			continue
		}
		if prev, dup := live[c.EnrollmentID]; dup {
			t.Fatalf("duplicate certificates for one enrollment: %s and %s", prev.Number, c.Number)
		}
		live[c.EnrollmentID] = c
	}
	return live
}

// The M4 exit gate: a batch of 100 is issued without duplicates.
func TestBulkIssuesAHundredWithoutDuplicates(t *testing.T) {
	names := make([]string, 100)
	for i := range names {
		names[i] = fmt.Sprintf("Student %03d", i+1)
	}
	e := newBulk(t, names...)
	j := e.start(t)
	got := e.get(t, j.ID)
	if got.Status != certificates.JobCompleted || got.Total != 100 || got.Succeeded != 100 || got.Failed != 0 || got.Remaining != 0 || got.FinishedAt == nil {
		t.Fatalf("job %+v", got)
	}
	certs := e.allCerts(t)
	if len(certs) != 100 {
		t.Fatalf("certificates: %d, want 100", len(certs))
	}
	numbers, publics := map[string]bool{}, map[string]bool{}
	for _, c := range certs {
		if c.Status != certificates.Issued || c.PDFSHA256 == "" {
			t.Fatalf("not issued: %+v", c)
		}
		if numbers[c.Number] || publics[c.PublicID] {
			t.Fatalf("duplicate number or public id: %s", c.Number)
		}
		numbers[c.Number], publics[c.PublicID] = true, true
		p, err := e.svc.PDF(context.Background(), e.tenant, c.ID)
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(p.Bytes)
		if hex.EncodeToString(sum[:]) != c.PDFSHA256 || !strings.Contains(string(p.Bytes), c.Snapshot.StudentName) || !strings.Contains(string(p.Bytes), c.Number) {
			t.Fatalf("stored pdf does not match its record: %s", c.Number)
		}
	}
	for i := 1; i <= 100; i++ {
		if n := certificates.FormatNumber(2026, i); !numbers[n] {
			t.Fatalf("numbers are not gapless: %s missing", n)
		}
	}
	if len(e.issuedPerEnrollment(t)) != 100 {
		t.Fatal("every enrollment must hold exactly one issued certificate")
	}
	items, err := e.jobs.Items(context.Background(), e.tenant, j.ID, certificates.ItemSucceeded, 200, 0)
	if err != nil || len(items) != 100 {
		t.Fatalf("items: %d %v", len(items), err)
	}
	// Doing it again issues nothing and creates nothing.
	_, err = e.jobs.Start(context.Background(), e.tenant, certificates.BulkRequest{BatchID: e.refs[0].BatchID, TemplateID: e.refs[0].TemplateID}, "req-2")
	if !errors.Is(err, certificates.ErrNothingToIssue) {
		t.Fatalf("second start: %v", err)
	}
	if _, err := e.jobs.Resume(context.Background(), e.tenant, j.ID, "req-3"); !errors.Is(err, certificates.ErrState) {
		t.Fatalf("resume of a finished job: %v", err)
	}
	if len(e.allCerts(t)) != 100 {
		t.Fatal("repeating the job created certificates")
	}
}

func TestBulkPartialFailureAndRetry(t *testing.T) {
	e := newBulk(t, "Asha", "Bela", "Chitra")
	e.rend.before = func(_ int, html string) error {
		if strings.Contains(html, "Bela") {
			return errors.New("renderer exploded")
		}
		return nil
	}
	j := e.start(t)
	got := e.get(t, j.ID)
	if got.Status != certificates.JobCompletedWithErrors || got.Succeeded != 2 || got.Failed != 1 || got.Remaining != 0 {
		t.Fatalf("job %+v", got)
	}
	failed, _ := e.jobs.Items(context.Background(), e.tenant, j.ID, certificates.ItemFailed, 10, 0)
	if len(failed) != 1 || failed[0].StudentName != "Bela" || failed[0].Error == "" || strings.Contains(failed[0].Error, "exploded") {
		t.Fatalf("failed items %+v (the error must be short, without internals)", failed)
	}
	if len(e.issuedPerEnrollment(t)) != 2 {
		t.Fatal("two certificates expected")
	}
	// Retry once the renderer works: only Bela is processed; nobody else gets a second certificate.
	e.rend.before = nil
	if _, err := e.jobs.RetryFailed(context.Background(), e.tenant, j.ID, "req-r"); err != nil {
		t.Fatal(err)
	}
	got = e.get(t, j.ID)
	if got.Status != certificates.JobCompleted || got.Succeeded != 3 || got.Failed != 0 {
		t.Fatalf("after retry %+v", got)
	}
	live := e.issuedPerEnrollment(t)
	if len(live) != 3 {
		t.Fatalf("issued: %d", len(live))
	}
	failedRecords := 0
	for _, c := range e.allCerts(t) {
		if c.Status == certificates.Failed {
			failedRecords++
		}
	}
	if failedRecords != 1 {
		t.Fatalf("the failed first attempt stays as a failed record, got %d", failedRecords)
	}
	if _, err := e.jobs.RetryFailed(context.Background(), e.tenant, j.ID, "req-r2"); !errors.Is(err, certificates.ErrState) {
		t.Fatalf("nothing left to retry: %v", err)
	}
}

func TestBulkResumesAfterAShutdown(t *testing.T) {
	names := make([]string, 30)
	for i := range names {
		names[i] = fmt.Sprintf("Student %02d", i+1)
	}
	e := newBulk(t, names...)
	// The process is told to stop while the 12th certificate is being rendered.
	e.rend.before = func(n int, _ string) error {
		if n == 12 {
			e.cancel()
		}
		return nil
	}
	j := e.start(t)
	mid := e.get(t, j.ID)
	if mid.Status != certificates.JobProcessing || mid.Succeeded != 11 || mid.Remaining != 19 {
		t.Fatalf("stopped job %+v", mid)
	}
	// A new process cannot take over until the dead runner's lease has run out ...
	e.newJobs(t)
	e.rend.before = nil
	if _, err := e.jobs.Resume(context.Background(), e.tenant, j.ID, "req-2"); !errors.Is(err, certificates.ErrState) {
		t.Fatalf("resume while the lease is valid: %v", err)
	}
	// ... after that it continues where it stopped.
	e.clock = e.clock.Add(certificates.JobLease + time.Second)
	if _, err := e.jobs.Resume(context.Background(), e.tenant, j.ID, "req-3"); err != nil {
		t.Fatal(err)
	}
	done := e.get(t, j.ID)
	if done.Status != certificates.JobCompleted || done.Succeeded != 30 || done.Remaining != 0 {
		t.Fatalf("resumed job %+v", done)
	}
	if len(e.issuedPerEnrollment(t)) != 30 {
		t.Fatal("every enrollment must hold exactly one issued certificate")
	}
}

// A runner that died after the certificate was stored but before the item was recorded must not fail the
// item or issue a second certificate: the finished certificate is adopted.
func TestBulkAdoptsACertificateIssuedJustBeforeACrash(t *testing.T) {
	e := newBulk(t, "Asha", "Bela")
	ctx := context.Background()
	job, err := e.store.CreateJob(ctx, e.scope, certificates.JobInput{BatchID: e.refs[0].BatchID, TemplateID: e.refs[0].TemplateID, CreatedBy: "user-1", Now: e.clock},
		certtestEvent())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.ClaimJob(ctx, e.scope, job.ID, "dead", e.clock, certificates.JobLease, certtestEvent()); err != nil {
		t.Fatal(err)
	}
	it, _, err := e.store.NextItem(ctx, e.scope, job.ID, "dead", e.clock, certificates.JobLease)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Issue(ctx, e.tenant, certificates.IssueRequest{EnrollmentID: it.EnrollmentID, TemplateID: job.TemplateID, TemplateVersion: job.TemplateVersion}, "r"); err != nil {
		t.Fatal(err)
	}
	// ... the process dies here, before RecordItem.
	e.clock = e.clock.Add(certificates.JobLease + time.Second)
	if _, err := e.jobs.Resume(ctx, e.tenant, job.ID, "req-2"); err != nil {
		t.Fatal(err)
	}
	got := e.get(t, job.ID)
	if got.Status != certificates.JobCompleted || got.Succeeded != 2 || got.Failed != 0 {
		t.Fatalf("job %+v", got)
	}
	if len(e.allCerts(t)) != 2 || len(e.issuedPerEnrollment(t)) != 2 {
		t.Fatal("expected exactly two certificates")
	}
}

// A certificate someone else issued for an enrollment while the job was queued is not claimed by the job.
func TestBulkDoesNotAdoptForeignCertificates(t *testing.T) {
	e := newBulk(t, "Asha", "Bela")
	ctx := context.Background()
	job, err := e.store.CreateJob(ctx, e.scope, certificates.JobInput{BatchID: e.refs[0].BatchID, TemplateID: e.refs[0].TemplateID, CreatedBy: "user-1", Now: e.clock}, certtestEvent())
	if err != nil {
		t.Fatal(err)
	}
	other := tenancy.Tenant{Institute: e.tenant.Institute, UserID: "user-2"}
	if _, err := e.svc.Issue(ctx, other, certificates.IssueRequest{EnrollmentID: e.refs[1].EnrollmentID, TemplateID: e.refs[1].TemplateID}, "r"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.jobs.Resume(ctx, e.tenant, job.ID, "req-2"); err != nil {
		t.Fatal(err)
	}
	got := e.get(t, job.ID)
	if got.Status != certificates.JobCompletedWithErrors || got.Succeeded != 1 || got.Failed != 1 {
		t.Fatalf("job %+v", got)
	}
	failed, _ := e.jobs.Items(ctx, e.tenant, job.ID, certificates.ItemFailed, 10, 0)
	if len(failed) != 1 || failed[0].Error != "This enrollment already has a certificate." {
		t.Fatalf("failed %+v", failed)
	}
	if len(e.issuedPerEnrollment(t)) != 2 {
		t.Fatal("two certificates expected")
	}
}

func TestBulkCancelStopsTheRun(t *testing.T) {
	e := newBulk(t, "A1", "A2", "A3", "A4", "A5", "A6")
	var jobID string
	e.rend.before = func(n int, _ string) error {
		if n == 3 {
			if _, err := e.jobs.Cancel(context.Background(), e.tenant, jobID, "req-c"); err != nil {
				t.Errorf("cancel: %v", err)
			}
		}
		return nil
	}
	// The id is only known after Start returns, which is after the inline run: look it up on the first render.
	e.rend.before = func(n int, _ string) error {
		if n == 3 {
			list, _ := e.jobs.List(context.Background(), e.tenant, 5, 0)
			if _, err := e.jobs.Cancel(context.Background(), e.tenant, list[0].ID, "req-c"); err != nil {
				t.Errorf("cancel: %v", err)
			}
		}
		return nil
	}
	j := e.start(t)
	got := e.get(t, j.ID)
	if got.Status != certificates.JobCancelled || got.Succeeded != 3 || got.Remaining != 3 {
		t.Fatalf("cancelled job %+v", got)
	}
	if len(e.issuedPerEnrollment(t)) != 3 {
		t.Fatal("the in-flight certificate is kept, nothing after it is issued")
	}
	// The slot is free again.
	e.rend.before = nil
	if _, err := e.jobs.Start(context.Background(), e.tenant, certificates.BulkRequest{BatchID: e.refs[0].BatchID, TemplateID: e.refs[0].TemplateID}, "req-n"); err != nil {
		t.Fatal(err)
	}
	if len(e.issuedPerEnrollment(t)) != 6 {
		t.Fatal("a new job issues the rest")
	}
}

func TestBulkStopsWhenTheInstituteIsSuspended(t *testing.T) {
	e := newBulk(t, "A1", "A2", "A3", "A4")
	e.rend.before = func(n int, _ string) error {
		if n == 2 {
			e.store.SetInstituteActive(e.scope, false)
		}
		return nil
	}
	j := e.start(t)
	got := e.get(t, j.ID)
	if got.Status != certificates.JobProcessing || got.Succeeded != 2 {
		t.Fatalf("job %+v", got)
	}
	e.store.SetInstituteActive(e.scope, true)
	e.rend.before = nil
	e.clock = e.clock.Add(certificates.JobLease + time.Second)
	if _, err := e.jobs.Resume(context.Background(), e.tenant, j.ID, "r"); err != nil {
		t.Fatal(err)
	}
	if got = e.get(t, j.ID); got.Status != certificates.JobCompleted || got.Succeeded != 4 {
		t.Fatalf("job %+v", got)
	}
}

func TestBulkRefusesWithoutARenderer(t *testing.T) {
	e := newBulk(t, "Asha")
	svc := certificates.NewService(e.store, nil, "https://certs.example.com")
	jobs := certificates.NewJobs(context.Background(), svc, e.store, nil)
	_, err := jobs.Start(context.Background(), e.tenant, certificates.BulkRequest{BatchID: e.refs[0].BatchID, TemplateID: e.refs[0].TemplateID}, "r")
	if !errors.Is(err, certificates.ErrRenderUnavailable) {
		t.Fatalf("got %v", err)
	}
	if list, _ := jobs.List(context.Background(), e.tenant, 10, 0); len(list) != 0 {
		t.Fatal("nothing may be written")
	}
}

func TestBulkValidation(t *testing.T) {
	e := newBulk(t, "Asha")
	_, err := e.jobs.Start(context.Background(), e.tenant, certificates.BulkRequest{}, "r")
	var ve *certificates.ValidationError
	if !errors.As(err, &ve) || ve.Problems["batch_id"] == "" || ve.Problems["template_id"] == "" {
		t.Fatalf("got %v", err)
	}
}

func certtestEvent() audit.Event {
	return audit.Event{ActorType: audit.ActorSystem, Action: "certificate.bulk_test", EntityType: "bulk_job"}
}
