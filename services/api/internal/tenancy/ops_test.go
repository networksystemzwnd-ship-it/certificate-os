package tenancy_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/certificate-os/api/internal/audit/audittest"
	"github.com/certificate-os/api/internal/auth"
	"github.com/certificate-os/api/internal/auth/authhttp"
	"github.com/certificate-os/api/internal/auth/authtest"
	"github.com/certificate-os/api/internal/platform/httpx"
	"github.com/certificate-os/api/internal/tenancy"
	"github.com/certificate-os/api/internal/tenancy/tenancytest"
)

const opsPW = "correct horse battery"

type opsEnv struct {
	srv   http.Handler
	users *authtest.MemStore
	store *tenancytest.MemStore
	w     *audittest.MemWriter
}

func opsSetup(t *testing.T) *opsEnv {
	t.Helper()
	users := authtest.NewMemStore()
	svc, _ := auth.NewService(users, auth.Hasher{Iterations: 1000}, time.Hour)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	ah := authhttp.NewHandler(svc, authhttp.CookieConfig{TTL: time.Hour}, log)
	store, w := tenancytest.NewMemStore(), audittest.NewMemWriter()
	store.Audit = w
	mux := http.NewServeMux()
	ah.Register(mux)
	(&tenancy.OpsHandler{Svc: &tenancy.OpsService{Store: store}, Log: log, Authn: ah.Authenticate}).Register(mux)
	return &opsEnv{srv: httpx.Chain(log, httpx.CSRF("http://web", mux)), users: users, store: store, w: w}
}

func (e *opsEnv) do(method, path, body string, c *http.Cookie) *httptest.ResponseRecorder {
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

func (e *opsEnv) login(t *testing.T, email string, role auth.PlatformRole) *http.Cookie {
	t.Helper()
	e.do("POST", "/api/v1/auth/register", `{"email":"`+email+`","password":"`+opsPW+`"}`, nil)
	if role != auth.PlatformNone {
		u, _ := e.users.UserByEmail(context.Background(), email)
		_ = e.users.SetPlatformRole(context.Background(), u.ID, role)
	}
	for _, c := range e.do("POST", "/api/v1/auth/login", `{"email":"`+email+`","password":"`+opsPW+`"}`, nil).Result().Cookies() {
		if c.Name == "cos_session" {
			return c
		}
	}
	t.Fatal("no cookie")
	return nil
}

func TestOpsSuspendReactivate(t *testing.T) {
	e := opsSetup(t)
	ops := e.login(t, "ops@example.com", auth.PlatformOps)
	user := e.login(t, "u@example.com", auth.PlatformNone)
	inst, _ := e.store.CreateInstitute(context.Background(), "alpha", "Alpha Academy")
	base := "/api/v1/ops/institutes/" + inst.ID

	if rr := e.do("POST", base+"/suspend", `{"reason":"Fee dispute under review"}`, user); rr.Code != 403 {
		t.Fatalf("plain user: %d", rr.Code)
	}
	if rr := e.do("POST", base+"/suspend", `{"reason":"Fee dispute under review"}`, nil); rr.Code != 401 {
		t.Fatalf("anonymous: %d", rr.Code)
	}
	if rr := e.do("POST", base+"/suspend", `{"reason":"short"}`, ops); rr.Code != 422 {
		t.Fatalf("short reason: %d", rr.Code)
	}
	if len(e.w.Records()) != 0 {
		t.Fatal("failed requests must not audit a change")
	}
	if rr := e.do("POST", base+"/suspend", `{"reason":"Fee dispute under review"}`, ops); rr.Code != 200 || !strings.Contains(rr.Body.String(), `"suspended"`) {
		t.Fatalf("suspend: %d %s", rr.Code, rr.Body)
	}
	if rr := e.do("POST", base+"/suspend", `{"reason":"Fee dispute under review"}`, ops); rr.Code != 409 {
		t.Fatalf("double suspend: %d", rr.Code)
	}
	if rr := e.do("POST", base+"/reactivate", "", ops); rr.Code != 200 || !strings.Contains(rr.Body.String(), `"active"`) {
		t.Fatalf("reactivate: %d %s", rr.Code, rr.Body)
	}
	if rr := e.do("POST", base+"/reactivate", "", ops); rr.Code != 409 {
		t.Fatalf("reactivate active: %d", rr.Code)
	}
	if got := strings.Join(e.w.Actions(), ","); got != "institute.suspended,institute.reactivated" {
		t.Fatalf("audit actions: %s", got)
	}
	// Closed institutes cannot be touched from here.
	_ = e.store.SetInstituteStatus(context.Background(), inst.ID, tenancy.InstituteClosed)
	if rr := e.do("POST", base+"/reactivate", "", ops); rr.Code != 409 {
		t.Fatalf("closed institute: %d", rr.Code)
	}
	if rr := e.do("POST", "/api/v1/ops/institutes/nope/reactivate", "", ops); rr.Code != 404 {
		t.Fatalf("unknown id: %d", rr.Code)
	}
	if rr := e.do("GET", "/api/v1/ops/institutes?q=alp", "", ops); rr.Code != 200 || !strings.Contains(rr.Body.String(), "alpha") {
		t.Fatalf("list: %d %s", rr.Code, rr.Body)
	}
}
