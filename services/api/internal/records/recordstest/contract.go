package recordstest

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/certificate-os/api/internal/audit"
	"github.com/certificate-os/api/internal/records"
	"github.com/certificate-os/api/internal/tenancy"
)

// Harness wires one store implementation into the shared contract.
type Harness struct {
	Store records.Store
	// NewScope returns the Scope of a brand-new institute (a real row for Postgres).
	NewScope func() tenancy.Scope
	// AuditActions lists audit actions recorded for an entity, oldest first.
	AuditActions func(entityType, entityID string) []string
	// AuditTotal counts every audit row written for an institute (to prove failed calls write nothing).
	AuditTotal func(instituteID string) int
}

func ev(action, entityType string) audit.Event {
	return audit.Event{ActorType: audit.ActorSystem, Action: action, EntityType: entityType}
}

func is(t *testing.T, err, want error, what string) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("%s: got %v, want %v", what, err, want)
	}
}

func conflictField(err error) string {
	var c *records.ConflictError
	if errors.As(err, &c) {
		return c.Field
	}
	return ""
}

// RunStoreContract is the executable specification every records.Store must satisfy.
func RunStoreContract(t *testing.T, newHarness func(t *testing.T) Harness) {
	ctx := context.Background()
	must := func(t *testing.T, err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	course := func(t *testing.T, h Harness, s tenancy.Scope, name string) records.Course {
		t.Helper()
		c, err := h.Store.CreateCourse(ctx, s, records.CourseInput{Name: name}, ev("course.created", "course"))
		must(t, err)
		return c
	}
	batch := func(t *testing.T, h Harness, s tenancy.Scope, courseID, name string) records.Batch {
		t.Helper()
		b, err := h.Store.CreateBatch(ctx, s, records.BatchInput{CourseID: courseID, Name: name, StartDate: "2026-01-10", EndDate: "2026-06-30"}, ev("batch.created", "batch"))
		must(t, err)
		return b
	}
	student := func(t *testing.T, h Harness, s tenancy.Scope, name, number string) records.Student {
		t.Helper()
		st, err := h.Store.CreateStudent(ctx, s, records.StudentInput{FullName: name, StudentNumber: number, Email: "x@example.com"}, ev("student.created", "student"))
		must(t, err)
		return st
	}

	t.Run("courses: create, read, uniqueness, archive and restore", func(t *testing.T) {
		h := newHarness(t)
		a := h.NewScope()
		c := course(t, h, a, "Web Development")
		got, err := h.Store.GetCourse(ctx, a, c.ID)
		must(t, err)
		if got.Name != "Web Development" || got.ArchivedAt != nil || got.CreatedAt.IsZero() {
			t.Fatalf("unexpected course %+v", got)
		}
		_, err = h.Store.CreateCourse(ctx, a, records.CourseInput{Name: "web development"}, ev("course.created", "course"))
		if conflictField(err) != "name" {
			t.Fatalf("case-insensitive duplicate name must conflict on name, got %v", err)
		}
		c2, err := h.Store.CreateCourse(ctx, a, records.CourseInput{Name: "Accounting", Code: "ACC-1"}, ev("course.created", "course"))
		must(t, err)
		_, err = h.Store.CreateCourse(ctx, a, records.CourseInput{Name: "Bookkeeping", Code: "ACC-1"}, ev("course.created", "course"))
		if conflictField(err) != "code" {
			t.Fatalf("duplicate code must conflict on code, got %v", err)
		}
		up, err := h.Store.UpdateCourse(ctx, a, c2.ID, records.CourseInput{Name: "Accounting Basics", Code: "ACC-1", Description: "d"}, ev("course.updated", "course"))
		must(t, err)
		if up.Name != "Accounting Basics" || up.Description != "d" {
			t.Fatalf("update not applied: %+v", up)
		}
		// Archive hides from the default list, shows in archived/all; the name becomes reusable.
		_, err = h.Store.SetCourseArchived(ctx, a, c.ID, true, ev("course.archived", "course"))
		must(t, err)
		active, _ := h.Store.ListCourses(ctx, a, records.Page{Limit: 50})
		if len(active) != 1 || active[0].ID != c2.ID {
			t.Fatalf("default list must hide archived: %+v", active)
		}
		arch, _ := h.Store.ListCourses(ctx, a, records.Page{Archived: records.ArchivedOnly, Limit: 50})
		all, _ := h.Store.ListCourses(ctx, a, records.Page{Archived: records.IncludeAll, Limit: 50})
		if len(arch) != 1 || arch[0].ID != c.ID || len(all) != 2 {
			t.Fatalf("archived/all filters wrong: %d %d", len(arch), len(all))
		}
		_, err = h.Store.UpdateCourse(ctx, a, c.ID, records.CourseInput{Name: "Renamed"}, ev("course.updated", "course"))
		is(t, err, records.ErrArchived, "update archived course")
		reuse, err := h.Store.CreateCourse(ctx, a, records.CourseInput{Name: "Web Development"}, ev("course.created", "course"))
		must(t, err)
		_, err = h.Store.SetCourseArchived(ctx, a, c.ID, false, ev("course.restored", "course"))
		if conflictField(err) != "name" {
			t.Fatalf("restoring over a newer active course with the same name must conflict, got %v", err)
		}
		must(t, func() error {
			_, e := h.Store.SetCourseArchived(ctx, a, reuse.ID, true, ev("course.archived", "course"))
			return e
		}())
		_, err = h.Store.SetCourseArchived(ctx, a, c.ID, false, ev("course.restored", "course"))
		must(t, err)
	})

	t.Run("courses: search, paging and ordering", func(t *testing.T) {
		h := newHarness(t)
		a := h.NewScope()
		for _, n := range []string{"Zebra Studies", "alpha basics", "Beta Advanced"} {
			course(t, h, a, n)
		}
		list, _ := h.Store.ListCourses(ctx, a, records.Page{Limit: 50})
		if len(list) != 3 || list[0].Name != "alpha basics" || list[2].Name != "Zebra Studies" {
			t.Fatalf("must sort case-insensitively by name: %+v", list)
		}
		q, _ := h.Store.ListCourses(ctx, a, records.Page{Q: "BETA", Limit: 50})
		if len(q) != 1 || q[0].Name != "Beta Advanced" {
			t.Fatalf("search must be case-insensitive: %+v", q)
		}
		p1, _ := h.Store.ListCourses(ctx, a, records.Page{Limit: 2})
		if len(p1) != 3 { // Limit+1 rows signal "more exist"
			t.Fatalf("store must return Limit+1 rows when more exist, got %d", len(p1))
		}
		p2, _ := h.Store.ListCourses(ctx, a, records.Page{Limit: 2, Offset: 2})
		if len(p2) != 1 || p2[0].Name != "Zebra Studies" {
			t.Fatalf("second page wrong: %+v", p2)
		}
		// LIKE wildcards in the search text are literals, not patterns.
		pct, _ := h.Store.ListCourses(ctx, a, records.Page{Q: "%", Limit: 50})
		und, _ := h.Store.ListCourses(ctx, a, records.Page{Q: "_", Limit: 50})
		if len(pct) != 0 || len(und) != 0 {
			t.Fatalf("wildcards must be escaped: %d %d", len(pct), len(und))
		}
	})

	t.Run("batches: need an active course, per-course names, archive rules", func(t *testing.T) {
		h := newHarness(t)
		a := h.NewScope()
		c := course(t, h, a, "Python")
		other := course(t, h, a, "Java")
		b := batch(t, h, a, c.ID, "Morning 2026")
		if b.CourseName != "Python" || b.StartDate != "2026-01-10" || b.EndDate != "2026-06-30" {
			t.Fatalf("batch view wrong: %+v", b)
		}
		_, err := h.Store.CreateBatch(ctx, a, records.BatchInput{CourseID: c.ID, Name: "morning 2026"}, ev("batch.created", "batch"))
		if conflictField(err) != "name" {
			t.Fatalf("duplicate batch name in one course: %v", err)
		}
		batch(t, h, a, other.ID, "Morning 2026") // same name, different course: fine
		_, err = h.Store.CreateBatch(ctx, a, records.BatchInput{CourseID: "no-such-course", Name: "X1"}, ev("batch.created", "batch"))
		is(t, err, records.ErrBadReference, "unknown course")
		_, err = h.Store.SetCourseArchived(ctx, a, c.ID, true, ev("course.archived", "course"))
		is(t, err, records.ErrHasActiveItems, "archive course with active batch")
		up, err := h.Store.UpdateBatch(ctx, a, b.ID, records.BatchInput{CourseID: other.ID, Name: "Evening 2026"}, ev("batch.updated", "batch"))
		must(t, err)
		if up.CourseID != c.ID || up.Name != "Evening 2026" {
			t.Fatalf("a batch must never move to another course: %+v", up)
		}
		list, _ := h.Store.ListBatches(ctx, a, records.BatchFilter{Page: records.Page{Limit: 50}, CourseID: c.ID})
		if len(list) != 1 || list[0].ID != b.ID {
			t.Fatalf("course filter wrong: %+v", list)
		}
		_, err = h.Store.SetBatchArchived(ctx, a, b.ID, true, ev("batch.archived", "batch"))
		must(t, err)
		_, err = h.Store.SetCourseArchived(ctx, a, c.ID, true, ev("course.archived", "course"))
		must(t, err)
		_, err = h.Store.CreateBatch(ctx, a, records.BatchInput{CourseID: c.ID, Name: "New"}, ev("batch.created", "batch"))
		is(t, err, records.ErrArchived, "batch under archived course")
		_, err = h.Store.SetBatchArchived(ctx, a, b.ID, false, ev("batch.restored", "batch"))
		is(t, err, records.ErrArchived, "restore batch under archived course")
	})

	t.Run("students: optional unique number, search, detail", func(t *testing.T) {
		h := newHarness(t)
		a := h.NewScope()
		s1 := student(t, h, a, "Asha Rao", "S-001")
		student(t, h, a, "Ben Carter", "")
		student(t, h, a, "Chen Wei", "") // many students may lack a number
		_, err := h.Store.CreateStudent(ctx, a, records.StudentInput{FullName: "Dup", StudentNumber: "S-001"}, ev("student.created", "student"))
		if conflictField(err) != "student_number" {
			t.Fatalf("duplicate student number: %v", err)
		}
		_, err = h.Store.SetStudentArchived(ctx, a, s1.ID, true, ev("student.archived", "student"))
		must(t, err)
		_, err = h.Store.CreateStudent(ctx, a, records.StudentInput{FullName: "Reuse", StudentNumber: "S-001"}, ev("student.created", "student"))
		if conflictField(err) != "student_number" {
			t.Fatalf("an archived student keeps its number reserved: %v", err)
		}
		_, err = h.Store.UpdateStudent(ctx, a, s1.ID, records.StudentInput{FullName: "Asha R"}, ev("student.updated", "student"))
		is(t, err, records.ErrArchived, "update archived student")
		_, err = h.Store.SetStudentArchived(ctx, a, s1.ID, false, ev("student.restored", "student"))
		must(t, err)
		by, _ := h.Store.ListStudents(ctx, a, records.StudentFilter{Page: records.Page{Q: "s-00", Limit: 50}})
		if len(by) != 1 || by[0].ID != s1.ID {
			t.Fatalf("search by student number: %+v", by)
		}
		by, _ = h.Store.ListStudents(ctx, a, records.StudentFilter{Page: records.Page{Q: "CARTER", Limit: 50}})
		if len(by) != 1 || by[0].FullName != "Ben Carter" {
			t.Fatalf("search by name: %+v", by)
		}
		got, err := h.Store.GetStudent(ctx, a, s1.ID)
		must(t, err)
		if got.Enrollments == nil {
			t.Fatal("GetStudent must return a non-nil (possibly empty) enrollment list")
		}
	})

	t.Run("enrollments: lifecycle, completion rules, filters", func(t *testing.T) {
		h := newHarness(t)
		a := h.NewScope()
		c := course(t, h, a, "Welding")
		b1, b2 := batch(t, h, a, c.ID, "Batch 1"), batch(t, h, a, c.ID, "Batch 2")
		st, st2 := student(t, h, a, "Asha Rao", ""), student(t, h, a, "Ben Carter", "")
		e, err := h.Store.AddEnrollment(ctx, a, st.ID, b1.ID, ev("enrollment.created", "enrollment"))
		must(t, err)
		if e.Status != records.Enrolled || e.BatchName != "Batch 1" || e.CourseName != "Welding" || e.CourseID != c.ID {
			t.Fatalf("enrollment view wrong: %+v", e)
		}
		_, err = h.Store.AddEnrollment(ctx, a, st.ID, b1.ID, ev("enrollment.created", "enrollment"))
		if conflictField(err) != "batch_id" {
			t.Fatalf("duplicate enrollment: %v", err)
		}
		_, err = h.Store.AddEnrollment(ctx, a, st.ID, "no-such-batch", ev("enrollment.created", "enrollment"))
		is(t, err, records.ErrBadReference, "unknown batch")
		_, err = h.Store.AddEnrollment(ctx, a, "no-such-student", b1.ID, ev("enrollment.created", "enrollment"))
		is(t, err, records.ErrNotFound, "unknown student")

		_, err = h.Store.SetBatchArchived(ctx, a, b1.ID, true, ev("batch.archived", "batch"))
		is(t, err, records.ErrHasActiveItems, "archive batch with enrolled students")

		completed := records.Completed
		var ve *records.ValidationError
		_, err = h.Store.UpdateEnrollment(ctx, a, st.ID, e.ID, records.EnrollmentPatch{Status: &completed}, ev("enrollment.updated", "enrollment"))
		if !errors.As(err, &ve) || ve.Problems["completion_date"] == "" {
			t.Fatalf("completed without a date must be a validation error, got %v", err)
		}
		date, grade := "2026-06-30", "A"
		got, err := h.Store.UpdateEnrollment(ctx, a, st.ID, e.ID, records.EnrollmentPatch{Status: &completed, CompletionDate: &date, Grade: &grade}, ev("enrollment.updated", "enrollment"))
		must(t, err)
		if got.Status != records.Completed || got.CompletionDate != date || got.Grade != "A" {
			t.Fatalf("completion not applied: %+v", got)
		}
		enrolled := records.Enrolled
		got, err = h.Store.UpdateEnrollment(ctx, a, st.ID, e.ID, records.EnrollmentPatch{Status: &enrolled}, ev("enrollment.updated", "enrollment"))
		must(t, err)
		if got.CompletionDate != "" {
			t.Fatalf("moving away from completed must clear the date: %+v", got)
		}
		_, err = h.Store.UpdateEnrollment(ctx, a, st2.ID, e.ID, records.EnrollmentPatch{Grade: &grade}, ev("enrollment.updated", "enrollment"))
		is(t, err, records.ErrNotFound, "enrollment of another student")
		withdrawn := records.Withdrawn
		_, err = h.Store.UpdateEnrollment(ctx, a, st.ID, e.ID, records.EnrollmentPatch{Status: &withdrawn}, ev("enrollment.updated", "enrollment"))
		must(t, err)
		_, err = h.Store.SetBatchArchived(ctx, a, b1.ID, true, ev("batch.archived", "batch"))
		must(t, err) // nobody is still "enrolled"
		_, err = h.Store.AddEnrollment(ctx, a, st2.ID, b1.ID, ev("enrollment.created", "enrollment"))
		is(t, err, records.ErrArchived, "enroll into archived batch")

		_, err = h.Store.AddEnrollment(ctx, a, st2.ID, b2.ID, ev("enrollment.created", "enrollment"))
		must(t, err)
		f := func(sf records.StudentFilter) int {
			sf.Limit = 50
			l, err := h.Store.ListStudents(ctx, a, sf)
			must(t, err)
			return len(l)
		}
		if f(records.StudentFilter{BatchID: b2.ID}) != 1 || f(records.StudentFilter{CourseID: c.ID}) != 2 ||
			f(records.StudentFilter{Status: records.Withdrawn}) != 1 || f(records.StudentFilter{BatchID: b2.ID, Status: records.Withdrawn}) != 0 {
			t.Fatal("student filters by batch/course/status are wrong")
		}
		full, err := h.Store.GetStudent(ctx, a, st.ID)
		must(t, err)
		if len(full.Enrollments) != 1 || full.Enrollments[0].Status != records.Withdrawn {
			t.Fatalf("detail must list enrollments: %+v", full.Enrollments)
		}
		// Archived students cannot be enrolled.
		arch := student(t, h, a, "Old Student", "")
		_, err = h.Store.SetStudentArchived(ctx, a, arch.ID, true, ev("student.archived", "student"))
		must(t, err)
		_, err = h.Store.AddEnrollment(ctx, a, arch.ID, b2.ID, ev("enrollment.created", "enrollment"))
		is(t, err, records.ErrArchived, "enroll archived student")
	})

	t.Run("isolation: another institute cannot see or touch these records", func(t *testing.T) {
		h := newHarness(t)
		a, b := h.NewScope(), h.NewScope()
		c := course(t, h, a, "Secret Course")
		bt := batch(t, h, a, c.ID, "Secret Batch")
		st := student(t, h, a, "Secret Student", "A-1")
		e, err := h.Store.AddEnrollment(ctx, a, st.ID, bt.ID, ev("enrollment.created", "enrollment"))
		must(t, err)

		_, err = h.Store.GetCourse(ctx, b, c.ID)
		is(t, err, records.ErrNotFound, "B reads A's course")
		_, err = h.Store.GetBatch(ctx, b, bt.ID)
		is(t, err, records.ErrNotFound, "B reads A's batch")
		_, err = h.Store.GetStudent(ctx, b, st.ID)
		is(t, err, records.ErrNotFound, "B reads A's student")
		_, err = h.Store.UpdateCourse(ctx, b, c.ID, records.CourseInput{Name: "Hijacked"}, ev("course.updated", "course"))
		is(t, err, records.ErrNotFound, "B updates A's course")
		_, err = h.Store.UpdateStudent(ctx, b, st.ID, records.StudentInput{FullName: "Hijacked"}, ev("student.updated", "student"))
		is(t, err, records.ErrNotFound, "B updates A's student")
		_, err = h.Store.SetStudentArchived(ctx, b, st.ID, true, ev("student.archived", "student"))
		is(t, err, records.ErrNotFound, "B archives A's student")
		_, err = h.Store.SetCourseArchived(ctx, b, c.ID, true, ev("course.archived", "course"))
		is(t, err, records.ErrNotFound, "B archives A's course")
		_, err = h.Store.UpdateEnrollment(ctx, b, st.ID, e.ID, records.EnrollmentPatch{}, ev("enrollment.updated", "enrollment"))
		is(t, err, records.ErrNotFound, "B updates A's enrollment")
		// B cannot attach its own records to A's, nor A's to B's.
		_, err = h.Store.CreateBatch(ctx, b, records.BatchInput{CourseID: c.ID, Name: "Smuggled"}, ev("batch.created", "batch"))
		is(t, err, records.ErrBadReference, "B creates a batch under A's course")
		bs := student(t, h, b, "B Student", "")
		_, err = h.Store.AddEnrollment(ctx, b, bs.ID, bt.ID, ev("enrollment.created", "enrollment"))
		is(t, err, records.ErrBadReference, "B enrolls into A's batch")
		_, err = h.Store.AddEnrollment(ctx, b, st.ID, bt.ID, ev("enrollment.created", "enrollment"))
		is(t, err, records.ErrNotFound, "B enrolls A's student")

		for name, n := range map[string]int{
			"courses":  len(must2(h.Store.ListCourses(ctx, b, records.Page{Archived: records.IncludeAll, Limit: 50}))),
			"batches":  len(must2(h.Store.ListBatches(ctx, b, records.BatchFilter{Page: records.Page{Archived: records.IncludeAll, Limit: 50}}))),
			"students": len(must2(h.Store.ListStudents(ctx, b, records.StudentFilter{Page: records.Page{Archived: records.IncludeAll, Limit: 50, Q: "Secret"}}))),
		} {
			if n != 0 {
				t.Fatalf("B listed %d of A's %s", n, name)
			}
		}
		// Uniqueness is per institute, not global.
		if _, err := h.Store.CreateCourse(ctx, b, records.CourseInput{Name: "Secret Course"}, ev("course.created", "course")); err != nil {
			t.Fatalf("same course name in another institute must be allowed: %v", err)
		}
		if _, err := h.Store.CreateStudent(ctx, b, records.StudentInput{FullName: "Same Number", StudentNumber: "A-1"}, ev("student.created", "student")); err != nil {
			t.Fatalf("same student number in another institute must be allowed: %v", err)
		}
		// A's data is untouched.
		got, _ := h.Store.GetCourse(ctx, a, c.ID)
		if got.Name != "Secret Course" || got.ArchivedAt != nil {
			t.Fatalf("A's course was modified: %+v", got)
		}
	})

	t.Run("audit: one event per change, same transaction, nothing on failure", func(t *testing.T) {
		h := newHarness(t)
		a := h.NewScope()
		c := course(t, h, a, "Audited")
		if acts := h.AuditActions("course", c.ID); len(acts) != 1 || acts[0] != "course.created" {
			t.Fatalf("create must record exactly one event, got %v", acts)
		}
		before := h.AuditTotal(a.InstituteID())
		_, err := h.Store.CreateCourse(ctx, a, records.CourseInput{Name: "audited"}, ev("course.created", "course"))
		if conflictField(err) != "name" {
			t.Fatalf("expected conflict, got %v", err)
		}
		_, err = h.Store.GetCourse(ctx, a, "nope")
		is(t, err, records.ErrNotFound, "get")
		_, err = h.Store.UpdateCourse(ctx, a, "nope", records.CourseInput{Name: "Whatever"}, ev("course.updated", "course"))
		is(t, err, records.ErrNotFound, "update")
		_, err = h.Store.CreateBatch(ctx, a, records.BatchInput{CourseID: "nope", Name: "B"}, ev("batch.created", "batch"))
		is(t, err, records.ErrBadReference, "bad ref")
		if after := h.AuditTotal(a.InstituteID()); after != before {
			t.Fatalf("failed calls must not write audit events (%d -> %d)", before, after)
		}
		_, err = h.Store.UpdateCourse(ctx, a, c.ID, records.CourseInput{Name: "Audited 2"}, ev("course.updated", "course"))
		must(t, err)
		_, err = h.Store.SetCourseArchived(ctx, a, c.ID, true, ev("course.archived", "course"))
		must(t, err)
		want := fmt.Sprint([]string{"course.created", "course.updated", "course.archived"})
		if got := fmt.Sprint(h.AuditActions("course", c.ID)); got != want {
			t.Fatalf("audit trail %s, want %s", got, want)
		}
	})
}

func must2[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}
