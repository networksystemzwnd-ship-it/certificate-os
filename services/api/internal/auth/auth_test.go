package auth_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/certificate-os/api/internal/auth"
	"github.com/certificate-os/api/internal/auth/authtest"
)

func newSvc(t *testing.T) (*auth.Service, *authtest.MemStore, *time.Time) {
	t.Helper()
	store := authtest.NewMemStore()
	svc, err := auth.NewService(store, auth.Hasher{Iterations: 1000}, time.Hour) // fast hashing for tests only
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	svc.Now = func() time.Time { return now }
	return svc, store, &now
}

const pw = "correct horse battery"

func TestPasswordHashVerifyAndRehash(t *testing.T) {
	h := auth.Hasher{Iterations: 1000}
	enc, err := h.Hash(pw)
	if err != nil {
		t.Fatal(err)
	}
	if !h.Verify(pw, enc) || h.Verify("wrong password!!", enc) {
		t.Fatal("verify mismatch")
	}
	if strings.Contains(enc, pw) {
		t.Fatal("hash leaks password")
	}
	enc2, _ := h.Hash(pw)
	if enc == enc2 {
		t.Fatal("salts must differ")
	}
	if h.NeedsRehash(enc) || !(auth.Hasher{Iterations: 2000}).NeedsRehash(enc) {
		t.Fatal("rehash detection wrong")
	}
	if h.Verify(pw, "garbage") || h.Verify(pw, "") || h.Verify(pw, "pbkdf2-sha256$x$y$z") {
		t.Fatal("malformed hashes must not verify")
	}
}

func TestPasswordPolicy(t *testing.T) {
	if auth.ValidatePassword("short", "a@b.co") == "" {
		t.Error("short password accepted")
	}
	if auth.ValidatePassword(strings.Repeat("a", 129), "a@b.co") == "" {
		t.Error("overlong password accepted")
	}
	if auth.ValidatePassword("someone@example.com", "SOMEONE@example.com") == "" {
		t.Error("password equal to email accepted")
	}
	if auth.ValidatePassword(pw, "a@b.co") != "" {
		t.Error("valid password rejected")
	}
}

func TestNormalizeEmail(t *testing.T) {
	if e, ok := auth.NormalizeEmail("  User@Example.COM "); !ok || e != "user@example.com" {
		t.Fatalf("got %q %v", e, ok)
	}
	for _, bad := range []string{"", "nope", "Bob <bob@x.com>", "a@b.co, c@d.co", strings.Repeat("a", 250) + "@x.com"} {
		if _, ok := auth.NormalizeEmail(bad); ok {
			t.Errorf("accepted %q", bad)
		}
	}
}

func TestRegisterLoginAuthenticateLogout(t *testing.T) {
	svc, _, _ := newSvc(t)
	ctx := context.Background()
	if _, err := svc.Register(ctx, "a@x.com", pw); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Register(ctx, "a@x.com", pw); !errors.Is(err, auth.ErrEmailTaken) {
		t.Fatalf("duplicate: %v", err)
	}
	tok, p, err := svc.Login(ctx, "a@x.com", pw)
	if err != nil || p.Email != "a@x.com" || tok == "" {
		t.Fatalf("login: %v", err)
	}
	got, err := svc.Authenticate(ctx, tok)
	if err != nil || got.UserID != p.UserID {
		t.Fatalf("authenticate: %v", err)
	}
	if err := svc.Logout(ctx, tok); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Authenticate(ctx, tok); !errors.Is(err, auth.ErrUnauthenticated) {
		t.Fatal("session usable after logout")
	}
}

func TestLoginFailuresAreIndistinguishable(t *testing.T) {
	svc, store, _ := newSvc(t)
	ctx := context.Background()
	u, _ := svc.Register(ctx, "a@x.com", pw)
	_, _, errWrong := svc.Login(ctx, "a@x.com", "wrong password here")
	_, _, errUnknown := svc.Login(ctx, "nobody@x.com", pw)
	_ = store.SetUserStatus(ctx, u.ID, auth.StatusSuspended)
	_, _, errSuspended := svc.Login(ctx, "a@x.com", pw)
	for _, e := range []error{errWrong, errUnknown, errSuspended} {
		if !errors.Is(e, auth.ErrInvalidLogin) {
			t.Fatalf("expected ErrInvalidLogin, got %v", e)
		}
	}
}

func TestSuspensionTakesEffectOnLiveSession(t *testing.T) {
	svc, store, _ := newSvc(t)
	ctx := context.Background()
	u, _ := svc.Register(ctx, "a@x.com", pw)
	tok, _, _ := svc.Login(ctx, "a@x.com", pw)

	// Status flipped without session revocation: Authenticate must still reject.
	_ = store.SetUserStatus(ctx, u.ID, auth.StatusSuspended)
	if _, err := svc.Authenticate(ctx, tok); !errors.Is(err, auth.ErrUnauthenticated) {
		t.Fatal("suspended user kept access")
	}
	_ = svc.ReactivateUser(ctx, u.ID)
	if _, err := svc.Authenticate(ctx, tok); err != nil {
		t.Fatal("reactivated user should regain the live session if not revoked")
	}
	// SuspendUser also revokes sessions permanently.
	_ = svc.SuspendUser(ctx, u.ID)
	_ = svc.ReactivateUser(ctx, u.ID)
	if _, err := svc.Authenticate(ctx, tok); !errors.Is(err, auth.ErrUnauthenticated) {
		t.Fatal("sessions should stay revoked after SuspendUser")
	}
}

func TestSessionExpiry(t *testing.T) {
	svc, _, now := newSvc(t)
	ctx := context.Background()
	_, _ = svc.Register(ctx, "a@x.com", pw)
	tok, _, _ := svc.Login(ctx, "a@x.com", pw)
	*now = now.Add(59 * time.Minute)
	if _, err := svc.Authenticate(ctx, tok); err != nil {
		t.Fatal("should still be valid")
	}
	*now = now.Add(2 * time.Minute)
	if _, err := svc.Authenticate(ctx, tok); !errors.Is(err, auth.ErrUnauthenticated) {
		t.Fatal("expired session accepted")
	}
}

func TestForgedAndEmptyTokensRejected(t *testing.T) {
	svc, _, _ := newSvc(t)
	for _, tok := range []string{"", "forged", strings.Repeat("A", 43)} {
		if _, err := svc.Authenticate(context.Background(), tok); !errors.Is(err, auth.ErrUnauthenticated) {
			t.Errorf("token %q accepted", tok)
		}
	}
}

func TestRoleChangeVisibleImmediately(t *testing.T) {
	svc, store, _ := newSvc(t)
	ctx := context.Background()
	u, _ := svc.Register(ctx, "ops@x.com", pw)
	tok, _, _ := svc.Login(ctx, "ops@x.com", pw)
	p, _ := svc.Authenticate(ctx, tok)
	if p.HasPlatformPermission(auth.PermOpsApplicationsReview) {
		t.Fatal("new user must have no platform permissions")
	}
	_ = store.SetPlatformRole(ctx, u.ID, auth.PlatformOps)
	p, _ = svc.Authenticate(ctx, tok)
	if !p.HasPlatformPermission(auth.PermOpsApplicationsReview) {
		t.Fatal("role change not reflected")
	}
}

func TestLimiter(t *testing.T) {
	l := auth.NewLimiter(3, time.Minute)
	now := time.Now()
	l.SetClock(func() time.Time { return now })
	for i := 0; i < 3; i++ {
		if ok, _ := l.Allow("k"); !ok {
			t.Fatalf("attempt %d denied", i)
		}
	}
	if ok, retry := l.Allow("k"); ok || retry <= 0 {
		t.Fatal("4th attempt should be denied with retry-after")
	}
	if ok, _ := l.Allow("other"); !ok {
		t.Fatal("keys must be independent")
	}
	now = now.Add(61 * time.Second)
	if ok, _ := l.Allow("k"); !ok {
		t.Fatal("window should reset")
	}
}

// --- Role-denial tests (brain §3.3) ---

func TestPlatformOpsHaveNoStudentOrCertificateAccess(t *testing.T) {
	denied := []auth.Permission{auth.PermStudentsRead, auth.PermStudentsWrite, auth.PermStudentsExport,
		auth.PermCertsRead, auth.PermCertsIssue, auth.PermCertsRevoke, auth.PermStaffManage}
	for _, role := range []auth.PlatformRole{auth.PlatformOps, auth.PlatformSuperAdmin} {
		for _, p := range denied {
			if auth.HasPlatformPermission(role, p) {
				t.Errorf("platform role %q must not hold %q", role, p)
			}
		}
	}
	if !auth.HasPlatformPermission(auth.PlatformOps, auth.PermOpsApplicationsReview) {
		t.Error("ops must be able to review applications")
	}
	if auth.HasPlatformPermission(auth.PlatformOps, auth.PermPlatformAdmin) {
		t.Error("ops must not be platform admin")
	}
}

func TestUnknownRolesAndPermissionsFailClosed(t *testing.T) {
	if auth.HasPlatformPermission("root", auth.PermPlatformAdmin) || auth.HasPlatformPermission(auth.PlatformNone, auth.PermOpsApplicationsRead) {
		t.Error("unknown/none platform role granted access")
	}
	if auth.HasInstitutePermission("god", auth.PermCertsIssue) {
		t.Error("unknown institute role granted access")
	}
	if auth.HasInstitutePermission(auth.InstituteOwner, "does.not.exist") {
		t.Error("unknown permission granted")
	}
}

func TestInstituteRoleBoundaries(t *testing.T) {
	if !auth.HasInstitutePermission(auth.InstituteOwner, auth.PermStaffManage) {
		t.Error("owner must manage staff")
	}
	if auth.HasInstitutePermission(auth.InstituteAdmin, auth.PermStaffManage) || auth.HasInstitutePermission(auth.InstituteAdmin, auth.PermInstituteManage) {
		t.Error("admin must not manage staff/institute settings")
	}
	if !auth.HasInstitutePermission(auth.InstituteAdmin, auth.PermCertsIssue) {
		t.Error("admin must issue certificates")
	}
	for _, p := range []auth.Permission{auth.PermCertsIssue, auth.PermCertsRevoke, auth.PermCertsDeliver, auth.PermStaffManage, auth.PermStudentsExport} {
		if auth.HasInstitutePermission(auth.InstituteStaff, p) {
			t.Errorf("certificate staff must not hold %q by default", p)
		}
	}
	for _, r := range []auth.InstituteRole{auth.InstituteOwner, auth.InstituteAdmin, auth.InstituteStaff} {
		for _, p := range auth.InstitutePermissions(r) {
			if strings.HasPrefix(string(p), "ops.") || p == auth.PermPlatformAdmin {
				t.Errorf("institute role %q holds platform permission %q", r, p)
			}
		}
	}
}

func TestSuspendedPrincipalHasNoPermissions(t *testing.T) {
	p := auth.Principal{PlatformRole: auth.PlatformSuperAdmin, Status: auth.StatusSuspended}
	if p.HasPlatformPermission(auth.PermOpsApplicationsReview) {
		t.Error("suspended principal must be denied")
	}
}
