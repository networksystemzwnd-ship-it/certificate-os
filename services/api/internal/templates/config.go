package templates

import (
	"regexp"
	"strings"
	"unicode"
)

// Placeholders is the closed list of values a template can insert. Anything else in {{...}} is rejected,
// so template text can never reference other data.
var Placeholders = []string{"student_name", "course_name", "batch_name", "institute_name",
	"completion_date", "issue_date", "certificate_number", "grade"}

var (
	placeholderRe = regexp.MustCompile(`\{\{([^{}]*)\}\}`)
	colorRe       = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
	presets       = []string{"classic", "modern", "minimal"}
)

// PresetInfo is one ready-made starting point.
type PresetInfo struct {
	Preset string
	Label  string
	Config Config
}

// Presets returns the ready-made starting points (label for the UI, a valid Config to build on).
func Presets() []PresetInfo {
	mk := func(p, accent, title string) Config {
		return Config{Preset: p, Orientation: "landscape", AccentColor: accent, Title: title,
			Body:          "This is to certify that\n{{student_name}}\nhas successfully completed the course\n{{course_name}}\nat {{institute_name}}.",
			SignatoryName: "", SignatoryTitle: "Director",
			ShowCompletion: true, ShowCertificateNo: true, ShowQR: true}
	}
	return []PresetInfo{
		{"classic", "Classic (serif, double border)", mk("classic", "#8a6d1d", "Certificate of Completion")},
		{"modern", "Modern (sans-serif, accent bar)", mk("modern", "#1f5fbf", "Certificate of Achievement")},
		{"minimal", "Minimal (thin border)", mk("minimal", "#333333", "Certificate")},
	}
}

func runes(s string) int { return len([]rune(s)) }

func hasControl(s string, allowNewline bool) bool {
	for _, r := range s {
		if unicode.IsControl(r) && !(allowNewline && r == '\n') {
			return true
		}
	}
	return false
}

func oneOf(v string, set ...string) bool {
	for _, s := range set {
		if v == s {
			return true
		}
	}
	return false
}

func allowedPlaceholder(n string) bool { return oneOf(n, Placeholders...) }

// checkPlaceholders reports whether text only uses known placeholders and has no stray braces.
func checkPlaceholders(text string) bool {
	for _, m := range placeholderRe.FindAllStringSubmatch(text, -1) {
		if !allowedPlaceholder(strings.TrimSpace(m[1])) {
			return false
		}
	}
	rest := placeholderRe.ReplaceAllString(text, "")
	return !strings.Contains(rest, "{{") && !strings.Contains(rest, "}}")
}

func hasPlaceholder(text, name string) bool {
	for _, m := range placeholderRe.FindAllStringSubmatch(text, -1) {
		if strings.TrimSpace(m[1]) == name {
			return true
		}
	}
	return false
}

// NormalizeConfig trims and validates a template configuration.
func NormalizeConfig(c Config) (Config, error) {
	c.Preset = strings.TrimSpace(c.Preset)
	c.Orientation = strings.TrimSpace(c.Orientation)
	c.AccentColor = strings.ToLower(strings.TrimSpace(c.AccentColor))
	c.Title = strings.TrimSpace(c.Title)
	c.Body = strings.TrimSpace(strings.ReplaceAll(c.Body, "\r\n", "\n"))
	c.SignatoryName = strings.TrimSpace(c.SignatoryName)
	c.SignatoryTitle = strings.TrimSpace(c.SignatoryTitle)
	c.Footer = strings.TrimSpace(c.Footer)

	p := map[string]string{}
	if !oneOf(c.Preset, presets...) {
		p["preset"] = "Choose classic, modern or minimal."
	}
	if !oneOf(c.Orientation, "landscape", "portrait") {
		p["orientation"] = "Choose landscape or portrait."
	}
	if !colorRe.MatchString(c.AccentColor) {
		p["accent_color"] = "Use a colour like #1f5fbf."
	}
	if n := runes(c.Title); n < 3 || n > 80 || hasControl(c.Title, false) {
		p["title"] = "Enter a title (3–80 characters)."
	}
	switch n := runes(c.Body); {
	case n < 1 || n > 600 || hasControl(c.Body, true):
		p["body"] = "Enter the certificate text (up to 600 characters)."
	case !checkPlaceholders(c.Body):
		p["body"] = "Only these placeholders are allowed: " + placeholderList() + "."
	case !hasPlaceholder(c.Body, "student_name"):
		p["body"] = "The text must include {{student_name}}."
	}
	if runes(c.SignatoryName) > 100 || hasControl(c.SignatoryName, false) {
		p["signatory_name"] = "Use up to 100 characters."
	}
	if runes(c.SignatoryTitle) > 100 || hasControl(c.SignatoryTitle, false) {
		p["signatory_title"] = "Use up to 100 characters."
	}
	if runes(c.Footer) > 200 || hasControl(c.Footer, false) || strings.Contains(c.Footer, "{{") {
		p["footer"] = "Use up to 200 characters of plain text (no placeholders)."
	}
	if len(p) > 0 {
		return c, &ValidationError{Problems: p}
	}
	return c, nil
}

func placeholderList() string {
	out := make([]string, len(Placeholders))
	for i, n := range Placeholders {
		out[i] = "{{" + n + "}}"
	}
	return strings.Join(out, ", ")
}

// NormalizeName validates a template name (2–100 characters).
func NormalizeName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if n := runes(name); n < 2 || n > 100 || hasControl(name, false) {
		return name, &ValidationError{Problems: map[string]string{"name": "Enter a template name (2–100 characters)."}}
	}
	return name, nil
}
