package delivery_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/certificate-os/api/internal/audit/audittest"
	"github.com/certificate-os/api/internal/certificates"
	"github.com/certificate-os/api/internal/certificates/certtest"
	"github.com/certificate-os/api/internal/delivery"
	"github.com/certificate-os/api/internal/delivery/deliverytest"
	"github.com/certificate-os/api/internal/delivery/mail"
	"github.com/certificate-os/api/internal/pdf"
	"github.com/certificate-os/api/internal/platform/httpx"
	"github.com/certificate-os/api/internal/tenancy"
	"github.com/certificate-os/api/internal/tenancy/tenancytest"
)

type hEnv struct {
	f      *tenancytest.Fixture
	w      *audittest.MemWriter
	certs  *certtest.MemStore
	store  *deliverytest.MemStore
	mailer *mail.Fake
	svc    *delivery.Service
	jobs   *certificates.Jobs
	certSv *certificates.Service
}

func newHEnv(t *testing.T) *hEnv {
	w := audittest.NewMemWriter()
	cs := certtest.NewMemStore(w)
	ds := deliverytest.NewMemStore(cs, w)
	e := &hEnv{w: w, certs: cs, store: ds, mailer: mail.NewFake()}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	e.f = tenancytest.NewFixture(t, func(f *tenancytest.Fixture) {
		e.certSv = certificates.NewService(cs, &pdf.Fake{}, "https://certs.example.com/")
		e.jobs = certificates.NewJobs(context.Background(), e.certSv, cs, nil)
		e.jobs.Go = func(fn func()) { fn() }
		e.jobs.Audit = w
		e.svc = delivery.NewService(ds, cs, e.mailer, "no-reply@example.com", "https://certs.example.com", log)
		h := &delivery.Handler{Svc: e.svc, JobCertificates: delivery.JobCertificates(e.jobs), Tenancy: f.TenantH, Log: log}
		h.Register(f.Mux)
		ch := &certificates.Handler{Svc: e.certSv, Jobs: e.jobs, Tenancy: f.TenantH, Log: log}
		ch.Register(f.Mux)
	})
	return e
}

func (e *hEnv) raw(method, path, email, inst string, body any) *httptest.ResponseRecorder {
	e.f.T.Helper()
	var rd io.Reader = strings.NewReader("")
	if body != nil {
		b, _ := json.Marshal(body)
		rd = strings.NewReader(string(b))
	}
	req := httptest.NewRequest(method, path, rd)
	req.RemoteAddr = "203.0.113.9:1234"
	if method != http.MethodGet {
		req.Header.Set(httpx.CSRFHeader, "1")
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if inst != "" {
		req.Header.Set(tenancy.InstituteHeader, inst)
	}
	if email != "" {
		tok, _, err := e.f.Svc.Login(context.Background(), email, tenancytest.Password)
		if err != nil {
			e.f.T.Fatal(err)
		}
		req.AddCookie(&http.Cookie{Name: "cos_session", Value: tok})
	}
	rr := httptest.NewRecorder()
	e.f.Handler.ServeHTTP(rr, req)
	return rr
}

func (e *hEnv) call(method, path, email, inst string, body any) (int, map[string]any) {
	rr := e.raw(method, path, email, inst, body)
	var out map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &out)
	return rr.Code, out
}

func sv(m map[string]any, path ...string) string {
	var cur any = m
	for _, p := range path {
		mm, _ := cur.(map[string]any)
		cur = mm[p]
	}
	s, _ := cur.(string)
	return s
}

func (e *hEnv) cert(inst, name, email string) certificates.Certificate {
	e.f.T.Helper()
	sc := tenancy.TrustedScope(inst)
	r := e.certs.AddFixture(sc, certtest.FixtureOpts{StudentName: name})
	pid, _ := certificates.NewPublicID()
	c, err := e.certs.BeginIssue(context.Background(), sc, certificates.BeginInput{EnrollmentID: r.EnrollmentID, TemplateID: r.TemplateID, PublicID: pid, IssuedBy: "u", Now: e.svc.Now()}, certtest.EventForTest())
	if err != nil {
		e.f.T.Fatal(err)
	}
	if c, err = e.certs.Complete(context.Background(), sc, c.ID, []byte("%PDF-1.4 x"), certtest.EventForTest()); err != nil {
		e.f.T.Fatal(err)
	}
	if email != "" {
		e.store.SetEmail(sc, c.ID, email)
	}
	return c
}

func TestPermissionMatrix(t *testing.T) {
	e := newHEnv(t)
	A := e.f.A.ID
	c := e.cert(A, "Asha", "asha@example.com")
	path := "/api/v1/certificates/" + c.ID + "/deliveries"
	// Sending needs certificates.deliver: admin and owner have it; staff and the issue grant do not.
	for _, who := range []string{"a-staff@t.test", "a-issuer@t.test"} {
		if code, _ := e.call("POST", path, who, A, nil); code != 403 {
			t.Errorf("%s send: %d", who, code)
		}
	}
	if code, _ := e.call("POST", path, "", A, nil); code != 401 {
		t.Errorf("anonymous send: %d", code)
	}
	if code, _ := e.call("POST", path, "b-owner@t.test", A, nil); code != 403 {
		t.Errorf("other institute's owner: %d", code)
	}
	code, res := e.call("POST", path, "a-admin@t.test", A, nil)
	if code != http.StatusAccepted || sv(res, "delivery", "status") != "queued" {
		t.Fatalf("admin send: %d %v", code, res)
	}
	id := sv(res, "delivery", "id")
	if e.mailer.Calls() != 0 {
		t.Fatal("the request must not send inline")
	}
	for _, who := range []string{"a-staff@t.test", "a-issuer@t.test", "a-admin@t.test", "a-owner@t.test"} {
		for _, p := range []string{"/api/v1/deliveries", "/api/v1/deliveries/" + id, path, "/api/v1/email/status"} {
			if code, _ := e.call("GET", p, who, A, nil); code != 200 {
				t.Errorf("%s GET %s: %d", who, p, code)
			}
		}
	}
	for _, who := range []string{"a-staff@t.test", "a-issuer@t.test"} {
		if code, _ := e.call("POST", "/api/v1/deliveries/"+id+"/retry", who, A, nil); code != 403 {
			t.Errorf("%s retry: %d", who, code)
		}
		if code, _ := e.call("POST", "/api/v1/bulk-jobs/x/deliveries", who, A, nil); code != 403 {
			t.Errorf("%s bulk send: %d", who, code)
		}
	}
	if code, _ := e.call("GET", "/api/v1/deliveries", "b-staff@t.test", A, nil); code != 403 {
		t.Errorf("other institute's staff: %d", code)
	}
}

func TestSuspendedInstituteCanReadButNotSend(t *testing.T) {
	e := newHEnv(t)
	C := e.f.C.ID
	c := e.cert(C, "Asha", "asha@example.com")
	if code, _ := e.call("POST", "/api/v1/certificates/"+c.ID+"/deliveries", "c-owner@t.test", C, nil); code != 403 {
		t.Errorf("send while suspended: %d", code)
	}
	if code, _ := e.call("POST", "/api/v1/deliveries/x/retry", "c-owner@t.test", C, nil); code != 403 {
		t.Errorf("retry while suspended: %d", code)
	}
	if code, _ := e.call("POST", "/api/v1/bulk-jobs/x/deliveries", "c-owner@t.test", C, nil); code != 403 {
		t.Errorf("bulk send while suspended: %d", code)
	}
	if code, _ := e.call("GET", "/api/v1/deliveries", "c-owner@t.test", C, nil); code != 200 {
		t.Errorf("read while suspended: %d", code)
	}
}

func TestSendValidationAndIsolation(t *testing.T) {
	e := newHEnv(t)
	A, B := e.f.A.ID, e.f.B.ID
	c := e.cert(A, "Asha", "asha@example.com")
	nomail := e.cert(A, "Bela", "")
	if code, res := e.call("POST", "/api/v1/certificates/"+nomail.ID+"/deliveries", "a-admin@t.test", A, nil); code != 422 || sv(res, "error", "details", "email") == "" {
		t.Errorf("no email: %d %v", code, res)
	}
	if code, _ := e.call("POST", "/api/v1/certificates/nope/deliveries", "a-admin@t.test", A, nil); code != 404 {
		t.Errorf("unknown: %d", code)
	}
	// Institute B cannot send A's certificate, even naming its own institute header.
	if code, _ := e.call("POST", "/api/v1/certificates/"+c.ID+"/deliveries", "b-owner@t.test", B, nil); code != 404 {
		t.Errorf("foreign certificate: %d", code)
	}
	code, res := e.call("POST", "/api/v1/certificates/"+c.ID+"/deliveries", "a-owner@t.test", A, nil)
	if code != 202 {
		t.Fatalf("%d %v", code, res)
	}
	id := sv(res, "delivery", "id")
	if code, _ := e.call("POST", "/api/v1/certificates/"+c.ID+"/deliveries", "a-owner@t.test", A, nil); code != 409 {
		t.Errorf("double click: %d", code)
	}
	if code, _ := e.call("GET", "/api/v1/deliveries/"+id, "b-owner@t.test", B, nil); code != 404 {
		t.Errorf("foreign get: %d", code)
	}
	if code, _ := e.call("POST", "/api/v1/deliveries/"+id+"/retry", "b-owner@t.test", B, nil); code != 404 {
		t.Errorf("foreign retry: %d", code)
	}
	_, l := e.call("GET", "/api/v1/deliveries", "b-owner@t.test", B, nil)
	if l["deliveries"] == nil || len(l["deliveries"].([]any)) != 0 {
		t.Errorf("foreign list: %v", l)
	}
	if code, _ := e.call("GET", "/api/v1/deliveries?status=bogus", "a-owner@t.test", A, nil); code != 422 {
		t.Errorf("bad status: %d", code)
	}
	if code, _ := e.call("GET", "/api/v1/deliveries?limit=0", "a-owner@t.test", A, nil); code != 422 {
		t.Errorf("bad limit: %d", code)
	}
	// A delivery that is not failed cannot be retried.
	if code, _ := e.call("POST", "/api/v1/deliveries/"+id+"/retry", "a-owner@t.test", A, nil); code != 409 {
		t.Errorf("retry queued: %d", code)
	}
}

func TestRecipientIsMaskedEverywhere(t *testing.T) {
	e := newHEnv(t)
	A := e.f.A.ID
	c := e.cert(A, "Asha", "asha.verma@example.com")
	_, res := e.call("POST", "/api/v1/certificates/"+c.ID+"/deliveries", "a-owner@t.test", A, nil)
	id := sv(res, "delivery", "id")
	if _, err := e.svc.ProcessDue(context.Background(), 5); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"/api/v1/deliveries", "/api/v1/deliveries/" + id, "/api/v1/certificates/" + c.ID + "/deliveries"} {
		rr := e.raw("GET", p, "a-owner@t.test", A, nil)
		body := rr.Body.String()
		if rr.Code != 200 || strings.Contains(body, "asha.verma") || !strings.Contains(body, "a***@example.com") {
			t.Errorf("%s: %d %s", p, rr.Code, body)
		}
	}
	rr := e.raw("GET", "/api/v1/deliveries/"+id, "a-owner@t.test", A, nil)
	var got map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &got)
	d := got["delivery"].(map[string]any)
	if d["status"] != "accepted" || len(d["attempt_log"].([]any)) != 1 {
		t.Errorf("detail %v", d)
	}
	if delivery.MaskEmail("x") != "***" || delivery.MaskEmail("ab@c.de") != "a***@c.de" {
		t.Error("MaskEmail")
	}
}

func TestFailedDeliveryCanBeRetriedAndDisabledIsReported(t *testing.T) {
	e := newHEnv(t)
	A := e.f.A.ID
	c := e.cert(A, "Asha", "typo@exmaple.com")
	e.mailer.FailNext(&mail.Error{Code: mail.CodeRecipient})
	_, res := e.call("POST", "/api/v1/certificates/"+c.ID+"/deliveries", "a-owner@t.test", A, nil)
	id := sv(res, "delivery", "id")
	e.svc.ProcessDue(context.Background(), 5)
	_, got := e.call("GET", "/api/v1/deliveries/"+id, "a-owner@t.test", A, nil)
	if sv(got, "delivery", "status") != "failed" || sv(got, "delivery", "last_error") != mail.CodeRecipient {
		t.Fatalf("%v", got)
	}
	e.store.SetEmail(tenancy.TrustedScope(A), c.ID, "asha@example.com")
	code, res := e.call("POST", "/api/v1/deliveries/"+id+"/retry", "a-owner@t.test", A, nil)
	if code != 202 || sv(res, "delivery", "status") != "queued" {
		t.Fatalf("retry %d %v", code, res)
	}
	e.svc.ProcessDue(context.Background(), 5)
	_, got = e.call("GET", "/api/v1/deliveries/"+id, "a-owner@t.test", A, nil)
	if sv(got, "delivery", "status") != "accepted" {
		t.Fatalf("%v", got)
	}

	e.svc.Mailer = nil
	other := e.cert(A, "Bela", "bela@example.com")
	if code, res := e.call("POST", "/api/v1/certificates/"+other.ID+"/deliveries", "a-owner@t.test", A, nil); code != 503 || sv(res, "error", "code") != "email_unavailable" {
		t.Errorf("disabled: %d %v", code, res)
	}
	_, st := e.call("GET", "/api/v1/email/status", "a-owner@t.test", A, nil)
	if st["enabled"] != false {
		t.Errorf("status %v", st)
	}
}

func TestBulkJobDelivery(t *testing.T) {
	e := newHEnv(t)
	A := e.f.A.ID
	sc := tenancy.TrustedScope(A)
	var rs []certtest.Refs
	for i, n := range []string{"Asha", "Bela", "Chitra"} {
		o := certtest.FixtureOpts{StudentName: n}
		if i > 0 {
			o.BatchID, o.TemplateID = rs[0].BatchID, rs[0].TemplateID
		}
		rs = append(rs, e.certs.AddFixture(sc, o))
	}
	code, res := e.call("POST", "/api/v1/certificates/bulk", "a-admin@t.test", A, map[string]any{"batch_id": rs[0].BatchID, "template_id": rs[0].TemplateID})
	if code != 202 {
		t.Fatalf("%d %v", code, res)
	}
	job := sv(res, "job", "id")
	// Give two of the three students an address (the fixture knows students by enrollment: look the certificates up).
	_, list := e.call("GET", "/api/v1/bulk-jobs/"+job+"/items", "a-admin@t.test", A, nil)
	items := list["items"].([]any)
	if len(items) != 3 {
		t.Fatalf("%v", list)
	}
	for i, it := range items[:2] {
		e.store.SetEmail(sc, sv(it.(map[string]any), "certificate_id"), []string{"asha@example.com", "bela@example.com"}[i])
	}
	code, res = e.call("POST", "/api/v1/bulk-jobs/"+job+"/deliveries", "a-admin@t.test", A, nil)
	if code != 202 || res["queued"] != float64(2) || res["skipped_no_email"] != float64(1) {
		t.Fatalf("bulk %d %v", code, res)
	}
	if n, _ := e.svc.ProcessDue(context.Background(), 10); n != 2 || len(e.mailer.Sent()) != 2 {
		t.Fatalf("processed %d sent %d", n, len(e.mailer.Sent()))
	}
	// A second click mails nobody twice.
	code, res = e.call("POST", "/api/v1/bulk-jobs/"+job+"/deliveries", "a-admin@t.test", A, nil)
	if code != 202 || res["queued"] != float64(0) || res["skipped_already_delivered"] != float64(2) || res["skipped_no_email"] != float64(1) {
		t.Fatalf("second bulk %d %v", code, res)
	}
	if code, _ := e.call("POST", "/api/v1/bulk-jobs/nope/deliveries", "a-admin@t.test", A, nil); code != 404 {
		t.Errorf("unknown job: %d", code)
	}
	B := e.f.B.ID
	if code, _ := e.call("POST", "/api/v1/bulk-jobs/"+job+"/deliveries", "b-owner@t.test", B, nil); code != 404 {
		t.Errorf("foreign job: %d", code)
	}
}
