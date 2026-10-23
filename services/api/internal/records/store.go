package records

import (
	"context"

	"github.com/certificate-os/api/internal/audit"
	"github.com/certificate-os/api/internal/tenancy"
)

// Store persists tenant-owned records. EVERY method takes the Scope and may only ever touch rows of
// that institute; another institute's id is indistinguishable from a missing one (ErrNotFound).
// Mutations run in one transaction with their audit event; the store fills in EntityID and
// InstituteID of the supplied event. A failed mutation records nothing.
//
// List methods return up to Limit+1 rows so the caller can tell whether more exist.
type Store interface {
	CreateCourse(ctx context.Context, s tenancy.Scope, in CourseInput, ev audit.Event) (Course, error)
	GetCourse(ctx context.Context, s tenancy.Scope, id string) (Course, error)
	ListCourses(ctx context.Context, s tenancy.Scope, p Page) ([]Course, error)
	UpdateCourse(ctx context.Context, s tenancy.Scope, id string, in CourseInput, ev audit.Event) (Course, error)
	// SetCourseArchived refuses to archive a course that still has active batches (ErrHasActiveItems).
	SetCourseArchived(ctx context.Context, s tenancy.Scope, id string, archived bool, ev audit.Event) (Course, error)

	// CreateBatch: the course must exist in this institute and not be archived (ErrBadReference / ErrArchived).
	CreateBatch(ctx context.Context, s tenancy.Scope, in BatchInput, ev audit.Event) (Batch, error)
	GetBatch(ctx context.Context, s tenancy.Scope, id string) (Batch, error)
	ListBatches(ctx context.Context, s tenancy.Scope, f BatchFilter) ([]Batch, error)
	// UpdateBatch cannot move a batch to another course (CourseID in the input is ignored).
	UpdateBatch(ctx context.Context, s tenancy.Scope, id string, in BatchInput, ev audit.Event) (Batch, error)
	// SetBatchArchived refuses while students are still enrolled (status enrolled) in it.
	SetBatchArchived(ctx context.Context, s tenancy.Scope, id string, archived bool, ev audit.Event) (Batch, error)

	// CreateStudent: a duplicate student number is a *ConflictError{Field: "student_number"}.
	CreateStudent(ctx context.Context, s tenancy.Scope, in StudentInput, ev audit.Event) (Student, error)
	// GetStudent includes the student's enrollments.
	GetStudent(ctx context.Context, s tenancy.Scope, id string) (Student, error)
	ListStudents(ctx context.Context, s tenancy.Scope, f StudentFilter) ([]Student, error)
	UpdateStudent(ctx context.Context, s tenancy.Scope, id string, in StudentInput, ev audit.Event) (Student, error)
	SetStudentArchived(ctx context.Context, s tenancy.Scope, id string, archived bool, ev audit.Event) (Student, error)

	// LookupStudents returns students (archived included, enrollments not loaded) whose student number
	// (compared case-insensitively) is in numbers or whose email (lowercase) is in emails. Used by import
	// duplicate detection; at most MaxLookupValues values per call.
	LookupStudents(ctx context.Context, s tenancy.Scope, numbers, emails []string) ([]Student, error)

	// AddEnrollment: student and batch must exist in this institute and be active; one enrollment
	// per (student, batch) (*ConflictError{Field: "batch_id"}).
	AddEnrollment(ctx context.Context, s tenancy.Scope, studentID, batchID string, ev audit.Event) (Enrollment, error)
	// UpdateEnrollment applies the patch atomically; the resulting state must satisfy
	// "completed requires a completion date" and "only completed has one" (caller validates, DB enforces).
	UpdateEnrollment(ctx context.Context, s tenancy.Scope, studentID, enrollmentID string, p EnrollmentPatch, ev audit.Event) (Enrollment, error)
}
