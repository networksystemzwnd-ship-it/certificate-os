package records_test

import (
	"context"
	"encoding/csv"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/certificate-os/api/internal/tenancy"
	"github.com/certificate-os/api/internal/tenancy/tenancytest"
)

func (e *env) raw(path, email, inst string) *httptest.ResponseRecorder {
	e.f.T.Helper()
	req := httptest.NewRequest("GET", path, nil)
	req.RemoteAddr = "203.0.113.9:1234"
	req.Header.Set(tenancy.InstituteHeader, inst)
	tok, _, err := e.f.Svc.Login(context.Background(), email, tenancytest.Password)
	if err != nil {
		e.f.T.Fatal(err)
	}
	req.AddCookie(&http.Cookie{Name: "cos_session", Value: tok})
	rr := httptest.NewRecorder()
	e.f.Handler.ServeHTTP(rr, req)
	return rr
}

func TestStudentExport(t *testing.T) {
	e := newEnv(t)
	A, B := e.f.A.ID, e.f.B.ID
	// 130 students (more than one page of 100), one with a formula-like name, one archived.
	for i := 0; i < 130; i++ {
		name := fmt.Sprintf("Student %03d", i)
		if i == 0 {
			name = "=HYPERLINK(\"http://evil\")"
		}
		code, b := e.call("POST", "/api/v1/students", "a-admin@t.test", A, map[string]string{"full_name": name, "email": fmt.Sprintf("s%d@x.com", i)})
		if code != 201 {
			t.Fatalf("create: %d %v", code, b)
		}
		if i == 1 {
			if c, _ := e.call("DELETE", "/api/v1/students/"+id(b, "student"), "a-admin@t.test", A, nil); c != 200 && c != 204 {
				t.Fatalf("archive: %d", c)
			}
		}
	}
	e.call("POST", "/api/v1/students", "b-admin@t.test", B, map[string]string{"full_name": "Other Tenant", "email": "o@x.com"})

	// Staff do not have students.export; admin does.
	if rr := e.raw("/api/v1/students/export", "a-staff@t.test", A); rr.Code != 403 {
		t.Errorf("staff: want 403, got %d", rr.Code)
	}
	rr := e.raw("/api/v1/students/export?archived=all", "a-admin@t.test", A)
	if rr.Code != 200 {
		t.Fatalf("admin: %d %s", rr.Code, rr.Body.String())
	}
	if ct := rr.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/csv") {
		t.Errorf("content type %q", ct)
	}
	if rr.Header().Get("Cache-Control") != "private, no-store" || !strings.Contains(rr.Header().Get("Content-Disposition"), "attachment") {
		t.Errorf("headers: %v", rr.Header())
	}
	rows, err := csv.NewReader(rr.Body).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 131 { // header + 130 (all pages, archived included)
		t.Fatalf("want 131 rows, got %d", len(rows))
	}
	body := fmt.Sprint(rows)
	if strings.Contains(body, "Other Tenant") {
		t.Error("another institute's student leaked into the export")
	}
	formula := false
	for _, r := range rows[1:] {
		if strings.HasPrefix(r[1], "=") {
			t.Errorf("formula not neutralised: %q", r[1])
		}
		if strings.HasPrefix(r[1], "'=HYPERLINK") {
			formula = true
		}
	}
	if !formula {
		t.Error("formula-like name missing")
	}

	// The default export hides archived students.
	rr = e.raw("/api/v1/students/export", "a-admin@t.test", A)
	if rows, _ := csv.NewReader(rr.Body).ReadAll(); len(rows) != 130 {
		t.Errorf("default export: want 130 rows, got %d", len(rows))
	}

	// Bad filters are rejected before anything is written.
	if rr := e.raw("/api/v1/students/export?status=bogus", "a-admin@t.test", A); rr.Code != 422 {
		t.Errorf("bad status: %d", rr.Code)
	}

	// Audited with a count only.
	n := 0
	for _, r := range e.w.Records() {
		if r.Action == "student.exported" {
			n++
			if strings.Contains(fmt.Sprint(r.Metadata), "@") || strings.Contains(fmt.Sprint(r.Metadata), "Student") {
				t.Errorf("audit metadata holds personal data: %v", r.Metadata)
			}
		}
	}
	if n != 2 {
		t.Errorf("want 2 audit events (two successful exports), got %d", n)
	}
}

// Exports are blocked while an institute is suspended (D-114).
func TestStudentExportBlockedWhileSuspended(t *testing.T) {
	e := newEnv(t)
	if rr := e.raw("/api/v1/students/export", "c-owner@t.test", e.f.C.ID); rr.Code != 403 {
		t.Errorf("suspended export: want 403, got %d", rr.Code)
	}
}

func TestStudentExportHasOneRowPerEnrollment(t *testing.T) {
	e := newEnv(t)
	A, u := e.f.A.ID, "a-admin@t.test"
	_, c := e.call("POST", "/api/v1/courses", u, A, map[string]string{"name": "Welding"})
	course := id(c, "course")
	_, b1 := e.call("POST", "/api/v1/batches", u, A, map[string]string{"course_id": course, "name": "Jan"})
	_, b2 := e.call("POST", "/api/v1/batches", u, A, map[string]string{"course_id": course, "name": "Feb"})
	_, s1 := e.call("POST", "/api/v1/students", u, A, map[string]string{"full_name": "Asha Rao", "email": "asha@x.com"})
	_, s2 := e.call("POST", "/api/v1/students", u, A, map[string]string{"full_name": "Zed Alone", "email": "z@x.com"})
	st1 := id(s1, "student")
	_, en1 := e.call("POST", "/api/v1/students/"+st1+"/enrollments", u, A, map[string]string{"batch_id": id(b1, "batch")})
	e.call("POST", "/api/v1/students/"+st1+"/enrollments", u, A, map[string]string{"batch_id": id(b2, "batch")})
	if code, body := e.call("PATCH", "/api/v1/students/"+st1+"/enrollments/"+id(en1, "enrollment"), u, A,
		map[string]string{"status": "completed", "completion_date": "2026-03-01", "grade": "=A+"}); code != 200 {
		t.Fatalf("complete: %d %v", code, body)
	}
	_ = s2

	rr := e.raw("/api/v1/students/export", u, A)
	rows, err := csv.NewReader(rr.Body).ReadAll()
	if err != nil || rr.Code != 200 {
		t.Fatalf("%d %v", rr.Code, err)
	}
	if len(rows) != 4 { // header + Asha x2 + Zed x1
		t.Fatalf("want 4 rows, got %d: %v", len(rows), rows)
	}
	if got := strings.Join(rows[0][6:], ","); got != "course,batch,enrollment_status,completion_date,grade" {
		t.Fatalf("header: %s", got)
	}
	var completed, plain, alone int
	for _, r := range rows[1:] {
		switch {
		case r[1] == "Zed Alone":
			alone++
			if r[6] != "" || r[8] != "" {
				t.Errorf("student without enrollment must have blank enrollment columns: %v", r)
			}
		case r[8] == "completed":
			completed++
			if r[6] != "Welding" || r[7] != "Jan" || r[9] != "2026-03-01" || r[10] != "'=A+" {
				t.Errorf("completed row: %v", r)
			}
		case r[8] == "enrolled":
			plain++
			if r[7] != "Feb" {
				t.Errorf("enrolled row: %v", r)
			}
		}
	}
	if completed != 1 || plain != 1 || alone != 1 {
		t.Errorf("rows: completed=%d enrolled=%d alone=%d", completed, plain, alone)
	}
	for _, r := range e.w.Records() {
		if r.Action == "student.exported" && fmt.Sprint(r.Metadata["enrollments"]) != "2" {
			t.Errorf("audit must count enrollments: %v", r.Metadata)
		}
	}
}
