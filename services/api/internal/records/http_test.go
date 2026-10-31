package records_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/certificate-os/api/internal/audit/audittest"
	"github.com/certificate-os/api/internal/platform/httpx"
	"github.com/certificate-os/api/internal/records"
	"github.com/certificate-os/api/internal/records/recordstest"
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
		svc := records.NewService(recordstest.NewMemStore(w))
		svc.Audit = w
		h := &records.Handler{Svc: svc, Tenancy: f.TenantH, Log: slogDiscard()}
		h.Register(f.Mux)
	})
	return e
}

// call performs a JSON request as a fixture user.
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

func id(m map[string]any, key string) string {
	o, _ := m[key].(map[string]any)
	s, _ := o["id"].(string)
	return s
}

func TestCrossTenantIsolation(t *testing.T) {
	e := newEnv(t)
	A, B := e.f.A.ID, e.f.B.ID

	code, c := e.call("POST", "/api/v1/courses", "a-staff@t.test", A, map[string]string{"name": "Welding", "code": "WLD"})
	if code != 201 {
		t.Fatalf("create course: %d %v", code, c)
	}
	courseA := id(c, "course")
	_, bt := e.call("POST", "/api/v1/batches", "a-staff@t.test", A, map[string]string{"course_id": courseA, "name": "Jan"})
	batchA := id(bt, "batch")
	_, st := e.call("POST", "/api/v1/students", "a-staff@t.test", A, map[string]string{"full_name": "Asha Rao", "email": "asha@x.com"})
	studentA := id(st, "student")
	if courseA == "" || batchA == "" || studentA == "" {
		t.Fatalf("setup failed: %v %v %v", c, bt, st)
	}

	// B's staff guesses A's ids: always 404, never 403 or data, for every verb.
	for _, tc := range []struct{ m, p string }{
		{"GET", "/api/v1/courses/" + courseA}, {"PATCH", "/api/v1/courses/" + courseA},
		{"DELETE", "/api/v1/courses/" + courseA}, {"POST", "/api/v1/courses/" + courseA + "/restore"},
		{"GET", "/api/v1/batches/" + batchA}, {"PATCH", "/api/v1/batches/" + batchA}, {"DELETE", "/api/v1/batches/" + batchA},
		{"GET", "/api/v1/students/" + studentA}, {"PATCH", "/api/v1/students/" + studentA}, {"DELETE", "/api/v1/students/" + studentA},
		{"PATCH", "/api/v1/students/" + studentA + "/enrollments/x"},
	} {
		var body any
		if tc.m == "PATCH" {
			switch {
			case strings.Contains(tc.p, "/enrollments/"):
				body = map[string]string{"grade": "A"}
			case strings.Contains(tc.p, "/students/"):
				body = map[string]string{"full_name": "Hacked"}
			default:
				body = map[string]string{"name": "Hacked"}
			}
		}
		if code, _ := e.call(tc.m, tc.p, "b-staff@t.test", B, body); code != 404 {
			t.Errorf("%s %s as B: want 404, got %d", tc.m, tc.p, code)
		}
	}

	// Enrolling A's student in B's batch (or vice versa) is impossible.
	_, bc := e.call("POST", "/api/v1/courses", "b-staff@t.test", B, map[string]string{"name": "Plumbing"})
	_, bb := e.call("POST", "/api/v1/batches", "b-staff@t.test", B, map[string]string{"course_id": id(bc, "course"), "name": "Feb"})
	if code, _ := e.call("POST", "/api/v1/students/"+studentA+"/enrollments", "a-staff@t.test", A, map[string]string{"batch_id": id(bb, "batch")}); code != 422 {
		t.Errorf("enroll in another tenant's batch: want 422, got %d", code)
	}
	if code, _ := e.call("POST", "/api/v1/batches", "a-staff@t.test", A, map[string]string{"course_id": id(bc, "course"), "name": "X"}); code != 422 {
		t.Errorf("batch under another tenant's course: want 422, got %d", code)
	}

	// Lists never leak, even with a forged institute header (user is not a member of B... except multi).
	if code, _ := e.call("GET", "/api/v1/courses", "a-staff@t.test", B, nil); code != 403 {
		t.Errorf("A staff with B header: want 403, got %d", code)
	}
	_, list := e.call("GET", "/api/v1/courses", "b-staff@t.test", B, nil)
	if cs, _ := list["courses"].([]any); len(cs) != 1 {
		t.Errorf("B should see exactly its own course, got %v", list)
	}
	// A multi-institute user sees only the selected institute's data.
	_, ml := e.call("GET", "/api/v1/students", "multi@t.test", A, nil)
	if ss, _ := ml["students"].([]any); len(ss) != 1 {
		t.Errorf("multi in A: %v", ml)
	}
	_, ml = e.call("GET", "/api/v1/students", "multi@t.test", B, nil)
	if ss, _ := ml["students"].([]any); len(ss) != 0 {
		t.Errorf("multi in B leaked: %v", ml)
	}
}

func TestPermissionsAndAuth(t *testing.T) {
	e := newEnv(t)
	A := e.f.A.ID
	if code, _ := e.call("GET", "/api/v1/courses", "", "", nil); code != 401 {
		t.Errorf("anonymous: %d", code)
	}
	if code, _ := e.call("GET", "/api/v1/students", "nobody@t.test", A, nil); code != 403 {
		t.Errorf("non-member: %d", code)
	}
	if code, _ := e.call("GET", "/api/v1/students", "removed@t.test", A, nil); code != 403 {
		t.Errorf("removed member: %d", code)
	}
	if code, _ := e.call("GET", "/api/v1/students", "ops@t.test", A, nil); code != 403 {
		t.Errorf("platform ops must not read tenant records: %d", code)
	}
	for _, u := range []string{"a-staff@t.test", "a-admin@t.test", "a-owner@t.test"} {
		if code, b := e.call("POST", "/api/v1/students", u, A, map[string]string{"full_name": "S " + u}); code != 201 {
			t.Errorf("%s create: %d %v", u, code, b)
		}
	}
}

func TestSuspendedIsReadOnlyAndClosedHasNoAccess(t *testing.T) {
	e := newEnv(t)
	C, D := e.f.C.ID, e.f.D.ID
	if code, _ := e.call("GET", "/api/v1/courses", "c-owner@t.test", C, nil); code != 200 {
		t.Errorf("suspended read: %d", code)
	}
	for _, p := range []struct{ path string }{{"/api/v1/courses"}, {"/api/v1/students"}} {
		if code, _ := e.call("POST", p.path, "c-owner@t.test", C, map[string]string{"name": "X", "full_name": "X"}); code != 403 {
			t.Errorf("suspended write %s: %d", p.path, code)
		}
	}
	if code, _ := e.call("GET", "/api/v1/courses", "d-owner@t.test", D, nil); code == 200 {
		t.Errorf("closed institute must have no member access")
	}
}

func TestValidationConflictAndLifecycle(t *testing.T) {
	e := newEnv(t)
	A := e.f.A.ID
	u := "a-staff@t.test"

	if code, b := e.call("POST", "/api/v1/courses", u, A, map[string]string{"name": "x"}); code != 422 {
		t.Fatalf("short name: %d %v", code, b)
	}
	if code, _ := e.call("POST", "/api/v1/courses", u, A, map[string]any{"name": "Welding", "bogus": 1}); code != 400 {
		t.Errorf("unknown field should be 400, got %d", code)
	}
	_, c := e.call("POST", "/api/v1/courses", u, A, map[string]string{"name": "Welding", "code": "WLD"})
	course := id(c, "course")
	if code, b := e.call("POST", "/api/v1/courses", u, A, map[string]string{"name": "welding"}); code != 409 {
		t.Errorf("duplicate name: %d %v", code, b)
	}

	_, bt := e.call("POST", "/api/v1/batches", u, A, map[string]string{"course_id": course, "name": "Jan", "start_date": "2026-01-01", "end_date": "2026-03-01"})
	batch := id(bt, "batch")
	if code, _ := e.call("POST", "/api/v1/batches", u, A, map[string]string{"course_id": course, "name": "Bad", "start_date": "2026-03-01", "end_date": "2026-01-01"}); code != 422 {
		t.Errorf("end before start: %d", code)
	}

	_, st := e.call("POST", "/api/v1/students", u, A, map[string]string{"full_name": "Asha Rao", "student_number": "S-1"})
	student := id(st, "student")
	if code, _ := e.call("POST", "/api/v1/students", u, A, map[string]string{"full_name": "Dup", "student_number": "S-1"}); code != 409 {
		t.Errorf("duplicate student number: %d", code)
	}

	code, en := e.call("POST", "/api/v1/students/"+student+"/enrollments", u, A, map[string]string{"batch_id": batch})
	if code != 201 {
		t.Fatalf("enroll: %d %v", code, en)
	}
	eid := id(en, "enrollment")
	if code, _ := e.call("POST", "/api/v1/students/"+student+"/enrollments", u, A, map[string]string{"batch_id": batch}); code != 409 {
		t.Errorf("double enrollment: %d", code)
	}
	if code, _ := e.call("PATCH", "/api/v1/students/"+student+"/enrollments/"+eid, u, A, map[string]string{"status": "completed"}); code != 422 {
		t.Errorf("completed without date: %d", code)
	}
	if code, b := e.call("PATCH", "/api/v1/students/"+student+"/enrollments/"+eid, u, A,
		map[string]string{"status": "completed", "completion_date": "2026-03-01", "grade": "A"}); code != 200 {
		t.Errorf("complete: %d %v", code, b)
	}

	// Filter by status and course; pagination reports has_more.
	_, l := e.call("GET", "/api/v1/students?status=completed&course_id="+course, u, A, nil)
	if ss, _ := l["students"].([]any); len(ss) != 1 {
		t.Errorf("filter: %v", l)
	}
	if code, _ := e.call("GET", "/api/v1/students?status=nope", u, A, nil); code != 422 {
		t.Errorf("bad status filter: %d", code)
	}
	if code, _ := e.call("GET", "/api/v1/students?limit=0", u, A, nil); code != 422 {
		t.Errorf("bad limit: %d", code)
	}
	for i := 0; i < 3; i++ {
		e.call("POST", "/api/v1/students", u, A, map[string]string{"full_name": fmt.Sprintf("Extra %d", i)})
	}
	_, l = e.call("GET", "/api/v1/students?limit=2", u, A, nil)
	if ss, _ := l["students"].([]any); len(ss) != 2 || l["has_more"] != true {
		t.Errorf("pagination: %v", l)
	}

	// Archive/restore round-trip; archived students leave the default list but stay readable.
	if code, _ := e.call("DELETE", "/api/v1/students/"+student, u, A, nil); code != 200 {
		t.Errorf("archive student: %d", code)
	}
	if code, _ := e.call("GET", "/api/v1/students/"+student, u, A, nil); code != 200 {
		t.Errorf("archived student still readable: %d", code)
	}
	_, l = e.call("GET", "/api/v1/students?archived=only", u, A, nil)
	if ss, _ := l["students"].([]any); len(ss) != 1 {
		t.Errorf("archived=only: %v", l)
	}
	if code, _ := e.call("POST", "/api/v1/students/"+student+"/restore", u, A, nil); code != 200 {
		t.Errorf("restore: %d", code)
	}
}

// Audit metadata carries ids and field NAMES only: never names, emails, phones or grades.
func TestAuditMetadataHoldsNoPII(t *testing.T) {
	e := newEnv(t)
	A, u := e.f.A.ID, "a-staff@t.test"
	secrets := []string{"Zelda Quill", "zelda@secret.test", "5550001234", "TOPGRADE"}
	_, c := e.call("POST", "/api/v1/courses", u, A, map[string]string{"name": "Secret Course", "description": "classified text"})
	_, bt := e.call("POST", "/api/v1/batches", u, A, map[string]string{"course_id": id(c, "course"), "name": "Hidden Batch"})
	_, st := e.call("POST", "/api/v1/students", u, A, map[string]string{"full_name": secrets[0], "email": secrets[1], "phone": secrets[2]})
	s := id(st, "student")
	e.call("PATCH", "/api/v1/students/"+s, u, A, map[string]string{"full_name": "Zelda Quill", "email": "zelda2@secret.test", "phone": secrets[2]})
	_, en := e.call("POST", "/api/v1/students/"+s+"/enrollments", u, A, map[string]string{"batch_id": id(bt, "batch")})
	e.call("PATCH", "/api/v1/students/"+s+"/enrollments/"+id(en, "enrollment"), u, A,
		map[string]string{"status": "completed", "completion_date": "2026-02-02", "grade": secrets[3]})
	e.call("DELETE", "/api/v1/students/"+s, u, A, nil)

	recs := e.w.Records()
	if len(recs) < 6 {
		t.Fatalf("expected audit records, got %d", len(recs))
	}
	for _, r := range recs {
		b, _ := json.Marshal(r)
		for _, bad := range append(secrets, "zelda2@secret.test", "Secret Course", "Hidden Batch", "classified text") {
			if strings.Contains(string(b), bad) {
				t.Errorf("audit record %s leaks %q: %s", r.Action, bad, b)
			}
		}
	}
}

func slogDiscard() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }
