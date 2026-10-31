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
