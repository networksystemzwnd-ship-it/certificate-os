package authhttp_test

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

	"github.com/certificate-os/api/internal/auth"
	"github.com/certificate-os/api/internal/auth/authhttp"
	"github.com/certificate-os/api/internal/auth/authtest"
	"github.com/certificate-os/api/internal/platform/httpx"
)

const pw = "correct horse battery"

type env struct {
	srv   http.Handler
	store *authtest.MemStore
	svc   *auth.Service
	h     *authhttp.Handler
}

func setup(t *testing.T) *env {
	t.Helper()
	store := authtest.NewMemStore()
	svc, err := auth.NewService(store, auth.Hasher{Iterations: 1000}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := authhttp.NewHandler(svc, authhttp.CookieConfig{TTL: time.Hour}, log)
	mux := http.NewServeMux()
	h.Register(mux)
	// RequirePlatformPermission must sit inside Authenticate.
	mux.Handle("GET /api/v1/ops/secure", h.Authenticate(authhttp.RequirePlatformPermission(auth.PermOpsApplicationsReview, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))))
	return &env{srv: httpx.Chain(log, httpx.CSRF("http://web", mux)), store: store, svc: svc, h: h}
}

func (e *env) do(method, path, body string, cookie *http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.RemoteAddr = "203.0.113.9:1234"
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if method != "GET" {
		req.Header.Set(httpx.CSRFHeader, "1")
	}
	if cookie != nil {
		req.AddCookie(cookie)
	}
	rr := httptest.NewRecorder()
	e.srv.ServeHTTP(rr, req)
	return rr
}

func sessionCookie(t *testing.T, rr *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	for _, c := range rr.Result().Cookies() {
		if c.Name == "cos_session" {
			return c
		}
	}
	t.Fatal("no session cookie set")
	return nil
}

func body(rr *httptest.ResponseRecorder) map[string]any {
	var m map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &m)
	return m
}

func (e *env) registerAndLogin(t *testing.T, email string) *http.Cookie {
	t.Helper()
	if rr := e.do("POST", "/api/v1/auth/register", `{"email":"`+email+`","password":"`+pw+`"}`, nil); rr.Code != 201 {
		t.Fatalf("register: %d %s", rr.Code, rr.Body)
	}
	rr := e.do("POST", "/api/v1/auth/login", `{"email":"`+email+`","password":"`+pw+`"}`, nil)
	if rr.Code != 200 {
		t.Fatalf("login: %d %s", rr.Code, rr.Body)
	}
	return sessionCookie(t, rr)
}

func TestHappyPathAndCookieFlags(t *testing.T) {
	e := setup(t)
	ck := e.registerAndLogin(t, "A@Example.com")
	if !ck.HttpOnly || ck.SameSite != http.SameSiteLaxMode || ck.Path != "/" {
		t.Fatalf("weak cookie: %+v", ck)
	}
	rr := e.do("GET", "/api/v1/me", "", ck)
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), `"a@example.com"`) {
		t.Fatalf("me: %d %s", rr.Code, rr.Body)
	}
	if strings.Contains(rr.Body.String(), "password") {
		t.Fatal("response leaks password material")
	}
}

func TestProtectedRouteRequiresAuth(t *testing.T) {
	e := setup(t)
	if rr := e.do("GET", "/api/v1/me", "", nil); rr.Code != 401 {
		t.Fatalf("got %d", rr.Code)
	}
	if rr := e.do("GET", "/api/v1/me", "", &http.Cookie{Name: "cos_session", Value: "forged"}); rr.Code != 401 {
		t.Fatalf("forged cookie got %d", rr.Code)
	}
}

func TestCannotSelfAssignRole(t *testing.T) {
	e := setup(t)
	rr := e.do("POST", "/api/v1/auth/register", `{"email":"x@example.com","password":"`+pw+`","platform_role":"super_admin"}`, nil)
	if rr.Code != 400 {
		t.Fatalf("role field must be rejected, got %d", rr.Code)
	}
	if _, err := e.store.UserByEmail(context.Background(), "x@example.com"); err == nil {
		t.Fatal("user was created despite rejected request")
	}
	ck := e.registerAndLogin(t, "y@example.com")
	if rr := e.do("GET", "/api/v1/ops/secure", "", ck); rr.Code != 403 {
		t.Fatalf("normal user reached ops route: %d", rr.Code)
	}
}

func TestOpsRouteAllowsOpsRoleOnly(t *testing.T) {
	e := setup(t)
	ck := e.registerAndLogin(t, "ops@example.com")
	u, _ := e.store.UserByEmail(context.Background(), "ops@example.com")
	_ = e.store.SetPlatformRole(context.Background(), u.ID, auth.PlatformOps)
	if rr := e.do("GET", "/api/v1/ops/secure", "", ck); rr.Code != 204 {
		t.Fatalf("ops denied: %d", rr.Code)
	}
	if rr := e.do("GET", "/api/v1/ops/secure", "", nil); rr.Code != 401 {
		t.Fatalf("anonymous should be 401, got %d", rr.Code)
	}
}

func TestValidationErrors(t *testing.T) {
	e := setup(t)
	rr := e.do("POST", "/api/v1/auth/register", `{"email":"nope","password":"short"}`, nil)
	if rr.Code != 422 {
		t.Fatalf("got %d", rr.Code)
	}
	details := body(rr)["error"].(map[string]any)["details"].(map[string]any)
	if details["email"] == nil || details["password"] == nil {
		t.Fatalf("details: %v", details)
	}
}

func TestDuplicateRegistrationConflict(t *testing.T) {
	e := setup(t)
	e.registerAndLogin(t, "a@example.com")
	if rr := e.do("POST", "/api/v1/auth/register", `{"email":"A@example.com","password":"`+pw+`"}`, nil); rr.Code != 409 {
		t.Fatalf("got %d", rr.Code)
	}
}

func TestLoginFailureIsGeneric(t *testing.T) {
	e := setup(t)
	e.registerAndLogin(t, "a@example.com")
	wrong := e.do("POST", "/api/v1/auth/login", `{"email":"a@example.com","password":"not the password"}`, nil)
	unknown := e.do("POST", "/api/v1/auth/login", `{"email":"ghost@example.com","password":"not the password"}`, nil)
	if wrong.Code != 401 || unknown.Code != 401 {
		t.Fatalf("codes %d %d", wrong.Code, unknown.Code)
	}
	a, b := body(wrong)["error"].(map[string]any), body(unknown)["error"].(map[string]any)
	if a["message"] != b["message"] || a["code"] != b["code"] {
		t.Fatal("login errors are distinguishable")
	}
	if len(wrong.Result().Cookies()) != 0 {
		t.Fatal("cookie set on failed login")
	}
}

func TestLogoutRevokesSession(t *testing.T) {
	e := setup(t)
	ck := e.registerAndLogin(t, "a@example.com")
	if rr := e.do("POST", "/api/v1/auth/logout", "", ck); rr.Code != 204 {
		t.Fatalf("logout %d", rr.Code)
	}
	if rr := e.do("GET", "/api/v1/me", "", ck); rr.Code != 401 {
		t.Fatalf("session survived logout: %d", rr.Code)
	}
	if rr := e.do("POST", "/api/v1/auth/logout", "", nil); rr.Code != 204 {
		t.Fatal("logout must be idempotent")
	}
}

func TestSuspensionCutsOffLiveSession(t *testing.T) {
	e := setup(t)
	ck := e.registerAndLogin(t, "a@example.com")
	u, _ := e.store.UserByEmail(context.Background(), "a@example.com")
	_ = e.svc.SuspendUser(context.Background(), u.ID)
	if rr := e.do("GET", "/api/v1/me", "", ck); rr.Code != 401 {
		t.Fatalf("suspended user kept access: %d", rr.Code)
	}
	if rr := e.do("POST", "/api/v1/auth/login", `{"email":"a@example.com","password":"`+pw+`"}`, nil); rr.Code != 401 {
		t.Fatalf("suspended user could log in: %d", rr.Code)
	}
}

func TestStateChangingRequestsNeedCSRFHeader(t *testing.T) {
	e := setup(t)
	req := httptest.NewRequest("POST", "/api/v1/auth/login", strings.NewReader(`{"email":"a@example.com","password":"x"}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	e.srv.ServeHTTP(rr, req)
	if rr.Code != 403 {
		t.Fatalf("got %d", rr.Code)
	}
}

func TestLoginRateLimited(t *testing.T) {
	e := setup(t)
	var last *httptest.ResponseRecorder
	for i := 0; i < 11; i++ {
		last = e.do("POST", "/api/v1/auth/login", `{"email":"a@example.com","password":"wrong password"}`, nil)
	}
	if last.Code != 429 || last.Header().Get("Retry-After") == "" {
		t.Fatalf("expected 429 with Retry-After, got %d", last.Code)
	}
}
