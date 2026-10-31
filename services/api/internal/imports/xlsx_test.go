package imports_test

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/certificate-os/api/internal/imports"
)

type xl struct {
	sheet, shared, styles string
	date1904              bool
	extra                 map[string][]byte
}

// build assembles a minimal .xlsx archive.
func build(t *testing.T, x xl) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	put := func(name string, b []byte) {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write(b)
	}
	pr := ""
	if x.date1904 {
		pr = `<workbookPr date1904="1"/>`
	}
	put("xl/workbook.xml", []byte(`<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">`+pr+
		`<sheets><sheet name="Students" sheetId="1" r:id="rId1"/><sheet name="Other" sheetId="2" r:id="rId2"/></sheets></workbook>`))
	put("xl/_rels/workbook.xml.rels", []byte(`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">`+
		`<Relationship Id="rId1" Type="x" Target="worksheets/sheet1.xml"/><Relationship Id="rId2" Type="x" Target="worksheets/sheet2.xml"/></Relationships>`))
	put("xl/worksheets/sheet1.xml", []byte(`<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData>`+x.sheet+`</sheetData></worksheet>`))
	put("xl/worksheets/sheet2.xml", []byte(`<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData><row r="1"><c r="A1" t="inlineStr"><is><t>SECRET</t></is></c></row></sheetData></worksheet>`))
	if x.shared != "" {
		put("xl/sharedStrings.xml", []byte(`<sst xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">`+x.shared+`</sst>`))
	}
	if x.styles != "" {
		put("xl/styles.xml", []byte(`<styleSheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">`+x.styles+`</styleSheet>`))
	}
	for k, v := range x.extra {
		put(k, v)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

const styles = `<numFmts count="1"><numFmt numFmtId="164" formatCode="dd\-mmm\-yyyy"/></numFmts>` +
	`<cellXfs count="3"><xf numFmtId="0"/><xf numFmtId="14"/><xf numFmtId="164"/></cellXfs>`

func TestParseXLSX(t *testing.T) {
	t.Run("shared and inline strings, gaps, dates, numbers, formulas, rich text; only the first sheet", func(t *testing.T) {
		data := build(t, xl{
			shared: `<si><t>Name</t></si><si><t>Email</t></si><si><t>Completion date</t></si><si><t>Phone</t></si>` +
				`<si><t>Asha Rao</t></si><si><r><t>Ra</t></r><r><t>vi</t><rPh><t>PHONETIC</t></rPh></r></si>`,
			styles: styles,
			sheet: `<row r="1"><c r="A1" t="s"><v>0</v></c><c r="B1" t="s"><v>1</v></c><c r="C1" t="s"><v>2</v></c><c r="D1" t="s"><v>3</v></c></row>` +
				// B is empty (gap); C is a date (style 1 = built-in 14): 46000 = 2025-12-09; D is a number
				`<row r="2"><c r="A2" t="s"><v>4</v></c><c r="C2" s="1"><v>46000</v></c><c r="D2"><v>9876543210</v></c></row>` +
				// custom date format 164; exponent notation number; inline string; formula with cached string
				`<row r="3"><c r="A3" t="s"><v>5</v></c><c r="B3" t="inlineStr"><is><t>r@x.com</t></is></c><c r="C3" s="2"><v>45000.75</v></c><c r="D3"><v>9.19876543210E+11</v></c></row>` +
				`<row r="4"><c r="A4" t="str"><f>A2&amp;"!"</f><v>Asha Rao!</v></c><c r="B4" t="e"><v>#N/A</v></c><c r="C4" t="b"><v>1</v></c></row>`,
		})
		h, rows, err := imports.ParseXLSX(data)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Join(h, "|") != "Name|Email|Completion date|Phone" {
			t.Fatalf("headers %v", h)
		}
		want := [][]string{
			{"Asha Rao", "", "2025-12-09", "9876543210"},
			{"Ravi", "r@x.com", "2023-03-15", "919876543210"},
			{"Asha Rao!", "", "TRUE", ""},
		}
		if fmt.Sprint(rows) != fmt.Sprint(want) {
			t.Fatalf("rows\n got %v\nwant %v", rows, want)
		}
		if strings.Contains(fmt.Sprint(h, rows), "SECRET") || strings.Contains(fmt.Sprint(rows), "PHONETIC") {
			t.Fatal("second sheet or phonetic text leaked")
		}
	})

	t.Run("1904 date system", func(t *testing.T) {
		data := build(t, xl{date1904: true, styles: styles,
			sheet: `<row r="1"><c r="A1" t="inlineStr"><is><t>Date</t></is></c></row><row r="2"><c r="A2" s="1"><v>1</v></c></row>`})
		_, rows, err := imports.ParseXLSX(data)
		if err != nil || rows[0][0] != "1904-01-02" {
			t.Fatalf("%v %v", rows, err)
		}
	})

	t.Run("formulas and macros-looking text are plain text", func(t *testing.T) {
		data := build(t, xl{sheet: `<row r="1"><c r="A1" t="inlineStr"><is><t>Name</t></is></c></row><row r="2"><c r="A2" t="inlineStr"><is><t>=HYPERLINK("http://evil")</t></is></c></row>`})
		_, rows, err := imports.ParseXLSX(data)
		if err != nil || rows[0][0] != `=HYPERLINK("http://evil")` {
			t.Fatalf("%v %v", rows, err)
		}
	})

	t.Run("a zip bomb is refused, not expanded", func(t *testing.T) {
		big := bytes.Repeat([]byte("A"), 30<<20) // compresses to a few KB
		data := build(t, xl{sheet: `<row r="1"><c r="A1"><v>1</v></c></row>`, extra: map[string][]byte{"xl/sharedStrings.xml": big}})
		if len(data) > imports.MaxBytes {
			t.Fatalf("test archive too big: %d", len(data))
		}
		_, _, err := imports.ParseUpload(data)
		var fe *imports.FileError
		if !errors.As(err, &fe) || !strings.Contains(fe.Error(), "too large") {
			t.Fatalf("want a too-large file error, got %v", err)
		}
	})

	t.Run("a workbook without rows below the header, or with too many rows or columns, is refused", func(t *testing.T) {
		onlyHeader := build(t, xl{sheet: `<row r="1"><c r="A1" t="inlineStr"><is><t>Name</t></is></c></row>`})
		if _, _, err := imports.ParseXLSX(onlyHeader); err == nil {
			t.Error("header only must fail")
		}
		var sb strings.Builder
		sb.WriteString(`<row r="1"><c r="A1" t="inlineStr"><is><t>Name</t></is></c></row>`)
		for i := 2; i <= imports.MaxRows+3; i++ {
			fmt.Fprintf(&sb, `<row r="%d"><c r="A%d" t="inlineStr"><is><t>S%d</t></is></c></row>`, i, i, i)
		}
		if _, _, err := imports.ParseXLSX(build(t, xl{sheet: sb.String()})); err == nil || !strings.Contains(err.Error(), "more than") {
			t.Errorf("too many rows: %v", err)
		}
		wide := `<row r="1">`
		for i := 0; i < imports.MaxCols+1; i++ {
			wide += fmt.Sprintf(`<c r="%s1" t="inlineStr"><is><t>h%d</t></is></c>`, colName(i), i)
		}
		wide += `</row><row r="2"><c r="A2"><v>1</v></c></row>`
		if _, _, err := imports.ParseXLSX(build(t, xl{sheet: wide})); err == nil {
			t.Error("too many columns must fail")
		}
		far := `<row r="1"><c r="A1" t="inlineStr"><is><t>N</t></is></c></row><row r="2"><c r="ZZZ2"><v>5</v></c></row>`
		if _, _, err := imports.ParseXLSX(build(t, xl{sheet: far})); err == nil {
			t.Error("data far to the right must fail")
		}
	})

	t.Run("not a workbook, damaged XML, and old .xls give clear errors", func(t *testing.T) {
		var buf bytes.Buffer
		zw := zip.NewWriter(&buf)
		w, _ := zw.Create("hello.txt")
		_, _ = w.Write([]byte("hi"))
		_ = zw.Close()
		for name, data := range map[string][]byte{
			"plain zip":   buf.Bytes(),
			"junk":        []byte("PK\x03\x04junk"),
			"damaged xml": build(t, xl{sheet: `<row r="1"><c r="A1" t="inlineStr"><is><t>x</row>`}),
			"old xls":     []byte("\xd0\xcf\x11\xe0abc"),
		} {
			_, _, err := imports.ParseUpload(data)
			var fe *imports.FileError
			if !errors.As(err, &fe) {
				t.Errorf("%s: want a FileError, got %v", name, err)
			}
		}
	})

	t.Run("CSV still goes through ParseUpload", func(t *testing.T) {
		h, rows, err := imports.ParseUpload([]byte("Name,Email\nAsha,a@x.com\n"))
		if err != nil || len(h) != 2 || len(rows) != 1 {
			t.Fatalf("%v %v %v", h, rows, err)
		}
	})
}

func colName(i int) string {
	s := ""
	for i++; i > 0; i = (i - 1) / 26 {
		s = string(rune('A'+(i-1)%26)) + s
	}
	return s
}

func TestParseDateAcceptsISOForEveryFormat(t *testing.T) {
	for _, f := range []string{"iso", "dmy", "mdy"} {
		if d, ok := imports.ParseDate("2025-12-09", f); !ok || d != "2025-12-09" {
			t.Errorf("%s: %q %v", f, d, ok)
		}
	}
	if _, ok := imports.ParseDate("09/12/2025", "iso"); ok {
		t.Error("dmy text must still be rejected under iso")
	}
}
