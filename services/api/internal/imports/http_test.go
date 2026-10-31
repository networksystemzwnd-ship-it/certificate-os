package imports_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/certificate-os/api/internal/audit/audittest"
	"github.com/certificate-os/api/internal/imports"
	"github.com/certificate-os/api/internal/imports/importstest"
	"github.com/certificate-os/api/internal/platform/httpx"
	"github.com/certificate-os/api/internal/records"
	"github.com/certificate-os/api/internal/records/recordstest"
	"github.com/certificate-os/api/internal/tenancy"
	"github.com/certificate-os/api/internal/tenancy/tenancytest"
)

type httpEnv struct {
	f   *tenancytest.Fixture
	rec *records.Service
}

func newHTTPEnv(t *testing.T) *httpEnv {
	w := audittest.NewMemWriter()
	e := &httpEnv{}
	e.f = tenancytest.NewFixture(t, func(f *tenancytest.Fixture) {
		log := slog.New(slog.NewTextHandler(io.Discard, nil))
		e.rec = records.NewService(recordstest.NewMemStore(w))
		(&records.Handler{Svc: e.rec, Tenancy: f.TenantH, Log: log}).Register(f.Mux)
		(&imports.Handler{Svc: imports.NewService(importstest.NewMemStore(w), e.rec), Tenancy: f.TenantH, Log: log}).Register(f.Mux)
	})
	return e
}

func (e *httpEnv) do(method, path, email, inst, ctype string, body []byte) (*httptest.ResponseRecorder, map[string]any) {
	e.f.T.Helper()
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	req.RemoteAddr = "203.0.113.9:1234"
	if method != http.MethodGet {
		req.Header.Set(httpx.CSRFHeader, "1")
	}
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
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
	return rr, out
}

func multipartFile(field, name string, data []byte) ([]byte, string) {
	var b bytes.Buffer
	mw := multipart.NewWriter(&b)
	if field != "" {
		fw, _ := mw.CreateFormFile(field, name)
		_, _ = fw.Write(data)
	}
	_ = mw.Close()
	return b.Bytes(), mw.FormDataContentType()
}

func (e *httpEnv) upload(email, inst string, data []byte) (*httptest.ResponseRecorder, map[string]any) {
	body, ct := multipartFile("file", "students.csv", data)
	return e.do("POST", "/api/v1/students/imports", email, inst, ct, body)
}

func importID(m map[string]any) string {
	o, _ := m["import"].(map[string]any)
	s, _ := o["id"].(string)
	return s
}

func TestImportPermissions(t *testing.T) {
	e := newHTTPEnv(t)
	A := e.f.A.ID
	csv := []byte("Name\nAsha Rao\n")
	body, ct := multipartFile("file", "s.csv", csv)

	if rr, _ := e.do("POST", "/api/v1/students/imports", "", "", ct, body); rr.Code != 401 {
		t.Errorf("anonymous: %d", rr.Code)
	}
	for _, tc := range []struct {
		email, inst string
		want        int
	}{
		{"a-staff@t.test", A, 403},        // staff do not hold students.import by default
		{"nobody@t.test", A, 403},         // not a member
		{"ops@t.test", A, 403},            // platform ops have no student access
		{"removed@t.test", A, 403},        // removed member
		{"c-owner@t.test", e.f.C.ID, 403}, // suspended institute is read-only
		{"d-owner@t.test", e.f.D.ID, 403}, // closed institute
		{"a-admin@t.test", A, 201},
		{"a-owner@t.test", A, 201},
	} {
		if rr, _ := e.do("POST", "/api/v1/students/imports", tc.email, tc.inst, ct, body); rr.Code != tc.want {
			t.Errorf("%s: want %d, got %d (%s)", tc.email, tc.want, rr.Code, rr.Body.String())
		}
	}
}

func TestImportWizardOverHTTP(t *testing.T) {
	e := newHTTPEnv(t)
	A := e.f.A.ID
	ctx := context.Background()
	c, err := e.rec.CreateCourse(ctx, tenancy.Tenant{Institute: e.f.A, UserID: e.f.UserID("a-admin@t.test")}, records.CourseInput{Name: "Welding"}, "r")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.rec.CreateBatch(ctx, tenancy.Tenant{Institute: e.f.A, UserID: e.f.UserID("a-admin@t.test")}, records.BatchInput{CourseID: c.ID, Name: "Morning"}, "r"); err != nil {
		t.Fatal(err)
	}

	rr, out := e.upload("a-admin@t.test", A, []byte("Student Name,Roll No,Email,Course,Batch,Status,Completed On\n"+
		"Asha Rao,ST-1,asha@example.com,Welding,Morning,completed,2026-03-15\n"+
		"=cmd|' /C calc'!A0,ST-2,,,,,\n"+ // formula-like name: stored as text, neutralized in the report
		"Bad Mail,ST-3,nope,,,,\n"+
		"=1+1 Evil,ST-4,nope,,,,\n")) // invalid AND formula-like: lands in the error report
	if rr.Code != 201 {
		t.Fatalf("upload: %d %s", rr.Code, rr.Body.String())
	}
	id := importID(out)
	imp := out["import"].(map[string]any)
	sug := imp["suggested_mapping"].(map[string]any)
	if sug["full_name"].(float64) != 0 || sug["completion_date"].(float64) != 6 || len(imp["preview"].([]any)) != 4 {
		t.Fatalf("upload response: %v", imp)
	}

	// mapping body with an unknown JSON key is rejected outright
	if rr, _ := e.do("PUT", "/api/v1/students/imports/"+id+"/mapping", "a-admin@t.test", A, "application/json", []byte(`{"mapping":{"full_name":0},"surprise":1}`)); rr.Code != 400 {
		t.Errorf("unknown key: %d", rr.Code)
	}
	if rr, out := e.do("PUT", "/api/v1/students/imports/"+id+"/mapping", "a-admin@t.test", A, "application/json", []byte(`{"mapping":{"email":2}}`)); rr.Code != 422 || out["error"].(map[string]any)["details"] == nil {
		t.Errorf("missing full_name: %d %v", rr.Code, out)
	}
	rr, out = e.do("PUT", "/api/v1/students/imports/"+id+"/mapping", "a-admin@t.test", A, "application/json", []byte(`{"mapping":{"full_name":0,"student_number":1,"email":2,"course":3,"batch":4,"status":5,"completion_date":6},"date_format":"iso"}`))
	if rr.Code != 200 {
		t.Fatalf("mapping: %d %s", rr.Code, rr.Body.String())
	}
	counts := out["import"].(map[string]any)["counts"].(map[string]any)
	if counts["valid"].(float64) != 2 || counts["errors"].(float64) != 2 {
		t.Fatalf("counts: %v", counts)
	}
	if rr, _ := e.do("GET", "/api/v1/students", "a-admin@t.test", A, "", nil); !strings.Contains(rr.Body.String(), `"students":[]`) {
		t.Fatalf("nothing may be imported before confirm: %s", rr.Body.String())
	}

	rr, out = e.do("POST", "/api/v1/students/imports/"+id+"/confirm", "a-admin@t.test", A, "", nil)
	if rr.Code != 200 || out["import"].(map[string]any)["status"] != "completed" {
		t.Fatalf("confirm: %d %s", rr.Code, rr.Body.String())
	}
	if rr, _ := e.do("POST", "/api/v1/students/imports/"+id+"/confirm", "a-admin@t.test", A, "", nil); rr.Code != 409 {
		t.Errorf("second confirm: %d", rr.Code)
	}
	if rr, _ := e.do("GET", "/api/v1/students?q=Asha", "a-staff@t.test", A, "", nil); !strings.Contains(rr.Body.String(), "Asha Rao") {
		t.Errorf("imported student not visible to staff: %s", rr.Body.String())
	}

	rr, _ = e.do("GET", "/api/v1/students/imports/"+id+"/errors", "a-admin@t.test", A, "", nil)
	if rr.Code != 200 || !strings.HasPrefix(rr.Header().Get("Content-Type"), "text/csv") ||
		!strings.Contains(rr.Header().Get("Content-Disposition"), "attachment") || rr.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("error report headers: %d %v", rr.Code, rr.Header())
	}
	rep := rr.Body.String()
	if !strings.Contains(rep, "Bad Mail") || !strings.Contains(rep, "email:") || strings.Contains(rep, "Asha Rao") {
		t.Errorf("report content: %q", rep)
	}
	if !strings.Contains(rep, "'=1+1 Evil") || strings.Contains(rep, ",=1+1") || strings.Contains(rep, "\n=1+1") {
		t.Errorf("formula-like cells must be neutralized in the report: %q", rep)
	}
	// the formula-like name was valid, so it is imported as plain text; verify it is stored verbatim
	if rr, _ := e.do("GET", "/api/v1/students?q=cmd", "a-admin@t.test", A, "", nil); !strings.Contains(rr.Body.String(), "=cmd") {
		t.Errorf("formula-like name must be stored as text: %s", rr.Body.String())
	}
}

func TestImportUploadAcceptsXLSX(t *testing.T) {
	e := newHTTPEnv(t)
	data := build(t, xl{shared: `<si><t>Name</t></si><si><t>Email</t></si><si><t>Asha Rao</t></si><si><t>asha@x.com</t></si>`,
		sheet: `<row r="1"><c r="A1" t="s"><v>0</v></c><c r="B1" t="s"><v>1</v></c></row><row r="2"><c r="A2" t="s"><v>2</v></c><c r="B2" t="s"><v>3</v></c></row>`})
	body, ct := multipartFile("file", "students.xlsx", data)
	rr, out := e.do("POST", "/api/v1/students/imports", "a-admin@t.test", e.f.A.ID, ct, body)
	if rr.Code != 201 {
		t.Fatalf("xlsx upload: %d %s", rr.Code, rr.Body.String())
	}
	imp, _ := out["import"].(map[string]any)
	if imp["filename"] != "students.xlsx" || imp["row_count"] != nil && imp["row_count"] != float64(1) {
		t.Errorf("unexpected import %v", imp)
	}
	if hs, _ := imp["headers"].([]any); len(hs) != 2 {
		t.Errorf("headers %v", imp["headers"])
	}
}

func TestImportUploadRejections(t *testing.T) {
	e := newHTTPEnv(t)
	A := e.f.A.ID
	cases := map[string]struct {
		field string
		data  []byte
		want  int
	}{
		"broken xlsx": {"file", []byte("PK\x03\x04..."), 422},
		"old xls":     {"file", []byte("\xd0\xcf\x11\xe0..."), 422},
		"not csv/utf": {"file", []byte("Name\nJos\xe9\n"), 422},
		"header only": {"file", []byte("Name\n"), 422},
		"too large":   {"file", bytes.Repeat([]byte("x"), imports.MaxBytes+1000), 422},
		"no file":     {"", nil, 400},
		"wrong field": {"upload", []byte("Name\nA B\n"), 400},
	}
	for name, c := range cases {
		body, ct := multipartFile(c.field, "f.csv", c.data)
		if rr, _ := e.do("POST", "/api/v1/students/imports", "a-admin@t.test", A, ct, body); rr.Code != c.want {
			t.Errorf("%s: want %d got %d (%s)", name, c.want, rr.Code, rr.Body.String())
		}
	}
	if rr, _ := e.do("POST", "/api/v1/students/imports", "a-admin@t.test", A, "application/json", []byte(`{}`)); rr.Code != 400 {
		t.Errorf("json body: %d", rr.Code)
	}
	huge := bytes.Repeat([]byte("a"), imports.MaxBytes+(512<<10))
	body, ct := multipartFile("file", "f.csv", huge)
	if rr, _ := e.do("POST", "/api/v1/students/imports", "a-admin@t.test", A, ct, body); rr.Code != 413 {
		t.Errorf("hard size cap: %d", rr.Code)
	}
}

func TestImportCrossTenantIsolation(t *testing.T) {
	e := newHTTPEnv(t)
	A, B := e.f.A.ID, e.f.B.ID
	_, out := e.upload("a-admin@t.test", A, []byte("Name\nAsha Rao\nRavi Kumar\n"))
	id := importID(out)
	if id == "" {
		t.Fatal("upload failed")
	}
	for _, tc := range []struct {
		m, p, ct string
		body     string
	}{
		{"GET", "/api/v1/students/imports/" + id, "", ""},
		{"PUT", "/api/v1/students/imports/" + id + "/mapping", "application/json", `{"mapping":{"full_name":0}}`},
		{"GET", "/api/v1/students/imports/" + id + "/errors", "", ""},
		{"POST", "/api/v1/students/imports/" + id + "/confirm", "", ""},
		{"DELETE", "/api/v1/students/imports/" + id, "", ""},
	} {
		// b-admin names its own institute and guesses A's import id: always 404
		if rr, _ := e.do(tc.m, tc.p, "b-admin@t.test", B, tc.ct, []byte(tc.body)); rr.Code != 404 {
			t.Errorf("%s %s as B: want 404, got %d", tc.m, tc.p, rr.Code)
		}
		// naming A's institute without membership is the identical 403 used everywhere
		if rr, _ := e.do(tc.m, tc.p, "b-admin@t.test", A, tc.ct, []byte(tc.body)); rr.Code != 403 {
			t.Errorf("%s %s naming A as B's admin: want 403, got %d", tc.m, tc.p, rr.Code)
		}
	}
	if rr, out := e.do("GET", "/api/v1/students/imports/"+id, "a-admin@t.test", A, "", nil); rr.Code != 200 || out["import"].(map[string]any)["status"] != "uploaded" {
		t.Fatalf("A's import must be untouched: %d %v", rr.Code, out)
	}
	// The multi-institute user picks the institute explicitly; the job only resolves under A.
	if rr, _ := e.do("GET", "/api/v1/students/imports/"+id, "multi@t.test", B, "", nil); rr.Code != 404 {
		t.Errorf("multi as B admin: %d", rr.Code)
	}
}
