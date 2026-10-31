package imports

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"math"
	"path"
	"strconv"
	"strings"
	"time"
)

// XLSX support uses only the standard library (archive/zip + encoding/xml), so there is no new dependency.
// Safety rules, because a spreadsheet is an archive of attacker-controlled XML:
//   - only the few parts needed are opened (workbook, relationships, shared strings, styles, FIRST sheet);
//   - every part is read through a hard size cap, so a "zip bomb" is refused instead of expanded;
//   - macros, external links, drawings and every other part are never opened;
//   - encoding/xml never resolves external entities;
//   - cells are read as text; formulas are NOT evaluated (the cached value is used) and nothing is executed.
const (
	maxXLSXPart    = 24 << 20 // uncompressed bytes per part
	maxXLSXEntries = 500
	maxXLSXCol     = 200
)

// LooksLikeXLSX reports whether data starts like a ZIP archive.
func LooksLikeXLSX(data []byte) bool { return bytes.HasPrefix(data, []byte("PK\x03\x04")) }

// ParseUpload parses an uploaded CSV or XLSX file.
func ParseUpload(data []byte) (headers []string, rows [][]string, err error) {
	if len(data) > MaxBytes {
		return nil, nil, &FileError{fmt.Sprintf("The file is larger than %d MB.", MaxBytes>>20)}
	}
	if bytes.HasPrefix(data, []byte("\xd0\xcf\x11\xe0")) {
		return nil, nil, &FileError{"Old Excel (.xls) files are not supported. In Excel choose Save As → Excel Workbook (.xlsx) or CSV UTF-8."}
	}
	if LooksLikeXLSX(data) {
		return ParseXLSX(data)
	}
	return ParseCSV(data)
}

func xlsxErr(msg string) error { return &FileError{msg} }

// ParseXLSX reads the first worksheet of an .xlsx workbook. Dates are returned as YYYY-MM-DD.
func ParseXLSX(data []byte) (headers []string, rows [][]string, err error) {
	zr, zerr := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if zerr != nil || len(zr.File) > maxXLSXEntries {
		return nil, nil, xlsxErr("The file is not a valid Excel (.xlsx) workbook.")
	}
	parts := map[string]*zip.File{}
	for _, f := range zr.File {
		parts[path.Clean(strings.TrimPrefix(f.Name, "/"))] = f
	}
	read := func(name string) ([]byte, bool, error) {
		f, ok := parts[name]
		if !ok {
			return nil, false, nil
		}
		if f.UncompressedSize64 > maxXLSXPart {
			return nil, true, xlsxErr("The workbook is too large to import. Save the student list on its own sheet and try again.")
		}
		rc, err := f.Open()
		if err != nil {
			return nil, true, xlsxErr("The file is not a valid Excel (.xlsx) workbook.")
		}
		defer rc.Close()
		b, err := io.ReadAll(io.LimitReader(rc, maxXLSXPart+1))
		if err != nil || len(b) > maxXLSXPart {
			return nil, true, xlsxErr("The workbook is too large to import. Save the student list on its own sheet and try again.")
		}
		return b, true, nil
	}

	wbXML, ok, err := read("xl/workbook.xml")
	if err != nil {
		return nil, nil, err
	}
	if !ok {
		return nil, nil, xlsxErr("The file is not an Excel (.xlsx) workbook.")
	}
	sheetRID, date1904, err := firstSheet(wbXML)
	if err != nil || sheetRID == "" {
		return nil, nil, xlsxErr("The workbook has no worksheet.")
	}
	relXML, _, err := read("xl/_rels/workbook.xml.rels")
	if err != nil {
		return nil, nil, err
	}
	target := relTarget(relXML, sheetRID)
	if target == "" {
		return nil, nil, xlsxErr("The workbook has no readable worksheet.")
	}
	sheetPath := path.Clean(strings.TrimPrefix(target, "/"))
	if !strings.HasPrefix(target, "/") {
		sheetPath = path.Clean("xl/" + target)
	}
	if !strings.HasPrefix(sheetPath, "xl/worksheets/") {
		return nil, nil, xlsxErr("The workbook has no readable worksheet.")
	}
	var shared []string
	if b, ok, err := read("xl/sharedStrings.xml"); err != nil {
		return nil, nil, err
	} else if ok {
		if shared, err = parseShared(b); err != nil {
			return nil, nil, xlsxErr("The workbook's text table is damaged.")
		}
	}
	var dateStyles map[int]bool
	if b, ok, err := read("xl/styles.xml"); err != nil {
		return nil, nil, err
	} else if ok {
		dateStyles = parseDateStyles(b)
	}
	sheet, ok, err := read(sheetPath)
	if err != nil {
		return nil, nil, err
	}
	if !ok {
		return nil, nil, xlsxErr("The workbook has no readable worksheet.")
	}
	raw, err := parseSheet(sheet, shared, dateStyles, date1904)
	if err != nil {
		return nil, nil, err
	}
	return shape(raw)
}

// --- workbook.xml and relationships ---

func attr(se xml.StartElement, local string) string {
	for _, a := range se.Attr {
		if a.Name.Local == local {
			return a.Value
		}
	}
	return ""
}

func firstSheet(b []byte) (rid string, date1904 bool, err error) {
	d := xml.NewDecoder(bytes.NewReader(b))
	for {
		tok, err := d.Token()
		if err == io.EOF {
			return rid, date1904, nil
		}
		if err != nil {
			return "", false, err
		}
		if se, ok := tok.(xml.StartElement); ok {
			switch se.Name.Local {
			case "workbookPr":
				v := attr(se, "date1904")
				date1904 = v == "1" || v == "true"
			case "sheet":
				if rid == "" {
					rid = attr(se, "id")
				}
			}
		}
	}
}

func relTarget(b []byte, id string) string {
	d := xml.NewDecoder(bytes.NewReader(b))
	for {
		tok, err := d.Token()
		if err != nil {
			return ""
		}
		if se, ok := tok.(xml.StartElement); ok && se.Name.Local == "Relationship" && attr(se, "Id") == id {
			return attr(se, "Target")
		}
	}
}

// --- shared strings ---

func parseShared(b []byte) ([]string, error) {
	d := xml.NewDecoder(bytes.NewReader(b))
	var out []string
	var cur strings.Builder
	inSI, inT, skip := false, false, 0
	for {
		tok, err := d.Token()
		if err == io.EOF {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "si":
				inSI = true
				cur.Reset()
			case "rPh": // phonetic hints are not part of the text
				skip++
			case "t":
				inT = inSI && skip == 0
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "si":
				out = append(out, cur.String())
				inSI = false
			case "rPh":
				skip--
			case "t":
				inT = false
			}
		case xml.CharData:
			if inT {
				cur.Write(t)
			}
		}
		if len(out) > 1_000_000 {
			return nil, fmt.Errorf("too many strings")
		}
	}
}

// --- styles: which cell formats are dates ---

var builtinDate = map[int]bool{14: true, 15: true, 16: true, 17: true, 18: true, 19: true, 20: true, 21: true, 22: true, 45: true, 46: true, 47: true}

func parseDateStyles(b []byte) map[int]bool {
	d := xml.NewDecoder(bytes.NewReader(b))
	custom := map[int]bool{}
	var xfs []int
	inCellXfs := false
	for {
		tok, err := d.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "numFmt":
				if id, err := strconv.Atoi(attr(t, "numFmtId")); err == nil {
					custom[id] = isDateFormat(attr(t, "formatCode"))
				}
			case "cellXfs":
				inCellXfs = true
			case "xf":
				if inCellXfs {
					id, _ := strconv.Atoi(attr(t, "numFmtId"))
					xfs = append(xfs, id)
				}
			}
		case xml.EndElement:
			if t.Name.Local == "cellXfs" {
				inCellXfs = false
			}
		}
	}
	out := map[int]bool{}
	for i, id := range xfs {
		if c, ok := custom[id]; ok {
			out[i] = c
		} else {
			out[i] = builtinDate[id]
		}
	}
	return out
}

// isDateFormat reports whether a custom number format shows a date (d, m, y outside quotes and brackets).
func isDateFormat(code string) bool {
	inQuote, inBracket, esc := false, false, false
	for _, r := range code {
		switch {
		case esc:
			esc = false
		case r == '\\' || r == '_' || r == '*':
			esc = true
		case r == '"':
			inQuote = !inQuote
		case inQuote:
		case r == '[':
			inBracket = true
		case r == ']':
			inBracket = false
		case inBracket:
		case r == 'd' || r == 'D' || r == 'y' || r == 'Y':
			return true
		}
	}
	return false
}

// --- worksheet ---

func colIndex(ref string) int {
	n := 0
	for _, r := range ref {
		switch {
		case r >= 'A' && r <= 'Z':
			n = n*26 + int(r-'A') + 1
		case r >= 'a' && r <= 'z':
			n = n*26 + int(r-'a') + 1
		default:
			return n - 1
		}
		if n > 1_000_000 {
			return -1
		}
	}
	return n - 1
}

func serialToDate(f float64, date1904 bool) (string, bool) {
	if math.IsNaN(f) || f < 1 || f > 2958465 { // year 9999
		return "", false
	}
	days := math.Floor(f)
	var epoch time.Time
	if date1904 {
		epoch = time.Date(1904, 1, 1, 0, 0, 0, 0, time.UTC)
	} else {
		epoch = time.Date(1899, 12, 30, 0, 0, 0, 0, time.UTC) // Excel's 1900 leap-year bug is already folded in after Feb 1900
		if days < 61 {
			days++ // serials 1..60 are one day earlier in the bugged calendar
		}
	}
	return epoch.AddDate(0, 0, int(days)).Format("2006-01-02"), true
}

func numberText(v string) string {
	if strings.ContainsAny(v, "eE") {
		if f, err := strconv.ParseFloat(v, 64); err == nil && !math.IsInf(f, 0) {
			return strconv.FormatFloat(f, 'f', -1, 64)
		}
	}
	return v
}

func parseSheet(b []byte, shared []string, dateStyles map[int]bool, date1904 bool) ([][]string, error) {
	d := xml.NewDecoder(bytes.NewReader(b))
	var out [][]string
	var row []string
	rowNum := 0
	var (
		inC, inV, inIS, inT bool
		ctype, ref          string
		style               int
		val                 strings.Builder
		col                 int
	)
	bad := xlsxErr("The worksheet could not be read.")
	for {
		tok, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, bad
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "row":
				row = nil
				if n, err := strconv.Atoi(attr(t, "r")); err == nil && n > 0 {
					if n > MaxRows+1+MaxBlankRows {
						return nil, xlsxErr(fmt.Sprintf("The sheet has more than %d rows. Split it into smaller files.", MaxRows))
					}
					for rowNum < n-1 { // keep blank rows so row numbers match the sheet
						out = append(out, nil)
						rowNum++
					}
				}
			case "c":
				inC, ctype, ref, val = true, attr(t, "t"), attr(t, "r"), strings.Builder{}
				style, _ = strconv.Atoi(attr(t, "s"))
				col = colIndex(ref)
				if col < 0 || col >= maxXLSXCol {
					col = -1
				} else if col < len(row) {
					col = len(row) // a malformed, repeated reference must not overwrite earlier cells
				}
			case "v":
				inV = inC
			case "is":
				inIS = inC
			case "t":
				inT = inC && inIS
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "v":
				inV = false
			case "t":
				inT = false
			case "is":
				inIS = false
			case "c":
				if inC && col >= 0 {
					text := val.String()
					switch ctype {
					case "s":
						if i, err := strconv.Atoi(strings.TrimSpace(text)); err == nil && i >= 0 && i < len(shared) {
							text = shared[i]
						} else {
							text = ""
						}
					case "e": // #N/A, #REF! ...
						text = ""
					case "b":
						if strings.TrimSpace(text) == "1" {
							text = "TRUE"
						} else {
							text = "FALSE"
						}
					case "str", "inlineStr":
					default: // number
						text = strings.TrimSpace(text)
						if text != "" {
							if dateStyles[style] {
								if f, err := strconv.ParseFloat(text, 64); err == nil {
									if ds, ok := serialToDate(f, date1904); ok {
										text = ds
									}
								}
							} else {
								text = numberText(text)
							}
						}
					}
					for len(row) < col {
						row = append(row, "")
					}
					row = append(row, text)
				} else if inC && col < 0 && strings.TrimSpace(val.String()) != "" {
					return nil, xlsxErr("The sheet has data too far to the right to be a student list.")
				}
				inC = false
			case "row":
				out = append(out, row)
				rowNum++
				row = nil
			}
		case xml.CharData:
			if inV || inT {
				val.Write(t)
				if val.Len() > 4*MaxCell {
					return nil, xlsxErr(fmt.Sprintf("A cell is longer than %d characters; this does not look like a student list.", MaxCell))
				}
			}
		}
	}
	return out, nil
}
