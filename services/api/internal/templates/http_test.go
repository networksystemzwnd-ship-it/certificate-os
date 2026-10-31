package templates_test

import (
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
	"github.com/certificate-os/api/internal/templates"
	"github.com/certificate-os/api/internal/templates/templatestest"
	"github.com/certificate-os/api/internal/tenancy"
	"github.com/certificate-os/api/internal/tenancy/tenancytest"
)

type env struct {
	f *tenancytest.Fixture
	w *audittest.MemWriter
}

func newEnv(t *testing.T) *env {
	w := audittest.NewMemWriter()
	e := &env{w: w}
	e.f = tenancytest.NewFixture(t, func(f *tenancytest.Fixture) {
		h := &templates.Handler{Svc: templates.NewService(templatestest.NewMemStore(w)), Tenancy: f.TenantH,
			Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
		h.Register(f.Mux)
	})
	return e
}

func (e *env) call(method, path, email, inst string, body any) (int, map[string]any) {
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

func cfgBody(title string) map[string]any {
	c := templates.Presets()[0].Config
	c.Title = title
	b, _ := json.Marshal(c)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	return m
}

func TestPermissionMatrix(t *testing.T) {
	e := newEnv(t)
	A := e.f.A.ID
	// Admin creates; staff can read and preview but not write; issuer-grant staff is still read-only here.
	code, c := e.call("POST", "/api/v1/certificate-templates", "a-admin@t.test", A, map[string]any{"name": "Diploma", "config": cfgBody("Diploma title")})
	if code != 201 {
		t.Fatalf("admin create: %d %v", code, c)
	}
	id := str(c, "template", "id")

	for _, who := range []string{"a-staff@t.test", "a-issuer@t.test"} {
		for _, tc := range []struct {
			m, p string
			body any
			want int
		}{
			{"GET", "/api/v1/certificate-templates", nil, 200},
			{"GET", "/api/v1/certificate-templates/" + id, nil, 200},
			{"GET", "/api/v1/certificate-presets", nil, 200},
			{"GET", "/api/v1/certificate-templates/" + id + "/versions/1", nil, 200},
			{"POST", "/api/v1/certificate-templates/" + id + "/preview", map[string]any{}, 200},
			{"POST", "/api/v1/certificate-templates/preview", map[string]any{"config": cfgBody("A title")}, 200},
			{"POST", "/api/v1/certificate-templates", map[string]any{"name": "X1", "config": cfgBody("A title")}, 403},
			{"PATCH", "/api/v1/certificate-templates/" + id, map[string]any{"name": "Renamed"}, 403},
			{"DELETE", "/api/v1/certificate-templates/" + id, nil, 403},
			{"POST", "/api/v1/certificate-templates/" + id + "/restore", nil, 403},
			{"POST", "/api/v1/certificate-templates/" + id + "/versions", map[string]any{"config": cfgBody("A title")}, 403},
		} {
			if got, _ := e.call(tc.m, tc.p, who, A, tc.body); got != tc.want {
				t.Errorf("%s %s %s: want %d, got %d", who, tc.m, tc.p, tc.want, got)
			}
		}
	}
	// Anonymous and non-members.
	if got, _ := e.call("GET", "/api/v1/certificate-templates", "", A, nil); got != 401 {
		t.Errorf("anonymous: %d", got)
	}
	if got, _ := e.call("GET", "/api/v1/certificate-templates", "b-staff@t.test", A, nil); got != 403 {
		t.Errorf("other institute's staff: %d", got)
	}
	if got, _ := e.call("GET", "/api/v1/certificate-templates", "removed@t.test", A, nil); got != 403 {
		t.Errorf("removed member: %d", got)
	}
}

func TestSuspendedInstituteIsReadOnly(t *testing.T) {
	e := newEnv(t)
	C := e.f.C.ID
	if got, _ := e.call("POST", "/api/v1/certificate-templates", "c-owner@t.test", C, map[string]any{"name": "Nope", "config": cfgBody("A title")}); got != 403 {
		t.Errorf("create in suspended institute: %d", got)
	}
	if got, _ := e.call("GET", "/api/v1/certificate-templates", "c-owner@t.test", C, nil); got != 200 {
		t.Errorf("read in suspended institute: %d", got)
	}
	if got, _ := e.call("POST", "/api/v1/certificate-templates/preview", "c-owner@t.test", C, map[string]any{"config": cfgBody("A title")}); got != 200 {
		t.Errorf("preview in suspended institute: %d", got)
	}
	if got, _ := e.call("GET", "/api/v1/certificate-templates", "d-owner@t.test", e.f.D.ID, nil); got == 200 {
		t.Errorf("closed institute must not be readable")
	}
}

func TestLifecycleVersionsAndAudit(t *testing.T) {
	e := newEnv(t)
	A := e.f.A.ID
	code, c := e.call("POST", "/api/v1/certificate-templates", "a-owner@t.test", A, map[string]any{"name": "  Diploma ", "config": cfgBody("First title")})
	if code != 201 || str(c, "template", "name") != "Diploma" || str(c, "template", "current_version", "config", "title") != "First title" {
		t.Fatalf("create: %d %v", code, c)
	}
	id := str(c, "template", "id")

	// Duplicate name (case-insensitive) -> 409 with the field.
	code, d := e.call("POST", "/api/v1/certificate-templates", "a-admin@t.test", A, map[string]any{"name": "DIPLOMA", "config": cfgBody("A title")})
	if code != 409 || str(d, "error", "details", "name") == "" {
		t.Fatalf("duplicate: %d %v", code, d)
	}

	// New version; the old one stays readable and unchanged.
	code, v := e.call("POST", "/api/v1/certificate-templates/"+id+"/versions", "a-admin@t.test", A, map[string]any{"config": cfgBody("Second title")})
	if code != 201 || v["version"].(map[string]any)["number"].(float64) != 2 {
		t.Fatalf("add version: %d %v", code, v)
	}
	_, old := e.call("GET", "/api/v1/certificate-templates/"+id+"/versions/1", "a-staff@t.test", A, nil)
	if str(old, "version", "config", "title") != "First title" {
		t.Fatalf("version 1 changed: %v", old)
	}
	_, got := e.call("GET", "/api/v1/certificate-templates/"+id, "a-staff@t.test", A, nil)
	if str(got, "template", "current_version", "config", "title") != "Second title" {
		t.Fatalf("current: %v", got)
	}
	if vs, _ := got["template"].(map[string]any)["versions"].([]any); len(vs) != 2 {
		t.Fatalf("versions: %v", got)
	}
	for _, p := range []string{"/versions/0", "/versions/3", "/versions/abc", "/versions/101", "/versions/-1"} {
		if code, _ := e.call("GET", "/api/v1/certificate-templates/"+id+p, "a-staff@t.test", A, nil); code != 404 {
			t.Errorf("%s: want 404, got %d", p, code)
		}
	}

	// Preview a stored version and a draft; previews are HTML with sample data, never real data.
	_, pv := e.call("POST", "/api/v1/certificate-templates/"+id+"/preview", "a-staff@t.test", A, map[string]any{"version": 1})
	html := str(pv, "html")
	if !strings.Contains(html, "First title") || !strings.Contains(html, "Asha Verma") || strings.Contains(html, "<script") {
		t.Fatalf("preview v1: %s", html)
	}
	_, pc := e.call("POST", "/api/v1/certificate-templates/"+id+"/preview", "a-staff@t.test", A, map[string]any{})
	if !strings.Contains(str(pc, "html"), "Second title") {
		t.Fatal("default preview should use the current version")
	}
	_, pd := e.call("POST", "/api/v1/certificate-templates/"+id+"/preview", "a-staff@t.test", A, map[string]any{"config": cfgBody("Draft title")})
	if !strings.Contains(str(pd, "html"), "Draft title") {
		t.Fatal("draft preview")
	}
	// Draft preview with an invalid config is a 422 with field messages.
	bad := cfgBody("A title")
	bad["body"] = "{{student_name}} {{secret}}"
	bad["accent_color"] = "red"
	code, pe := e.call("POST", "/api/v1/certificate-templates/preview", "a-staff@t.test", A, map[string]any{"config": bad})
	if code != 422 || str(pe, "error", "details", "body") == "" || str(pe, "error", "details", "accent_color") == "" {
		t.Fatalf("invalid draft: %d %v", code, pe)
	}
	if code, _ := e.call("POST", "/api/v1/certificate-templates/"+id+"/preview", "a-staff@t.test", A, map[string]any{"version": 9}); code != 404 {
		t.Errorf("preview of a missing version: %d", code)
	}

	// Rename, archive (no new versions), restore.
	if code, r := e.call("PATCH", "/api/v1/certificate-templates/"+id, "a-admin@t.test", A, map[string]any{"name": "Diploma 2"}); code != 200 || str(r, "template", "name") != "Diploma 2" {
		t.Fatalf("rename: %d %v", code, r)
	}
	if code, _ := e.call("PATCH", "/api/v1/certificate-templates/"+id, "a-admin@t.test", A, map[string]any{"name": "x"}); code != 422 {
		t.Errorf("short name: %d", code)
	}
	if code, _ := e.call("DELETE", "/api/v1/certificate-templates/"+id, "a-admin@t.test", A, nil); code != 200 {
		t.Fatalf("archive: %d", code)
	}
	if code, _ := e.call("POST", "/api/v1/certificate-templates/"+id+"/versions", "a-admin@t.test", A, map[string]any{"config": cfgBody("A title")}); code != 409 {
		t.Errorf("version on archived: %d", code)
	}
	_, active := e.call("GET", "/api/v1/certificate-templates", "a-admin@t.test", A, nil)
	if ts, _ := active["templates"].([]any); len(ts) != 0 {
		t.Errorf("archived template listed: %v", active)
	}
	_, all := e.call("GET", "/api/v1/certificate-templates?archived=all", "a-admin@t.test", A, nil)
	if ts, _ := all["templates"].([]any); len(ts) != 1 {
		t.Errorf("archived=all: %v", all)
	}
	if code, _ := e.call("POST", "/api/v1/certificate-templates/"+id+"/restore", "a-admin@t.test", A, nil); code != 200 {
		t.Errorf("restore: %d", code)
	}
	if code, _ := e.call("GET", "/api/v1/certificate-templates?archived=bogus", "a-admin@t.test", A, nil); code != 422 {
		t.Errorf("bad archived filter: %d", code)
	}

	// Audit: names/ids/preset only, never template text or personal data.
	var actions []string
	for _, r := range e.w.ForEntity("certificate_template", id) {
		actions = append(actions, r.Action)
		b, _ := json.Marshal(r.Metadata)
		for _, secret := range []string{"First title", "Second title", "Asha", "student_name"} {
			if strings.Contains(string(b), secret) {
				t.Errorf("audit metadata of %s contains %q: %s", r.Action, secret, b)
			}
		}
	}
	want := "template.created,template.version_created,template.renamed,template.archived,template.restored"
	if strings.Join(actions, ",") != want {
		t.Errorf("audit actions = %v", actions)
	}
}

func TestCrossTenantIsolation(t *testing.T) {
	e := newEnv(t)
	A, B := e.f.A.ID, e.f.B.ID
	_, c := e.call("POST", "/api/v1/certificate-templates", "a-admin@t.test", A, map[string]any{"name": "Secret layout", "config": cfgBody("A title")})
	id := str(c, "template", "id")
	if id == "" {
		t.Fatal("setup failed")
	}
	for _, tc := range []struct {
		m, p string
		body any
	}{
		{"GET", "/api/v1/certificate-templates/" + id, nil},
		{"PATCH", "/api/v1/certificate-templates/" + id, map[string]any{"name": "Hacked"}},
		{"DELETE", "/api/v1/certificate-templates/" + id, nil},
		{"POST", "/api/v1/certificate-templates/" + id + "/restore", nil},
		{"POST", "/api/v1/certificate-templates/" + id + "/versions", map[string]any{"config": cfgBody("Hijack")}},
		{"GET", "/api/v1/certificate-templates/" + id + "/versions/1", nil},
		{"POST", "/api/v1/certificate-templates/" + id + "/preview", map[string]any{}},
		{"POST", "/api/v1/certificate-templates/" + id + "/preview", map[string]any{"config": cfgBody("A title")}},
	} {
		if code, _ := e.call(tc.m, tc.p, "b-admin@t.test", B, tc.body); code != 404 {
			t.Errorf("%s %s as B: want 404, got %d", tc.m, tc.p, code)
		}
	}
	if code, _ := e.call("GET", "/api/v1/certificate-templates", "a-admin@t.test", B, nil); code != 403 {
		t.Errorf("A admin with B header: %d", code)
	}
	_, l := e.call("GET", "/api/v1/certificate-templates?archived=all", "b-admin@t.test", B, nil)
	if ts, _ := l["templates"].([]any); len(ts) != 0 {
		t.Errorf("B sees A's templates: %v", l)
	}
	// The same name works in B.
	if code, _ := e.call("POST", "/api/v1/certificate-templates", "b-admin@t.test", B, map[string]any{"name": "Secret layout", "config": cfgBody("A title")}); code != 201 {
		t.Errorf("same name in B: %d", code)
	}
	// A's template is untouched.
	_, got := e.call("GET", "/api/v1/certificate-templates/"+id, "a-admin@t.test", A, nil)
	if str(got, "template", "name") != "Secret layout" || got["template"].(map[string]any)["archived_at"] != nil {
		t.Errorf("A's template changed: %v", got)
	}
}

func TestValidationAndMalformedInput(t *testing.T) {
	e := newEnv(t)
	A := e.f.A.ID
	bad := cfgBody("A title")
	bad["preset"] = "fancy"
	bad["title"] = "x"
	code, r := e.call("POST", "/api/v1/certificate-templates", "a-admin@t.test", A, map[string]any{"name": "a", "config": bad})
	if code != 422 {
		t.Fatalf("want 422, got %d %v", code, r)
	}
	for _, f := range []string{"name", "preset", "title"} {
		if str(r, "error", "details", f) == "" {
			t.Errorf("missing problem for %q: %v", f, r)
		}
	}
	// Unknown JSON fields and non-JSON are 400 (strict decoding).
	if code, _ := e.call("POST", "/api/v1/certificate-templates", "a-admin@t.test", A, map[string]any{"name": "Okay", "config": cfgBody("A title"), "institute_id": e.f.B.ID}); code != 400 {
		t.Errorf("unknown field: %d", code)
	}
	// A malformed template id is a plain 404.
	if code, _ := e.call("GET", "/api/v1/certificate-templates/not-a-uuid", "a-admin@t.test", A, nil); code != 404 {
		t.Errorf("malformed id: %d", code)
	}
}
