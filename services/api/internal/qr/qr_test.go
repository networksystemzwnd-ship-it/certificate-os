package qr

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSizesAndErrors(t *testing.T) {
	for _, tc := range []struct{ n, size int }{{1, 21}, {14, 21}, {15, 25}, {26, 25}, {27, 29}, {62, 33}, {213, 57}} {
		m, err := Encode(strings.Repeat("a", tc.n))
		if err != nil || len(m) != tc.size {
			t.Fatalf("len %d: size %d err %v, want %d", tc.n, len(m), err, tc.size)
		}
	}
	if _, err := Encode(strings.Repeat("a", MaxBytes+1)); err != ErrTooLong {
		t.Fatalf("want ErrTooLong, got %v", err)
	}
}

func TestFixedPatterns(t *testing.T) {
	m, _ := Encode("https://example.com/verify/abc")
	n := len(m)
	// finder corners and the dark module
	for _, p := range [][2]int{{0, 0}, {n - 1, 0}, {0, n - 1}, {6, 6}, {n - 7, 0}, {8, n - 8}} {
		if !m[p[1]][p[0]] {
			t.Fatalf("expected dark module at %v", p)
		}
	}
	for i := 8; i < n-8; i++ {
		if m[6][i] != (i%2 == 0) || m[i][6] != (i%2 == 0) {
			t.Fatalf("timing pattern broken at %d", i)
		}
	}
}

func TestDeterministic(t *testing.T) {
	a, _ := SVG("https://example.com/verify/x")
	b, _ := SVG("https://example.com/verify/x")
	if a != b || !strings.HasPrefix(a, "<svg ") {
		t.Fatal("not deterministic or not an svg")
	}
	for _, bad := range []string{"<script", "href=", "http://", "onload"} {
		if strings.Contains(strings.ReplaceAll(a, "http://www.w3.org/2000/svg", ""), bad) {
			t.Fatalf("svg contains %q", bad)
		}
	}
}

// With QR_DUMP_DIR set, writes test codes (versions 1–10, every size class) as PBM images plus a .txt with
// the expected text, so an independent decoder (e.g. OpenCV's QRCodeDetector) can check the encoder:
//
//	QR_DUMP_DIR=/tmp/qr go test ./internal/qr/ -run TestDump
//
// Otherwise a no-op. All 55 codes decoded correctly when this was verified.
func TestDump(t *testing.T) {
	dir := os.Getenv("QR_DUMP_DIR")
	if dir == "" {
		t.Skip("QR_DUMP_DIR not set")
	}
	var texts []string
	for n := 1; n <= MaxBytes; n += 4 {
		texts = append(texts, strings.Repeat(string(rune('A'+n%26)), n/2)+fmt.Sprint(n)+strings.Repeat("z/", n/4))
	}
	texts = append(texts, "https://certificates.example.org/verify/Xk3pQ9wLr2VtYb7mNc4HaJ8dFgS1uEoZiT6yRqMvBnA")
	for i, s := range texts {
		if len(s) > MaxBytes {
			s = s[:MaxBytes]
		}
		m, err := Encode(s)
		if err != nil {
			t.Fatal(err)
		}
		const scale, quiet = 8, 4
		size := (len(m) + 2*quiet) * scale
		var b strings.Builder
		fmt.Fprintf(&b, "P1\n%d %d\n", size, size)
		for y := 0; y < size; y++ {
			for x := 0; x < size; x++ {
				mx, my := x/scale-quiet, y/scale-quiet
				dark := mx >= 0 && my >= 0 && mx < len(m) && my < len(m) && m[my][mx]
				if dark {
					b.WriteByte('1')
				} else {
					b.WriteByte('0')
				}
				b.WriteByte(' ')
			}
			b.WriteByte('\n')
		}
		name := filepath.Join(dir, fmt.Sprintf("q%03d.pbm", i))
		if err := os.WriteFile(name, []byte(b.String()), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(name+".txt", []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}
