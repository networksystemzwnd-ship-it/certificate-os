package templates

import (
	"errors"
	"regexp"
	"strings"
	"testing"
)

func valid() Config { return Presets()[0].Config }

func problem(t *testing.T, c Config, field string) {
	t.Helper()
	_, err := NormalizeConfig(c)
	var ve *ValidationError
	if !errors.As(err, &ve) || ve.Problems[field] == "" {
		t.Fatalf("expected a problem on %q, got %v", field, err)
	}
}

func TestPresetsAreValid(t *testing.T) {
	if len(Presets()) != 3 {
		t.Fatal("expected three presets")
	}
	for _, p := range Presets() {
		if _, err := NormalizeConfig(p.Config); err != nil {
			t.Fatalf("preset %s: %v", p.Preset, err)
		}
	}
}

func TestNormalizeConfigRules(t *testing.T) {
	c := valid()
	c.AccentColor = "  #1F5FBF "
	got, err := NormalizeConfig(c)
	if err != nil || got.AccentColor != "#1f5fbf" {
		t.Fatalf("colour not normalised: %v %q", err, got.AccentColor)
	}

	bad := func(mut func(*Config), field string) {
		c := valid()
		mut(&c)
		problem(t, c, field)
	}
	bad(func(c *Config) { c.Preset = "fancy" }, "preset")
	bad(func(c *Config) { c.Orientation = "square" }, "orientation")
	bad(func(c *Config) { c.AccentColor = "red" }, "accent_color")
	bad(func(c *Config) { c.AccentColor = "#fff;} body{display:none" }, "accent_color") // CSS injection
	bad(func(c *Config) { c.AccentColor = "#12345g" }, "accent_color")
	bad(func(c *Config) { c.Title = "ab" }, "title")
	bad(func(c *Config) { c.Title = strings.Repeat("x", 81) }, "title")
	bad(func(c *Config) { c.Title = "Bad\x00title" }, "title")
	bad(func(c *Config) { c.Body = "" }, "body")
	bad(func(c *Config) { c.Body = strings.Repeat("a", 601) + "{{student_name}}" }, "body")
	bad(func(c *Config) { c.Body = "No name here" }, "body")
	bad(func(c *Config) { c.Body = "{{student_name}} {{password}}" }, "body")
	bad(func(c *Config) { c.Body = "{{student_name}} {{ .Secret }}" }, "body")
	bad(func(c *Config) { c.Body = "{{student_name}} {{" }, "body")
	bad(func(c *Config) { c.Body = "{{student_name}} }}" }, "body")
	bad(func(c *Config) { c.Body = "{{student_name}} {{student_name" }, "body")
	bad(func(c *Config) { c.SignatoryName = strings.Repeat("x", 101) }, "signatory_name")
	bad(func(c *Config) { c.Footer = "see {{student_name}}" }, "footer")
	bad(func(c *Config) { c.Footer = strings.Repeat("x", 201) }, "footer")
}

func TestPlaceholdersAllowSpacesAndEveryKnownName(t *testing.T) {
	c := valid()
	c.Body = "{{ student_name }}"
	for _, n := range Placeholders {
		c.Body += " {{" + n + "}}"
	}
	if _, err := NormalizeConfig(c); err != nil {
		t.Fatal(err)
	}
}

func TestNormalizeName(t *testing.T) {
	if n, err := NormalizeName("  Diploma  "); err != nil || n != "Diploma" {
		t.Fatalf("%q %v", n, err)
	}
	for _, bad := range []string{"", "a", strings.Repeat("x", 101), "bad\x07"} {
		if _, err := NormalizeName(bad); err == nil {
			t.Fatalf("%q accepted", bad)
		}
	}
}

func TestRenderEscapesEverythingUserControlled(t *testing.T) {
	c := valid()
	c.Title = `<script>alert(1)</script>`
	c.Body = "Hello {{student_name}} <img src=x onerror=alert(2)>\nnext line"
	c.SignatoryName, c.SignatoryTitle = `<b onmouseover=x>`, `"><svg/onload=y>`
	c.Footer = `<iframe src="https://evil.example">`
	d := SampleData
	d.StudentName = `</div><script>steal()</script>`
	d.CourseName = `<a href="javascript:x">`
	out := Render(c, d, true)

	for _, needle := range []string{"<script", "<img", "<iframe", "<svg", "<b "} {
		if strings.Contains(out, needle) {
			t.Fatalf("output contains %q:\n%s", needle, out)
		}
	}
	if !strings.Contains(out, "&lt;script&gt;steal()") || !strings.Contains(out, "<br>next line") {
		t.Fatalf("expected escaped values and line breaks:\n%s", out)
	}
	// Only our own markup may appear: inspect every real tag (escaped text is inert and is not a tag).
	tagRe := regexp.MustCompile(`<([a-zA-Z0-9]+)([^>]*)>`)
	allowed := map[string]bool{"html": true, "head": true, "meta": true, "title": true, "style": true, "body": true,
		"div": true, "br": true, "strong": true}
	for _, m := range tagRe.FindAllStringSubmatch(out, -1) {
		if !allowed[strings.ToLower(m[1])] {
			t.Fatalf("unexpected tag <%s>", m[1])
		}
		if regexp.MustCompile(`(?i)\bon[a-z]+\s*=|\b(src|href)\s*=`).MatchString(m[2]) {
			t.Fatalf("tag <%s%s> has a handler or external reference", m[1], m[2])
		}
	}
	css := out[strings.Index(out, "<style>"):strings.Index(out, "</style>")]
	if regexp.MustCompile(`(?i)url\(|@import|expression\(`).MatchString(css) {
		t.Fatal("external reference in CSS")
	}
	if !strings.Contains(out, "default-src 'none'") {
		t.Fatal("missing CSP")
	}
}

func TestRenderOptions(t *testing.T) {
	c := valid()
	c.ShowGrade, c.ShowCompletion, c.ShowCertificateNo, c.ShowQR = true, true, true, true
	out := Render(c, SampleData, true)
	for _, want := range []string{"Grade: A", "Completed on 15 March 2026", "Certificate no. CERT-000123", "QR code", "Preview with sample data"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q", want)
		}
	}
	c.ShowGrade, c.ShowCompletion, c.ShowCertificateNo, c.ShowQR = false, false, false, false
	out = Render(c, SampleData, false)
	for _, gone := range []string{"Grade:", "Completed on", "Certificate no.", "QR code", "Preview with sample data"} {
		if strings.Contains(out, gone) {
			t.Fatalf("unexpected %q", gone)
		}
	}
	// Empty grade is not shown even when enabled.
	c.ShowGrade = true
	d := SampleData
	d.Grade = " "
	if strings.Contains(Render(c, d, false), "Grade:") {
		t.Fatal("empty grade shown")
	}
	c.Orientation = "portrait"
	if !strings.Contains(Render(c, SampleData, false), "width:210mm;height:297mm") {
		t.Fatal("portrait size")
	}
	c.Orientation = "landscape"
	if !strings.Contains(Render(c, SampleData, false), "width:297mm;height:210mm") {
		t.Fatal("landscape size")
	}
}

func TestRenderEmbedsOnlyTheTrustedQRAndSetsPageSize(t *testing.T) {
	c := valid()
	c.ShowQR = true
	d := SampleData
	d.QRSVG = `<svg id="trusted"></svg>`
	out := Render(c, d, false)
	if !strings.Contains(out, `<div class="qr real"><svg id="trusted"></svg></div>`) || strings.Contains(out, "QR code") {
		t.Fatalf("qr not embedded:\n%s", out)
	}
	if !strings.Contains(out, "@page{size:A4 landscape;margin:0}") {
		t.Fatal("missing @page size")
	}
	c.Orientation = "portrait"
	if !strings.Contains(Render(c, d, false), "@page{size:A4 portrait;margin:0}") {
		t.Fatal("portrait @page")
	}
	// QR hidden when the template does not show it, even if an SVG is supplied.
	c.ShowQR = false
	if strings.Contains(Render(c, d, false), "trusted") {
		t.Fatal("qr shown although disabled")
	}
	// A user-controlled value can never become the QR markup.
	c.ShowQR = true
	d2 := SampleData
	d2.StudentName = `<svg id="evil"></svg>`
	if strings.Contains(Render(c, d2, true), `<svg id="evil">`) {
		t.Fatal("user value rendered as markup")
	}
}
