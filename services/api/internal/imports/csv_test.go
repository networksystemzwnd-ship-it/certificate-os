package imports_test

import (
	"strings"
	"testing"

	"github.com/certificate-os/api/internal/imports"
)

func TestParseCSV(t *testing.T) {
	t.Run("plain file, BOM, ragged rows and blank lines", func(t *testing.T) {
		h, rows, err := imports.ParseCSV([]byte("\xef\xbb\xbfName,Email,Phone\r\nAsha Rao,asha@x.com\r\n\r\n,,\r\n\"Ravi, K\",r@x.com,98765 43210\r\n"))
		if err != nil {
			t.Fatal(err)
		}
		if len(h) != 3 || h[0] != "Name" {
			t.Fatalf("headers %v", h)
		}
		if len(rows) != 2 || len(rows[0]) != 3 || rows[0][2] != "" || rows[1][0] != "Ravi, K" {
			t.Fatalf("rows %v", rows)
		}
	})
	t.Run("semicolon and tab delimiters are detected", func(t *testing.T) {
		for _, d := range []string{";", "\t"} {
			h, rows, err := imports.ParseCSV([]byte("Name" + d + "Email\nAsha" + d + "a@x.com\n"))
			if err != nil || len(h) != 2 || rows[0][1] != "a@x.com" {
				t.Fatalf("delimiter %q: %v %v %v", d, h, rows, err)
			}
		}
	})
	t.Run("formulas are plain text, never evaluated", func(t *testing.T) {
		_, rows, err := imports.ParseCSV([]byte("Name\n\"=HYPERLINK(\"\"http://evil\"\")\"\n"))
		if err != nil || rows[0][0] != `=HYPERLINK("http://evil")` {
			t.Fatalf("%v %v", rows, err)
		}
	})
	bad := map[string]string{
		"xlsx":            "PK\x03\x04binary",
		"legacy xls":      "\xd0\xcf\x11\xe0binary",
		"not utf8":        "Name\nJos\xe9\n",
		"nul byte":        "Name\nA\x00B\n",
		"control char":    "Name\nA\x07B\n",
		"header only":     "Name,Email\n",
		"empty":           "",
		"too many cols":   strings.Repeat("c,", 40) + "c\nx\n",
		"long cell":       "Name\n" + strings.Repeat("x", 501) + "\n",
		"extra cells":     "Name\nAsha,surprise\n",
		"unterminated \"": "Name\n\"Asha\n",
	}
	for name, in := range bad {
		t.Run("rejects "+name, func(t *testing.T) {
			_, _, err := imports.ParseCSV([]byte(in))
			var fe *imports.FileError
			if err == nil || !asFileError(err, &fe) || fe.Msg == "" {
				t.Fatalf("want a FileError, got %v", err)
			}
		})
	}
	t.Run("rejects too many rows and too large a file", func(t *testing.T) {
		var b strings.Builder
		b.WriteString("Name\n")
		for i := 0; i <= imports.MaxRows; i++ {
			b.WriteString("Student Name\n")
		}
		if _, _, err := imports.ParseCSV([]byte(b.String())); err == nil {
			t.Fatal("more than MaxRows rows must be rejected")
		}
		if _, _, err := imports.ParseCSV(make([]byte, imports.MaxBytes+1)); err == nil {
			t.Fatal("oversized file must be rejected")
		}
	})
}

func asFileError(err error, target **imports.FileError) bool {
	fe, ok := err.(*imports.FileError)
	if ok {
		*target = fe
	}
	return ok
}

func TestSuggestMapping(t *testing.T) {
	m := imports.SuggestMapping([]string{"Roll No.", "Student Name", "E-mail", "Mobile Number", "Course", "Batch", "Result", "Date of Completion", "Notes"})
	want := imports.Mapping{"student_number": 0, "full_name": 1, "email": 2, "phone": 3, "course": 4, "batch": 5, "grade": 6, "completion_date": 7}
	for f, i := range want {
		if got, ok := m[f]; !ok || got != i {
			t.Errorf("%s: got %v want %d", f, m[f], i)
		}
	}
	if len(m) != len(want) {
		t.Errorf("unexpected extra mappings: %v", m)
	}
	// a column is never used twice
	m = imports.SuggestMapping([]string{"Name", "Name"})
	if m["full_name"] != 0 || len(m) != 1 {
		t.Errorf("duplicate header handling: %v", m)
	}
}

func TestParseDate(t *testing.T) {
	cases := []struct {
		in, f, want string
		ok          bool
	}{
		{"2026-03-05", "iso", "2026-03-05", true},
		{"05/03/2026", "dmy", "2026-03-05", true},
		{"5-3-2026", "dmy", "2026-03-05", true},
		{"05.03.2026", "dmy", "2026-03-05", true},
		{"03/05/2026", "mdy", "2026-03-05", true},
		{"13/01/2026", "mdy", "", false}, // month 13: never guess the other order
		{"05/03/26", "dmy", "", false},
		{"2026-02-30", "iso", "", false},
		{"05/03/2026", "iso", "", false},
		{"", "iso", "", true},
		{"0001-01-01", "iso", "", false},
	}
	for _, c := range cases {
		got, ok := imports.ParseDate(c.in, c.f)
		if got != c.want || ok != c.ok {
			t.Errorf("ParseDate(%q,%s) = %q,%v want %q,%v", c.in, c.f, got, ok, c.want, c.ok)
		}
	}
}

func TestNeutralizeFormula(t *testing.T) {
	for in, want := range map[string]string{"=1+1": "'=1+1", "+44": "'+44", "-2": "'-2", "@SUM(A1)": "'@SUM(A1)", "\tx": "'\tx", "Asha": "Asha", "": ""} {
		if got := imports.NeutralizeFormula(in); got != want {
			t.Errorf("%q -> %q want %q", in, got, want)
		}
	}
}
