package certificates_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

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
