package templates

import (
	"html"
	"strings"
)

// RenderData is the data a certificate shows. Every value is HTML-escaped on output.
type RenderData struct {
	StudentName, CourseName, BatchName, InstituteName string
	CompletionDate, IssueDate, CertificateNumber      string
	Grade                                             string
}

// SampleData is what previews show (never real student data).
var SampleData = RenderData{StudentName: "Asha Verma", CourseName: "Certificate in Web Design", BatchName: "Batch 2026-A",
	InstituteName: "Your Institute Name", CompletionDate: "15 March 2026", IssueDate: "20 March 2026",
	CertificateNumber: "CERT-000123", Grade: "A"}

func (d RenderData) value(name string) string {
	switch name {
	case "student_name":
		return d.StudentName
	case "course_name":
		return d.CourseName
	case "batch_name":
		return d.BatchName
	case "institute_name":
		return d.InstituteName
	case "completion_date":
		return d.CompletionDate
	case "issue_date":
		return d.IssueDate
	case "certificate_number":
		return d.CertificateNumber
	case "grade":
		return d.Grade
	}
	return ""
}

// fill replaces known placeholders and escapes everything: the literal text AND the values.
func fill(text string, d RenderData) string {
	var b strings.Builder
	last := 0
	for _, loc := range placeholderRe.FindAllStringSubmatchIndex(text, -1) {
		b.WriteString(html.EscapeString(text[last:loc[0]]))
		b.WriteString(html.EscapeString(d.value(strings.TrimSpace(text[loc[2]:loc[3]]))))
		last = loc[1]
	}
	b.WriteString(html.EscapeString(text[last:]))
	return strings.ReplaceAll(b.String(), "\n", "<br>")
}

type look struct{ font, border, titleCase string }

var looks = map[string]look{
	"classic": {font: "Georgia, 'Times New Roman', serif", border: "double 8px", titleCase: "uppercase"},
	"modern":  {font: "'Helvetica Neue', Arial, sans-serif", border: "none", titleCase: "none"},
	"minimal": {font: "Arial, Helvetica, sans-serif", border: "solid 1px", titleCase: "none"},
}

// Render produces a self-contained HTML page for a (validated) configuration. The output contains no
// script, no external resource and no user-controlled markup: user text is escaped, the colour is a
// validated #rrggbb value, and the only other inputs are fixed enums. preview adds a "Preview" caption
// and a QR placeholder (the real QR code is generated at issuance).
func Render(c Config, d RenderData, preview bool) string {
	lk := looks[c.Preset]
	if lk.font == "" {
		lk = looks["minimal"]
	}
	w, h := "297mm", "210mm"
	if c.Orientation == "portrait" {
		w, h = "210mm", "297mm"
	}
	var b strings.Builder
	b.WriteString(`<!doctype html><html lang="en"><head><meta charset="utf-8">`)
	b.WriteString(`<meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'unsafe-inline'">`)
	b.WriteString(`<title>Certificate</title><style>`)
	b.WriteString(`*{box-sizing:border-box}body{margin:0;background:#fff;color:#222;font-family:` + lk.font + `}`)
	b.WriteString(`.page{position:relative;width:` + w + `;height:` + h + `;padding:16mm;text-align:center;`)
	switch c.Preset {
	case "modern":
		b.WriteString(`border-left:14mm solid ` + c.AccentColor + `;`)
	default:
		b.WriteString(`border:` + lk.border + ` ` + c.AccentColor + `;`)
	}
	b.WriteString(`}.title{margin:14mm 0 8mm;font-size:30pt;letter-spacing:1px;color:` + c.AccentColor + `;text-transform:` + lk.titleCase + `}`)
	b.WriteString(`.body{font-size:15pt;line-height:1.7}.meta{margin-top:10mm;font-size:11pt;color:#555}`)
	b.WriteString(`.sign{position:absolute;left:16mm;bottom:16mm;text-align:left;font-size:11pt}.sign .line{width:60mm;border-top:1px solid #333;margin-bottom:2mm}`)
	b.WriteString(`.qr{position:absolute;right:16mm;bottom:16mm;width:24mm;height:24mm;border:1px dashed #999;font:9pt Arial,sans-serif;color:#777;display:flex;align-items:center;justify-content:center}`)
	b.WriteString(`.foot{position:absolute;left:0;right:0;bottom:6mm;font-size:9pt;color:#777}.prev{position:absolute;top:4mm;right:6mm;font:9pt Arial,sans-serif;color:#999}`)
	b.WriteString(`</style></head><body><div class="page">`)
	if preview {
		b.WriteString(`<div class="prev">Preview with sample data</div>`)
	}
	b.WriteString(`<div class="title">` + html.EscapeString(c.Title) + `</div>`)
	b.WriteString(`<div class="body">` + fill(c.Body, d) + `</div>`)
	var meta []string
	if c.ShowCompletion {
		meta = append(meta, "Completed on "+html.EscapeString(d.CompletionDate))
	}
	if c.ShowGrade && strings.TrimSpace(d.Grade) != "" {
		meta = append(meta, "Grade: "+html.EscapeString(d.Grade))
	}
	if c.ShowCertificateNo {
		meta = append(meta, "Certificate no. "+html.EscapeString(d.CertificateNumber))
	}
	if len(meta) > 0 {
		b.WriteString(`<div class="meta">` + strings.Join(meta, " &middot; ") + `</div>`)
	}
	if c.SignatoryName != "" || c.SignatoryTitle != "" {
		b.WriteString(`<div class="sign"><div class="line"></div>`)
		if c.SignatoryName != "" {
			b.WriteString(`<div><strong>` + html.EscapeString(c.SignatoryName) + `</strong></div>`)
		}
		if c.SignatoryTitle != "" {
			b.WriteString(`<div>` + html.EscapeString(c.SignatoryTitle) + `</div>`)
		}
		b.WriteString(`</div>`)
	}
	if c.ShowQR && preview {
		b.WriteString(`<div class="qr">QR code</div>`)
	}
	if c.Footer != "" {
		b.WriteString(`<div class="foot">` + html.EscapeString(c.Footer) + `</div>`)
	}
	b.WriteString(`</div></body></html>`)
	return b.String()
}
