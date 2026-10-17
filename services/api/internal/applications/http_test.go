package applications_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/certificate-os/api/internal/applications"
	"github.com/certificate-os/api/internal/applications/applicationstest"
	"github.com/certificate-os/api/internal/audit/audittest"
	"github.com/certificate-os/api/internal/auth"
	"github.com/certificate-os/api/internal/auth/authhttp"
	"github.com/certificate-os/api/internal/auth/authtest"
	"github.com/certificate-os/api/internal/platform/httpx"
	"github.com/certificate-os/api/internal/tenancy/tenancytest"
)

const pw = "correct horse battery"

type env struct {
	srv     http.Handler
	users   *authtest.MemStore
	audit   *audittest.MemWriter
	tenants *tenancytest.MemStore
}

func setup(t *testing.T) *env {
	t.Helper()
	users := authtest.NewMemStore()
	svc, err := auth.NewService(users, auth.Hasher{Iterations: 1000}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	ah := authhttp.NewHandler(svc, authhttp.CookieConfig{TTL: time.Hour}, log)
	tenants, w := tenancytest.NewMemStore(), audittest.NewMemWriter()
	h := &applications.Handler{Svc: applications.NewService(applicationstest.NewMemStore(tenants, w), w), Log: log, Authn: ah.Authenticate}
	mux := http.NewServeMux()
	ah.Register(mux)
	h.Register(mux)
	return &env{srv: httpx.Chain(log, httpx.CSRF("http://web", mux)), users: users, audit: w, tenants: tenants}
}

func (e *env) do(method, path, body string, c *http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.RemoteAddr = "203.0.113.9:1234"
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if method != "GET" {
		req.Header.Set(httpx.CSRFHeader, "1")
	}
	if c != nil {
		req.AddCookie(c)
	}
	rr := httptest.NewRecorder()
	e.srv.ServeHTTP(rr, req)
	return rr
}

func (e *env) user(t *testing.T, email string, role auth.PlatformRole) *http.Cookie {
	t.Helper()
	if rr := e.do("POST", "/api/v1/auth/register", `{"email":"`+email+`","password":"`+pw+`"}`, nil); rr.Code != 201 {
		t.Fatalf("register %d %s", rr.Code, rr.Body)
	}
	if role != auth.PlatformNone {
		u, _ := e.users.UserByEmail(context.Background(), email)
		_ = e.users.SetPlatformRole(context.Background(), u.ID, role)
	}
	rr := e.do("POST", "/api/v1/auth/login", `{"email":"`+email+`","password":"`+pw+`"}`, nil)
	for _, c := range rr.Result().Cookies() {
		if c.Name == "cos_session" {
			return c
		}
	}
	t.Fatal("no cookie")
	return nil
}

func js(rr *httptest.ResponseRecorder) map[string]any {
	var m map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &m)
	return m
}

func appID(rr *httptest.ResponseRecorder) string {
	return js(rr)["application"].(map[string]any)["id"].(string)
}

const full = `{"name":"Alpha Academy","slug":"alpha","category":"academy","address_line1":"12 Main Road","city":"Pune","country":"in","official_email":"Office@Alpha.example","phone":"+91 20 5550 1234","rep_name":"Asha Rao","rep_role":"Director","attested":true}`

func TestApplicantToApprovalFlow(t *testing.T) {
	e := setup(t)
	app := e.user(t, "asha@example.com", auth.PlatformNone)
	ops := e.user(t, "ops@example.com", auth.PlatformOps)

	rr := e.do("POST", "/api/v1/institute-applications", full, app)
	if rr.Code != 201 {
		t.Fatalf("create %d %s", rr.Code, rr.Body)
	}
	id := appID(rr)
	if d := js(rr)["application"].(map[string]any)["details"].(map[string]any); d["country"] != "IN" || d["official_email"] != "office@alpha.example" {
		t.Fatalf("not normalized: %v", d)
	}
	// Drafts are invisible to operations.
	if rr := e.do("GET", "/api/v1/ops/institute-applications/"+id, "", ops); rr.Code != 404 {
		t.Fatalf("ops saw draft: %d", rr.Code)
	}
	if rr := e.do("POST", "/api/v1/institute-applications/"+id+"/submit", "", app); rr.Code != 200 {
		t.Fatalf("submit %d %s", rr.Code, rr.Body)
	}
	// Submitted applications can no longer be edited by the applicant.
	if rr := e.do("PATCH", "/api/v1/institute-applications/"+id, `{"name":"Changed"}`, app); rr.Code != 409 {
		t.Fatalf("edit after submit: %d", rr.Code)
	}
	// Cannot approve before review starts.
	if rr := e.do("POST", "/api/v1/ops/institute-applications/"+id+"/approve", "", ops); rr.Code != 409 {
		t.Fatalf("approve before review: %d", rr.Code)
	}
	if rr := e.do("POST", "/api/v1/ops/institute-applications/"+id+"/start-review", "", ops); rr.Code != 200 {
		t.Fatalf("start review %d %s", rr.Code, rr.Body)
	}
	rr = e.do("POST", "/api/v1/ops/institute-applications/"+id+"/approve", `{"note":"documents verified offline"}`, ops)
	if rr.Code != 200 || js(rr)["institute"].(map[string]any)["slug"] != "alpha" {
		t.Fatalf("approve %d %s", rr.Code, rr.Body)
	}
	if rr := e.do("POST", "/api/v1/ops/institute-applications/"+id+"/approve", "", ops); rr.Code != 409 {
		t.Fatalf("double approve: %d", rr.Code)
	}
	rr = e.do("GET", "/api/v1/ops/institute-applications/"+id+"/history", "", ops)
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), "application.approved") || !strings.Contains(rr.Body.String(), "application.submitted") {
		t.Fatalf("history %d %s", rr.Code, rr.Body)
	}
}

func TestValidationAndOpenLimit(t *testing.T) {
	e := setup(t)
	c := e.user(t, "a@example.com", auth.PlatformNone)
	rr := e.do("POST", "/api/v1/institute-applications", `{"slug":"Bad Slug!","country":"India"}`, c)
	if rr.Code != 422 {
		t.Fatalf("want 422, got %d %s", rr.Code, rr.Body)
	}
	details := js(rr)["error"].(map[string]any)["details"].(map[string]any)
	if details["slug"] == nil || details["country"] == nil {
		t.Fatalf("missing field errors: %v", details)
	}
	if rr := e.do("POST", "/api/v1/institute-applications", `{"role":"owner"}`, c); rr.Code != 400 {
		t.Fatalf("unknown fields must be rejected: %d", rr.Code)
	}
	rr = e.do("POST", "/api/v1/institute-applications", `{"name":"Partial"}`, c)
	if rr.Code != 201 {
		t.Fatalf("draft create %d", rr.Code)
	}
	id := appID(rr)
	if rr := e.do("POST", "/api/v1/institute-applications", `{"name":"Second"}`, c); rr.Code != 409 {
		t.Fatalf("second open application: %d", rr.Code)
	}
	rr = e.do("POST", "/api/v1/institute-applications/"+id+"/submit", "", c)
	if rr.Code != 422 || js(rr)["error"].(map[string]any)["details"].(map[string]any)["attested"] == nil {
		t.Fatalf("incomplete submit must list problems: %d %s", rr.Code, rr.Body)
	}
}

func TestCrossUserAccessIs404(t *testing.T) {
	e := setup(t)
	a := e.user(t, "a@example.com", auth.PlatformNone)
	b := e.user(t, "b@example.com", auth.PlatformNone)
	id := appID(e.do("POST", "/api/v1/institute-applications", full, a))
	for _, req := range [][2]string{{"GET", ""}, {"PATCH", `{"name":"Hijack"}`}, {"POST", ""}} {
		path := "/api/v1/institute-applications/" + id
		if req[0] == "POST" {
			path += "/submit"
		}
		if rr := e.do(req[0], path, req[1], b); rr.Code != 404 {
			t.Fatalf("%s by other user: %d", req[0], rr.Code)
		}
	}
	if rr := e.do("GET", "/api/v1/institute-applications/not-a-uuid", "", a); rr.Code != 404 {
		t.Fatalf("malformed id: %d", rr.Code)
	}
	if rr := e.do("GET", "/api/v1/institute-applications", "", b); rr.Code != 200 || strings.Contains(rr.Body.String(), id) {
		t.Fatalf("list leaks: %d %s", rr.Code, rr.Body)
	}
}

func TestPermissionsAndSelfReview(t *testing.T) {
	e := setup(t)
	user := e.user(t, "u@example.com", auth.PlatformNone)
	ops := e.user(t, "ops@example.com", auth.PlatformOps)
	for _, p := range []string{"", "/x/history"} {
		if rr := e.do("GET", "/api/v1/ops/institute-applications"+p, "", nil); rr.Code != 401 {
			t.Fatalf("anonymous: %d", rr.Code)
		}
		if rr := e.do("GET", "/api/v1/ops/institute-applications"+p, "", user); rr.Code != 403 {
			t.Fatalf("plain user on ops route: %d", rr.Code)
		}
	}
	if rr := e.do("POST", "/api/v1/institute-applications", full, nil); rr.Code != 401 {
		t.Fatalf("anonymous create: %d", rr.Code)
	}
	// An ops user who applies cannot review their own application.
	id := appID(e.do("POST", "/api/v1/institute-applications", full, ops))
	e.do("POST", "/api/v1/institute-applications/"+id+"/submit", "", ops)
	if rr := e.do("POST", "/api/v1/ops/institute-applications/"+id+"/start-review", "", ops); rr.Code != 403 {
		t.Fatalf("self review: %d %s", rr.Code, rr.Body)
	}
}

func TestRejectAndRequestChangesNeedReasonAndLoop(t *testing.T) {
	e := setup(t)
	app := e.user(t, "a@example.com", auth.PlatformNone)
	ops := e.user(t, "ops@example.com", auth.PlatformOps)
	id := appID(e.do("POST", "/api/v1/institute-applications", full, app))
	e.do("POST", "/api/v1/institute-applications/"+id+"/submit", "", app)
	e.do("POST", "/api/v1/ops/institute-applications/"+id+"/start-review", "", ops)
	if rr := e.do("POST", "/api/v1/ops/institute-applications/"+id+"/request-changes", `{"reason":"short"}`, ops); rr.Code != 422 {
		t.Fatalf("short reason: %d", rr.Code)
	}
	if rr := e.do("POST", "/api/v1/ops/institute-applications/"+id+"/request-changes", `{"reason":"Please correct the phone number."}`, ops); rr.Code != 200 {
		t.Fatalf("request changes: %d %s", rr.Code, rr.Body)
	}
	rr := e.do("GET", "/api/v1/institute-applications/"+id, "", app)
	a := js(rr)["application"].(map[string]any)
	if a["status"] != "changes_requested" || a["decision_reason"] != "Please correct the phone number." || a["editable"] != true {
		t.Fatalf("applicant view: %v", a)
	}
	if rr := e.do("PATCH", "/api/v1/institute-applications/"+id, `{"phone":"+91 20 5550 9999"}`, app); rr.Code != 200 {
		t.Fatalf("fix: %d %s", rr.Code, rr.Body)
	}
	if rr := e.do("POST", "/api/v1/institute-applications/"+id+"/submit", "", app); rr.Code != 200 {
		t.Fatalf("resubmit: %d %s", rr.Code, rr.Body)
	}
	e.do("POST", "/api/v1/ops/institute-applications/"+id+"/start-review", "", ops)
	if rr := e.do("POST", "/api/v1/ops/institute-applications/"+id+"/reject", `{"reason":"Cannot verify the institute exists."}`, ops); rr.Code != 200 {
		t.Fatalf("reject: %d", rr.Code)
	}
	// Rejection releases the slug and the applicant may apply again.
	if rr := e.do("POST", "/api/v1/institute-applications", full, app); rr.Code != 201 {
		t.Fatalf("reapply after rejection: %d %s", rr.Code, rr.Body)
	}
	// No credential-like data ever lands in audit metadata.
	for _, rec := range e.audit.Records() {
		for k := range rec.Metadata {
			if strings.Contains(strings.ToLower(k), "password") || strings.Contains(strings.ToLower(k), "token") {
				t.Fatalf("sensitive key %q in audit", k)
			}
		}
	}
}

func TestOpsListFilter(t *testing.T) {
	e := setup(t)
	ops := e.user(t, "ops@example.com", auth.PlatformOps)
	if rr := e.do("GET", "/api/v1/ops/institute-applications?status=draft", "", ops); rr.Code != 422 {
		t.Fatalf("draft filter: %d", rr.Code)
	}
	if rr := e.do("GET", "/api/v1/ops/institute-applications?status=submitted,under_review&limit=500", "", ops); rr.Code != 200 || js(rr)["limit"].(float64) != 100 {
		t.Fatalf("list: %d %s", rr.Code, rr.Body)
	}
}
