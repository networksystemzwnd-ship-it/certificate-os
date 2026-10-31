package dashboard_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/certificate-os/api/internal/dashboard"
	"github.com/certificate-os/api/internal/platform/httpx"
	"github.com/certificate-os/api/internal/tenancy"
	"github.com/certificate-os/api/internal/tenancy/tenancytest"
)

// fakeStore returns canned figures per institute and records what it was asked.
type fakeStore struct {
	byInstitute map[string]dashboard.InstituteStats
	scopes      []string
	since       time.Time
	platformErr error
}

func (f *fakeStore) Institute(_ context.Context, s tenancy.Scope, since time.Time) (dashboard.InstituteStats, error) {
	f.scopes = append(f.scopes, s.InstituteID())
	f.since = since
	return f.byInstitute[s.InstituteID()], nil
}

func (f *fakeStore) Platform(_ context.Context, since time.Time) (dashboard.PlatformStats, error) {
	f.since = since
	if f.platformErr != nil {
		return dashboard.PlatformStats{}, f.platformErr
	}
	return dashboard.PlatformStats{Applications: map[string]int{"submitted": 2}, Institutes: map[string]int{"active": 3, "suspended": 1},
		CertificatesIssued: 40, CertificatesRevoked: 1, BulkJobsStarted: 2, EmailsAccepted: 35, EmailsFailed: 3,
		RecentDecisions: []dashboard.RecentDecision{{Action: "application.approved", At: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)}}}, nil
}

var now = time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)

type env struct {
	f  *tenancytest.Fixture
	st *fakeStore
}

func newEnv(t *testing.T) *env {
	e := &env{st: &fakeStore{byInstitute: map[string]dashboard.InstituteStats{}}}
	e.f = tenancytest.NewFixture(t, func(f *tenancytest.Fixture) {
		e.st.byInstitute[f.A.ID] = dashboard.InstituteStats{
			Records:      dashboard.Records{ActiveStudents: 12, ArchivedStudents: 2, ActiveBatches: 3},
			Certificates: dashboard.CertificateCounts{IssuedInPeriod: 5, Issued: 9, Revoked: 1, Failed: 2},
			LastJob:      &dashboard.JobSummary{ID: "j-1", BatchName: "Jan", Status: "completed_with_errors", Total: 10, Succeeded: 9, Failed: 1, CreatedAt: now},
			Deliveries:   dashboard.DeliveryCounts{Queued: 1, Accepted: 6, Failed: 2},
			Recent:       []dashboard.RecentCertificate{{ID: "c-1", Number: "CERT-2026-000001", StudentName: "Asha Verma", CourseName: "Web Design", Status: "issued", At: now}},
		}
		e.st.byInstitute[f.B.ID] = dashboard.InstituteStats{Records: dashboard.Records{ActiveStudents: 99}}
		h := &dashboard.Handler{Store: e.st, Tenancy: f.TenantH, Authn: f.AuthH.Authenticate, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Now: func() time.Time { return now }}
		h.Register(f.Mux)
	})
	return e
}

func (e *env) call(method, path, email, inst string) (int, map[string]any) {
	e.f.T.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(""))
	req.RemoteAddr = "203.0.113.9:1234"
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

func TestInstituteDashboardIsScopedToTheMembersInstitute(t *testing.T) {
	e := newEnv(t)
	A, B := e.f.A.ID, e.f.B.ID
	code, res := e.call("GET", "/api/v1/dashboard", "a-staff@t.test", A)
	if code != 200 {
		t.Fatalf("%d %v", code, res)
	}
	d := res["dashboard"].(map[string]any)
	rec := d["records"].(map[string]any)
	cer := d["certificates"].(map[string]any)
	if rec["active_students"] != float64(12) || rec["active_batches"] != float64(3) || cer["issued_in_period"] != float64(5) || cer["failed"] != float64(2) {
		t.Fatalf("figures %v", d)
	}
	if d["days"] != float64(30) || e.st.since != now.AddDate(0, 0, -30) {
		t.Fatalf("default period: %v since %v", d["days"], e.st.since)
	}
	job := d["last_job"].(map[string]any)
	if job["status"] != "completed_with_errors" || job["failed"] != float64(1) {
		t.Fatalf("job %v", job)
	}
	if d["deliveries"].(map[string]any)["accepted"] != float64(6) || len(d["recent"].([]any)) != 1 {
		t.Fatalf("%v", d)
	}
	if e.st.scopes[len(e.st.scopes)-1] != A {
		t.Fatalf("store was asked for %v", e.st.scopes)
	}
	// Another institute's member sees only their own figures, never A's, whatever header they send for A.
	if code, _ := e.call("GET", "/api/v1/dashboard", "b-staff@t.test", A); code != 403 {
		t.Errorf("foreign institute header: %d", code)
	}
	_, res = e.call("GET", "/api/v1/dashboard", "b-staff@t.test", B)
	if res["dashboard"].(map[string]any)["records"].(map[string]any)["active_students"] != float64(99) {
		t.Fatalf("B %v", res)
	}
	if code, _ := e.call("GET", "/api/v1/dashboard", "", A); code != 401 {
		t.Errorf("anonymous: %d", code)
	}
	if code, _ := e.call("GET", "/api/v1/dashboard", "removed@t.test", A); code != 403 {
		t.Errorf("removed member: %d", code)
	}
	if code, _ := e.call("GET", "/api/v1/dashboard", "c-owner@t.test", e.f.C.ID); code != 200 {
		t.Errorf("suspended institute is read-only, not hidden: %d", code)
	}
}

func TestPeriodsAreValidated(t *testing.T) {
	e := newEnv(t)
	A := e.f.A.ID
	for _, d := range []string{"7", "30", "90", "365"} {
		if code, res := e.call("GET", "/api/v1/dashboard?days="+d, "a-owner@t.test", A); code != 200 || res["dashboard"].(map[string]any)["days"] == nil {
			t.Errorf("days=%s: %d", d, code)
		}
	}
	if code, _ := e.call("GET", "/api/v1/dashboard?days=7", "a-owner@t.test", A); code != 200 || e.st.since != now.AddDate(0, 0, -7) {
		t.Errorf("since %v", e.st.since)
	}
	for _, d := range []string{"0", "-1", "8", "abc", "99999"} {
		if code, _ := e.call("GET", "/api/v1/dashboard?days="+d, "a-owner@t.test", A); code != 422 {
			t.Errorf("days=%s: %d", d, code)
		}
	}
}

func TestSectionsFollowPermissions(t *testing.T) {
	st := dashboard.InstituteStats{Records: dashboard.Records{ActiveStudents: 1}, Recent: []dashboard.RecentCertificate{{StudentName: "Asha"}}}
	// A tenant without any permission gets no section (and so no student names).
	out := dashboard.Build(tenancy.Tenant{}, 30, now, st)
	for _, k := range []string{"records", "certificates", "deliveries", "recent", "last_job"} {
		if _, ok := out[k]; ok {
			t.Errorf("section %q leaked to a member without permission", k)
		}
	}
	b, _ := json.Marshal(out)
	if strings.Contains(string(b), "Asha") {
		t.Fatal("student name leaked")
	}
}

func TestPlatformDashboardIsForPlatformStaffAndHasNoPersonalData(t *testing.T) {
	e := newEnv(t)
	code, res := e.call("GET", "/api/v1/ops/dashboard?days=90", "ops@t.test", "")
	if code != 200 {
		t.Fatalf("%d %v", code, res)
	}
	d := res["dashboard"].(map[string]any)
	v := d["volume"].(map[string]any)
	if d["days"] != float64(90) || v["certificates_issued"] != float64(40) || v["emails_failed"] != float64(3) ||
		d["applications"].(map[string]any)["submitted"] != float64(2) || d["institutes"].(map[string]any)["suspended"] != float64(1) {
		t.Fatalf("%v", d)
	}
	b, _ := json.Marshal(res)
	for _, bad := range []string{"Asha", "student", "email\"", "recipient"} {
		if strings.Contains(string(b), bad) {
			t.Errorf("platform payload mentions %q", bad)
		}
	}
	for _, who := range []string{"a-owner@t.test", "nobody@t.test", "multi@t.test"} {
		if code, _ := e.call("GET", "/api/v1/ops/dashboard", who, ""); code != 403 {
			t.Errorf("%s: %d", who, code)
		}
	}
	if code, _ := e.call("GET", "/api/v1/ops/dashboard", "", ""); code != 401 {
		t.Errorf("anonymous: %d", code)
	}
	if code, _ := e.call("GET", "/api/v1/ops/dashboard?days=5", "ops@t.test", ""); code != 422 {
		t.Errorf("bad days: %d", code)
	}
	e.st.platformErr = context.DeadlineExceeded
	code, res = e.call("GET", "/api/v1/ops/dashboard", "ops@t.test", "")
	if code != 500 || strings.Contains(string(mustJSON(res)), "deadline") {
		t.Errorf("error must be generic: %d %v", code, res)
	}
	_ = httpx.CSRFHeader
}

func mustJSON(v any) []byte { b, _ := json.Marshal(v); return b }
