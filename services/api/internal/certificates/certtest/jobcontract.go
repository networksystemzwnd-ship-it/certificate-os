package certtest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/certificate-os/api/internal/audit"
	"github.com/certificate-os/api/internal/certificates"
	"github.com/certificate-os/api/internal/tenancy"
)

// JobHarness wires one JobStore implementation into the shared contract.
type JobHarness struct {
	Harness
	Jobs certificates.JobStore
	// SetActive suspends (false) or reactivates (true) the institute.
	SetActive func(s tenancy.Scope, active bool)
	// JobAudit lists the audit actions of a job, oldest first, and the metadata of the newest one.
	JobAudit func(jobID string) (actions []string, lastMeta map[string]any)
}

func jev(action string) audit.Event {
	return audit.Event{ActorType: audit.ActorSystem, Action: action, EntityType: "bulk_job"}
}

// RunJobStoreContract is the executable specification every certificates.JobStore must satisfy.
func RunJobStoreContract(t *testing.T, newHarness func(t *testing.T) JobHarness) {
	ctx := context.Background()
	must := func(t *testing.T, err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	const lease = time.Minute
	mk := func(h JobHarness, s tenancy.Scope, names ...string) []Refs {
		var all []Refs
		for i, n := range names {
			o := FixtureOpts{StudentName: n}
			if i > 0 {
				o.BatchID, o.TemplateID = all[0].BatchID, all[0].TemplateID
			}
			all = append(all, h.NewFixture(s, o))
		}
		return all
	}
	create := func(h JobHarness, s tenancy.Scope, u string, r Refs, at time.Time) (certificates.Job, error) {
		return h.Jobs.CreateJob(ctx, s, certificates.JobInput{BatchID: r.BatchID, TemplateID: r.TemplateID, CreatedBy: u, Now: at}, jev("certificate.bulk_created"))
	}
	claim := func(h JobHarness, s tenancy.Scope, id, tok string, at time.Time) (certificates.Job, error) {
		return h.Jobs.ClaimJob(ctx, s, id, tok, at, lease, jev("certificate.bulk_started"))
	}
	issue := func(h JobHarness, s tenancy.Scope, r Refs, u string, at time.Time) certificates.Certificate {
		t.Helper()
		c, err := h.Store.BeginIssue(ctx, s, certificates.BeginInput{EnrollmentID: r.EnrollmentID, TemplateID: r.TemplateID, PublicID: pub(), IssuedBy: u, Now: at}, ev("certificate.issue_started"))
		must(t, err)
		c, err = h.Store.Complete(ctx, s, c.ID, []byte("%PDF-1.4 fake "+c.Number), ev("certificate.issued"))
		must(t, err)
		return c
	}
	t.Run("create fixes the eligible enrollments in order and counts what is skipped", func(t *testing.T) {
		h := newHarness(t)
		s, u := h.NewScope(), h.NewUser()
		rs := mk(h, s, "Asha", "Bela", "Chitra")
		bad := h.NewFixture(s, FixtureOpts{StudentName: "Dev", Incomplete: true, BatchID: rs[0].BatchID, TemplateID: rs[0].TemplateID})
		_ = bad
		issue(h, s, rs[1], u, t0) // Bela already holds a certificate
		pv, err := h.Jobs.Preview(ctx, s, rs[0].BatchID, rs[0].TemplateID)
		must(t, err)
		if pv.Eligible != 2 || pv.AlreadyIssued != 1 || pv.NotEligible != 1 {
			t.Fatalf("preview %+v", pv)
		}
		j, err := create(h, s, u, rs[0], t0)
		must(t, err)
		if j.Status != certificates.JobQueued || j.Total != 2 || j.Remaining != 2 || j.Succeeded != 0 || j.Failed != 0 ||
			j.SkippedIssued != 1 || j.SkippedIneligible != 1 || j.TemplateVersion != 1 || j.CreatedBy != u || !j.CreatedAt.Equal(t0) {
			t.Fatalf("job %+v", j)
		}
		items, err := h.Jobs.ListItems(ctx, s, j.ID, "", 100, 0)
		must(t, err)
		if len(items) != 2 || items[0].StudentName != "Asha" || items[1].StudentName != "Chitra" || items[0].Position != 1 || items[1].Position != 2 ||
			items[0].Status != certificates.ItemQueued {
			t.Fatalf("items %+v", items)
		}
		got, err := h.Jobs.GetJob(ctx, s, j.ID)
		must(t, err)
		if got.ID != j.ID || got.Total != 2 {
			t.Fatalf("get %+v", got)
		}
		acts, meta := h.JobAudit(j.ID)
		if len(acts) != 1 || acts[0] != "certificate.bulk_created" || meta["total"] != float64(2) && meta["total"] != 2 {
			t.Fatalf("audit %v %v", acts, meta)
		}
		for k := range meta {
			if k == "student_name" || k == "name" || k == "email" {
				t.Fatalf("personal data in audit metadata: %v", meta)
			}
		}
	})

	t.Run("a job needs something to issue, a real batch and template, and a usable template", func(t *testing.T) {
		h := newHarness(t)
		s, u := h.NewScope(), h.NewUser()
		done := h.NewFixture(s, FixtureOpts{})
		issue(h, s, done, u, t0)
		_, err := create(h, s, u, done, t0)
		is(t, err, certificates.ErrNothingToIssue, "everything already issued")
		other := h.NewFixture(s, FixtureOpts{})
		_, err = h.Jobs.CreateJob(ctx, s, certificates.JobInput{BatchID: other.BatchID, TemplateID: "00000000-0000-0000-0000-000000000000", CreatedBy: u, Now: t0}, jev("certificate.bulk_created"))
		is(t, err, certificates.ErrNotFound, "unknown template")
		_, err = h.Jobs.CreateJob(ctx, s, certificates.JobInput{BatchID: "00000000-0000-0000-0000-000000000000", TemplateID: other.TemplateID, CreatedBy: u, Now: t0}, jev("certificate.bulk_created"))
		is(t, err, certificates.ErrNotFound, "unknown batch")
		_, err = h.Jobs.CreateJob(ctx, s, certificates.JobInput{BatchID: other.BatchID, TemplateID: other.TemplateID, Version: 9, CreatedBy: u, Now: t0}, jev("certificate.bulk_created"))
		is(t, err, certificates.ErrNotFound, "unknown version")
		arch := h.NewFixture(s, FixtureOpts{TemplateArchived: true})
		_, err = create(h, s, u, arch, t0)
		var ee *certificates.EligibilityError
		if !errorsAs(err, &ee) || ee.Field != "template_id" {
			t.Fatalf("archived template: %v", err)
		}
		_, err = h.Jobs.Preview(ctx, s, arch.BatchID, arch.TemplateID)
		if !errorsAs(err, &ee) {
			t.Fatalf("preview archived: %v", err)
		}
	})

	t.Run("only one active job per institute; another institute is unaffected; a finished job frees the slot", func(t *testing.T) {
		h := newHarness(t)
		s, s2, u := h.NewScope(), h.NewScope(), h.NewUser()
		a, b := h.NewFixture(s, FixtureOpts{}), h.NewFixture(s, FixtureOpts{})
		j, err := create(h, s, u, a, t0)
		must(t, err)
		_, err = create(h, s, u, b, t0)
		is(t, err, certificates.ErrJobActive, "second active job")
		c2 := h.NewFixture(s2, FixtureOpts{})
		_, err = create(h, s2, h.NewUser(), c2, t0)
		must(t, err)
		_, err = h.Jobs.CancelJob(ctx, s, j.ID, t0, jev("certificate.bulk_cancelled"))
		must(t, err)
		_, err = create(h, s, u, b, t0.Add(time.Minute))
		must(t, err)
		jobs, err := h.Jobs.ListJobs(ctx, s, 10, 0)
		must(t, err)
		if len(jobs) != 2 || jobs[0].BatchID != b.BatchID || jobs[1].Status != certificates.JobCancelled {
			t.Fatalf("list %+v", jobs)
		}
	})

	t.Run("runner lifecycle: claim, items one by one, record, finish; wrong tokens are refused", func(t *testing.T) {
		h := newHarness(t)
		s, u := h.NewScope(), h.NewUser()
		rs := mk(h, s, "Asha", "Bela")
		j, err := create(h, s, u, rs[0], t0)
		must(t, err)
		_, _, err = h.Jobs.NextItem(ctx, s, j.ID, "tok", t0, lease)
		is(t, err, certificates.ErrState, "next item before claiming")
		j, err = claim(h, s, j.ID, "tok-1", t0)
		must(t, err)
		if j.Status != certificates.JobProcessing || j.StartedAt == nil {
			t.Fatalf("claimed %+v", j)
		}
		_, err = claim(h, s, j.ID, "tok-2", t0.Add(time.Second))
		is(t, err, certificates.ErrState, "second runner while the lease is valid")
		_, _, err = h.Jobs.NextItem(ctx, s, j.ID, "tok-2", t0, lease)
		is(t, err, certificates.ErrState, "foreign token")
		it, ok, err := h.Jobs.NextItem(ctx, s, j.ID, "tok-1", t0, lease)
		must(t, err)
		if !ok || it.EnrollmentID != rs[0].EnrollmentID || it.Status != certificates.ItemProcessing || it.Attempts != 1 {
			t.Fatalf("item %+v", it)
		}
		is(t, h.Jobs.RecordItem(ctx, s, j.ID, it.EnrollmentID, "tok-2", certificates.ItemResult{Error: "x"}, t0), certificates.ErrState, "record with a foreign token")
		is(t, h.Jobs.RecordItem(ctx, s, j.ID, it.EnrollmentID, "tok-1", certificates.ItemResult{}, t0), certificates.ErrState, "record without outcome")
		_, err = h.Jobs.FinishJob(ctx, s, j.ID, "tok-1", t0, jev("certificate.bulk_finished"))
		is(t, err, certificates.ErrState, "finish with items left")
		c := issue(h, s, rs[0], u, t0)
		must(t, h.Jobs.RecordItem(ctx, s, j.ID, it.EnrollmentID, "tok-1", certificates.ItemResult{CertificateID: c.ID}, t0))
		is(t, h.Jobs.RecordItem(ctx, s, j.ID, it.EnrollmentID, "tok-1", certificates.ItemResult{CertificateID: c.ID}, t0), certificates.ErrState, "record twice")
		is(t, h.Jobs.RecordItem(ctx, s, j.ID, "00000000-0000-0000-0000-000000000000", "tok-1", certificates.ItemResult{Error: "x"}, t0), certificates.ErrNotFound, "unknown item")
		it2, ok, err := h.Jobs.NextItem(ctx, s, j.ID, "tok-1", t0, lease)
		must(t, err)
		if !ok || it2.EnrollmentID != rs[1].EnrollmentID {
			t.Fatalf("second item %+v", it2)
		}
		c2 := issue(h, s, rs[1], u, t0)
		must(t, h.Jobs.RecordItem(ctx, s, j.ID, it2.EnrollmentID, "tok-1", certificates.ItemResult{CertificateID: c2.ID}, t0))
		if _, ok, err = h.Jobs.NextItem(ctx, s, j.ID, "tok-1", t0, lease); err != nil || ok {
			t.Fatalf("queue should be empty: %v %v", ok, err)
		}
		done, err := h.Jobs.FinishJob(ctx, s, j.ID, "tok-1", t0.Add(time.Minute), jev("certificate.bulk_finished"))
		must(t, err)
		if done.Status != certificates.JobCompleted || done.Succeeded != 2 || done.Failed != 0 || done.Remaining != 0 || done.FinishedAt == nil {
			t.Fatalf("finished %+v", done)
		}
		items, err := h.Jobs.ListItems(ctx, s, j.ID, certificates.ItemSucceeded, 10, 0)
		must(t, err)
		if len(items) != 2 || items[0].CertificateID != c.ID || items[0].CertificateNumber != c.Number {
			t.Fatalf("items %+v", items)
		}
		_, err = h.Jobs.FinishJob(ctx, s, j.ID, "tok-1", t0, jev("certificate.bulk_finished"))
		is(t, err, certificates.ErrState, "finish twice")
		_, err = claim(h, s, j.ID, "tok-3", t0.Add(time.Hour))
		is(t, err, certificates.ErrState, "a finished job cannot be claimed")
		acts, meta := h.JobAudit(j.ID)
		if len(acts) != 3 || acts[2] != "certificate.bulk_finished" || meta["status"] != "completed" {
			t.Fatalf("audit %v %v", acts, meta)
		}
	})

	t.Run("a dead runner's job is taken over after the lease; its half-done item goes back to the queue", func(t *testing.T) {
		h := newHarness(t)
		s, u := h.NewScope(), h.NewUser()
		rs := mk(h, s, "Asha", "Bela")
		j, err := create(h, s, u, rs[0], t0)
		must(t, err)
		_, err = claim(h, s, j.ID, "old", t0)
		must(t, err)
		it, _, err := h.Jobs.NextItem(ctx, s, j.ID, "old", t0, lease)
		must(t, err)
		if it.Attempts != 1 {
			t.Fatal("attempt count")
		}
		_, err = claim(h, s, j.ID, "new", t0.Add(lease-time.Second))
		is(t, err, certificates.ErrState, "lease still valid")
		got, err := claim(h, s, j.ID, "new", t0.Add(lease+time.Second))
		must(t, err)
		if got.Remaining != 2 || got.Status != certificates.JobProcessing {
			t.Fatalf("taken over %+v", got)
		}
		_, _, err = h.Jobs.NextItem(ctx, s, j.ID, "old", t0, lease)
		is(t, err, certificates.ErrState, "the old runner is locked out")
		is(t, h.Jobs.RecordItem(ctx, s, j.ID, it.EnrollmentID, "old", certificates.ItemResult{Error: "late"}, t0), certificates.ErrState, "late record by the old runner")
		again, ok, err := h.Jobs.NextItem(ctx, s, j.ID, "new", t0.Add(lease+time.Second), lease)
		must(t, err)
		if !ok || again.EnrollmentID != it.EnrollmentID || again.Attempts != 2 {
			t.Fatalf("retaken item %+v", again)
		}
	})

	t.Run("failures are kept per item; all failed = failed; retry-failed requeues only the failed ones", func(t *testing.T) {
		h := newHarness(t)
		s, u := h.NewScope(), h.NewUser()
		rs := mk(h, s, "Asha", "Bela", "Chitra")
		j, err := create(h, s, u, rs[0], t0)
		must(t, err)
		_, err = claim(h, s, j.ID, "t", t0)
		must(t, err)
		for i := 0; i < 3; i++ {
			it, ok, err := h.Jobs.NextItem(ctx, s, j.ID, "t", t0, lease)
			must(t, err)
			if !ok {
				t.Fatal("item expected")
			}
			if i == 1 {
				must(t, h.Jobs.RecordItem(ctx, s, j.ID, it.EnrollmentID, "t", certificates.ItemResult{Error: "The PDF could not be created. Retry this item."}, t0))
			} else {
				c := issue(h, s, rs[i], u, t0)
				must(t, h.Jobs.RecordItem(ctx, s, j.ID, it.EnrollmentID, "t", certificates.ItemResult{CertificateID: c.ID}, t0))
			}
		}
		done, err := h.Jobs.FinishJob(ctx, s, j.ID, "t", t0, jev("certificate.bulk_finished"))
		must(t, err)
		if done.Status != certificates.JobCompletedWithErrors || done.Succeeded != 2 || done.Failed != 1 {
			t.Fatalf("done %+v", done)
		}
		failed, err := h.Jobs.ListItems(ctx, s, j.ID, certificates.ItemFailed, 10, 0)
		must(t, err)
		if len(failed) != 1 || failed[0].StudentName != "Bela" || failed[0].Error == "" || failed[0].CertificateID != "" {
			t.Fatalf("failed items %+v", failed)
		}
		// A retry needs the institute's single active-job slot to be free.
		other := h.NewFixture(s, FixtureOpts{})
		blocker, err := create(h, s, u, other, t0)
		must(t, err)
		_, _, err = h.Jobs.RetryFailed(ctx, s, j.ID, t0, jev("certificate.bulk_retry"))
		is(t, err, certificates.ErrJobActive, "retry while another job is active")
		_, err = h.Jobs.CancelJob(ctx, s, blocker.ID, t0, jev("certificate.bulk_cancelled"))
		must(t, err)
		back, n, err := h.Jobs.RetryFailed(ctx, s, j.ID, t0.Add(time.Minute), jev("certificate.bulk_retry"))
		must(t, err)
		if n != 1 || back.Status != certificates.JobQueued || back.Remaining != 1 || back.Succeeded != 2 || back.Failed != 0 || back.FinishedAt != nil {
			t.Fatalf("retried %+v n=%d", back, n)
		}
		_, _, err = h.Jobs.RetryFailed(ctx, s, j.ID, t0, jev("certificate.bulk_retry"))
		is(t, err, certificates.ErrState, "retry of a queued job")
		// Fail everything: status failed.
		_, err = claim(h, s, j.ID, "t2", t0.Add(2*time.Minute))
		must(t, err)
		it, _, err := h.Jobs.NextItem(ctx, s, j.ID, "t2", t0, lease)
		must(t, err)
		if it.StudentName != "Bela" || it.Attempts != 2 {
			t.Fatalf("retried item %+v", it)
		}
		c := issue(h, s, rs[1], u, t0)
		must(t, h.Jobs.RecordItem(ctx, s, j.ID, it.EnrollmentID, "t2", certificates.ItemResult{CertificateID: c.ID}, t0))
		fin, err := h.Jobs.FinishJob(ctx, s, j.ID, "t2", t0, jev("certificate.bulk_finished"))
		must(t, err)
		if fin.Status != certificates.JobCompleted || fin.Succeeded != 3 {
			t.Fatalf("after retry %+v", fin)
		}

		s2 := h.NewScope()
		x := mk(h, s2, "Zed")
		j2, err := create(h, s2, h.NewUser(), x[0], t0)
		must(t, err)
		_, err = claim(h, s2, j2.ID, "z", t0)
		must(t, err)
		it, _, err = h.Jobs.NextItem(ctx, s2, j2.ID, "z", t0, lease)
		must(t, err)
		must(t, h.Jobs.RecordItem(ctx, s2, j2.ID, it.EnrollmentID, "z", certificates.ItemResult{Error: "nope"}, t0))
		fin, err = h.Jobs.FinishJob(ctx, s2, j2.ID, "z", t0, jev("certificate.bulk_finished"))
		must(t, err)
		if fin.Status != certificates.JobFailed || fin.Failed != 1 || fin.Succeeded != 0 {
			t.Fatalf("all failed %+v", fin)
		}
	})

	t.Run("cancel stops the runner, keeps queued items queued and still accepts the in-flight result", func(t *testing.T) {
		h := newHarness(t)
		s, u := h.NewScope(), h.NewUser()
		rs := mk(h, s, "Asha", "Bela")
		j, err := create(h, s, u, rs[0], t0)
		must(t, err)
		_, err = claim(h, s, j.ID, "t", t0)
		must(t, err)
		it, _, err := h.Jobs.NextItem(ctx, s, j.ID, "t", t0, lease)
		must(t, err)
		c, err := h.Jobs.CancelJob(ctx, s, j.ID, t0, jev("certificate.bulk_cancelled"))
		must(t, err)
		if c.Status != certificates.JobCancelled || c.FinishedAt == nil {
			t.Fatalf("cancelled %+v", c)
		}
		_, _, err = h.Jobs.NextItem(ctx, s, j.ID, "t", t0, lease)
		is(t, err, certificates.ErrState, "runner must stop")
		cert := issue(h, s, rs[0], u, t0)
		must(t, h.Jobs.RecordItem(ctx, s, j.ID, it.EnrollmentID, "t", certificates.ItemResult{CertificateID: cert.ID}, t0))
		got, err := h.Jobs.GetJob(ctx, s, j.ID)
		must(t, err)
		if got.Status != certificates.JobCancelled || got.Succeeded != 1 || got.Remaining != 1 {
			t.Fatalf("after cancel %+v", got)
		}
		_, err = h.Jobs.CancelJob(ctx, s, j.ID, t0, jev("certificate.bulk_cancelled"))
		is(t, err, certificates.ErrState, "cancel twice")
		_, err = h.Jobs.FinishJob(ctx, s, j.ID, "t", t0, jev("certificate.bulk_finished"))
		is(t, err, certificates.ErrState, "finish after cancel")
	})

	t.Run("a suspended institute cannot start or continue a job", func(t *testing.T) {
		h := newHarness(t)
		s, u := h.NewScope(), h.NewUser()
		rs := mk(h, s, "Asha", "Bela")
		j, err := create(h, s, u, rs[0], t0)
		must(t, err)
		h.SetActive(s, false)
		_, err = claim(h, s, j.ID, "t", t0)
		is(t, err, certificates.ErrState, "claim while suspended")
		h.SetActive(s, true)
		_, err = claim(h, s, j.ID, "t", t0)
		must(t, err)
		h.SetActive(s, false)
		_, _, err = h.Jobs.NextItem(ctx, s, j.ID, "t", t0, lease)
		is(t, err, certificates.ErrState, "next item while suspended")
		h.SetActive(s, true)
		if _, ok, err := h.Jobs.NextItem(ctx, s, j.ID, "t", t0, lease); err != nil || !ok {
			t.Fatalf("resumes after reactivation: %v %v", ok, err)
		}
	})

	t.Run("find live certificate", func(t *testing.T) {
		h := newHarness(t)
		s, u := h.NewScope(), h.NewUser()
		r := h.NewFixture(s, FixtureOpts{})
		_, err := h.Jobs.FindLive(ctx, s, r.EnrollmentID)
		is(t, err, certificates.ErrNotFound, "none yet")
		c := issue(h, s, r, u, t0)
		got, err := h.Jobs.FindLive(ctx, s, r.EnrollmentID)
		must(t, err)
		if got.ID != c.ID || got.Status != certificates.Issued || got.IssuedBy != u || got.TemplateID != r.TemplateID {
			t.Fatalf("live %+v", got)
		}
		_, err = h.Store.Revoke(ctx, s, c.ID, "mistake", u, ev("certificate.revoked"))
		must(t, err)
		_, err = h.Jobs.FindLive(ctx, s, r.EnrollmentID)
		is(t, err, certificates.ErrNotFound, "revoked is not live")
	})

	t.Run("another institute sees and changes nothing", func(t *testing.T) {
		h := newHarness(t)
		s1, s2, u := h.NewScope(), h.NewScope(), h.NewUser()
		rs := mk(h, s1, "Asha")
		j, err := create(h, s1, u, rs[0], t0)
		must(t, err)
		_, err = h.Jobs.GetJob(ctx, s2, j.ID)
		is(t, err, certificates.ErrNotFound, "get")
		_, err = h.Jobs.ListItems(ctx, s2, j.ID, "", 10, 0)
		is(t, err, certificates.ErrNotFound, "items")
		_, err = claim(h, s2, j.ID, "t", t0)
		is(t, err, certificates.ErrNotFound, "claim")
		_, err = h.Jobs.CancelJob(ctx, s2, j.ID, t0, jev("certificate.bulk_cancelled"))
		is(t, err, certificates.ErrNotFound, "cancel")
		_, _, err = h.Jobs.RetryFailed(ctx, s2, j.ID, t0, jev("certificate.bulk_retry"))
		is(t, err, certificates.ErrNotFound, "retry")
		_, err = create(h, s2, u, rs[0], t0)
		is(t, err, certificates.ErrNotFound, "create with a foreign batch")
		_, err = h.Jobs.Preview(ctx, s2, rs[0].BatchID, rs[0].TemplateID)
		is(t, err, certificates.ErrNotFound, "preview with a foreign batch")
		jobs, err := h.Jobs.ListJobs(ctx, s2, 10, 0)
		must(t, err)
		if len(jobs) != 0 {
			t.Fatalf("foreign list %+v", jobs)
		}
		got, err := h.Jobs.GetJob(ctx, s1, j.ID)
		must(t, err)
		if got.Status != certificates.JobQueued {
			t.Fatal("job changed by another institute")
		}
	})
}

func errorsAs(err error, target any) bool { return errors.As(err, target) }
