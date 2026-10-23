package imports

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/certificate-os/api/internal/audit"
	"github.com/certificate-os/api/internal/records"
	"github.com/certificate-os/api/internal/tenancy"
)

// StaleConfirm is how long a job may sit in "confirming" before another confirm may resume it.
const StaleConfirm = 5 * time.Minute

// progressEvery bounds how much work is lost if the process dies mid-confirm.
const progressEvery = 100

type Service struct {
	Store   Store
	Records *records.Service
	Now     func() time.Time
}

func NewService(st Store, rec *records.Service) *Service {
	return &Service{Store: st, Records: rec, Now: time.Now}
}

func event(t tenancy.Tenant, action, reqID string, meta map[string]any) audit.Event {
	return audit.Event{ActorType: audit.ActorUser, ActorUserID: t.UserID, Action: action, EntityType: "student_import", RequestID: reqID, Metadata: meta}
}

// Upload parses a CSV and stores a new job awaiting column mapping. Audit metadata holds counts only:
// never file names or cell values.
func (s *Service) Upload(ctx context.Context, t tenancy.Tenant, filename string, data []byte, reqID string) (Import, error) {
	headers, rows, err := ParseCSV(data)
	if err != nil {
		return Import{}, err
	}
	if len(filename) > 255 {
		filename = filename[:255]
	}
	filename = strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return -1
		}
		return r
	}, filename)
	imp := Import{CreatedBy: t.UserID, Status: Uploaded, Filename: filename, Headers: headers, Rows: rows,
		Mapping: SuggestMapping(headers), DateFormat: "iso", Results: []RowResult{}}
	return s.Store.Create(ctx, t.Scope(), imp, event(t, "student_import.uploaded", reqID, map[string]any{"rows": len(rows), "columns": len(headers)}))
}

func (s *Service) Get(ctx context.Context, t tenancy.Tenant, id string) (Import, error) {
	return s.Store.Get(ctx, t.Scope(), id)
}

func validateMapping(m Mapping, cols int, dateFormat string) error {
	p := map[string]string{}
	known := map[string]bool{}
	for _, f := range Fields {
		known[f] = true
	}
	used := map[int]string{}
	for f, i := range m {
		switch {
		case !known[f]:
			p["mapping"] = "Unknown field " + f + "."
		case i < 0 || i >= cols:
			p[f] = "Choose one of the columns in the file."
		case used[i] != "":
			p[f] = "That column is already used for another field."
		default:
			used[i] = f
		}
	}
	if _, ok := m[FieldFullName]; !ok && p[FieldFullName] == "" {
		p[FieldFullName] = "Choose the column that holds the student's full name."
	}
	if !ValidDateFormat(dateFormat) {
		p["date_format"] = "Choose iso, dmy or mdy."
	}
	if len(p) > 0 {
		return &MappingError{Problems: p}
	}
	return nil
}

type lookups struct {
	batches []records.Batch
	codes   map[string]string
	ex      Existing
}

func (s *Service) load(ctx context.Context, t tenancy.Tenant, imp Import) (lookups, error) {
	var l lookups
	l.codes = map[string]string{}
	for off := 0; off < 5000; off += 100 {
		bs, err := s.Records.ListBatches(ctx, t, records.BatchFilter{Page: records.Page{Limit: 100, Offset: off}})
		if err != nil {
			return l, err
		}
		more := len(bs) > 100
		if more {
			bs = bs[:100]
		}
		l.batches = append(l.batches, bs...)
		if !more {
			break
		}
	}
	for off := 0; off < 5000; off += 100 {
		cs, err := s.Records.ListCourses(ctx, t, records.Page{Limit: 100, Offset: off})
		if err != nil {
			return l, err
		}
		more := len(cs) > 100
		if more {
			cs = cs[:100]
		}
		for _, c := range cs {
			l.codes[c.ID] = c.Code
		}
		if !more {
			break
		}
	}
	nums, mails := Keys(imp)
	existing, err := s.Records.LookupStudents(ctx, t, nums, mails)
	if err != nil {
		return l, err
	}
	l.ex = NewExisting(existing)
	return l, nil
}

// SetMapping validates the mapping, evaluates every row (writing nothing to the student tables) and stores the
// preview. It can be repeated until the import is confirmed.
func (s *Service) SetMapping(ctx context.Context, t tenancy.Tenant, id string, m Mapping, dateFormat, reqID string) (Import, error) {
	imp, err := s.Store.Get(ctx, t.Scope(), id)
	if err != nil {
		return Import{}, err
	}
	if imp.Status != Uploaded && imp.Status != Validated {
		return Import{}, ErrState
	}
	if dateFormat == "" {
		dateFormat = "iso"
	}
	if err := validateMapping(m, len(imp.Headers), dateFormat); err != nil {
		return Import{}, err
	}
	from := imp.Status
	imp.Mapping, imp.DateFormat = m, dateFormat
	l, err := s.load(ctx, t, imp)
	if err != nil {
		return Import{}, err
	}
	imp.Results, _ = Evaluate(imp, l.batches, l.codes, l.ex, nil)
	imp.Counts = Tally(imp.Results)
	imp.Status = Validated
	ev := event(t, "student_import.validated", reqID, map[string]any{"rows": imp.Counts.Total, "valid": imp.Counts.Valid,
		"errors": imp.Counts.Errors, "duplicates": imp.Counts.Duplicates})
	return s.Store.Save(ctx, t.Scope(), imp, from, &ev)
}

// Confirm writes the valid rows. Rows are saved one at a time through the records service (so every
// student keeps its own validation, uniqueness rules and audit event) and progress is stored regularly;
// an interrupted confirm resumes where it stopped and never imports a row twice.
func (s *Service) Confirm(ctx context.Context, t tenancy.Tenant, id, reqID string) (Import, error) {
	imp, err := s.Store.Get(ctx, t.Scope(), id)
	if err != nil {
		return Import{}, err
	}
	switch {
	case imp.Status == Validated:
	case imp.Status == Confirming && s.Now().Sub(imp.UpdatedAt) > StaleConfirm:
	default:
		return Import{}, ErrState
	}
	if _, ok := imp.Mapping[FieldFullName]; !ok {
		return Import{}, ErrState
	}
	from := imp.Status
	imp.Status = Confirming
	if imp, err = s.Store.Save(ctx, t.Scope(), imp, from, nil); err != nil { // claims the job: a concurrent confirm gets ErrState
		return Import{}, err
	}
	l, err := s.load(ctx, t, imp)
	if err != nil {
		return Import{}, err
	}
	results, plans := Evaluate(imp, l.batches, l.codes, l.ex, imp.Results)
	save := func() error {
		imp.Results, imp.Counts = results, Tally(results)
		var e error
		imp, e = s.Store.Save(ctx, t.Scope(), imp, Confirming, nil)
		return e
	}
	done := 0
	for i := range results {
		if results[i].State != RowValid {
			continue
		}
		results[i] = s.importRow(ctx, t, results[i], plans[i], reqID)
		if results[i].State == RowFailed && results[i].Message == "" { // infrastructure failure: stop, keep progress
			results[i].State, results[i].Message = RowValid, ""
			_ = save()
			return Import{}, errors.New("import interrupted; confirm again to resume")
		}
		if done++; done%progressEvery == 0 {
			if err := save(); err != nil {
				return Import{}, err
			}
		}
	}
	counts := Tally(results)
	for i := range results { // keep only the rows an error report needs
		if st := results[i].State; st == RowImported || st == RowValid {
			imp.Rows[i] = []string{}
		}
	}
	now := s.Now()
	imp.Results, imp.Counts, imp.Status, imp.CompletedAt = results, counts, Completed, &now
	ev := event(t, "student_import.completed", reqID, map[string]any{"rows": counts.Total, "imported": counts.Imported,
		"errors": counts.Errors, "duplicates": counts.Duplicates, "failed": counts.Failed})
	return s.Store.Save(ctx, t.Scope(), imp, Confirming, &ev)
}

// importRow writes one student (and enrollment). Row-level problems become the row's result; an
// unexpected infrastructure error is signalled by RowFailed with an empty message.
func (s *Service) importRow(ctx context.Context, t tenancy.Tenant, res RowResult, p Plan, reqID string) RowResult {
	st, err := s.Records.CreateStudent(ctx, t, p.Student, reqID)
	if err != nil {
		var ve *records.ValidationError
		var ce *records.ConflictError
		switch {
		case errors.As(err, &ce):
			res.State, res.Message = RowDuplicate, "A student with this "+strings.ReplaceAll(ce.Field, "_", " ")+" already exists; not changed."
		case errors.As(err, &ve):
			res.State, res.Message = RowError, "This row is no longer valid."
		default:
			res.State, res.Message = RowFailed, ""
		}
		return res
	}
	if p.BatchID != "" {
		en, err := s.Records.Enroll(ctx, t, st.ID, p.BatchID, reqID)
		if err == nil && (p.Status != records.Enrolled || p.Grade != "") {
			patch := records.EnrollmentPatch{Status: &p.Status, Grade: &p.Grade}
			if p.CompletionDate != "" {
				patch.CompletionDate = &p.CompletionDate
			}
			_, err = s.Records.UpdateEnrollment(ctx, t, st.ID, en.ID, patch, reqID)
		}
		if err != nil {
			res.State, res.Message = RowFailed, "The student was created but the course enrollment could not be saved; add it on the student's page."
			return res
		}
	}
	res.State, res.Message = RowImported, ""
	return res
}

// Cancel discards an import that has not been confirmed, including its stored rows.
func (s *Service) Cancel(ctx context.Context, t tenancy.Tenant, id, reqID string) (Import, error) {
	imp, err := s.Store.Get(ctx, t.Scope(), id)
	if err != nil {
		return Import{}, err
	}
	if imp.Status != Uploaded && imp.Status != Validated {
		return Import{}, ErrState
	}
	from := imp.Status
	for i := range imp.Rows {
		imp.Rows[i] = []string{}
	}
	imp.Status = Cancelled
	ev := event(t, "student_import.cancelled", reqID, map[string]any{"rows": len(imp.Rows)})
	return s.Store.Save(ctx, t.Scope(), imp, from, &ev)
}

// ErrorReport builds the downloadable CSV of rows that were not imported: row number, result, the problem,
// then the original cells. Cells that a spreadsheet could treat as a formula are neutralized.
func ErrorReport(imp Import) [][]string {
	out := [][]string{append([]string{"Row", "Result", "Problem"}, imp.Headers...)}
	for i, r := range imp.Results {
		if r.State == RowImported || r.State == RowValid {
			continue
		}
		line := []string{fmt.Sprint(r.Row), string(r.State), r.Message}
		if i < len(imp.Rows) {
			line = append(line, imp.Rows[i]...)
		}
		out = append(out, line)
	}
	for _, rec := range out {
		for i, c := range rec {
			rec[i] = NeutralizeFormula(c)
		}
	}
	return out
}

// NeutralizeFormula prefixes cells that Excel/Sheets would evaluate as a formula (CSV injection).
func NeutralizeFormula(c string) string {
	if c != "" && strings.ContainsRune("=+-@\t\r", rune(c[0])) {
		return "'" + c
	}
	return c
}
