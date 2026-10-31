package certificates_test

import (
	"archive/zip"
	"bytes"
	"encoding/csv"
	"io"
	"strings"
	"testing"

	"github.com/certificate-os/api/internal/certificates"
	"github.com/certificate-os/api/internal/certificates/certtest"
	"github.com/certificate-os/api/internal/tenancy"
)

// batchOf creates n completed enrollments sharing one batch and template.
func (e *env) batchOf(inst string, names ...string) []certtest.Refs {
	var out []certtest.Refs
	for i, n := range names {
		o := certtest.FixtureOpts{StudentName: n}
		if i > 0 {
			o.BatchID, o.TemplateID = out[0].BatchID, out[0].TemplateID
		}
		out = append(out, e.fixture(inst, o))
	}
	return out
}

func bulkBody(r certtest.Refs) map[string]any {
	return map[string]any{"batch_id": r.BatchID, "template_id": r.TemplateID}
}

func TestBulkPermissionMatrix(t *testing.T) {
	e := newEnv(t)
	A := e.f.A.ID
	rs := e.batchOf(A, "Asha", "Bela")
	if code, _ := e.call("POST", "/api/v1/certificates/bulk", "a-staff@t.test", A, bulkBody(rs[0])); code != 403 {
		t.Errorf("staff start: %d", code)
	}
	if code, _ := e.call("GET", "/api/v1/certificates/bulk-preview?batch_id="+rs[0].BatchID+"&template_id="+rs[0].TemplateID, "a-staff@t.test", A, nil); code != 403 {
		t.Errorf("staff preview: %d", code)
	}
	code, res := e.call("POST", "/api/v1/certificates/bulk", "a-issuer@t.test", A, bulkBody(rs[0]))
	if code != 202 {
		t.Fatalf("issuer start: %d %v", code, res)
	}
	id := str(res, "job", "id")
	for _, who := range []string{"a-staff@t.test", "a-issuer@t.test", "a-admin@t.test", "a-owner@t.test"} {
		for _, p := range []string{"/api/v1/bulk-jobs", "/api/v1/bulk-jobs/" + id, "/api/v1/bulk-jobs/" + id + "/items"} {
			if code, _ := e.call("GET", p, who, A, nil); code != 200 {
				t.Errorf("%s GET %s: %d", who, p, code)
			}
		}
	}
	// Export is its own permission: staff and the issue grant do not have it, admin and owner do.
	for _, who := range []string{"a-staff@t.test", "a-issuer@t.test"} {
		if code, _ := e.call("GET", "/api/v1/bulk-jobs/"+id+"/export", who, A, nil); code != 403 {
			t.Errorf("%s export: %d", who, code)
		}
		for _, act := range []string{"cancel", "resume", "retry-failed"} {
			if who == "a-staff@t.test" {
				if code, _ := e.call("POST", "/api/v1/bulk-jobs/"+id+"/"+act, who, A, nil); code != 403 {
					t.Errorf("%s %s: %d", who, act, code)
				}
			}
		}
	}
	for _, who := range []string{"a-admin@t.test", "a-owner@t.test"} {
		if rr := e.raw("GET", "/api/v1/bulk-jobs/"+id+"/export", who, A, nil); rr.Code != 200 {
			t.Errorf("%s export: %d", who, rr.Code)
		}
	}
	if code, _ := e.call("GET", "/api/v1/bulk-jobs", "", A, nil); code != 401 {
		t.Errorf("anonymous: %d", code)
	}
	if code, _ := e.call("GET", "/api/v1/bulk-jobs", "b-staff@t.test", A, nil); code != 403 {
		t.Errorf("other institute's staff: %d", code)
	}
}

func TestBulkSuspendedInstituteIsReadOnlyAndCannotExport(t *testing.T) {
	e := newEnv(t)
	C := e.f.C.ID
	rs := e.batchOf(C, "Asha")
	if code, _ := e.call("POST", "/api/v1/certificates/bulk", "c-owner@t.test", C, bulkBody(rs[0])); code != 403 {
		t.Errorf("start while suspended: %d", code)
	}
	if code, _ := e.call("GET", "/api/v1/bulk-jobs", "c-owner@t.test", C, nil); code != 200 {
		t.Errorf("read while suspended: %d", code)
	}
	for _, p := range []string{"/api/v1/bulk-jobs/x/export"} {
		if code, _ := e.call("GET", p, "c-owner@t.test", C, nil); code != 403 {
			t.Errorf("export while suspended: %d", code)
		}
	}
	for _, act := range []string{"cancel", "resume", "retry-failed"} {
		if code, _ := e.call("POST", "/api/v1/bulk-jobs/x/"+act, "c-owner@t.test", C, nil); code != 403 {
			t.Errorf("%s while suspended: %d", act, code)
		}
	}
}

func TestBulkEndToEndAndExport(t *testing.T) {
	e := newEnv(t)
	A := e.f.A.ID
	rs := e.batchOf(A, "Asha Verma", "=HYPERLINK(\"http://evil\")", "Chitra Rao")
	e.fixture(A, certtest.FixtureOpts{}) // an enrollment of another batch must not be touched
	e.store.AddFixture(tenancy.TrustedScope(A), certtest.FixtureOpts{StudentName: "Not Done", Incomplete: true, BatchID: rs[0].BatchID, TemplateID: rs[0].TemplateID})

	code, pv := e.call("GET", "/api/v1/certificates/bulk-preview?batch_id="+rs[0].BatchID+"&template_id="+rs[0].TemplateID, "a-admin@t.test", A, nil)
	p := pv["preview"].(map[string]any)
	if code != 200 || p["eligible"] != float64(3) || p["not_eligible"] != float64(1) || p["already_issued"] != float64(0) || p["max_per_job"] != float64(certificates.MaxJobItems) {
		t.Fatalf("preview %d %v", code, pv)
	}
	code, res := e.call("POST", "/api/v1/certificates/bulk", "a-admin@t.test", A, bulkBody(rs[0]))
	if code != 202 {
		t.Fatalf("start %d %v", code, res)
	}
	id := str(res, "job", "id")
	_, got := e.call("GET", "/api/v1/bulk-jobs/"+id, "a-admin@t.test", A, nil)
	j := got["job"].(map[string]any)
	if j["status"] != "completed" || j["total"] != float64(3) || j["succeeded"] != float64(3) || j["failed"] != float64(0) || j["remaining"] != float64(0) ||
		j["skipped_not_eligible"] != float64(1) || j["template_version"] != float64(1) {
		t.Fatalf("job %v", j)
	}
	_, it := e.call("GET", "/api/v1/bulk-jobs/"+id+"/items?status=succeeded&limit=2", "a-admin@t.test", A, nil)
	items := it["items"].([]any)
	if len(items) != 2 || it["has_more"] != true || !strings.HasPrefix(str(items[0].(map[string]any), "certificate_number"), "CERT-") {
		t.Fatalf("items %v", it)
	}

	zr := func() *zip.Reader {
		rr := e.raw("GET", "/api/v1/bulk-jobs/"+id+"/export", "a-admin@t.test", A, nil)
		if rr.Code != 200 || rr.Header().Get("Content-Type") != "application/zip" || rr.Header().Get("Cache-Control") != "private, no-store" ||
			rr.Header().Get("X-Content-Type-Options") != "nosniff" || !strings.HasPrefix(rr.Header().Get("Content-Disposition"), `attachment; filename="certificates-`) {
			t.Fatalf("export %d %v", rr.Code, rr.Header())
		}
		z, err := zip.NewReader(bytes.NewReader(rr.Body.Bytes()), int64(rr.Body.Len()))
		if err != nil {
			t.Fatal(err)
		}
		return z
	}
	z := zr()
	names := map[string][]byte{}
	for _, f := range z.File {
		r, _ := f.Open()
		b, _ := io.ReadAll(r)
		r.Close()
		names[f.Name] = b
	}
	if len(names) != 4 || names["manifest.csv"] == nil {
		t.Fatalf("zip entries: %v", len(names))
	}
	_, list := e.call("GET", "/api/v1/certificates?status=issued", "a-admin@t.test", A, nil)
	for _, c := range list["certificates"].([]any) {
		m := c.(map[string]any)
		b := names[str(m, "certificate_number")+".pdf"]
		if !bytes.HasPrefix(b, []byte("%PDF-")) {
			t.Errorf("%s is not in the archive", str(m, "certificate_number"))
		}
	}
	rows, err := csv.NewReader(bytes.NewReader(names["manifest.csv"])).ReadAll()
	if err != nil || len(rows) != 4 || rows[0][0] != "certificate_number" {
		t.Fatalf("manifest %v %v", rows, err)
	}
	for _, r := range rows[1:] {
		if strings.HasPrefix(r[1], "=") || strings.HasPrefix(r[1], "+") || strings.HasPrefix(r[1], "-") || strings.HasPrefix(r[1], "@") {
			t.Errorf("formula injection in manifest: %q", r[1])
		}
		if !strings.HasPrefix(r[5], "https://certs.example.com/verify/") {
			t.Errorf("verify url %q", r[5])
		}
	}
	// The export is audited, without personal data.
	found := false
	for _, r := range e.w.ForEntity("bulk_job", id) {
		if r.Action == "certificate.bulk_exported" {
			found = true
			if r.Metadata["files"] != 3 || r.ActorUserID == "" || r.InstituteID != A {
				t.Errorf("export audit %+v", r)
			}
			for k := range r.Metadata {
				if k != "files" {
					t.Errorf("unexpected audit metadata %q", k)
				}
			}
		}
	}
	if !found {
		t.Fatal("export not audited")
	}
	// Revoked certificates are left out; when nothing is left there is nothing to export.
	for i, c := range list["certificates"].([]any) {
		cid := str(c.(map[string]any), "id")
		if code, _ := e.call("POST", "/api/v1/certificates/"+cid+"/revoke", "a-admin@t.test", A, map[string]any{"reason": "issued in error"}); code != 200 {
			t.Fatalf("revoke: %d", code)
		}
		if i == 0 {
			if z := zr(); len(z.File) != 3 {
				t.Fatalf("after one revocation the archive has %d entries", len(z.File))
			}
		}
	}
	if rr := e.raw("GET", "/api/v1/bulk-jobs/"+id+"/export", "a-admin@t.test", A, nil); rr.Code != 409 {
		t.Fatalf("all revoked: %d", rr.Code)
	}
	// Running it again finds nothing new (revoked ones free their enrollment, so they ARE eligible again; the
	// others in the batch stay skipped).
	code, pv = e.call("GET", "/api/v1/certificates/bulk-preview?batch_id="+rs[0].BatchID+"&template_id="+rs[0].TemplateID, "a-admin@t.test", A, nil)
	if p := pv["preview"].(map[string]any); code != 200 || p["eligible"] != float64(3) {
		t.Fatalf("preview after revoking %v", pv)
	}
}

func TestBulkNothingToIssueAndValidation(t *testing.T) {
	e := newEnv(t)
	A := e.f.A.ID
	rs := e.batchOf(A, "Asha")
	if code, _ := e.call("POST", "/api/v1/certificates/bulk", "a-admin@t.test", A, bulkBody(rs[0])); code != 202 {
		t.Fatalf("first run: %d", code)
	}
	code, res := e.call("POST", "/api/v1/certificates/bulk", "a-admin@t.test", A, bulkBody(rs[0]))
	if code != 422 || str(res, "error", "details", "batch_id") == "" && str(res, "error", "message") == "" {
		t.Fatalf("second run: %d %v", code, res)
	}
	if code, _ := e.call("POST", "/api/v1/certificates/bulk", "a-admin@t.test", A, map[string]any{}); code != 422 {
		t.Errorf("empty body: %d", code)
	}
	if code, _ := e.call("POST", "/api/v1/certificates/bulk", "a-admin@t.test", A, map[string]any{"batch_id": "nope", "template_id": rs[0].TemplateID}); code != 404 {
		t.Errorf("unknown batch: %d", code)
	}
	for _, p := range []string{"/api/v1/bulk-jobs?limit=0", "/api/v1/bulk-jobs?offset=-1", "/api/v1/bulk-jobs/x/items?status=bogus"} {
		if code, _ := e.call("GET", p, "a-admin@t.test", A, nil); code != 422 {
			t.Errorf("%s: %d", p, code)
		}
	}
	if code, _ := e.call("GET", "/api/v1/bulk-jobs/unknown", "a-admin@t.test", A, nil); code != 404 {
		t.Errorf("unknown job: %d", code)
	}
}

func TestBulkJobOfAnotherInstituteIsInvisible(t *testing.T) {
	e := newEnv(t)
	A, B := e.f.A.ID, e.f.B.ID
	rs := e.batchOf(A, "Asha")
	_, res := e.call("POST", "/api/v1/certificates/bulk", "a-admin@t.test", A, bulkBody(rs[0]))
	id := str(res, "job", "id")
	for _, c := range []struct{ m, p string }{
		{"GET", "/api/v1/bulk-jobs/" + id}, {"GET", "/api/v1/bulk-jobs/" + id + "/items"}, {"GET", "/api/v1/bulk-jobs/" + id + "/export"},
		{"POST", "/api/v1/bulk-jobs/" + id + "/cancel"}, {"POST", "/api/v1/bulk-jobs/" + id + "/resume"}, {"POST", "/api/v1/bulk-jobs/" + id + "/retry-failed"},
	} {
		if code, _ := e.call(c.m, c.p, "b-owner@t.test", B, nil); code != 404 {
			t.Errorf("%s %s from another institute: %d", c.m, c.p, code)
		}
	}
	if code, _ := e.call("POST", "/api/v1/certificates/bulk", "b-owner@t.test", B, bulkBody(rs[0])); code != 404 {
		t.Errorf("foreign batch: %d", code)
	}
	_, l := e.call("GET", "/api/v1/bulk-jobs", "b-owner@t.test", B, nil)
	if jobs, _ := l["jobs"].([]any); len(jobs) != 0 {
		t.Errorf("foreign list: %v", l)
	}
}

func TestBulkStateErrorsAndActiveJob(t *testing.T) {
	e := newEnv(t)
	A := e.f.A.ID
	rs := e.batchOf(A, "Asha")
	other := e.fixture(A, certtest.FixtureOpts{})
	// A queued job (never started) blocks a second one.
	scope := tenancy.TrustedScope(A)
	pending, err := e.store.CreateJob(t.Context(), scope, certificates.JobInput{BatchID: other.BatchID, TemplateID: other.TemplateID, CreatedBy: "u"}, certtestEvent())
	if err != nil {
		t.Fatal(err)
	}
	code, res := e.call("POST", "/api/v1/certificates/bulk", "a-admin@t.test", A, bulkBody(rs[0]))
	if code != 409 {
		t.Fatalf("second active job: %d %v", code, res)
	}
	// A queued job can be started with resume, then it is finished: retry/cancel/resume refuse.
	if code, _ := e.call("POST", "/api/v1/bulk-jobs/"+pending.ID+"/resume", "a-admin@t.test", A, nil); code != 200 {
		t.Fatalf("resume: %d", code)
	}
	for _, act := range []string{"cancel", "resume", "retry-failed"} {
		if code, _ := e.call("POST", "/api/v1/bulk-jobs/"+pending.ID+"/"+act, "a-admin@t.test", A, nil); code != 409 {
			t.Errorf("%s of a finished job: %d", act, code)
		}
	}
}
