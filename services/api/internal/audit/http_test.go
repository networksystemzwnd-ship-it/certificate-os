package audit_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/certificate-os/api/internal/audit"
	"github.com/certificate-os/api/internal/audit/audittest"
	"github.com/certificate-os/api/internal/auth"
	"github.com/certificate-os/api/internal/auth/authhttp"
	"github.com/certificate-os/api/internal/auth/authtest"
	"github.com/certificate-os/api/internal/platform/httpx"
)

func TestAuditViewIsSuperAdminOnly(t *testing.T) {
	users := authtest.NewMemStore()
	svc, _ := auth.NewService(users, auth.Hasher{Iterations: 1000}, time.Hour)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	ah := authhttp.NewHandler(svc, authhttp.CookieConfig{TTL: time.Hour}, log)
	w := audittest.NewMemWriter()
	_ = w.Record(context.Background(), audit.Event{ActorType: audit.ActorSystem, Action: "institute.created", EntityType: "institute", EntityID: "i1",
		Metadata: map[string]any{"slug": "alpha", "password": "x"}})
	mux := http.NewServeMux()
	ah.Register(mux)
	(&audit.Handler{Reader: w, Log: log, Authn: ah.Authenticate}).Register(mux)
	srv := httpx.Chain(log, httpx.CSRF("http://web", mux))

	do := func(method, path, body string, c *http.Cookie) *httptest.ResponseRecorder {
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
		srv.ServeHTTP(rr, req)
		return rr
	}
	login := func(email string, role auth.PlatformRole) *http.Cookie {
		do("POST", "/api/v1/auth/register", `{"email":"`+email+`","password":"correct horse battery"}`, nil)
		if role != auth.PlatformNone {
			u, _ := users.UserByEmail(context.Background(), email)
			_ = users.SetPlatformRole(context.Background(), u.ID, role)
		}
		for _, c := range do("POST", "/api/v1/auth/login", `{"email":"`+email+`","password":"correct horse battery"}`, nil).Result().Cookies() {
			if c.Name == "cos_session" {
				return c
			}
		}
		t.Fatal("no cookie")
		return nil
	}
	if rr := do("GET", "/api/v1/ops/audit", "", nil); rr.Code != 401 {
		t.Fatalf("anonymous %d", rr.Code)
	}
	if rr := do("GET", "/api/v1/ops/audit", "", login("ops@example.com", auth.PlatformOps)); rr.Code != 403 {
		t.Fatalf("ops (non-super) %d", rr.Code)
	}
	rr := do("GET", "/api/v1/ops/audit?entity_type=institute", "", login("root@example.com", auth.PlatformSuperAdmin))
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), "institute.created") {
		t.Fatalf("super admin %d %s", rr.Code, rr.Body)
	}
	if strings.Contains(rr.Body.String(), `"password"`) {
		t.Fatal("credential-like metadata leaked")
	}
}
