package imports_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/certificate-os/api/internal/audit"
	"github.com/certificate-os/api/internal/audit/audittest"
	"github.com/certificate-os/api/internal/imports"
	"github.com/certificate-os/api/internal/imports/importstest"
	"github.com/certificate-os/api/internal/records"
	"github.com/certificate-os/api/internal/records/recordstest"
	"github.com/certificate-os/api/internal/tenancy"
)

type failingRecords struct {
	records.Store
	creates, failAt int
}

func (f *failingRecords) CreateStudent(ctx context.Context, s tenancy.Scope, in records.StudentInput, ev audit.Event) (records.Student, error) {
	f.creates++
	if f.failAt > 0 && f.creates == f.failAt {
		return records.Student{}, errors.New("database unavailable")
	}
	return f.Store.CreateStudent(ctx, s, in, ev)
}

type world struct {
	t     *testing.T
	w     *audittest.MemWriter
	rec   *records.Service
	recSt *failingRecords
	imp   *imports.Service
	a, b  tenancy.Tenant
	now   time.Time
	batch records.Batch
}

func tn(id string) tenancy.Tenant {
	return tenancy.Tenant{Institute: tenancy.Institute{ID: id, Status: tenancy.InstituteActive}, UserID: "user-" + id}
}

func newWorld(t *testing.T) *world {
	w := audittest.NewMemWriter()
	wd := &world{t: t, w: w, a: tn("inst-a"), b: tn("inst-b"), now: time.Date(2026, 10, 22, 9, 0, 0, 0, time.UTC)}
	wd.recSt = &failingRecords{Store: recordstest.NewMemStore(w)}
	wd.rec = records.NewService(wd.recSt)
	is := importstest.NewMemStore(w)
	is.Now = func() time.Time { return wd.now }
	wd.imp = imports.NewService(is, wd.rec)
	wd.imp.Now = func() time.Time { return wd.now }
	ctx := context.Background()
	c, err := wd.rec.CreateCourse(ctx, wd.a, records.CourseInput{Name: "Web Development", Code: "WD"}, "r")
	if err != nil {
		t.Fatal(err)
	}
	wd.batch, err = wd.rec.CreateBatch(ctx, wd.a, records.BatchInput{CourseID: c.ID, Name: "Jan 2026"}, "r")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := wd.rec.CreateStudent(ctx, wd.a, records.StudentInput{FullName: "Existing One", StudentNumber: "ST-1", Email: "family@example.com"}, "r"); err != nil {
		t.Fatal(err)
	}
	return wd
}

func (wd *world) studentCount(tn tenancy.Tenant) int {
	list, err := wd.rec.ListStudents(context.Background(), tn, records.StudentFilter{Page: records.Page{Limit: 100, Archived: records.IncludeAll}})
	if err != nil {
		wd.t.Fatal(err)
	}
	return len(list)
}

const header = "Name,Roll No,Email,Phone,Course,Batch,Status,Completed On,Grade\n"

func (wd *world) upload(tn tenancy.Tenant, csv string) imports.Import {
	wd.t.Helper()
	imp, err := wd.imp.Upload(context.Background(), tn, "students.csv", []byte(csv), "req-up")
	if err != nil {
		wd.t.Fatal(err)
	}
	return imp
}

func TestImportEndToEnd(t *testing.T) {
	wd := newWorld(t)
	ctx := context.Background()
	csv := header +
		"Asha Rao,ST-10,asha@example.com,98765 43210,Web Development,Jan 2026,completed,15/03/2026,A\n" + // r2 valid, completed
		"Bad Email,ST-11,not-an-email,,,,,,\n" + // r3 error
		"Dup Number,st-1,dup@example.com,,,,,,\n" + // r4 duplicate (case-insensitive number)
		"Repeat Of Asha,ST-10,other@example.com,,,,,,\n" + // r5 duplicate within the file
		"No Batch,ST-12,,,Web Development,Feb 2099,,,\n" + // r6 unknown batch
		"Plain Student,ST-13,plain@example.com,,,,,,\n" + // r7 valid, no enrollment
		"No Date,ST-14,,,WD,Jan 2026,completed,,\n" + // r8 completed without a date
		"Sibling,ST-15,family@example.com,,WD,Jan 2026,,,\n" + // r9 valid: shares an email, different name; enrolled
		"Existing One,,family@example.com,,,,,,\n" // r10 duplicate: same name and email as an existing student
	up := wd.upload(wd.a, csv)
	if up.Mapping["full_name"] != 0 || up.Mapping["student_number"] != 1 || up.Mapping["completion_date"] != 7 || up.Mapping["grade"] != 8 {
		t.Fatalf("suggested mapping: %v", up.Mapping)
	}

	v, err := wd.imp.SetMapping(ctx, wd.a, up.ID, up.Mapping, "dmy", "req-map")
	if err != nil {
		t.Fatal(err)
	}
	want := imports.Counts{Total: 9, Valid: 3, Errors: 3, Duplicates: 3}
	if v.Counts != want {
		t.Fatalf("counts after validation: %+v want %+v\n%+v", v.Counts, want, v.Results)
	}
	states := map[int]imports.RowState{2: imports.RowValid, 3: imports.RowError, 4: imports.RowDuplicate, 5: imports.RowDuplicate,
		6: imports.RowError, 7: imports.RowValid, 8: imports.RowError, 9: imports.RowValid, 10: imports.RowDuplicate}
	for _, r := range v.Results {
		if r.State != states[r.Row] {
			t.Errorf("row %d: state %s (%q), want %s", r.Row, r.State, r.Message, states[r.Row])
		}
		if r.State != imports.RowValid && r.Message == "" {
			t.Errorf("row %d has no explanation", r.Row)
		}
	}
	if !strings.Contains(v.Results[7].Message, "Shares an email") {
		t.Errorf("row 9 should carry the shared-email notice: %q", v.Results[7].Message)
	}
	if got := wd.studentCount(wd.a); got != 1 {
		t.Fatalf("validation must not write students; have %d", got)
	}

	done, err := wd.imp.Confirm(ctx, wd.a, up.ID, "req-confirm")
	if err != nil {
		t.Fatal(err)
	}
	if done.Status != imports.Completed || done.Counts.Imported != 3 || done.Counts.Errors != 3 || done.Counts.Duplicates != 3 || done.CompletedAt == nil {
		t.Fatalf("after confirm: %+v", done)
	}
	if got := wd.studentCount(wd.a); got != 4 {
		t.Fatalf("want 4 students (1 existing + 3 imported), have %d", got)
	}
	found, _ := wd.rec.ListStudents(ctx, wd.a, records.StudentFilter{Page: records.Page{Q: "ST-10"}})
	st, err := wd.rec.GetStudent(ctx, wd.a, found[0].ID)
	if err != nil || len(st.Enrollments) != 1 {
		t.Fatalf("Asha should have one enrollment: %+v %v", st, err)
	}
	if e := st.Enrollments[0]; e.Status != records.Completed || e.CompletionDate != "2026-03-15" || e.Grade != "A" || e.BatchID != wd.batch.ID {
		t.Fatalf("enrollment %+v", e)
	}
	if st.Phone != "98765 43210" || st.Email != "asha@example.com" {
		t.Fatalf("student %+v", st)
	}

	// Rows that need no more attention are discarded; problem rows are kept for the error report.
	for i, r := range done.Results {
		kept := len(done.Rows[i]) > 0
		if (r.State == imports.RowImported) == kept {
			t.Errorf("row %d (%s): kept=%v", r.Row, r.State, kept)
		}
	}
	report := imports.ErrorReport(done)
	if len(report) != 1+6 || report[0][0] != "Row" || report[1][0] != "3" || report[1][1] != "error" || report[1][3] != "Bad Email" {
		t.Fatalf("error report: %v", report)
	}

	if _, err := wd.imp.Confirm(ctx, wd.a, up.ID, "again"); !errors.Is(err, imports.ErrState) {
		t.Fatalf("a finished import cannot be confirmed again: %v", err)
	}
	if _, err := wd.imp.SetMapping(ctx, wd.a, up.ID, up.Mapping, "dmy", "x"); !errors.Is(err, imports.ErrState) {
		t.Fatalf("a finished import cannot be re-mapped: %v", err)
	}

	// Audit: one event per import step plus the ordinary student/enrollment events, none holding personal data.
	var steps []string
	for _, r := range wd.w.Records() {
		if r.EntityType == "student_import" {
			steps = append(steps, r.Action)
		}
		blob, _ := json.Marshal(r.Metadata)
		for _, pii := range []string{"asha", "Asha", "98765", "example.com", "Rao", "ST-1"} {
			if strings.Contains(string(blob), pii) {
				t.Fatalf("audit metadata leaks personal data %q: %s (%s)", pii, blob, r.Action)
			}
		}
	}
	if strings.Join(steps, ",") != "student_import.uploaded,student_import.validated,student_import.completed" {
		t.Fatalf("import audit trail: %v", steps)
	}
	created := 0
	for _, r := range wd.w.Records() {
		if r.Action == "student.created" && r.RequestID == "req-confirm" {
			created++
		}
	}
	if created != 3 {
		t.Fatalf("each imported student must be traceable to the confirm request; got %d", created)
	}
}

func TestMappingValidation(t *testing.T) {
	wd := newWorld(t)
	ctx := context.Background()
	up := wd.upload(wd.a, header+"Asha Rao,ST-10,,,,,,,\n")
	for name, m := range map[string]imports.Mapping{
		"missing name column": {"email": 2},
		"out of range":        {"full_name": 99},
		"negative":            {"full_name": -1},
		"column used twice":   {"full_name": 0, "email": 0},
		"unknown field":       {"full_name": 0, "shoe_size": 3},
	} {
		if _, err := wd.imp.SetMapping(ctx, wd.a, up.ID, m, "iso", "r"); err == nil {
			t.Errorf("%s: want a MappingError", name)
		} else if var_, ok := err.(*imports.MappingError); !ok || len(var_.Problems) == 0 {
			t.Errorf("%s: wrong error %v", name, err)
		}
	}
	if _, err := wd.imp.SetMapping(ctx, wd.a, up.ID, imports.Mapping{"full_name": 0}, "ymd", "r"); err == nil {
		t.Error("bad date format must be rejected")
	}
	if _, err := wd.imp.Confirm(ctx, wd.a, up.ID, "r"); !errors.Is(err, imports.ErrState) {
		t.Errorf("confirm before a valid mapping must fail: %v", err)
	}
	// re-mapping is allowed until confirm and recomputes results
	if v, err := wd.imp.SetMapping(ctx, wd.a, up.ID, imports.Mapping{"full_name": 0}, "iso", "r"); err != nil || v.Counts.Valid != 1 {
		t.Errorf("first mapping: %+v %v", v.Counts, err)
	}
	if v, err := wd.imp.SetMapping(ctx, wd.a, up.ID, imports.Mapping{"full_name": 0, "student_number": 1}, "iso", "r"); err != nil || v.Counts.Valid != 1 {
		t.Errorf("second mapping: %+v %v", v.Counts, err)
	}
}

func TestCancelDiscardsRows(t *testing.T) {
	wd := newWorld(t)
	ctx := context.Background()
	up := wd.upload(wd.a, header+"Asha Rao,ST-10,,,,,,,\n")
	c, err := wd.imp.Cancel(ctx, wd.a, up.ID, "r")
	if err != nil || c.Status != imports.Cancelled {
		t.Fatalf("%+v %v", c, err)
	}
	for _, r := range c.Rows {
		if len(r) != 0 {
			t.Fatal("cancelled import must not retain uploaded rows")
		}
	}
	if _, err := wd.imp.Confirm(ctx, wd.a, up.ID, "r"); !errors.Is(err, imports.ErrState) {
		t.Fatalf("cancelled import cannot be confirmed: %v", err)
	}
	if got := wd.studentCount(wd.a); got != 1 {
		t.Fatalf("cancel wrote students: %d", got)
	}
}

func TestInterruptedConfirmResumesWithoutDuplicates(t *testing.T) {
	wd := newWorld(t)
	ctx := context.Background()
	up := wd.upload(wd.a, "Name\nOne Person\nTwo Person\nThree Person\nFour Person\nFive Person\n") // no numbers or emails: only progress tracking prevents duplicates
	if _, err := wd.imp.SetMapping(ctx, wd.a, up.ID, imports.Mapping{"full_name": 0}, "iso", "r"); err != nil {
		t.Fatal(err)
	}
	wd.recSt.failAt = wd.recSt.creates + 3 // the third create fails with an infrastructure error
	if _, err := wd.imp.Confirm(ctx, wd.a, up.ID, "r1"); err == nil {
		t.Fatal("an infrastructure failure must surface")
	}
	if got := wd.studentCount(wd.a); got != 1+2 {
		t.Fatalf("two rows were imported before the failure; have %d students", got)
	}
	mid, _ := wd.imp.Get(ctx, wd.a, up.ID)
	if mid.Status != imports.Confirming || mid.Counts.Imported != 2 {
		t.Fatalf("progress must be saved: %+v", mid)
	}
	if _, err := wd.imp.Confirm(ctx, wd.a, up.ID, "r2"); !errors.Is(err, imports.ErrState) {
		t.Fatalf("a fresh in-progress confirm must not be taken over: %v", err)
	}
	wd.now = wd.now.Add(imports.StaleConfirm + time.Minute)
	done, err := wd.imp.Confirm(ctx, wd.a, up.ID, "r3")
	if err != nil {
		t.Fatal(err)
	}
	if done.Status != imports.Completed || done.Counts.Imported != 5 {
		t.Fatalf("resume result: %+v", done)
	}
	if got := wd.studentCount(wd.a); got != 1+5 {
		t.Fatalf("resume must import each row exactly once; have %d students", got)
	}
}

func TestImportsNeverCrossInstitutes(t *testing.T) {
	wd := newWorld(t)
	ctx := context.Background()
	upA := wd.upload(wd.a, "Name,Roll\nAsha Rao,ST-1\n") // ST-1 exists in A
	if _, err := wd.imp.Get(ctx, wd.b, upA.ID); !errors.Is(err, imports.ErrNotFound) {
		t.Fatalf("B reads A's import: %v", err)
	}
	for name, f := range map[string]func() error{
		"map": func() error {
			_, e := wd.imp.SetMapping(ctx, wd.b, upA.ID, imports.Mapping{"full_name": 0}, "iso", "r")
			return e
		},
		"confirm": func() error { _, e := wd.imp.Confirm(ctx, wd.b, upA.ID, "r"); return e },
		"cancel":  func() error { _, e := wd.imp.Cancel(ctx, wd.b, upA.ID, "r"); return e },
	} {
		if err := f(); !errors.Is(err, imports.ErrNotFound) {
			t.Errorf("%s from B: want ErrNotFound, got %v", name, err)
		}
	}
	// A's students must not make B's rows duplicates, and B's import lands only in B.
	upB := wd.upload(wd.b, "Name,Roll\nAsha Rao,ST-1\n")
	v, err := wd.imp.SetMapping(ctx, wd.b, upB.ID, upB.Mapping, "iso", "r")
	if err != nil || v.Counts.Valid != 1 || v.Counts.Duplicates != 0 {
		t.Fatalf("B's row must be valid: %+v %v", v.Counts, err)
	}
	if _, err := wd.imp.Confirm(ctx, wd.b, upB.ID, "r"); err != nil {
		t.Fatal(err)
	}
	if a, b := wd.studentCount(wd.a), wd.studentCount(wd.b); a != 1 || b != 1 {
		t.Fatalf("students: A=%d B=%d", a, b)
	}
}
