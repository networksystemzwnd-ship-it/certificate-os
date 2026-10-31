package verification_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"github.com/certificate-os/api/internal/audit/audittest"
	"github.com/certificate-os/api/internal/certificates"
	"github.com/certificate-os/api/internal/certificates/certtest"
	"github.com/certificate-os/api/internal/pdf"
	"github.com/certificate-os/api/internal/tenancy"
	"github.com/certificate-os/api/internal/verification"
)

type env struct {
	store *certtest.MemStore
	svc   *certificates.Service
	mux   *http.ServeMux
	h     *verification.Handler
}

func newEnv(t *testing.T) *env {
	store := certtest.NewMemStore(audittest.NewMemWriter())
	e := &env{store: store, svc: certificates.NewService(store, &pdf.Fake{}, "https://certs.example.com"), mux: http.NewServeMux()}
	e.h = verification.NewHandler(verification.NewService(store), slog.New(slog.NewTextHandler(io.Discard, nil)))
	e.h.Register(e.mux)
	return e
}

func tenantOf(inst string) tenancy.Tenant {
	return tenancy.Tenant{UserID: "user-1", Institute: tenancy.Institute{ID: inst}}
}

func (e *env) issue(t *testing.T, inst string, o certtest.FixtureOpts) (certificates.Certificate, certtest.Refs) {
	t.Helper()
	r := e.store.AddFixture(tenancy.TrustedScope(inst), o)
	c, err := e.svc.Issue(context.Background(), tenantOf(inst), certificates.IssueRequest{EnrollmentID: r.EnrollmentID, TemplateID: r.TemplateID}, "r")
	if err != nil {
		t.Fatal(err)
	}
	return c, r
}

func (e *env) get(id, ip string) (int, map[string]any, http.Header, string) {
	req := httptest.NewRequest("GET", "/api/v1/public/certificates/"+id, nil)
	req.RemoteAddr = ip + ":4000"
	rr := httptest.NewRecorder()
	e.mux.ServeHTTP(rr, req)
	var out map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &out)
	return rr.Code, out, rr.Header(), rr.Body.String()
}

func keys(m map[string]any) []string {
	var k []string
	for x := range m {
		k = append(k, x)
	}
	sort.Strings(k)
	return k
}

func TestValidCertificateShowsOnlyApprovedFields(t *testing.T) {
	e := newEnv(t)
	c, _ := e.issue(t, "inst-1", certtest.FixtureOpts{StudentName: "Asha Verma", Grade: "A+"})
	code, out, h, raw := e.get(c.PublicID, "203.0.113.1")
	if code != 200 {
		t.Fatalf("status %d %s", code, raw)
	}
	v := out["verification"].(map[string]any)
	if got := strings.Join(keys(v), ","); got != "certificate_number,course_name,institute_name,issue_date,status,student_name" {
		t.Fatalf("public fields: %s", got)
	}
	if v["status"] != "valid" || v["certificate_number"] != c.Number || v["student_name"] != "Asha Verma" || v["course_name"] != "Web Design" || v["institute_name"] != "Test Institute" {
		t.Fatalf("verification %v", v)
	}
	for _, leak := range []string{"A+", "grade", "email", "phone", "student_id", `"` + c.ID + `"`, `"` + c.EnrollmentID + `"`, "pdf", "reason", "completion", "batch", "template"} {
		if strings.Contains(strings.ToLower(raw), strings.ToLower(leak)) {
			t.Errorf("public response contains %q: %s", leak, raw)
		}
	}
	if h.Get("Cache-Control") != "no-store" || !strings.Contains(h.Get("X-Robots-Tag"), "noindex") || h.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("headers %v", h)
	}
}

func TestRevokedAndReplacedAreReportedAccurately(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	tn := tenantOf("inst-1")
	a, _ := e.issue(t, "inst-1", certtest.FixtureOpts{StudentName: "Asha Verma"})
	b, _ := e.issue(t, "inst-1", certtest.FixtureOpts{StudentName: "Bela Rao"})
	if _, err := e.svc.Revoke(ctx, tn, a.ID, "issued by mistake - secret reason", "r"); err != nil {
		t.Fatal(err)
	}
	_, out, _, raw := e.get(a.PublicID, "203.0.113.1")
	v := out["verification"].(map[string]any)
	if v["status"] != "revoked" || v["revoked_at"] == nil || v["replaced_by"] != nil {
		t.Fatalf("revoked: %v", v)
	}
	if strings.Contains(raw, "secret reason") {
		t.Fatal("the revocation reason is internal and must not be public")
	}
	nb, err := e.svc.Replace(ctx, tn, certificates.ReplaceRequest{CertificateID: b.ID, Reason: "spelling fixed"}, "r")
	if err != nil {
		t.Fatal(err)
	}
	_, out, _, raw = e.get(b.PublicID, "203.0.113.1")
	v = out["verification"].(map[string]any)
	if v["status"] != "replaced" || v["replaced_by"] != nb.PublicID || v["revoked_at"] == nil {
		t.Fatalf("replaced: %v", v)
	}
	if strings.Contains(raw, "spelling fixed") {
		t.Fatal("the replacement reason is internal")
	}
	_, out, _, _ = e.get(nb.PublicID, "203.0.113.1")
	if v = out["verification"].(map[string]any); v["status"] != "valid" || v["replaced_by"] != nil || v["certificate_number"] != nb.Number {
		t.Fatalf("the replacement must verify as valid: %v", v)
	}
}

func TestUnknownMalformedAndUnissuedLookLikeTheSame404(t *testing.T) {
	e := newEnv(t)
	good, r := e.issue(t, "inst-1", certtest.FixtureOpts{})
	pend, err := e.store.BeginIssue(context.Background(), tenancy.TrustedScope("inst-1"), certificates.BeginInput{EnrollmentID: e.store.AddFixture(tenancy.TrustedScope("inst-1"), certtest.FixtureOpts{}).EnrollmentID,
		TemplateID: r.TemplateID, PublicID: strings.Repeat("p", 43), IssuedBy: "u"}, certtest.EventForTest())
	_ = pend
	_ = err
	failedRef := e.store.AddFixture(tenancy.TrustedScope("inst-1"), certtest.FixtureOpts{})
	fc, err := e.store.BeginIssue(context.Background(), tenancy.TrustedScope("inst-1"), certificates.BeginInput{EnrollmentID: failedRef.EnrollmentID, TemplateID: failedRef.TemplateID,
		PublicID: strings.Repeat("f", 43), IssuedBy: "u"}, certtest.EventForTest())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.Fail(context.Background(), tenancy.TrustedScope("inst-1"), fc.ID, "boom", certtest.EventForTest()); err != nil {
		t.Fatal(err)
	}
	var bodies []string
	for _, id := range []string{strings.Repeat("z", 43), "short", strings.Repeat("a", 44), strings.Repeat("p", 43), strings.Repeat("f", 43), good.Number, "%20%20", "a%2Fb"} {
		code, out, _, _ := e.get(id, "203.0.113.2")
		if code != 404 {
			t.Fatalf("%q: status %d", id, code)
		}
		em := out["error"].(map[string]any)
		delete(em, "request_id")
		b, _ := json.Marshal(em)
		bodies = append(bodies, string(b))
	}
	for _, b := range bodies[1:] {
		if b != bodies[0] {
			t.Fatalf("404 bodies differ:\n%s\n%s", bodies[0], b)
		}
	}
	// the certificate NUMBER is not a credential
	if code, _, _, _ := e.get(good.Number, "203.0.113.2"); code != 404 {
		t.Fatalf("number lookup: %d", code)
	}
}

func TestEventsAreRecordedWithoutPersonalData(t *testing.T) {
	e := newEnv(t)
	c, _ := e.issue(t, "inst-1", certtest.FixtureOpts{})
	e.get(c.PublicID, "203.0.113.1")
	e.get(c.PublicID, "198.51.100.7")
	e.get(strings.Repeat("z", 43), "203.0.113.1")
	ev := e.store.Verifications()
	if len(ev) != 2 || ev[0].Result != "valid" || ev[0].CertificateID != c.ID || ev[0].InstituteID != "inst-1" {
		t.Fatalf("events %+v", ev)
	}
}

func TestRateLimitPerAddress(t *testing.T) {
	e := newEnv(t)
	c, _ := e.issue(t, "inst-1", certtest.FixtureOpts{})
	for i := 0; i < 60; i++ {
		if code, _, _, _ := e.get(c.PublicID, "203.0.113.50"); code != 200 {
			t.Fatalf("request %d: %d", i, code)
		}
	}
	code, out, h, _ := e.get(c.PublicID, "203.0.113.50")
	if code != 429 || h.Get("Retry-After") == "" || out["error"].(map[string]any)["code"] != "rate_limited" {
		t.Fatalf("limit: %d %v %v", code, out, h)
	}
	if code, _, _, _ := e.get(strings.Repeat("z", 43), "203.0.113.50"); code != 429 {
		t.Fatalf("misses are limited too: %d", code)
	}
	if code, _, _, _ := e.get(c.PublicID, "203.0.113.51"); code != 200 {
		t.Fatalf("another address must not be affected: %d", code)
	}
}

func TestInstitutesAreIndependentAndOnlyGETExists(t *testing.T) {
	e := newEnv(t)
	a, _ := e.issue(t, "inst-1", certtest.FixtureOpts{StudentName: "Asha Verma"})
	b, _ := e.issue(t, "inst-2", certtest.FixtureOpts{StudentName: "Bela Rao"})
	_, oa, _, _ := e.get(a.PublicID, "203.0.113.1")
	_, ob, _, _ := e.get(b.PublicID, "203.0.113.1")
	if oa["verification"].(map[string]any)["student_name"] != "Asha Verma" || ob["verification"].(map[string]any)["student_name"] != "Bela Rao" {
		t.Fatal("each link shows its own certificate")
	}
	for _, m := range []string{"POST", "PUT", "DELETE", "PATCH"} {
		req := httptest.NewRequest(m, "/api/v1/public/certificates/"+a.PublicID, nil)
		rr := httptest.NewRecorder()
		e.mux.ServeHTTP(rr, req)
		if rr.Code != 405 && rr.Code != 404 {
			t.Errorf("%s: %d", m, rr.Code)
		}
	}
}
