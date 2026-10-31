package imports

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"io"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Upload limits keep a confirm request bounded (it runs synchronously) and the stored job small.
const (
	MaxBytes = 2 << 20
	MaxRows  = 2000
	MaxCols  = 30
	MaxCell  = 500
)

func detectDelimiter(first string) rune {
	best, bestN := ',', strings.Count(first, ",")
	for _, d := range []rune{';', '\t'} {
		if n := strings.Count(first, string(d)); n > bestN {
			best, bestN = d, n
		}
	}
	return best
}

// ParseCSV parses an uploaded CSV without interpreting any cell: formulas are plain text. The first row is
// the header. Completely blank rows are dropped. It rejects binary/spreadsheet files, non-UTF-8 text,
// control characters (which PostgreSQL's jsonb cannot store), and files over the limits.
func ParseCSV(data []byte) (headers []string, rows [][]string, err error) {
	if len(data) > MaxBytes {
		return nil, nil, &FileError{fmt.Sprintf("The file is larger than %d MB.", MaxBytes>>20)}
	}
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	if bytes.HasPrefix(data, []byte("PK\x03\x04")) || bytes.HasPrefix(data, []byte("\xd0\xcf\x11\xe0")) {
		return nil, nil, &FileError{"Excel files are not supported yet. In Excel choose Save As → CSV UTF-8 and upload that."}
	}
	if !utf8.Valid(data) {
		return nil, nil, &FileError{"The file is not UTF-8 text. In Excel choose Save As → CSV UTF-8."}
	}
	if bytes.IndexByte(data, 0) >= 0 {
		return nil, nil, &FileError{"The file contains binary data and is not a CSV."}
	}
	firstLine := string(data)
	if i := strings.IndexAny(firstLine, "\r\n"); i >= 0 {
		firstLine = firstLine[:i]
	}
	r := csv.NewReader(bytes.NewReader(data))
	r.Comma = detectDelimiter(firstLine)
	r.FieldsPerRecord = -1
	r.TrimLeadingSpace = true
	r.ReuseRecord = false
	var all [][]string
	for {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, nil, &FileError{"The file is not valid CSV (" + shortCSVErr(err) + ")."}
		}
		all = append(all, rec)
		if len(all) > MaxRows+1+MaxBlankRows {
			return nil, nil, &FileError{fmt.Sprintf("The file has more than %d rows. Split it into smaller files.", MaxRows)}
		}
	}
	return shape(all)
}

// MaxBlankRows bounds how many blank lines are read (and dropped) beyond MaxRows.
const MaxBlankRows = 1000

// shape turns raw records (from CSV or a spreadsheet) into headers and rows: it trims cells, drops blank
// rows, enforces the limits, rejects control characters and pads or trims rows to the header width.
func shape(raw [][]string) (headers []string, rows [][]string, err error) {
	var all [][]string
	for _, rec := range raw {
		blank := true
		for i, c := range rec {
			c = strings.TrimSpace(c)
			rec[i] = c
			if c != "" {
				blank = false
			}
			if len([]rune(c)) > MaxCell {
				return nil, nil, &FileError{fmt.Sprintf("A cell is longer than %d characters; this does not look like a student list.", MaxCell)}
			}
			for _, ch := range c {
				if unicode.IsControl(ch) && ch != '\n' && ch != '\t' {
					return nil, nil, &FileError{"The file contains control characters and cannot be imported."}
				}
			}
		}
		if blank {
			continue
		}
		all = append(all, rec)
		if len(all) > MaxRows+1 {
			return nil, nil, &FileError{fmt.Sprintf("The file has more than %d rows. Split it into smaller files.", MaxRows)}
		}
	}
	if len(all) < 2 {
		return nil, nil, &FileError{"The file needs a header row and at least one student row."}
	}
	headers = all[0]
	for len(headers) > 0 && headers[len(headers)-1] == "" {
		headers = headers[:len(headers)-1]
	}
	if len(headers) == 0 || len(headers) > MaxCols {
		return nil, nil, &FileError{fmt.Sprintf("The header row must have between 1 and %d columns.", MaxCols)}
	}
	for i, h := range headers {
		if h == "" {
			headers[i] = fmt.Sprintf("Column %d", i+1)
		}
	}
	for n, rec := range all[1:] {
		if len(rec) > len(headers) {
			for _, c := range rec[len(headers):] {
				if c != "" {
					return nil, nil, &FileError{fmt.Sprintf("Row %d has more cells than the header row.", n+2)}
				}
			}
			rec = rec[:len(headers)]
		}
		for len(rec) < len(headers) {
			rec = append(rec, "")
		}
		rows = append(rows, rec)
	}
	return headers, rows, nil
}

func shortCSVErr(err error) string {
	if pe, ok := err.(*csv.ParseError); ok {
		return fmt.Sprintf("line %d", pe.Line)
	}
	return "unreadable"
}

var headerSynonyms = map[string][]string{
	FieldFullName:       {"fullname", "name", "studentname", "student", "candidate", "candidatename"},
	FieldStudentNumber:  {"studentnumber", "studentid", "studentno", "id", "rollno", "rollnumber", "roll", "enrollmentno", "enrolmentnumber", "enrollmentnumber", "regno", "registrationnumber", "registrationno", "admissionno", "admissionnumber"},
	FieldEmail:          {"email", "emailaddress", "emailid", "mail"},
	FieldPhone:          {"phone", "phonenumber", "mobile", "mobilenumber", "mobileno", "contact", "contactnumber"},
	FieldCourse:         {"course", "coursename", "program", "programme"},
	FieldBatch:          {"batch", "batchname"},
	FieldStatus:         {"status", "coursestatus", "enrollmentstatus"},
	FieldCompletionDate: {"completiondate", "completedon", "dateofcompletion", "datecompleted", "completed"},
	FieldGrade:          {"grade", "result", "gradeorresult"},
}

func normHeader(h string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(h) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// SuggestMapping guesses a mapping from header names. Each column is used at most once and the
// suggestion is only a starting point: the user confirms it.
func SuggestMapping(headers []string) Mapping {
	m := Mapping{}
	used := map[int]bool{}
	for _, f := range Fields {
		for _, syn := range headerSynonyms[f] {
			done := false
			for i, h := range headers {
				if !used[i] && normHeader(h) == syn {
					m[f], used[i], done = i, true, true
					break
				}
			}
			if done {
				break
			}
		}
	}
	return m
}
