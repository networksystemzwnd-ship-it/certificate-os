package certificates_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/certificate-os/api/internal/audit/audittest"
	"github.com/certificate-os/api/internal/certificates"
	"github.com/certificate-os/api/internal/certificates/certtest"
	"github.com/certificate-os/api/internal/pdf"
	"github.com/certificate-os/api/internal/tenancy"
)

// End-to-end with a REAL browser: TEST_CHROME_PATH=/path/to/chrome go test ./internal/certificates/ -run RealChromium
// With CERT_PDF_OUT=<dir> the PDF is also written there so the QR code can be decoded independently.
func TestRealChromiumIssuesAPdfMatchingTheSnapshot(t *testing.T) {
	path := os.Getenv("TEST_CHROME_PATH")
	if path == "" {
		t.Skip("TEST_CHROME_PATH not set")
	}
	r, err := pdf.NewChromium(path, os.Geteuid() == 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	w := audittest.NewMemWriter()
	store := certtest.NewMemStore(w)
	svc := certificates.NewService(store, r, "https://certs.example.com")
	scope := tenancy.TrustedScope("inst-real")
	refs := store.AddFixture(scope, certtest.FixtureOpts{StudentName: "Asha Verma", Grade: "A"})
	tenant := tenancy.Tenant{UserID: "user-1", Institute: tenancy.Institute{ID: "inst-real"}}

	c, err := svc.Issue(context.Background(), tenant, certificates.IssueRequest{EnrollmentID: refs.EnrollmentID, TemplateID: refs.TemplateID}, "req-1")
	if err != nil {
		t.Fatal(err)
	}
	p, err := svc.PDF(context.Background(), tenant, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(p.Bytes), "%PDF-") || p.SHA256 != c.PDFSHA256 {
		t.Fatal("stored pdf does not match the record")
	}
	dir := t.TempDir()
	if out := os.Getenv("CERT_PDF_OUT"); out != "" {
		dir = out
	}
	file := filepath.Join(dir, "certificate.pdf")
	if err := os.WriteFile(file, p.Bytes, 0o600); err != nil {
		t.Fatal(err)
	}
	if pt, err := exec.LookPath("pdftotext"); err == nil {
		text, err := exec.Command(pt, "-layout", file, "-").Output()
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{"Asha Verma", c.Number, "Web Design", "CERTIFICATE V1", "15 March 2026"} { // the classic preset upper-cases the title
			if !strings.Contains(strings.ToUpper(string(text)), strings.ToUpper(want)) {
				t.Errorf("PDF text misses %q:\n%s", want, text)
			}
		}
	}
	if pi, err := exec.LookPath("pdfinfo"); err == nil {
		info, _ := exec.Command(pi, file).Output()
		if !strings.Contains(string(info), "Pages:           1") || !strings.Contains(string(info), "A4") {
			t.Errorf("expected a single A4 page:\n%s", info)
		}
	}
}

// A bulk job with the REAL browser: every stored PDF is a valid single A4 page naming its own student.
func TestRealChromiumBulkJob(t *testing.T) {
	path := os.Getenv("TEST_CHROME_PATH")
	if path == "" {
		t.Skip("TEST_CHROME_PATH not set")
	}
	r, err := pdf.NewChromium(path, os.Geteuid() == 0, 2)
	if err != nil {
		t.Fatal(err)
	}
	store := certtest.NewMemStore(audittest.NewMemWriter())
	svc := certificates.NewService(store, r, "https://certs.example.com")
	jobs := certificates.NewJobs(context.Background(), svc, store, nil)
	jobs.Go = func(f func()) { f() }
	scope := tenancy.TrustedScope("inst-real")
	tenant := tenancy.Tenant{UserID: "user-1", Institute: tenancy.Institute{ID: "inst-real"}}
	names := []string{"Asha Verma", "Bela Rao", "Chitra Nair", "Dev Patel", "Esha Gupta", "Farid Khan"}
	var first certtest.Refs
	for i, n := range names {
		o := certtest.FixtureOpts{StudentName: n}
		if i > 0 {
			o.BatchID, o.TemplateID = first.BatchID, first.TemplateID
		}
		ref := store.AddFixture(scope, o)
		if i == 0 {
			first = ref
		}
	}
	started := time.Now()
	job, err := jobs.Start(context.Background(), tenant, certificates.BulkRequest{BatchID: first.BatchID, TemplateID: first.TemplateID}, "r")
	if err != nil {
		t.Fatal(err)
	}
	got, _ := jobs.Get(context.Background(), tenant, job.ID)
	t.Logf("%d certificates with Chromium in %s", got.Total, time.Since(started).Round(time.Millisecond))
	if got.Status != certificates.JobCompleted || got.Succeeded != len(names) {
		t.Fatalf("job %+v", got)
	}
	pt, _ := exec.LookPath("pdftotext")
	items, _ := jobs.Items(context.Background(), tenant, job.ID, certificates.ItemSucceeded, 100, 0)
	for _, it := range items {
		p, err := svc.PDF(context.Background(), tenant, it.CertificateID)
		if err != nil || !strings.HasPrefix(string(p.Bytes), "%PDF-") {
			t.Fatalf("pdf of %s: %v", it.StudentName, err)
		}
		if pt == "" {
			continue
		}
		file := filepath.Join(t.TempDir(), "c.pdf")
		if err := os.WriteFile(file, p.Bytes, 0o600); err != nil {
			t.Fatal(err)
		}
		text, err := exec.Command(pt, "-layout", file, "-").Output()
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(text), it.StudentName) || !strings.Contains(string(text), it.CertificateNumber) {
			t.Errorf("PDF of %s does not show its student and number:\n%s", it.StudentName, text)
		}
		for _, other := range names {
			if other != it.StudentName && strings.Contains(string(text), other) {
				t.Errorf("PDF of %s mentions %s", it.StudentName, other)
			}
		}
	}
}
