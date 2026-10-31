// Package pdf turns a certificate's self-contained HTML into PDF bytes.
//
// The production renderer drives a pinned headless Chromium through its command line (no third-party Go
// dependency). Rendering untrusted-looking input is hardened in depth: the HTML already carries a CSP with
// default-src 'none' (no script, no network) and contains only escaped text; Chromium additionally runs with
// DNS resolution disabled, a throw-away profile directory, an empty environment (no secrets), a hard
// timeout, a concurrency limit and an output size cap. (Disabling JavaScript through Blink settings makes
// print-to-pdf produce nothing, so the CSP is what keeps scripts out.) The browser sandbox stays ON unless NoSandbox is set (needed when
// running as root inside a container; run the worker as a non-root user to keep it on).
package pdf

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

var (
	// ErrUnavailable means no renderer is configured (PDF_CHROME_PATH unset).
	ErrUnavailable = errors.New("pdf renderer not configured")
	ErrRender      = errors.New("pdf rendering failed")
)

// MaxPDFBytes caps a rendered certificate.
const MaxPDFBytes = 5 << 20

// Renderer converts HTML to PDF.
type Renderer interface {
	HTMLToPDF(ctx context.Context, html string) ([]byte, error)
}

// Unavailable is the renderer used when none is configured: every call fails with ErrUnavailable.
type Unavailable struct{}

func (Unavailable) HTMLToPDF(context.Context, string) ([]byte, error) { return nil, ErrUnavailable }

// Chromium renders with a headless Chromium/Chrome binary.
type Chromium struct {
	Path      string        // absolute path of the browser binary
	NoSandbox bool          // pass --no-sandbox (containers running as root)
	Timeout   time.Duration // per render, default 30s
	sem       chan struct{}
}

// NewChromium validates the binary path and limits concurrent renders (default 2).
func NewChromium(path string, noSandbox bool, maxConcurrent int) (*Chromium, error) {
	if !filepath.IsAbs(path) {
		return nil, fmt.Errorf("pdf: browser path must be absolute, got %q", path)
	}
	if st, err := os.Stat(path); err != nil || st.IsDir() {
		return nil, fmt.Errorf("pdf: browser not found at %q", path)
	}
	if maxConcurrent < 1 {
		maxConcurrent = 2
	}
	return &Chromium{Path: path, NoSandbox: noSandbox, Timeout: 30 * time.Second, sem: make(chan struct{}, maxConcurrent)}, nil
}

func (c *Chromium) HTMLToPDF(ctx context.Context, html string) ([]byte, error) {
	select {
	case c.sem <- struct{}{}:
		defer func() { <-c.sem }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	dir, err := os.MkdirTemp("", "certos-pdf-*")
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrRender, err)
	}
	defer os.RemoveAll(dir)
	in, out := filepath.Join(dir, "in.html"), filepath.Join(dir, "out.pdf")
	if err := os.WriteFile(in, []byte(html), 0o600); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrRender, err)
	}
	args := []string{
		"--headless=new", "--disable-gpu", "--disable-extensions", "--disable-dev-shm-usage",
		"--no-first-run", "--no-default-browser-check", "--hide-scrollbars",
		`--host-resolver-rules=MAP * ~NOTFOUND`, // no DNS: nothing can reach the network
		"--user-data-dir=" + filepath.Join(dir, "profile"),
		"--no-pdf-header-footer", "--print-to-pdf=" + out,
	}
	if c.NoSandbox {
		args = append(args, "--no-sandbox")
	}
	args = append(args, "file://"+in)
	cmd := exec.CommandContext(ctx, c.Path, args...)
	cmd.Env = []string{"HOME=" + dir, "PATH=/usr/bin:/bin", "LANG=C.UTF-8"} // nothing from the parent environment (secrets)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("%w: timed out", ErrRender)
		}
		return nil, fmt.Errorf("%w: browser exited: %v", ErrRender, err)
	}
	st, err := os.Stat(out)
	if err != nil {
		return nil, fmt.Errorf("%w: no output", ErrRender)
	}
	if st.Size() > MaxPDFBytes {
		return nil, fmt.Errorf("%w: output too large", ErrRender)
	}
	b, err := os.ReadFile(out)
	if err != nil || !bytes.HasPrefix(b, []byte("%PDF-")) {
		return nil, fmt.Errorf("%w: output is not a PDF", ErrRender)
	}
	return b, nil
}

// Fake renders deterministic placeholder bytes for tests; it records the HTML it was given.
type Fake struct {
	Err  error
	Last string
}

func (f *Fake) HTMLToPDF(_ context.Context, html string) ([]byte, error) {
	f.Last = html
	if f.Err != nil {
		return nil, f.Err
	}
	return []byte("%PDF-1.4\n% fake\n" + html + "\n%%EOF\n"), nil
}
