package pdf

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestUnavailableAndFake(t *testing.T) {
	if _, err := (Unavailable{}).HTMLToPDF(context.Background(), "x"); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
	f := &Fake{}
	b, err := f.HTMLToPDF(context.Background(), "<p>hi</p>")
	if err != nil || !strings.HasPrefix(string(b), "%PDF-") || f.Last != "<p>hi</p>" {
		t.Fatal("fake renderer")
	}
}

func TestNewChromiumValidation(t *testing.T) {
	if _, err := NewChromium("chrome", false, 1); err == nil {
		t.Fatal("relative path accepted")
	}
	if _, err := NewChromium("/definitely/not/here", false, 1); err == nil {
		t.Fatal("missing binary accepted")
	}
	if _, err := NewChromium(os.TempDir(), false, 1); err == nil {
		t.Fatal("directory accepted")
	}
}

// Real-browser test: TEST_CHROME_PATH=/path/to/chrome go test ./internal/pdf/
func TestChromiumRendersRealPDF(t *testing.T) {
	path := os.Getenv("TEST_CHROME_PATH")
	if path == "" {
		t.Skip("TEST_CHROME_PATH not set")
	}
	c, err := NewChromium(path, os.Geteuid() == 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	html := `<!doctype html><html><head><meta charset="utf-8"><style>@page{size:A4 landscape;margin:0}body{margin:0}</style></head><body><h1>Certificate for Asha Verma</h1></body></html>`
	b, err := c.HTMLToPDF(context.Background(), html)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(b), "%PDF-") || len(b) < 500 {
		t.Fatalf("not a plausible PDF (%d bytes)", len(b))
	}
	if p, err := exec.LookPath("pdftotext"); err == nil {
		cmd := exec.Command(p, "-", "-")
		cmd.Stdin = strings.NewReader(string(b))
		out, _ := cmd.Output()
		if !strings.Contains(string(out), "Asha Verma") {
			t.Fatalf("PDF text = %q", out)
		}
	}
}

func TestChromiumTimeoutAndNoNetwork(t *testing.T) {
	path := os.Getenv("TEST_CHROME_PATH")
	if path == "" {
		t.Skip("TEST_CHROME_PATH not set")
	}
	c, _ := NewChromium(path, os.Geteuid() == 0, 1)
	c.Timeout = time.Nanosecond
	if _, err := c.HTMLToPDF(context.Background(), "<p>x</p>"); !errors.Is(err, ErrRender) {
		t.Fatalf("timeout should be ErrRender, got %v", err)
	}
}
