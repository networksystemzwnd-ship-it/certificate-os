package tenancytest

import (
	"context"
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
	"github.com/certificate-os/api/internal/tenancy"
)

const Password = "correct horse battery"

// Fixture is a complete in-memory API stack with four institutes and users in every role.
// REUSE IT for the cross-tenant negative tests of every new tenant-owned resource family:
// register your routes through `extra`, then assert that users of A cannot touch B's data.
//
//	A  active     a-owner a-admin a-staff a-issuer(staff + certificates.issue grant) removed(removed staff)
//	B  active     b-owner b-admin b-staff
//	C  suspended  c-owner
//	D  closed     d-owner
//	multi: staff in A and admin in B.  ops: platform ops, no membership.  nobody: no membership.
type Fixture struct {
	T          *testing.T
	Auth       *authtest.MemStore
	Svc        *auth.Service
	Tenants    *MemStore
	AuthH      *authhttp.Handler
	TenantH    *tenancy.Handler
	Mux        *http.ServeMux
	Handler    http.Handler
	A, B, C, D tenancy.Institute
	ids        map[string]string
}

func NewFixture(t *testing.T, extra func(f *Fixture)) *Fixture {
	t.Helper()
	ctx := context.Background()
	store := authtest.NewMemStore()
	svc, err := auth.NewService(store, auth.Hasher{Iterations: 1000}, time.Hour) // fast hashing: tests only
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	f := &Fixture{T: t, Auth: store, Svc: svc, Tenants: NewMemStore(), ids: map[string]string{}}

	f.AuthH = authhttp.NewHandler(svc, authhttp.CookieConfig{TTL: time.Hour}, log)
	f.TenantH = &tenancy.Handler{Svc: &tenancy.Service{Store: f.Tenants}, Log: log, Authn: f.AuthH.Authenticate}

	mk := func(slug, name string) tenancy.Institute {
		i, err := f.Tenants.CreateInstitute(ctx, slug, name)
		if err != nil {
			t.Fatal(err)
		}
		return i
	}
	f.A, f.B, f.C, f.D = mk("alpha-academy", "Alpha Academy"), mk("beta-institute", "Beta Institute"), mk("gamma-college", "Gamma College"), mk("delta-school", "Delta School")

	add := func(email string, inst tenancy.Institute, role auth.InstituteRole, grants ...auth.Permission) {
		id := f.ids[email]
		if id == "" {
			u, err := svc.Register(ctx, email, Password)
			if err != nil {
				t.Fatal(err)
			}
			id = u.ID
			f.ids[email] = id
		}
		if _, err := f.Tenants.AddMembership(ctx, inst.ID, id, role, grants); err != nil {
			t.Fatal(err)
		}
	}
	add("a-owner@t.test", f.A, auth.InstituteOwner)
	add("a-admin@t.test", f.A, auth.InstituteAdmin)
	add("a-staff@t.test", f.A, auth.InstituteStaff)
	add("a-issuer@t.test", f.A, auth.InstituteStaff, auth.PermCertsIssue)
	add("removed@t.test", f.A, auth.InstituteStaff)
	_ = f.Tenants.RemoveMembership(ctx, f.A.ID, f.ids["removed@t.test"])
	add("b-owner@t.test", f.B, auth.InstituteOwner)
	add("b-admin@t.test", f.B, auth.InstituteAdmin)
	add("b-staff@t.test", f.B, auth.InstituteStaff)
	add("multi@t.test", f.A, auth.InstituteStaff)
	add("multi@t.test", f.B, auth.InstituteAdmin)
	add("c-owner@t.test", f.C, auth.InstituteOwner)
	add("d-owner@t.test", f.D, auth.InstituteOwner)
	_ = f.Tenants.SetInstituteStatus(ctx, f.C.ID, tenancy.InstituteSuspended)
	_ = f.Tenants.SetInstituteStatus(ctx, f.D.ID, tenancy.InstituteClosed)
	for _, e := range []string{"ops@t.test", "nobody@t.test"} {
		u, err := svc.Register(ctx, e, Password)
		if err != nil {
			t.Fatal(err)
		}
		f.ids[e] = u.ID
	}
	_ = store.SetPlatformRole(ctx, f.ids["ops@t.test"], auth.PlatformOps)

	f.Mux = http.NewServeMux()
	f.AuthH.Register(f.Mux)
	f.TenantH.Register(f.Mux)
	if extra != nil {
		extra(f)
	}
	f.Handler = httpx.Chain(log, httpx.CSRF("http://web", f.Mux))
	return f
}

func (f *Fixture) UserID(email string) string { return f.ids[email] }

// Do performs a request as `email` (empty = anonymous). instituteID, when set, is sent in X-Institute-ID.
func (f *Fixture) Do(method, path, email, instituteID string) *httptest.ResponseRecorder {
	f.T.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(""))
	req.RemoteAddr = "203.0.113.9:1234"
	if method != http.MethodGet {
		req.Header.Set(httpx.CSRFHeader, "1")
	}
	if instituteID != "" {
		req.Header.Set(tenancy.InstituteHeader, instituteID)
	}
	if email != "" {
		tok, _, err := f.Svc.Login(context.Background(), email, Password)
		if err != nil {
			f.T.Fatalf("login %s: %v", email, err)
		}
		req.AddCookie(&http.Cookie{Name: "cos_session", Value: tok})
	}
	rr := httptest.NewRecorder()
	f.Handler.ServeHTTP(rr, req)
	return rr
}
