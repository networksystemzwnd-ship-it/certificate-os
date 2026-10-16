package tenancy_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/certificate-os/api/internal/auth"
	"github.com/certificate-os/api/internal/tenancy"
	"github.com/certificate-os/api/internal/tenancy/tenancytest"
)

func code(rr *httptest.ResponseRecorder) string {
	var b struct{ Error struct{ Code string } }
	_ = json.Unmarshal(rr.Body.Bytes(), &b)
	return b.Error.Code
}

func TestAnonymousCannotReachTenantRoutes(t *testing.T) {
	f := tenancytest.NewFixture(t, nil)
	for _, p := range []string{"/api/v1/institute", "/api/v1/institute/members", "/api/v1/me/institutes"} {
		if rr := f.Do("GET", p, "", ""); rr.Code != 401 {
			t.Errorf("%s: got %d", p, rr.Code)
		}
	}
}

func TestOwnerSeesOwnInstituteOnly(t *testing.T) {
	f := tenancytest.NewFixture(t, nil)
	rr := f.Do("GET", "/api/v1/institute", "a-owner@t.test", "")
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), f.A.ID) || strings.Contains(rr.Body.String(), f.B.ID) {
		t.Fatalf("%d %s", rr.Code, rr.Body)
	}
}

// --- The core negative tests: cross-tenant reads must fail ---

func TestCrossTenantInstituteSelectionDenied(t *testing.T) {
	f := tenancytest.NewFixture(t, nil)
	cases := []struct{ who, target string }{
		{"a-owner@t.test", f.B.ID}, {"b-owner@t.test", f.A.ID},
		{"a-admin@t.test", f.B.ID}, {"a-staff@t.test", f.B.ID},
		{"a-owner@t.test", f.C.ID}, {"a-owner@t.test", f.D.ID},
	}
	for _, c := range cases {
		for _, path := range []string{"/api/v1/institute", "/api/v1/institute/members"} {
			rr := f.Do("GET", path, c.who, c.target)
			if rr.Code != 403 {
				t.Errorf("%s -> %s %s: got %d, want 403", c.who, c.target, path, rr.Code)
			}
			if strings.Contains(rr.Body.String(), "Beta") || strings.Contains(rr.Body.String(), "Alpha") {
				t.Errorf("response leaks institute data: %s", rr.Body)
			}
		}
	}
}

func TestNonexistentAndForeignInstitutesAreIndistinguishable(t *testing.T) {
	f := tenancytest.NewFixture(t, nil)
	foreign := f.Do("GET", "/api/v1/institute", "a-owner@t.test", f.B.ID)
	ghost := f.Do("GET", "/api/v1/institute", "a-owner@t.test", "00000000-0000-0000-0000-000000000000")
	junk := f.Do("GET", "/api/v1/institute", "a-owner@t.test", "' OR 1=1 --")
	for _, rr := range []*httptest.ResponseRecorder{foreign, ghost, junk} {
		if rr.Code != 403 {
			t.Fatalf("got %d", rr.Code)
		}
	}
	norm := func(rr *httptest.ResponseRecorder) string {
		var m map[string]map[string]any
		_ = json.Unmarshal(rr.Body.Bytes(), &m)
		delete(m["error"], "request_id")
		b, _ := json.Marshal(m)
		return string(b)
	}
	if norm(foreign) != norm(ghost) || norm(ghost) != norm(junk) {
		t.Fatal("responses differ: institute existence can be enumerated")
	}
}

func TestMemberListContainsOnlyOwnInstituteMembers(t *testing.T) {
	f := tenancytest.NewFixture(t, nil)
	rr := f.Do("GET", "/api/v1/institute/members", "a-owner@t.test", "")
	if rr.Code != 200 {
		t.Fatalf("%d %s", rr.Code, rr.Body)
	}
	body := rr.Body.String()
	for _, mustHave := range []string{"a-owner@t.test", "a-admin@t.test", "a-staff@t.test", "multi@t.test"} {
		if !strings.Contains(body, f.UserID(mustHave)) {
			t.Errorf("missing member %s", mustHave)
		}
	}
	for _, mustNot := range []string{"b-owner@t.test", "b-admin@t.test", "b-staff@t.test", "c-owner@t.test", "d-owner@t.test"} {
		if strings.Contains(body, `"`+f.UserID(mustNot)+`"`) {
			t.Errorf("leaked member of another institute: %s", mustNot)
		}
	}
}

// --- Role/permission boundaries inside one tenant ---

func TestStaffManageRequiresOwner(t *testing.T) {
	f := tenancytest.NewFixture(t, nil)
	want := map[string]int{"a-owner@t.test": 200, "a-admin@t.test": 403, "a-staff@t.test": 403, "a-issuer@t.test": 403}
	for who, status := range want {
		if rr := f.Do("GET", "/api/v1/institute/members", who, ""); rr.Code != status {
			t.Errorf("%s: got %d want %d", who, rr.Code, status)
		}
	}
}

func TestUsersWithoutMembershipAreDenied(t *testing.T) {
	f := tenancytest.NewFixture(t, nil)
	for _, who := range []string{"nobody@t.test", "ops@t.test", "removed@t.test", "d-owner@t.test"} {
		if rr := f.Do("GET", "/api/v1/institute", who, ""); rr.Code != 403 {
			t.Errorf("%s: got %d want 403 (platform ops/removed/closed must not reach tenant data)", who, rr.Code)
		}
		if rr := f.Do("GET", "/api/v1/me/institutes", who, ""); rr.Code != 200 || strings.Contains(rr.Body.String(), f.B.ID) {
			t.Errorf("%s: /me/institutes %d %s", who, rr.Code, rr.Body)
		}
	}
}

func TestMultiMembershipRequiresExplicitSelection(t *testing.T) {
	f := tenancytest.NewFixture(t, nil)
	rr := f.Do("GET", "/api/v1/institute", "multi@t.test", "")
	if rr.Code != 400 || code(rr) != "institute_required" {
		t.Fatalf("got %d %s", rr.Code, rr.Body)
	}
	for inst, role := range map[string]string{f.A.ID: `"role":"staff"`, f.B.ID: `"role":"admin"`} {
		rr := f.Do("GET", "/api/v1/institute", "multi@t.test", inst)
		if rr.Code != 200 || !strings.Contains(rr.Body.String(), role) {
			t.Errorf("select %s: %d %s", inst, rr.Code, rr.Body)
		}
	}
	// Roles do not leak across memberships: staff in A cannot use admin-level permissions of B in A.
	if rr := f.Do("GET", "/api/v1/institute/members", "multi@t.test", f.B.ID); rr.Code != 403 {
		t.Errorf("admin in B must not manage staff: %d", rr.Code)
	}
}

func TestMembershipsListIsPerUser(t *testing.T) {
	f := tenancytest.NewFixture(t, nil)
	rr := f.Do("GET", "/api/v1/me/institutes", "multi@t.test", "")
	if !strings.Contains(rr.Body.String(), f.A.ID) || !strings.Contains(rr.Body.String(), f.B.ID) || strings.Contains(rr.Body.String(), f.C.ID) {
		t.Fatalf("%s", rr.Body)
	}
	rr = f.Do("GET", "/api/v1/me/institutes", "a-owner@t.test", "")
	if strings.Contains(rr.Body.String(), f.B.ID) {
		t.Fatalf("leak: %s", rr.Body)
	}
}

// --- Suspended / closed institutes ---

func TestSuspendedInstituteIsReadOnly(t *testing.T) {
	f := tenancytest.NewFixture(t, func(f *tenancytest.Fixture) {
		f.Mux.Handle("POST /api/v1/_test/write", f.TenantH.Guard(auth.PermStudentsWrite, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })))
		f.Mux.Handle("GET /api/v1/_test/read", f.TenantH.Guard(auth.PermStudentsRead, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })))
	})
	// Active institute: owner may write and read.
	if rr := f.Do("POST", "/api/v1/_test/write", "a-owner@t.test", ""); rr.Code != 204 {
		t.Fatalf("active write: %d", rr.Code)
	}
	// Suspended institute: reads allowed, writes denied, even for the owner.
	if rr := f.Do("GET", "/api/v1/_test/read", "c-owner@t.test", ""); rr.Code != 204 {
		t.Fatalf("suspended read: %d", rr.Code)
	}
	if rr := f.Do("POST", "/api/v1/_test/write", "c-owner@t.test", ""); rr.Code != 403 {
		t.Fatalf("suspended write must be denied: %d", rr.Code)
	}
	// Suspension takes effect on the very next request for an existing session.
	_ = f.Tenants.SetInstituteStatus(context.Background(), f.A.ID, tenancy.InstituteSuspended)
	if rr := f.Do("POST", "/api/v1/_test/write", "a-owner@t.test", ""); rr.Code != 403 {
		t.Fatalf("write after suspension must be denied: %d", rr.Code)
	}
	rr := f.Do("GET", "/api/v1/institute", "a-owner@t.test", "")
	if rr.Code != 200 || strings.Contains(rr.Body.String(), "students.write") || !strings.Contains(rr.Body.String(), "institute.read") {
		t.Fatalf("permissions shown for suspended institute: %s", rr.Body)
	}
}

func TestRemovedMembershipLosesAccessImmediately(t *testing.T) {
	f := tenancytest.NewFixture(t, nil)
	if rr := f.Do("GET", "/api/v1/institute", "a-staff@t.test", ""); rr.Code != 200 {
		t.Fatalf("before removal: %d", rr.Code)
	}
	_ = f.Tenants.RemoveMembership(context.Background(), f.A.ID, f.UserID("a-staff@t.test"))
	if rr := f.Do("GET", "/api/v1/institute", "a-staff@t.test", ""); rr.Code != 403 {
		t.Fatalf("after removal: %d", rr.Code)
	}
}

func TestClosedInstituteDeniesEveryone(t *testing.T) {
	f := tenancytest.NewFixture(t, nil)
	if rr := f.Do("GET", "/api/v1/institute", "d-owner@t.test", f.D.ID); rr.Code != 403 {
		t.Fatalf("got %d", rr.Code)
	}
}

// --- Unit-level checks on Tenant ---

func resolve(t *testing.T, f *tenancytest.Fixture, email, inst string) tenancy.Tenant {
	t.Helper()
	svc := &tenancy.Service{Store: f.Tenants}
	tn, err := svc.Resolve(context.Background(), auth.Principal{UserID: f.UserID(email), Status: auth.StatusActive}, inst)
	if err != nil {
		t.Fatalf("resolve %s: %v", email, err)
	}
	return tn
}

func TestGrantsExtendButNeverEscalate(t *testing.T) {
	f := tenancytest.NewFixture(t, nil)
	plain, issuer := resolve(t, f, "a-staff@t.test", ""), resolve(t, f, "a-issuer@t.test", "")
	if plain.Can(auth.PermCertsIssue) || !issuer.Can(auth.PermCertsIssue) {
		t.Fatal("grant not applied / applied to wrong member")
	}
	if issuer.Can(auth.PermStaffManage) || issuer.Can(auth.PermCertsRevoke) {
		t.Fatal("grant leaked extra permissions")
	}
	if tn := resolve(t, f, "a-owner@t.test", ""); tn.Can(auth.PermPlatformAdmin) || tn.Can(auth.PermOpsApplicationsReview) {
		t.Fatal("institute owner holds platform permissions")
	}
	if _, err := f.Tenants.AddMembership(context.Background(), f.B.ID, f.UserID("nobody@t.test"), auth.InstituteStaff, []auth.Permission{auth.PermStaffManage}); err == nil {
		t.Fatal("owner-only permission granted individually")
	}
	if _, err := f.Tenants.AddMembership(context.Background(), f.B.ID, f.UserID("nobody@t.test"), auth.InstituteStaff, []auth.Permission{auth.PermPlatformAdmin}); err == nil {
		t.Fatal("platform permission granted to a member")
	}
}

func TestCanFailsClosedWithoutTenant(t *testing.T) {
	var zero tenancy.Tenant
	if zero.Can(auth.PermInstituteRead) {
		t.Fatal("zero Tenant must hold nothing")
	}
	if tenancy.TrustedScope("").InstituteID() != "" {
		t.Fatal("unexpected")
	}
}

func TestSlugValidation(t *testing.T) {
	for _, bad := range []string{"", "a", "ab", "-abc", "abc-", "UPPER", "has space", "api", "admin", "ops", strings.Repeat("a", 41), "a_b_c"} {
		if tenancy.ValidateSlug(bad) == nil {
			t.Errorf("accepted %q", bad)
		}
	}
	for _, good := range []string{"alpha-academy", "abc", "my-institute-2026"} {
		if err := tenancy.ValidateSlug(good); err != nil {
			t.Errorf("rejected %q", good)
		}
	}
}
