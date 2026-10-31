package deliverytest

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/certificate-os/api/internal/audit"
	"github.com/certificate-os/api/internal/certificates"
	"github.com/certificate-os/api/internal/certificates/certtest"
	"github.com/certificate-os/api/internal/delivery"
	"github.com/certificate-os/api/internal/tenancy"
)

// Harness wires one delivery.Store implementation (and the certificates store it reads) into the contract.
type Harness struct {
	certtest.Harness
	Deliveries delivery.Store
	// SetEmail sets the email address of the student the certificate belongs to ("" removes it).
	SetEmail func(s tenancy.Scope, certID, email string)
	// SetActive suspends (false) or reactivates (true) the institute.
	SetActive func(s tenancy.Scope, active bool)
	// Audit lists the actions of a delivery, oldest first, and the metadata of the newest one.
	Audit func(deliveryID string) (actions []string, lastMeta map[string]any)
}

var t0 = time.Date(2026, 10, 24, 10, 0, 0, 0, time.UTC)

func uev(action string) audit.Event {
	return audit.Event{ActorType: audit.ActorSystem, Action: action, EntityType: "delivery"}
}

func is(t *testing.T, err, want error, what string) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("%s: got %v, want %v", what, err, want)
	}
}

// RunStoreContract is the executable specification every delivery.Store must satisfy.
func RunStoreContract(t *testing.T, newHarness func(t *testing.T) Harness) {
	ctx := context.Background()
	must := func(t *testing.T, err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	// cert issues a certificate for a fresh student with an email address.
	cert := func(t *testing.T, h Harness, s tenancy.Scope, u, email string) certificates.Certificate {
		t.Helper()
		r := h.NewFixture(s, certtest.FixtureOpts{})
		pid, err := certificates.NewPublicID()
		must(t, err)
		c, err := h.Store.BeginIssue(ctx, s, certificates.BeginInput{EnrollmentID: r.EnrollmentID, TemplateID: r.TemplateID, PublicID: pid, IssuedBy: u, Now: t0},
			audit.Event{ActorType: audit.ActorSystem, Action: "certificate.issue_started", EntityType: "certificate"})
		must(t, err)
		c, err = h.Store.Complete(ctx, s, c.ID, []byte("%PDF-1.4 fake"), audit.Event{ActorType: audit.ActorSystem, Action: "certificate.issued", EntityType: "certificate"})
		must(t, err)
		if email != "" {
			h.SetEmail(s, c.ID, email)
		}
		return c
	}
	req := func(h Harness, s tenancy.Scope, u, certID string, at time.Time, skip bool) (delivery.Delivery, error) {
		return h.Deliveries.Request(ctx, s, delivery.RequestInput{CertificateID: certID, CreatedBy: u, Now: at, SkipAccepted: skip}, uev("delivery.requested"))
	}
	claim := func(h Harness, s tenancy.Scope, id, tok string, at time.Time) (delivery.Claim, error) {
		return h.Deliveries.Claim(ctx, s, id, tok, at, time.Minute)
	}
	finish := func(h Harness, s tenancy.Scope, id, tok string, in delivery.FinishInput, action string) (delivery.Delivery, error) {
		if in.Provider == "" {
			in.Provider = "fake"
		}
		return h.Deliveries.Finish(ctx, s, id, tok, in, uev(action))
	}

	t.Run("request queues a delivery to the address on file and audits it without the address", func(t *testing.T) {
		h := newHarness(t)
		s, u := h.NewScope(), h.NewUser()
		c := cert(t, h, s, u, "asha@example.com")
		d, err := req(h, s, u, c.ID, t0, false)
		must(t, err)
		if d.Status != delivery.Queued || d.Recipient != "asha@example.com" || d.CertificateID != c.ID || d.CertificateNumber != c.Number ||
			d.Attempts != 0 || d.MaxAttempts != delivery.MaxAttempts || d.Channel != "email" || d.CreatedBy != u ||
			d.NextAttemptAt == nil || !d.NextAttemptAt.Equal(t0) || d.FinishedAt != nil || d.LastError != "" {
			t.Fatalf("delivery %+v", d)
		}
		got, err := h.Deliveries.Get(ctx, s, d.ID)
		must(t, err)
		if got.ID != d.ID || got.Status != delivery.Queued || got.Recipient != "asha@example.com" {
			t.Fatalf("get %+v", got)
		}
		acts, meta := h.Audit(d.ID)
		if len(acts) != 1 || acts[0] != "delivery.requested" || meta["certificate_id"] != c.ID || meta["number"] != c.Number {
			t.Fatalf("audit %v %v", acts, meta)
		}
		for k, v := range meta {
			if s, ok := v.(string); ok && strings.Contains(s, "example.com") {
				t.Fatalf("audit metadata %q holds the address", k)
			}
		}
	})

	t.Run("request refuses what cannot be delivered", func(t *testing.T) {
		h := newHarness(t)
		s, o, u := h.NewScope(), h.NewScope(), h.NewUser()
		c := cert(t, h, s, u, "asha@example.com")
		_, err := req(h, s, u, "00000000-0000-0000-0000-000000000000", t0, false)
		is(t, err, delivery.ErrNotFound, "unknown certificate")
		_, err = req(h, o, u, c.ID, t0, false)
		is(t, err, delivery.ErrNotFound, "another institute's certificate")
		nomail := cert(t, h, s, u, "")
		_, err = req(h, s, u, nomail.ID, t0, false)
		is(t, err, delivery.ErrNoRecipient, "student without an email address")
		d, err := req(h, s, u, c.ID, t0, false)
		must(t, err)
		_, err = req(h, s, u, c.ID, t0, false)
		is(t, err, delivery.ErrActive, "second delivery in flight")
		_ = d
		rev := cert(t, h, s, u, "bela@example.com")
		_, err = h.Store.Revoke(ctx, s, rev.ID, "mistake", u, audit.Event{ActorType: audit.ActorSystem, Action: "certificate.revoked", EntityType: "certificate"})
		must(t, err)
		_, err = req(h, s, u, rev.ID, t0, false)
		is(t, err, delivery.ErrNotFound, "revoked certificate")
	})

	t.Run("an accepted delivery can be sent again, except when the request asks to skip delivered certificates", func(t *testing.T) {
		h := newHarness(t)
		s, u := h.NewScope(), h.NewUser()
		c := cert(t, h, s, u, "asha@example.com")
		d, err := req(h, s, u, c.ID, t0, false)
		must(t, err)
		cl, err := claim(h, s, d.ID, "tok-1", t0)
		must(t, err)
		_, err = finish(h, s, d.ID, cl.Token, delivery.FinishInput{Outcome: delivery.OutcomeAccepted, StartedAt: t0, At: t0.Add(time.Second)}, "delivery.accepted")
		must(t, err)
		_, err = req(h, s, u, c.ID, t0.Add(time.Minute), true)
		is(t, err, delivery.ErrAlreadyAccepted, "skip delivered")
		d2, err := req(h, s, u, c.ID, t0.Add(time.Minute), false)
		must(t, err)
		if d2.ID == d.ID || d2.Status != delivery.Queued {
			t.Fatalf("resend %+v", d2)
		}
	})

	t.Run("a certificate can only be sent a limited number of times", func(t *testing.T) {
		h := newHarness(t)
		s, u := h.NewScope(), h.NewUser()
		c := cert(t, h, s, u, "asha@example.com")
		for i := 0; i < delivery.MaxPerCertificate; i++ {
			at := t0.Add(time.Duration(i) * time.Hour)
			d, err := req(h, s, u, c.ID, at, false)
			must(t, err)
			cl, err := claim(h, s, d.ID, "tok", at)
			must(t, err)
			_, err = finish(h, s, d.ID, cl.Token, delivery.FinishInput{Outcome: delivery.OutcomeAccepted, StartedAt: at, At: at}, "delivery.accepted")
			must(t, err)
		}
		_, err := req(h, s, u, c.ID, t0.Add(48*time.Hour), false)
		is(t, err, delivery.ErrLimit, "eleventh delivery")
	})

	t.Run("claim, retry with backoff, lease takeover and final acceptance", func(t *testing.T) {
		h := newHarness(t)
		s, u := h.NewScope(), h.NewUser()
		c := cert(t, h, s, u, "asha@example.com")
		d, err := req(h, s, u, c.ID, t0, false)
		must(t, err)

		due, err := h.Deliveries.Due(ctx, t0.Add(-time.Second), 10)
		must(t, err)
		if len(due) != 0 {
			t.Fatalf("not yet due: %v", due)
		}
		due, err = h.Deliveries.Due(ctx, t0, 10)
		must(t, err)
		if len(due) != 1 || due[0].DeliveryID != d.ID || due[0].InstituteID != s.InstituteID() {
			t.Fatalf("due %v", due)
		}
		cl, err := claim(h, s, d.ID, "tok-a", t0)
		must(t, err)
		if cl.Delivery.Status != delivery.Sending || cl.Delivery.Attempts != 1 || cl.Delivery.NextAttemptAt != nil || cl.Token != "tok-a" {
			t.Fatalf("claim %+v", cl)
		}
		_, err = claim(h, s, d.ID, "tok-b", t0.Add(time.Second))
		is(t, err, delivery.ErrState, "second worker while leased")
		due, _ = h.Deliveries.Due(ctx, t0.Add(30*time.Second), 10)
		if len(due) != 0 {
			t.Fatalf("a leased delivery is not due: %v", due)
		}
		// The first worker dies; after the lease another takes over and the attempt counts.
		due, _ = h.Deliveries.Due(ctx, t0.Add(2*time.Minute), 10)
		if len(due) != 1 {
			t.Fatalf("expired lease must be due: %v", due)
		}
		cl2, err := claim(h, s, d.ID, "tok-b", t0.Add(2*time.Minute))
		must(t, err)
		if cl2.Delivery.Attempts != 2 {
			t.Fatalf("takeover attempts %d", cl2.Delivery.Attempts)
		}
		_, err = finish(h, s, d.ID, "tok-a", delivery.FinishInput{Outcome: delivery.OutcomeAccepted, StartedAt: t0, At: t0.Add(3 * time.Minute)}, "delivery.accepted")
		is(t, err, delivery.ErrState, "the dead worker's token")
		_, err = finish(h, s, d.ID, "", delivery.FinishInput{Outcome: delivery.OutcomeAccepted, StartedAt: t0, At: t0.Add(3 * time.Minute)}, "delivery.accepted")
		is(t, err, delivery.ErrState, "empty token")

		next := t0.Add(10 * time.Minute)
		d2, err := finish(h, s, d.ID, "tok-b", delivery.FinishInput{Outcome: delivery.OutcomeRetry, Code: "connect_failed", StartedAt: t0.Add(2 * time.Minute), At: t0.Add(3 * time.Minute), NextAttemptAt: next}, "delivery.attempt_failed")
		must(t, err)
		if d2.Status != delivery.Queued || d2.LastError != "connect_failed" || d2.NextAttemptAt == nil || !d2.NextAttemptAt.Equal(next) || d2.FinishedAt != nil || d2.Attempts != 2 {
			t.Fatalf("after retry %+v", d2)
		}
		due, _ = h.Deliveries.Due(ctx, next.Add(-time.Second), 10)
		if len(due) != 0 {
			t.Fatalf("backoff ignored: %v", due)
		}
		cl3, err := claim(h, s, d.ID, "tok-c", next)
		must(t, err)
		if cl3.Delivery.Attempts != 3 {
			t.Fatalf("attempts %d", cl3.Delivery.Attempts)
		}
		d3, err := finish(h, s, d.ID, "tok-c", delivery.FinishInput{Outcome: delivery.OutcomeAccepted, StartedAt: next, At: next.Add(time.Second)}, "delivery.accepted")
		must(t, err)
		if d3.Status != delivery.Accepted || d3.LastError != "" || d3.FinishedAt == nil || d3.NextAttemptAt != nil {
			t.Fatalf("accepted %+v", d3)
		}
		_, err = finish(h, s, d.ID, "tok-c", delivery.FinishInput{Outcome: delivery.OutcomeFailed, Code: "x_y", StartedAt: next, At: next}, "delivery.failed")
		is(t, err, delivery.ErrState, "accepted is final")
		_, err = claim(h, s, d.ID, "tok-d", next.Add(time.Hour))
		is(t, err, delivery.ErrState, "accepted cannot be claimed")
		due, _ = h.Deliveries.Due(ctx, next.Add(24*time.Hour), 10)
		if len(due) != 0 {
			t.Fatalf("finished work is not due: %v", due)
		}

		at, err := h.Deliveries.Attempts(ctx, s, d.ID)
		must(t, err)
		if len(at) != 2 || at[0].No != 2 || at[0].Status != "failed" || at[0].ErrorCode != "connect_failed" || at[1].No != 3 || at[1].Status != "accepted" || at[1].ErrorCode != "" || at[1].Provider != "fake" {
			// attempt 1 died with its worker and never recorded an outcome
			t.Fatalf("attempts %+v", at)
		}
		acts, _ := h.Audit(d.ID)
		want := []string{"delivery.requested", "delivery.attempt_failed", "delivery.accepted"}
		if strings.Join(acts, ",") != strings.Join(want, ",") {
			t.Fatalf("audit %v", acts)
		}
	})

	t.Run("a permanent failure is final until someone retries, and the retry uses the current address", func(t *testing.T) {
		h := newHarness(t)
		s, u := h.NewScope(), h.NewUser()
		c := cert(t, h, s, u, "typo@exmaple.com")
		d, err := req(h, s, u, c.ID, t0, false)
		must(t, err)
		cl, err := claim(h, s, d.ID, "tok-1", t0)
		must(t, err)
		f, err := finish(h, s, d.ID, cl.Token, delivery.FinishInput{Outcome: delivery.OutcomeFailed, Code: "recipient_rejected", StartedAt: t0, At: t0}, "delivery.failed")
		must(t, err)
		if f.Status != delivery.Failed || f.LastError != "recipient_rejected" || f.FinishedAt == nil || f.NextAttemptAt != nil {
			t.Fatalf("failed %+v", f)
		}
		_, err = h.Deliveries.Retry(ctx, s, "00000000-0000-0000-0000-000000000000", t0, uev("delivery.retry_requested"))
		is(t, err, delivery.ErrNotFound, "unknown delivery")
		h.SetEmail(s, c.ID, "asha@example.com")
		r, err := h.Deliveries.Retry(ctx, s, d.ID, t0.Add(time.Hour), uev("delivery.retry_requested"))
		must(t, err)
		if r.Status != delivery.Queued || r.Recipient != "asha@example.com" || r.Attempts != 1 || r.MaxAttempts != 1+delivery.MaxAttempts ||
			r.NextAttemptAt == nil || !r.NextAttemptAt.Equal(t0.Add(time.Hour)) || r.FinishedAt != nil {
			t.Fatalf("retried %+v", r)
		}
		_, err = h.Deliveries.Retry(ctx, s, d.ID, t0.Add(time.Hour), uev("delivery.retry_requested"))
		is(t, err, delivery.ErrState, "retry of a queued delivery")
		cl, err = claim(h, s, d.ID, "tok-2", t0.Add(time.Hour))
		must(t, err)
		if cl.Delivery.Attempts != 2 || cl.Delivery.Recipient != "asha@example.com" {
			t.Fatalf("claim after retry %+v", cl.Delivery)
		}
		// A failed delivery cannot be retried once the certificate is revoked or the student has no address.
		_, err = finish(h, s, d.ID, cl.Token, delivery.FinishInput{Outcome: delivery.OutcomeFailed, Code: "provider_rejected", StartedAt: t0, At: t0.Add(time.Hour)}, "delivery.failed")
		must(t, err)
		h.SetEmail(s, c.ID, "")
		_, err = h.Deliveries.Retry(ctx, s, d.ID, t0.Add(2*time.Hour), uev("delivery.retry_requested"))
		is(t, err, delivery.ErrNoRecipient, "no address any more")
		h.SetEmail(s, c.ID, "asha@example.com")
		_, err = h.Store.Revoke(ctx, s, c.ID, "mistake", u, audit.Event{ActorType: audit.ActorSystem, Action: "certificate.revoked", EntityType: "certificate"})
		must(t, err)
		_, err = h.Deliveries.Retry(ctx, s, d.ID, t0.Add(2*time.Hour), uev("delivery.retry_requested"))
		is(t, err, delivery.ErrNotFound, "revoked certificate")
	})

	t.Run("a worker that keeps dying cannot send forever", func(t *testing.T) {
		h := newHarness(t)
		s, u := h.NewScope(), h.NewUser()
		c := cert(t, h, s, u, "asha@example.com")
		d, err := req(h, s, u, c.ID, t0, false)
		must(t, err)
		at := t0
		for i := 1; i <= delivery.MaxAttempts; i++ {
			cl, err := claim(h, s, d.ID, "tok", at)
			must(t, err)
			if cl.Delivery.Attempts != i {
				t.Fatalf("attempt %d", cl.Delivery.Attempts)
			}
			at = at.Add(2 * time.Minute) // lease expires, nobody records an outcome
		}
		_, err = claim(h, s, d.ID, "tok", at)
		is(t, err, delivery.ErrState, "attempts used up")
		got, err := h.Deliveries.Get(ctx, s, d.ID)
		must(t, err)
		if got.Status != delivery.Failed || got.LastError != delivery.CodeInterrupted || got.FinishedAt == nil {
			t.Fatalf("after repeated crashes %+v", got)
		}
		due, _ := h.Deliveries.Due(ctx, at.Add(time.Hour), 10)
		if len(due) != 0 {
			t.Fatalf("still due: %v", due)
		}
	})

	t.Run("a suspended institute is not sent for; postponing counts no attempt", func(t *testing.T) {
		h := newHarness(t)
		s, u := h.NewScope(), h.NewUser()
		c := cert(t, h, s, u, "asha@example.com")
		d, err := req(h, s, u, c.ID, t0, false)
		must(t, err)
		h.SetActive(s, false)
		_, err = claim(h, s, d.ID, "tok", t0)
		is(t, err, delivery.ErrSuspended, "suspended")
		got, _ := h.Deliveries.Get(ctx, s, d.ID)
		if got.Status != delivery.Queued || got.Attempts != 0 {
			t.Fatalf("claim must change nothing: %+v", got)
		}
		later := t0.Add(delivery.SuspendedRecheck)
		must(t, h.Deliveries.Postpone(ctx, s, d.ID, later))
		due, _ := h.Deliveries.Due(ctx, t0.Add(time.Minute), 10)
		if len(due) != 0 {
			t.Fatalf("postponed work is due: %v", due)
		}
		due, _ = h.Deliveries.Due(ctx, later, 10)
		if len(due) != 1 {
			t.Fatalf("postponed work never returns: %v", due)
		}
		h.SetActive(s, true)
		_, err = claim(h, s, d.ID, "tok", later)
		must(t, err)
	})

	t.Run("one institute never sees or touches another's deliveries", func(t *testing.T) {
		h := newHarness(t)
		a, b, u := h.NewScope(), h.NewScope(), h.NewUser()
		c := cert(t, h, a, u, "asha@example.com")
		d, err := req(h, a, u, c.ID, t0, false)
		must(t, err)
		_, err = h.Deliveries.Get(ctx, b, d.ID)
		is(t, err, delivery.ErrNotFound, "get")
		_, err = h.Deliveries.Attempts(ctx, b, d.ID)
		is(t, err, delivery.ErrNotFound, "attempts")
		_, err = h.Deliveries.Retry(ctx, b, d.ID, t0, uev("delivery.retry_requested"))
		is(t, err, delivery.ErrNotFound, "retry")
		_, err = claim(h, b, d.ID, "tok", t0)
		if !errors.Is(err, delivery.ErrNotFound) && !errors.Is(err, delivery.ErrState) {
			t.Fatalf("foreign claim: %v", err)
		}
		_, err = finish(h, b, d.ID, "tok", delivery.FinishInput{Outcome: delivery.OutcomeAccepted, StartedAt: t0, At: t0}, "delivery.accepted")
		if err == nil {
			t.Fatal("foreign finish succeeded")
		}
		if err := h.Deliveries.Postpone(ctx, b, d.ID, t0.Add(time.Hour)); err == nil {
			t.Fatal("foreign postpone succeeded")
		}
		l, err := h.Deliveries.List(ctx, b, delivery.Filter{Limit: 10})
		must(t, err)
		if len(l) != 0 {
			t.Fatalf("foreign list: %v", l)
		}
		got, _ := h.Deliveries.Get(ctx, a, d.ID)
		if got.Status != delivery.Queued || got.Attempts != 0 {
			t.Fatalf("victim changed: %+v", got)
		}
		due, _ := h.Deliveries.Due(ctx, t0, 10)
		if len(due) != 1 || due[0].InstituteID != a.InstituteID() {
			t.Fatalf("due: %v", due)
		}
	})

	t.Run("list filters, orders newest first and returns one extra row for paging", func(t *testing.T) {
		h := newHarness(t)
		s, u := h.NewScope(), h.NewUser()
		var ids []string
		for i := 0; i < 3; i++ {
			c := cert(t, h, s, u, "s@example.com")
			d, err := req(h, s, u, c.ID, t0.Add(time.Duration(i)*time.Minute), false)
			must(t, err)
			ids = append(ids, d.ID)
		}
		cl, err := claim(h, s, ids[0], "tok", t0.Add(10*time.Minute))
		must(t, err)
		_, err = finish(h, s, ids[0], cl.Token, delivery.FinishInput{Outcome: delivery.OutcomeFailed, Code: "provider_rejected", StartedAt: t0, At: t0}, "delivery.failed")
		must(t, err)
		l, err := h.Deliveries.List(ctx, s, delivery.Filter{Limit: 10})
		must(t, err)
		if len(l) != 3 || l[0].ID != ids[2] || l[2].ID != ids[0] {
			t.Fatalf("order %v", l)
		}
		l, _ = h.Deliveries.List(ctx, s, delivery.Filter{Limit: 2})
		if len(l) != 3 {
			t.Fatalf("limit+1: %d", len(l))
		}
		l, _ = h.Deliveries.List(ctx, s, delivery.Filter{Status: delivery.Failed, Limit: 10})
		if len(l) != 1 || l[0].ID != ids[0] {
			t.Fatalf("status filter %v", l)
		}
		l, _ = h.Deliveries.List(ctx, s, delivery.Filter{CertificateID: l[0].CertificateID, Limit: 10})
		if len(l) != 1 {
			t.Fatalf("certificate filter %v", l)
		}
	})
}
