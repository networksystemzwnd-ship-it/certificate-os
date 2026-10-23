package staff_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/certificate-os/api/internal/audit/audittest"
	"github.com/certificate-os/api/internal/platform/httpx"
	"github.com/certificate-os/api/internal/staff"
	"github.com/certificate-os/api/internal/staff/stafftest"
	"github.com/certificate-os/api/internal/tenancy"
	"github.com/certificate-os/api/internal/tenancy/tenancytest"
)

type env struct {
	f *tenancytest.Fixture
	w *audittest.MemWriter
	h *staff.Handler
}

func newEnv(t *testing.T) *env {
	e := &env{w: audittest.NewMemWriter()}
	e.f = tenancytest.NewFixture(t, func(f *tenancytest.Fixture) {
		ms := stafftest.NewMemStore(f.Tenants, e.w)
		ms.UserIDByEmail = f.UserID
		e.h = staff.NewHandler(staff.NewService(ms, ms), slog.New(slog.NewTextHandler(io.Discard, nil)), f.TenantH)
		e.h.Register(f.Mux)
	})
	return e
}

func (e *env) do(method, path, email, inst string, body any) (int, map[string]any, http.Header) {
	e.f.T.Helper()
	var rd io.Reader = bytes.NewReader(nil)
	if s, ok := body.(string); ok {
		rd = strings.NewReader(s)
	} else if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
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
			e.f.T.Fatalf("login %s: %v", email, err)
		}
		req.AddCookie(&http.Cookie{Name: "cos_session", Value: tok})
	}
	rr := httptest.NewRecorder()
	e.f.Handler.ServeHTTP(rr, req)
	var out map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &out)
	return rr.Code, out, rr.Header()
}

func (e *env) register(email string) {
	e.f.T.Helper()
	if _, err := e.f.Svc.Register(context.Background(), email, tenancytest.Password); err != nil {
		e.f.T.Fatal(err)
	}
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

func TestInvitePermissions(t *testing.T) {
	e := newEnv(t)
	A := e.f.A.ID
	body := map[string]any{"email": "new@t.test", "role": "staff"}
	if code, _, _ := e.do("POST", "/api/v1/institute/invitations", "", "", body); code != 401 {
		t.Errorf("anonymous: %d", code)
	}
	for _, tc := range []struct {
		email, inst string
		want        int
	}{
		{"a-admin@t.test", A, 403}, // admins cannot manage staff
		{"a-staff@t.test", A, 403},
		{"a-issuer@t.test", A, 403},
		{"nobody@t.test", A, 403},
		{"ops@t.test", A, 403},
		{"removed@t.test", A, 403},
		{"c-owner@t.test", e.f.C.ID, 403}, // suspended: read-only
		{"d-owner@t.test", e.f.D.ID, 403}, // closed
		{"a-owner@t.test", A, 201},
	} {
		if code, out, _ := e.do("POST", "/api/v1/institute/invitations", tc.email, tc.inst, body); code != tc.want {
			t.Errorf("%s: want %d, got %d %v", tc.email, tc.want, code, out)
		}
		body["email"] = "n" + tc.email // fresh email per attempt
	}
	for _, p := range []string{"/api/v1/institute/invitations"} {
		if code, _, _ := e.do("GET", p, "a-admin@t.test", A, nil); code != 403 {
			t.Errorf("admin listing invitations: %d", code)
		}
	}
}

func TestInviteAndAcceptFlow(t *testing.T) {
	e := newEnv(t)
	A := e.f.A.ID
	e.register("newbie@t.test")
	e.register("eve@t.test")

	code, out, hdr := e.do("POST", "/api/v1/institute/invitations", "a-owner@t.test", A,
		map[string]any{"email": " NewBie@T.test ", "role": "staff", "grants": []string{"certificates.issue"}})
	if code != 201 {
		t.Fatalf("invite: %d %v", code, out)
	}
	token := str(out, "token")
	if !strings.HasPrefix(token, "inv_") || len(token) < 40 || str(out, "accept_path") != "/accept-invite#token="+token || hdr.Get("Cache-Control") != "no-store" {
		t.Fatalf("invite response: %v %v", out, hdr)
	}
	if str(out, "invitation", "email") != "newbie@t.test" || str(out, "invitation", "status") != "pending" {
		t.Fatalf("invitation: %v", out)
	}
	invID := str(out, "invitation", "id")

	// the list never exposes the token
	code, list, _ := e.do("GET", "/api/v1/institute/invitations", "a-owner@t.test", A, nil)
	if code != 200 || strings.Contains(mustJSON(list), token) || !strings.Contains(mustJSON(list), "newbie@t.test") {
		t.Fatalf("list: %d %v", code, list)
	}

	// before accepting, newbie has no access to A
	if code, _, _ := e.do("GET", "/api/v1/institute", "newbie@t.test", A, nil); code != 403 {
		t.Fatalf("before accept: %d", code)
	}
	// somebody else holding the token cannot use it, and that attempt does not burn it
	if code, o, _ := e.do("POST", "/api/v1/invitations/accept", "eve@t.test", "", map[string]string{"token": token}); code != 404 {
		t.Fatalf("wrong account: %d %v", code, o)
	}
	if code, _, _ := e.do("POST", "/api/v1/invitations/accept", "", "", map[string]string{"token": token}); code != 401 {
		t.Fatalf("anonymous accept: %d", code)
	}
	code, acc, _ := e.do("POST", "/api/v1/invitations/accept", "newbie@t.test", "", map[string]string{"token": token})
	if code != 200 || str(acc, "institute", "id") != A || str(acc, "role") != "staff" {
		t.Fatalf("accept: %d %v", code, acc)
	}
	if code, inst, _ := e.do("GET", "/api/v1/institute", "newbie@t.test", A, nil); code != 200 || !strings.Contains(mustJSON(inst), "certificates.issue") {
		t.Fatalf("after accept: %d %v", code, inst)
	}
	if code, _, _ := e.do("POST", "/api/v1/invitations/accept", "newbie@t.test", "", map[string]string{"token": token}); code != 404 {
		t.Fatalf("second accept: %d", code)
	}
	_, list, _ = e.do("GET", "/api/v1/institute/invitations", "a-owner@t.test", A, nil)
	if !strings.Contains(mustJSON(list), `"accepted"`) {
		t.Fatalf("list after accept: %v", list)
	}
	// an accepted invitation cannot be revoked
	if code, _, _ := e.do("DELETE", "/api/v1/institute/invitations/"+invID, "a-owner@t.test", A, nil); code != 409 {
		t.Fatalf("revoke accepted: %d", code)
	}

	// the audit trail names no email, no token
	for _, r := range e.w.Records() {
		blob := mustJSON(r.Metadata)
		if strings.Contains(blob, "newbie") || strings.Contains(blob, token) || strings.Contains(blob, "@") {
			t.Fatalf("audit metadata leaks: %s (%s)", blob, r.Action)
		}
	}
	var acts []string
	for _, r := range e.w.Records() {
		acts = append(acts, r.Action)
	}
	if strings.Join(acts, ",") != "invitation.created,invitation.accepted" {
		t.Fatalf("audit actions: %v", acts)
	}
}

func mustJSON(v any) string { b, _ := json.Marshal(v); return string(b) }

func TestInviteValidationAndConflicts(t *testing.T) {
	e := newEnv(t)
	A := e.f.A.ID
	post := func(body any) (int, map[string]any) {
		c, o, _ := e.do("POST", "/api/v1/institute/invitations", "a-owner@t.test", A, body)
		return c, o
	}
	for name, body := range map[string]any{
		"bad email":          map[string]any{"email": "not-an-email", "role": "staff"},
		"empty email":        map[string]any{"email": "", "role": "staff"},
		"display-name email": map[string]any{"email": "Eve <eve@t.test>", "role": "staff"},
		"owner role":         map[string]any{"email": "x@t.test", "role": "owner"},
		"unknown role":       map[string]any{"email": "x@t.test", "role": "root"},
		"staff.manage grant": map[string]any{"email": "x@t.test", "role": "staff", "grants": []string{"staff.manage"}},
		"platform grant":     map[string]any{"email": "x@t.test", "role": "staff", "grants": []string{"platform.admin"}},
		"unknown grant":      map[string]any{"email": "x@t.test", "role": "staff", "grants": []string{"made.up"}},
		"admin with grants":  map[string]any{"email": "x@t.test", "role": "admin", "grants": []string{"certificates.issue"}},
	} {
		if code, out := post(body); code != 422 || out["error"] == nil {
			t.Errorf("%s: want 422, got %d %v", name, code, out)
		}
	}
	if code, _ := post(`{"email":"x@t.test","role":"staff","surprise":1}`); code != 400 {
		t.Errorf("unknown JSON field: %d", code)
	}
	if code, _ := post(map[string]any{"email": "dup@t.test", "role": "admin"}); code != 201 {
		t.Fatalf("first invite: %d", code)
	}
	if code, _ := post(map[string]any{"email": "DUP@t.test", "role": "staff"}); code != 409 {
		t.Errorf("duplicate pending: %d", code)
	}
	if code, _ := post(map[string]any{"email": "a-staff@t.test", "role": "staff"}); code != 409 {
		t.Errorf("already a member: %d", code)
	}
}

func TestMemberManagement(t *testing.T) {
	e := newEnv(t)
	A, B := e.f.A.ID, e.f.B.ID
	members := func(email, inst string) []any {
		code, out, _ := e.do("GET", "/api/v1/institute/members", email, inst, nil)
		if code != 200 {
			t.Fatalf("members: %d", code)
		}
		l, _ := out["members"].([]any)
		return l
	}
	idOf := func(l []any, email string) string {
		for _, m := range l {
			mm := m.(map[string]any)
			if mm["email"] == email {
				return mm["id"].(string)
			}
		}
		return ""
	}
	ml := members("a-owner@t.test", A)
	staffID, ownerID, adminID := idOf(ml, "a-staff@t.test"), idOf(ml, "a-owner@t.test"), idOf(ml, "a-admin@t.test")
	if staffID == "" || ownerID == "" || adminID == "" {
		t.Fatalf("members list must carry emails: %v", ml)
	}

	if code, _, _ := e.do("PATCH", "/api/v1/institute/members/"+staffID, "a-admin@t.test", A, map[string]any{"role": "admin"}); code != 403 {
		t.Errorf("admin changing a member: %d", code)
	}
	code, out, _ := e.do("PATCH", "/api/v1/institute/members/"+staffID, "a-owner@t.test", A, map[string]any{"role": "admin"})
	if code != 200 || str(out, "member", "role") != "admin" || str(out, "member", "email") != "a-staff@t.test" {
		t.Fatalf("promote: %d %v", code, out)
	}
	if code, inst, _ := e.do("GET", "/api/v1/institute", "a-staff@t.test", A, nil); code != 200 || !strings.Contains(mustJSON(inst), "certificates.issue") {
		t.Fatalf("promotion must take effect on the next request: %d %v", code, inst)
	}
	for name, tc := range map[string]struct {
		id   string
		body any
		want int
	}{
		"owner is protected": {ownerID, map[string]any{"role": "staff"}, 409},
		"unknown role":       {staffID, map[string]any{"role": "owner"}, 422},
		"owner-only grant":   {staffID, map[string]any{"role": "staff", "grants": []string{"staff.manage"}}, 422},
		"unknown member":     {"mem-does-not-exist", map[string]any{"role": "staff"}, 404},
	} {
		if code, _, _ := e.do("PATCH", "/api/v1/institute/members/"+tc.id, "a-owner@t.test", A, tc.body); code != tc.want {
			t.Errorf("%s: want %d got %d", name, tc.want, code)
		}
	}
	if code, _, _ := e.do("DELETE", "/api/v1/institute/members/"+ownerID, "a-owner@t.test", A, nil); code != 409 {
		t.Errorf("removing the owner: %d", code)
	}

	// institute B's owner cannot reach A's memberships, whichever institute header is used
	bMembers := members("b-owner@t.test", B)
	bStaff := idOf(bMembers, "b-staff@t.test")
	if code, _, _ := e.do("PATCH", "/api/v1/institute/members/"+staffID, "b-owner@t.test", B, map[string]any{"role": "staff"}); code != 404 {
		t.Errorf("B changing A's member: %d", code)
	}
	if code, _, _ := e.do("DELETE", "/api/v1/institute/members/"+staffID, "b-owner@t.test", B, nil); code != 404 {
		t.Errorf("B removing A's member: %d", code)
	}
	if code, _, _ := e.do("DELETE", "/api/v1/institute/members/"+bStaff, "a-owner@t.test", A, nil); code != 404 {
		t.Errorf("A removing B's member: %d", code)
	}
	if code, _, _ := e.do("PATCH", "/api/v1/institute/members/"+staffID, "b-owner@t.test", A, map[string]any{"role": "staff"}); code != 403 {
		t.Errorf("B's owner naming A: %d", code)
	}

	if code, _, _ := e.do("DELETE", "/api/v1/institute/members/"+staffID, "a-owner@t.test", A, nil); code != 200 {
		t.Fatalf("remove: %d", code)
	}
	if code, _, _ := e.do("GET", "/api/v1/institute", "a-staff@t.test", A, nil); code != 403 {
		t.Errorf("a removed member must lose access immediately: %d", code)
	}
	if code, _, _ := e.do("DELETE", "/api/v1/institute/members/"+staffID, "a-owner@t.test", A, nil); code != 404 {
		t.Errorf("removing twice: %d", code)
	}
	// removed members can be invited again and regain access
	e.do("POST", "/api/v1/institute/invitations", "a-owner@t.test", A, map[string]any{"email": "a-staff@t.test", "role": "staff"})
}

func TestInvitationsAreTenantScoped(t *testing.T) {
	e := newEnv(t)
	A, B := e.f.A.ID, e.f.B.ID
	_, out, _ := e.do("POST", "/api/v1/institute/invitations", "a-owner@t.test", A, map[string]any{"email": "x@t.test", "role": "staff"})
	id := str(out, "invitation", "id")
	if code, list, _ := e.do("GET", "/api/v1/institute/invitations", "b-owner@t.test", B, nil); code != 200 || strings.Contains(mustJSON(list), "x@t.test") {
		t.Errorf("B lists A's invitations: %d %v", code, list)
	}
	if code, _, _ := e.do("DELETE", "/api/v1/institute/invitations/"+id, "b-owner@t.test", B, nil); code != 404 {
		t.Errorf("B revoking A's invitation: %d", code)
	}
	if code, _, _ := e.do("DELETE", "/api/v1/institute/invitations/"+id, "b-owner@t.test", A, nil); code != 403 {
		t.Errorf("B's owner naming A: %d", code)
	}
	if code, _, _ := e.do("DELETE", "/api/v1/institute/invitations/"+id, "a-owner@t.test", A, nil); code != 200 {
		t.Errorf("A revoking its own: %d", code)
	}
}

func TestAcceptIsRateLimited(t *testing.T) {
	e := newEnv(t)
	e.register("guesser@t.test")
	var last int
	for i := 0; i < 12; i++ {
		last, _, _ = e.do("POST", "/api/v1/invitations/accept", "guesser@t.test", "", map[string]string{"token": "inv_guess" + string(rune('a'+i))})
		if i < 10 && last != 404 {
			t.Fatalf("attempt %d: %d", i, last)
		}
	}
	if last != 429 {
		t.Fatalf("token guessing must be rate limited, got %d", last)
	}
}
