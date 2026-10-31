package certificates_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/certificate-os/api/internal/audit/audittest"
	"github.com/certificate-os/api/internal/certificates"
	"github.com/certificate-os/api/internal/certificates/certtest"
	"github.com/certificate-os/api/internal/pdf"
	"github.com/certificate-os/api/internal/platform/httpx"
	"github.com/certificate-os/api/internal/tenancy"
	"github.com/certificate-os/api/internal/tenancy/tenancytest"
)

type env struct {
	f     *tenancytest.Fixture
	w     *audittest.MemWriter
	store *certtest.MemStore
	pdf   *pdf.Fake
}

func newEnv(t *testing.T) *env {
	w := audittest.NewMemWriter()
	e := &env{w: w, store: certtest.NewMemStore(w), pdf: &pdf.Fake{}}
	e.f = tenancytest.NewFixture(t, func(f *tenancytest.Fixture) {
		svc := certificates.NewService(e.store, e.pdf, "https://certs.example.com/")
		jobs := certificates.NewJobs(context.Background(), svc, e.store, nil)
		jobs.Go = func(fn func()) { fn() } // inline: deterministic tests
		jobs.Audit = w
		h := &certificates.Handler{Svc: svc, Jobs: jobs, Tenancy: f.TenantH, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
		h.Register(f.Mux)
	})
	return e
}

func (e *env) raw(method, path, email, inst string, body any) *httptest.ResponseRecorder {
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

func (e *env) call(method, path, email, inst string, body any) (int, map[string]any) {
	rr := e.raw(method, path, email, inst, body)
	var out map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &out)
	return rr.Code, out
}

func str(m map[string]any, path ...string) string {
	var cur any = m
	for _, p := range path {
		mm, _ := cur.(map[string]any)
		cur = mm[p]
	}
	s, _ := cur.(string)
	return s
}

func (e *env) fixture(inst string, o certtest.FixtureOpts) certtest.Refs {
	return e.store.AddFixture(tenancy.TrustedScope(inst), o)
}

func body(r certtest.Refs) map[string]any {
	return map[string]any{"enrollment_id": r.EnrollmentID, "template_id": r.TemplateID}
}

func TestPermissionMatrix(t *testing.T) {
	e := newEnv(t)
	A := e.f.A.ID
	r := e.fixture(A, certtest.FixtureOpts{})
	// staff (read only) and the issuer grant: only the grant can issue.
	if code, _ := e.call("POST", "/api/v1/certificates", "a-staff@t.test", A, body(r)); code != 403 {
		t.Errorf("staff issue: %d", code)
	}
	code, c := e.call("POST", "/api/v1/certificates", "a-issuer@t.test", A, body(r))
	if code != 201 {
		t.Fatalf("issuer issue: %d %v", code, c)
	}
	id := str(c, "certificate", "id")
	for _, who := range []string{"a-staff@t.test", "a-issuer@t.test", "a-admin@t.test", "a-owner@t.test"} {
		for _, p := range []string{"/api/v1/certificates", "/api/v1/certificates/" + id, "/api/v1/certificates/" + id + "/pdf"} {
			if code, _ := e.call("GET", p, who, A, nil); code != 200 {
				t.Errorf("%s GET %s: %d", who, p, code)
			}
		}
	}
	// Revoking needs certificates.revoke (admin/owner), not the issue grant.
	for _, who := range []string{"a-staff@t.test", "a-issuer@t.test"} {
		if code, _ := e.call("POST", "/api/v1/certificates/"+id+"/revoke", who, A, map[string]any{"reason": "mistake made"}); code != 403 {
			t.Errorf("%s revoke: %d", who, code)
		}
	}
	if code, _ := e.call("GET", "/api/v1/certificates", "", A, nil); code != 401 {
		t.Errorf("anonymous: %d", code)
	}
	if code, _ := e.call("GET", "/api/v1/certificates", "b-staff@t.test", A, nil); code != 403 {
		t.Errorf("other institute's staff: %d", code)
	}
	if code, _ := e.call("GET", "/api/v1/certificates", "removed@t.test", A, nil); code != 403 {
		t.Errorf("removed member: %d", code)
	}
}

func TestSuspendedInstituteCanReadButNotIssueOrRevoke(t *testing.T) {
	e := newEnv(t)
	C := e.f.C.ID
	r := e.fixture(C, certtest.FixtureOpts{})
	if code, _ := e.call("POST", "/api/v1/certificates", "c-owner@t.test", C, body(r)); code != 403 {
		t.Errorf("issue while suspended: %d", code)
	}
	if code, _ := e.call("GET", "/api/v1/certificates", "c-owner@t.test", C, nil); code != 200 {
		t.Errorf("read while suspended: %d", code)
	}
	if code, _ := e.call("POST", "/api/v1/certificates/x/revoke", "c-owner@t.test", C, map[string]any{"reason": "reason here"}); code != 403 {
		t.Errorf("revoke while suspended: %d", code)
	}
	if code, _ := e.call("GET", "/api/v1/certificates", "d-owner@t.test", e.f.D.ID, nil); code == 200 {
		t.Errorf("closed institute must not be readable")
	}
}

func TestIssueDownloadAndRenderedPage(t *testing.T) {
	e := newEnv(t)
	A := e.f.A.ID
	r := e.fixture(A, certtest.FixtureOpts{StudentName: `Asha <b>Verma</b> & "Co"`, Grade: "A+"})
	code, c := e.call("POST", "/api/v1/certificates", "a-admin@t.test", A, body(r))
	if code != 201 {
		t.Fatalf("issue: %d %v", code, c)
	}
	cert := c["certificate"].(map[string]any)
	if cert["status"] != "issued" || !strings.HasPrefix(str(c, "certificate", "certificate_number"), "CERT-") || cert["pdf_sha256"] == "" {
		t.Fatalf("bad certificate: %v", cert)
	}
	if v := str(c, "certificate", "verify_url"); !strings.HasPrefix(v, "https://certs.example.com/verify/") || len(v) != len("https://certs.example.com/verify/")+43 {
		t.Fatalf("verify url = %q", v)
	}
	// The renderer got a page built from the snapshot: escaped text, long dates, number and an inline QR.
	page := e.pdf.Last
	for _, want := range []string{"Asha &lt;b&gt;Verma&lt;/b&gt; &amp; &#34;Co&#34;", "15 March 2026", str(c, "certificate", "certificate_number"), `<div class="qr real"><svg`} {
		if !strings.Contains(page, want) {
			t.Errorf("rendered page misses %q", want)
		}
	}
	for _, bad := range []string{"<script", "<b>Verma", "src=", "href=", "Grade:"} { // the preset hides the grade
		if strings.Contains(page, bad) {
			t.Errorf("rendered page contains %q", bad)
		}
	}
	// Download: headers and bytes.
	rr := e.raw("GET", "/api/v1/certificates/"+str(c, "certificate", "id")+"/pdf", "a-staff@t.test", A, nil)
	if rr.Code != 200 || rr.Header().Get("Content-Type") != "application/pdf" ||
		rr.Header().Get("Cache-Control") != "private, no-store" || rr.Header().Get("X-Content-Type-Options") != "nosniff" ||
		!strings.HasPrefix(rr.Header().Get("Content-Disposition"), `attachment; filename="CERT-`) ||
		rr.Header().Get("ETag") != `"`+str(c, "certificate", "pdf_sha256")+`"` {
		t.Fatalf("bad download: %d %v", rr.Code, rr.Header())
	}
	if !strings.HasPrefix(rr.Body.String(), "%PDF-") || !strings.Contains(rr.Body.String(), "CERT-") {
		t.Fatal("unexpected pdf body")
	}
}

func TestRenderFailureLeavesAFailedRecordAndNeverAnIssuedOne(t *testing.T) {
	e := newEnv(t)
	A := e.f.A.ID
	r := e.fixture(A, certtest.FixtureOpts{})
	e.pdf.Err = errors.New("chromium crashed with /secret/path")
	code, res := e.call("POST", "/api/v1/certificates", "a-admin@t.test", A, body(r))
	if code != 502 || str(res, "error", "code") != "render_failed" || strings.Contains(str(res, "error", "message"), "secret") {
		t.Fatalf("render failure: %d %v", code, res)
	}
	_, l := e.call("GET", "/api/v1/certificates?status=failed", "a-admin@t.test", A, nil)
	fs, _ := l["certificates"].([]any)
	if len(fs) != 1 {
		t.Fatalf("failed list: %v", l)
	}
	failed := fs[0].(map[string]any)
	if failed["failure"] != "pdf rendering failed" {
		t.Fatalf("failure reason = %v", failed["failure"])
	}
	if _, ok := e.call("GET", "/api/v1/certificates?status=issued", "a-admin@t.test", A, nil); ok == nil {
		t.Fatal("issued list")
	}
	_, li := e.call("GET", "/api/v1/certificates?status=issued", "a-admin@t.test", A, nil)
	if is, _ := li["certificates"].([]any); len(is) != 0 {
		t.Fatalf("a failed render must not appear issued: %v", li)
	}
	if code, _ := e.call("GET", "/api/v1/certificates/"+failed["id"].(string)+"/pdf", "a-admin@t.test", A, nil); code != 404 {
		t.Errorf("pdf of a failed certificate: %d", code)
	}
	// Retry works and gets the next number (the failed number is never reused).
	e.pdf.Err = nil
	code, res = e.call("POST", "/api/v1/certificates", "a-admin@t.test", A, body(r))
	if code != 201 || str(res, "certificate", "certificate_number") != failedNext(failed["certificate_number"].(string)) {
		t.Fatalf("retry: %d %v", code, res)
	}
}

func failedNext(n string) string { // CERT-2026-000001 -> CERT-2026-000002
	last := n[len(n)-1]
	return n[:len(n)-1] + string(last+1)
}

func TestNoRendererConfiguredIssuesNothing(t *testing.T) {
	w := audittest.NewMemWriter()
	store := certtest.NewMemStore(w)
	var A string
	f := tenancytest.NewFixture(t, func(f *tenancytest.Fixture) {
		h := &certificates.Handler{Svc: certificates.NewService(store, nil, "https://certs.example.com"), Tenancy: f.TenantH,
			Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
		h.Register(f.Mux)
	})
	A = f.A.ID
	e := &env{f: f, w: w, store: store}
	r := e.fixture(A, certtest.FixtureOpts{})
	code, res := e.call("POST", "/api/v1/certificates", "a-admin@t.test", A, body(r))
	if code != 503 || str(res, "error", "code") != "render_unavailable" {
		t.Fatalf("want 503, got %d %v", code, res)
	}
	_, l := e.call("GET", "/api/v1/certificates", "a-admin@t.test", A, nil)
	if cs, _ := l["certificates"].([]any); len(cs) != 0 {
		t.Fatalf("nothing may be written: %v", l)
	}
	if n := len(w.Records()); n != 0 {
		t.Fatalf("%d audit rows written", n)
	}
}

func TestIssueValidationAndConflicts(t *testing.T) {
	e := newEnv(t)
	A := e.f.A.ID
	if code, res := e.call("POST", "/api/v1/certificates", "a-admin@t.test", A, map[string]any{}); code != 422 ||
		str(res, "error", "details", "enrollment_id") == "" || str(res, "error", "details", "template_id") == "" {
		t.Fatalf("empty body: %d %v", code, res)
	}
	r := e.fixture(A, certtest.FixtureOpts{})
	if code, _ := e.call("POST", "/api/v1/certificates", "a-admin@t.test", A, map[string]any{"enrollment_id": r.EnrollmentID, "template_id": r.TemplateID, "template_version": -1}); code != 422 {
		t.Errorf("negative version: %d", code)
	}
	if code, _ := e.call("POST", "/api/v1/certificates", "a-admin@t.test", A, map[string]any{"enrollment_id": r.EnrollmentID, "template_id": r.TemplateID, "template_version": 5}); code != 404 {
		t.Errorf("unknown version: %d", code)
	}
	if code, _ := e.call("POST", "/api/v1/certificates", "a-admin@t.test", A, map[string]any{"enrollment_id": "nope", "template_id": r.TemplateID}); code != 404 {
		t.Errorf("unknown enrollment: %d", code)
	}
	if code, _ := e.call("POST", "/api/v1/certificates", "a-admin@t.test", A, map[string]any{"enrollment_id": r.EnrollmentID, "template_id": r.TemplateID, "institute_id": e.f.B.ID}); code != 400 {
		t.Errorf("unknown field must be rejected: %d", code)
	}
	for name, o := range map[string]certtest.FixtureOpts{"incomplete": {Incomplete: true}, "archived student": {StudentArchived: true}, "archived template": {TemplateArchived: true}} {
		code, res := e.call("POST", "/api/v1/certificates", "a-admin@t.test", A, body(e.fixture(A, o)))
		if code != 422 || len(res["error"].(map[string]any)["details"].(map[string]any)) != 1 {
			t.Errorf("%s: %d %v", name, code, res)
		}
	}
	if code, _ := e.call("POST", "/api/v1/certificates", "a-admin@t.test", A, body(r)); code != 201 {
		t.Fatalf("first issue: %d", code)
	}
	if code, _ := e.call("POST", "/api/v1/certificates", "a-admin@t.test", A, body(r)); code != 409 {
		t.Errorf("second issue: %d", code)
	}
	for _, q := range []string{"status=bogus", "limit=0", "limit=x", "offset=-1"} {
		if code, _ := e.call("GET", "/api/v1/certificates?"+q, "a-admin@t.test", A, nil); code != 422 {
			t.Errorf("%s: %d", q, code)
		}
	}
}

func TestRevokeFlowAndAudit(t *testing.T) {
	e := newEnv(t)
	A := e.f.A.ID
	r := e.fixture(A, certtest.FixtureOpts{StudentName: "Secret Student"})
	_, c := e.call("POST", "/api/v1/certificates", "a-admin@t.test", A, body(r))
	id := str(c, "certificate", "id")
	if code, _ := e.call("POST", "/api/v1/certificates/"+id+"/revoke", "a-admin@t.test", A, map[string]any{"reason": "no"}); code != 422 {
		t.Errorf("short reason: %d", code)
	}
	code, rv := e.call("POST", "/api/v1/certificates/"+id+"/revoke", "a-admin@t.test", A, map[string]any{"reason": "Wrong course printed"})
	if code != 200 || str(rv, "certificate", "status") != "revoked" || str(rv, "certificate", "revoke_reason") != "Wrong course printed" {
		t.Fatalf("revoke: %d %v", code, rv)
	}
	if code, _ := e.call("GET", "/api/v1/certificates/"+id+"/pdf", "a-staff@t.test", A, nil); code != 200 {
		t.Errorf("pdf of a revoked certificate: %d", code)
	}
	if code, _ := e.call("POST", "/api/v1/certificates/"+id+"/revoke", "a-admin@t.test", A, map[string]any{"reason": "Again please"}); code != 409 {
		t.Errorf("second revoke: %d", code)
	}
	if code, _ := e.call("POST", "/api/v1/certificates", "a-admin@t.test", A, body(r)); code != 201 {
		t.Errorf("re-issue after revoke: %d", code)
	}
	var actions []string
	for _, rec := range e.w.ForEntity("certificate", id) {
		actions = append(actions, rec.Action)
		b, _ := json.Marshal(rec.Metadata)
		for _, secret := range []string{"Secret Student", "Wrong course printed", "Verma"} {
			if strings.Contains(string(b), secret) {
				t.Errorf("audit metadata of %s contains %q: %s", rec.Action, secret, b)
			}
		}
	}
	if strings.Join(actions, ",") != "certificate.issue_started,certificate.issued,certificate.revoked" {
		t.Errorf("audit actions = %v", actions)
	}
}

func TestCrossTenantIsolation(t *testing.T) {
	e := newEnv(t)
	A, B := e.f.A.ID, e.f.B.ID
	ra := e.fixture(A, certtest.FixtureOpts{})
	_, c := e.call("POST", "/api/v1/certificates", "a-admin@t.test", A, body(ra))
	id := str(c, "certificate", "id")
	for _, tc := range []struct {
		m, p string
		body any
	}{
		{"GET", "/api/v1/certificates/" + id, nil},
		{"GET", "/api/v1/certificates/" + id + "/pdf", nil},
		{"POST", "/api/v1/certificates/" + id + "/revoke", map[string]any{"reason": "hijack attempt"}},
		{"POST", "/api/v1/certificates", body(ra)}, // A's enrollment and template ids, used from B
	} {
		if code, _ := e.call(tc.m, tc.p, "b-admin@t.test", B, tc.body); code != 404 {
			t.Errorf("%s %s as B: want 404, got %d", tc.m, tc.p, code)
		}
	}
	if code, _ := e.call("GET", "/api/v1/certificates", "a-admin@t.test", B, nil); code != 403 {
		t.Errorf("A admin with B header: %d", code)
	}
	_, l := e.call("GET", "/api/v1/certificates", "b-admin@t.test", B, nil)
	if cs, _ := l["certificates"].([]any); len(cs) != 0 {
		t.Errorf("B sees A's certificates: %v", l)
	}
	_, got := e.call("GET", "/api/v1/certificates/"+id, "a-admin@t.test", A, nil)
	if str(got, "certificate", "status") != "issued" {
		t.Errorf("A's certificate changed: %v", got)
	}
}

func TestReplaceFlow(t *testing.T) {
	e := newEnv(t)
	A := e.f.A.ID
	r := e.fixture(A, certtest.FixtureOpts{StudentName: "Asha Verma"})
	_, c := e.call("POST", "/api/v1/certificates", "a-admin@t.test", A, body(r))
	oldID := str(c, "certificate", "id")
	oldNumber := str(c, "certificate", "certificate_number")

	// Permissions: replacing needs revoke AND issue (admin/owner), not staff or the issue grant alone.
	rep := map[string]any{"reason": "name was misspelled"}
	for _, who := range []string{"a-staff@t.test", "a-issuer@t.test"} {
		if code, _ := e.call("POST", "/api/v1/certificates/"+oldID+"/replace", who, A, rep); code != 403 {
			t.Errorf("%s replace: %d", who, code)
		}
	}
	if code, _ := e.call("POST", "/api/v1/certificates/"+oldID+"/replace", "b-owner@t.test", e.f.B.ID, rep); code != 404 {
		t.Errorf("another institute's replace: %d", code)
	}
	if code, res := e.call("POST", "/api/v1/certificates/"+oldID+"/replace", "a-admin@t.test", A, map[string]any{"reason": "x"}); code != 422 {
		t.Errorf("short reason: %d %v", code, res)
	}
	// A failed render leaves the old certificate valid and creates no valid duplicate.
	e.pdf.Err = errors.New("chromium crashed")
	if code, _ := e.call("POST", "/api/v1/certificates/"+oldID+"/replace", "a-admin@t.test", A, rep); code != 502 {
		t.Errorf("render failure: %d", code)
	}
	e.pdf.Err = nil
	if _, g := e.call("GET", "/api/v1/certificates/"+oldID, "a-admin@t.test", A, nil); str(g, "certificate", "status") != "issued" || str(g, "certificate", "replaced_by_id") != "" {
		t.Fatalf("old certificate after a failed replacement: %v", g)
	}
	// Success.
	code, res := e.call("POST", "/api/v1/certificates/"+oldID+"/replace", "a-admin@t.test", A, rep)
	if code != 201 {
		t.Fatalf("replace: %d %v", code, res)
	}
	newID := str(res, "certificate", "id")
	if str(res, "certificate", "status") != "issued" || str(res, "certificate", "replaces_id") != oldID || str(res, "certificate", "certificate_number") == oldNumber {
		t.Fatalf("replacement %v", res)
	}
	_, g := e.call("GET", "/api/v1/certificates/"+oldID, "a-staff@t.test", A, nil)
	if str(g, "certificate", "status") != "revoked" || str(g, "certificate", "replaced_by_id") != newID || str(g, "certificate", "revoke_reason") != "name was misspelled" {
		t.Fatalf("old after replacement: %v", g)
	}
	_, l := e.call("GET", "/api/v1/certificates?status=issued", "a-admin@t.test", A, nil)
	if got := l["certificates"].([]any); len(got) != 1 || str(got[0].(map[string]any), "id") != newID {
		t.Fatalf("exactly one valid certificate expected: %v", l)
	}
	if rr := e.raw("GET", "/api/v1/certificates/"+oldID+"/pdf", "a-admin@t.test", A, nil); rr.Code != 200 {
		t.Errorf("the old PDF must stay downloadable: %d", rr.Code)
	}
	// Replacing something that is not valid any more.
	if code, _ := e.call("POST", "/api/v1/certificates/"+oldID+"/replace", "a-admin@t.test", A, rep); code != 409 {
		t.Errorf("replace a replaced certificate: %d", code)
	}
	// Audit: ids and counts only, no reason text.
	for _, rec := range e.w.ForEntity("certificate", oldID) {
		for k, v := range rec.Metadata {
			if s, ok := v.(string); ok && strings.Contains(s, "misspelled") {
				t.Errorf("reason text leaked into audit metadata %q", k)
			}
		}
	}
}
