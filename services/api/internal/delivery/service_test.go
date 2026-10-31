package delivery_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/certificate-os/api/internal/audit"
	"github.com/certificate-os/api/internal/audit/audittest"
	"github.com/certificate-os/api/internal/certificates"
	"github.com/certificate-os/api/internal/certificates/certtest"
	"github.com/certificate-os/api/internal/delivery"
	"github.com/certificate-os/api/internal/delivery/deliverytest"
	"github.com/certificate-os/api/internal/delivery/mail"
	"github.com/certificate-os/api/internal/tenancy"
)

type env struct {
	svc    *delivery.Service
	store  *deliverytest.MemStore
	certs  *certtest.MemStore
	mailer *mail.Fake
	audit  *audittest.MemWriter
	scope  tenancy.Scope
	tenant tenancy.Tenant
	clock  *time.Time
}

func newEnv(t *testing.T) *env {
	t.Helper()
	w := audittest.NewMemWriter()
	cs := certtest.NewMemStore(w)
	ds := deliverytest.NewMemStore(cs, w)
	m := mail.NewFake()
	now := time.Date(2026, 10, 24, 10, 0, 0, 0, time.UTC)
	svc := delivery.NewService(ds, cs, m, "Certificates <no-reply@example.com>", "https://certs.example.com/", slog.New(slog.NewTextHandler(io.Discard, nil)))
	e := &env{svc: svc, store: ds, certs: cs, mailer: m, audit: w, scope: tenancy.TrustedScope("inst-1"), clock: &now}
	svc.Now = func() time.Time { return *e.clock }
	e.tenant = tenancy.Tenant{Institute: tenancy.Institute{ID: "inst-1"}, UserID: "user-1"}
	return e
}

func (e *env) advance(d time.Duration) { *e.clock = e.clock.Add(d) }

func (e *env) cert(t *testing.T, name, email string) certificates.Certificate {
	t.Helper()
	ctx := context.Background()
	r := e.certs.AddFixture(e.scope, certtest.FixtureOpts{StudentName: name})
	pid, _ := certificates.NewPublicID()
	c, err := e.certs.BeginIssue(ctx, e.scope, certificates.BeginInput{EnrollmentID: r.EnrollmentID, TemplateID: r.TemplateID, PublicID: pid, IssuedBy: "user-1", Now: *e.clock}, certtest.EventForTest())
	if err != nil {
		t.Fatal(err)
	}
	if c, err = e.certs.Complete(ctx, e.scope, c.ID, []byte("%PDF-1.4 "+name), certtest.EventForTest()); err != nil {
		t.Fatal(err)
	}
	if email != "" {
		e.store.SetEmail(e.scope, c.ID, email)
	}
	return c
}

func (e *env) actions(id string) string {
	var a []string
	for _, r := range e.audit.ForEntity("delivery", id) {
		a = append(a, r.Action)
	}
	return strings.Join(a, ",")
}

func transient(code string) error {
	return &mail.Error{Transient: true, Code: code, Err: errors.New("raw provider text for a@example.com")}
}
func permanent(code string) error {
	return &mail.Error{Code: code, Err: errors.New("raw provider text for a@example.com")}
}

func TestSendAndDeliver(t *testing.T) {
	e, ctx := newEnv(t), context.Background()
	c := e.cert(t, "Asha Verma", "asha@example.com")
	d, err := e.svc.Send(ctx, e.tenant, c.ID, "req-1")
	if err != nil || d.Status != delivery.Queued {
		t.Fatalf("%+v %v", d, err)
	}
	if e.mailer.Calls() != 0 {
		t.Fatal("requesting must not send inline")
	}
	n, err := e.svc.ProcessDue(ctx, 10)
	if err != nil || n != 1 {
		t.Fatalf("processed %d %v", n, err)
	}
	sent := e.mailer.Sent()
	if len(sent) != 1 {
		t.Fatalf("sent %d", len(sent))
	}
	m := sent[0]
	if m.To != "asha@example.com" || m.From != "Certificates <no-reply@example.com>" || len(m.Attachments) != 1 ||
		m.Attachments[0].Filename != c.Number+".pdf" || m.Attachments[0].ContentType != "application/pdf" || string(m.Attachments[0].Data) != "%PDF-1.4 Asha Verma" {
		t.Fatalf("message %+v", m)
	}
	if !strings.Contains(m.Text, "https://certs.example.com/verify/"+c.PublicID) || !strings.Contains(m.Text, "Asha Verma") || !strings.Contains(m.Text, c.Number) {
		t.Fatalf("text:\n%s", m.Text)
	}
	got, _ := e.svc.Get(ctx, e.tenant, d.ID)
	if got.Status != delivery.Accepted || got.Attempts != 1 || got.LastError != "" {
		t.Fatalf("%+v", got)
	}
	if e.actions(d.ID) != "delivery.requested,delivery.accepted" {
		t.Fatalf("audit %s", e.actions(d.ID))
	}
	// Nothing is sent twice.
	if n, _ := e.svc.ProcessDue(ctx, 10); n != 0 || e.mailer.Calls() != 1 {
		t.Fatalf("second scan: %d calls %d", n, e.mailer.Calls())
	}
	// Delivery never touches the certificate.
	after, _ := e.certs.Get(ctx, e.scope, c.ID)
	if after.Status != certificates.Issued || after.PDFSHA256 != c.PDFSHA256 {
		t.Fatalf("certificate changed: %+v", after)
	}
}

func TestTransientFailuresRetryWithBackoffThenSucceed(t *testing.T) {
	e, ctx := newEnv(t), context.Background()
	c := e.cert(t, "Asha", "asha@example.com")
	e.mailer.FailNext(transient(mail.CodeConnect), transient(mail.CodeTemporary), nil)
	d, _ := e.svc.Send(ctx, e.tenant, c.ID, "r")
	e.svc.ProcessDue(ctx, 10)
	got, _ := e.svc.Get(ctx, e.tenant, d.ID)
	if got.Status != delivery.Queued || got.LastError != mail.CodeConnect || got.Attempts != 1 ||
		got.NextAttemptAt == nil || !got.NextAttemptAt.Equal(e.clock.Add(delivery.BackoffAfter(1))) {
		t.Fatalf("after 1st failure %+v", got)
	}
	if n, _ := e.svc.ProcessDue(ctx, 10); n != 0 {
		t.Fatal("retried before the backoff elapsed")
	}
	e.advance(delivery.BackoffAfter(1))
	e.svc.ProcessDue(ctx, 10)
	got, _ = e.svc.Get(ctx, e.tenant, d.ID)
	if got.Attempts != 2 || got.LastError != mail.CodeTemporary || !got.NextAttemptAt.Equal(e.clock.Add(delivery.BackoffAfter(2))) {
		t.Fatalf("after 2nd failure %+v", got)
	}
	e.advance(delivery.BackoffAfter(2))
	e.svc.ProcessDue(ctx, 10)
	got, _ = e.svc.Get(ctx, e.tenant, d.ID)
	if got.Status != delivery.Accepted || got.Attempts != 3 || got.LastError != "" {
		t.Fatalf("final %+v", got)
	}
	at, _ := e.svc.Attempts(ctx, e.tenant, d.ID)
	if len(at) != 3 || at[0].Status != "failed" || at[1].Status != "failed" || at[2].Status != "accepted" || at[0].Provider != "fake" {
		t.Fatalf("attempts %+v", at)
	}
	if len(e.mailer.Sent()) != 1 {
		t.Fatalf("the message must be accepted exactly once, got %d", len(e.mailer.Sent()))
	}
}

func TestRetriesAreBoundedAndErrorTextIsNeverStored(t *testing.T) {
	e, ctx := newEnv(t), context.Background()
	c := e.cert(t, "Asha", "asha@example.com")
	for i := 0; i < delivery.MaxAttempts; i++ {
		e.mailer.FailNext(transient(mail.CodeTimeout))
	}
	d, _ := e.svc.Send(ctx, e.tenant, c.ID, "r")
	for i := 0; i < delivery.MaxAttempts; i++ {
		e.svc.ProcessDue(ctx, 10)
		e.advance(2 * time.Hour)
	}
	got, _ := e.svc.Get(ctx, e.tenant, d.ID)
	if got.Status != delivery.Failed || got.Attempts != delivery.MaxAttempts || got.LastError != mail.CodeTimeout || got.FinishedAt == nil {
		t.Fatalf("%+v", got)
	}
	if e.mailer.Calls() != delivery.MaxAttempts {
		t.Fatalf("calls %d", e.mailer.Calls())
	}
	e.advance(24 * time.Hour)
	if n, _ := e.svc.ProcessDue(ctx, 10); n != 0 || e.mailer.Calls() != delivery.MaxAttempts {
		t.Fatal("a failed delivery must not be retried automatically")
	}
	for _, r := range e.audit.Records() {
		for k, v := range r.Metadata {
			if s, ok := v.(string); ok && (strings.Contains(s, "example.com") || strings.Contains(s, "raw provider")) {
				t.Fatalf("audit %s.%s leaks %q", r.Action, k, s)
			}
		}
	}
	at, _ := e.svc.Attempts(ctx, e.tenant, d.ID)
	for _, a := range at {
		if strings.Contains(a.ErrorCode, "example") || strings.Contains(a.ErrorCode, " ") {
			t.Fatalf("attempt error %q", a.ErrorCode)
		}
	}
	// Manual retry starts a fresh round and works.
	r, err := e.svc.Retry(ctx, e.tenant, d.ID, "r2")
	if err != nil || r.Status != delivery.Queued {
		t.Fatalf("%+v %v", r, err)
	}
	e.svc.ProcessDue(ctx, 10)
	got, _ = e.svc.Get(ctx, e.tenant, d.ID)
	if got.Status != delivery.Accepted || got.Attempts != delivery.MaxAttempts+1 {
		t.Fatalf("after manual retry %+v", got)
	}
}

func TestPermanentFailureStopsImmediately(t *testing.T) {
	e, ctx := newEnv(t), context.Background()
	c := e.cert(t, "Asha", "asha@example.com")
	e.mailer.FailNext(permanent(mail.CodeRecipient))
	d, _ := e.svc.Send(ctx, e.tenant, c.ID, "r")
	e.svc.ProcessDue(ctx, 10)
	got, _ := e.svc.Get(ctx, e.tenant, d.ID)
	if got.Status != delivery.Failed || got.LastError != mail.CodeRecipient || got.Attempts != 1 {
		t.Fatalf("%+v", got)
	}
	if e.actions(d.ID) != "delivery.requested,delivery.failed" {
		t.Fatalf("audit %s", e.actions(d.ID))
	}
}

func TestRevokedBeforeSendingIsNeverMailed(t *testing.T) {
	e, ctx := newEnv(t), context.Background()
	c := e.cert(t, "Asha", "asha@example.com")
	d, _ := e.svc.Send(ctx, e.tenant, c.ID, "r")
	if _, err := e.certs.Revoke(ctx, e.scope, c.ID, "mistake", "user-1", certtest.EventForTest()); err != nil {
		t.Fatal(err)
	}
	e.svc.ProcessDue(ctx, 10)
	got, _ := e.svc.Get(ctx, e.tenant, d.ID)
	if got.Status != delivery.Failed || got.LastError != delivery.CodeNotIssued || e.mailer.Calls() != 0 {
		t.Fatalf("%+v calls %d", got, e.mailer.Calls())
	}
}

func TestSuspendedInstituteIsNotMailedAndResumesLater(t *testing.T) {
	e, ctx := newEnv(t), context.Background()
	c := e.cert(t, "Asha", "asha@example.com")
	d, _ := e.svc.Send(ctx, e.tenant, c.ID, "r")
	e.store.SetActive("inst-1", false)
	e.svc.ProcessDue(ctx, 10)
	if e.mailer.Calls() != 0 {
		t.Fatal("sent for a suspended institute")
	}
	got, _ := e.svc.Get(ctx, e.tenant, d.ID)
	if got.Status != delivery.Queued || got.Attempts != 0 {
		t.Fatalf("%+v", got)
	}
	e.store.SetActive("inst-1", true)
	if n, _ := e.svc.ProcessDue(ctx, 10); n != 0 {
		t.Fatal("rechecked before the postponement ended")
	}
	e.advance(delivery.SuspendedRecheck)
	e.svc.ProcessDue(ctx, 10)
	got, _ = e.svc.Get(ctx, e.tenant, d.ID)
	if got.Status != delivery.Accepted {
		t.Fatalf("%+v", got)
	}
}

func TestShutdownDuringSendDoesNotBurnAnAttempt(t *testing.T) {
	e := newEnv(t)
	c := e.cert(t, "Asha", "asha@example.com")
	d, _ := e.svc.Send(context.Background(), e.tenant, c.ID, "r")
	ctx, cancel := context.WithCancel(context.Background())
	e.svc.Mailer = cancelMailer{cancel}
	e.svc.ProcessDue(ctx, 10)
	got, _ := e.svc.Get(context.Background(), e.tenant, d.ID)
	// Left as "sending": the lease expires and another worker retries it; no outcome and no backoff were recorded.
	if got.Status != delivery.Sending || got.LastError != "" {
		t.Fatalf("%+v", got)
	}
	if at, _ := e.svc.Attempts(context.Background(), e.tenant, d.ID); len(at) != 0 {
		t.Fatalf("attempt recorded during shutdown: %+v", at)
	}
}

func TestDisabledAndBulk(t *testing.T) {
	e, ctx := newEnv(t), context.Background()
	a := e.cert(t, "Asha", "asha@example.com")
	b := e.cert(t, "Bela", "")
	c := e.cert(t, "Chitra", "chitra@example.com")
	r := e.cert(t, "Dev", "dev@example.com")
	if _, err := e.certs.Revoke(ctx, e.scope, r.ID, "x", "user-1", certtest.EventForTest()); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Send(ctx, e.tenant, a.ID, "r"); err != nil {
		t.Fatal(err)
	}
	e.svc.ProcessDue(ctx, 10) // a is now accepted
	if _, err := e.svc.Send(ctx, e.tenant, c.ID, "r"); err != nil {
		t.Fatal(err)
	} // c is in progress
	res, err := e.svc.SendMany(ctx, e.tenant, []string{a.ID, b.ID, c.ID, r.ID, "unknown"}, "r")
	if err != nil || res != (delivery.ManyResult{AlreadyDelivered: 1, NoEmail: 1, InProgress: 1, NotDeliverable: 2}) {
		t.Fatalf("%+v %v", res, err)
	}
	d := e.cert(t, "Esha", "esha@example.com")
	res, _ = e.svc.SendMany(ctx, e.tenant, []string{d.ID}, "r")
	if res.Queued != 1 {
		t.Fatalf("%+v", res)
	}

	off := newEnv(t)
	off.svc.Mailer = nil
	x := off.cert(t, "Asha", "asha@example.com")
	if _, err := off.svc.Send(ctx, off.tenant, x.ID, "r"); !errors.Is(err, delivery.ErrDisabled) {
		t.Fatal(err)
	}
	if _, err := off.svc.SendMany(ctx, off.tenant, []string{x.ID}, "r"); !errors.Is(err, delivery.ErrDisabled) {
		t.Fatal(err)
	}
	if n, err := off.svc.ProcessDue(ctx, 10); n != 0 || err != nil {
		t.Fatal("worker must idle when disabled")
	}
	_ = audit.Event{}
}

func TestMessageSanitisesNames(t *testing.T) {
	e, ctx := newEnv(t), context.Background()
	c := e.cert(t, "Asha\r\nBcc: evil@example.com", "asha@example.com")
	e.svc.Send(ctx, e.tenant, c.ID, "r")
	e.svc.ProcessDue(ctx, 10)
	m := e.mailer.Sent()[0]
	if strings.Contains(m.Text, "\r") || strings.Contains(m.Subject, "\n") {
		t.Fatalf("%q", m.Text)
	}
	raw, err := mail.BuildMIME(m, *e.clock)
	if err != nil || strings.Contains(string(raw), "\r\nBcc:") {
		t.Fatalf("header injection: %v", err)
	}
}

// cancelMailer simulates a shutdown that interrupts the send.
type cancelMailer struct{ cancel func() }

func (c cancelMailer) Name() string { return "fake" }
func (c cancelMailer) Send(ctx context.Context, _ mail.Message) error {
	c.cancel()
	return ctx.Err()
}
