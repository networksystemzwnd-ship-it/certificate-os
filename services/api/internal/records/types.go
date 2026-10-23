// Package records manages an institute's courses, batches, students and enrollments (M3).
//
// Everything here is tenant-owned: every Store method takes a tenancy.Scope and the Postgres
// implementation runs inside tenantdb.WithTenant with FORCE row-level security (ADR-001).
// Records are archived, never hard-deleted. Audit metadata carries ids and CHANGED FIELD NAMES only,
// never student personal data.
package records

import (
	"errors"
	"time"
)

var (
	ErrNotFound       = errors.New("record not found")
	ErrConflict       = errors.New("record conflicts with an existing one") // see ConflictError for the field
	ErrArchived       = errors.New("record is archived")
	ErrHasActiveItems = errors.New("record still has active dependants")
	ErrBadReference   = errors.New("referenced record does not exist")
)

// ConflictError names the field whose uniqueness was violated (e.g. "student_number").
type ConflictError struct{ Field string }

func (e *ConflictError) Error() string   { return "duplicate " + e.Field }
func (e *ConflictError) Is(t error) bool { return t == ErrConflict }

// ValidationError carries per-field messages for HTTP 422 responses.
type ValidationError struct{ Problems map[string]string }

func (e *ValidationError) Error() string { return "validation failed" }

// Archive filters list queries: active records (default), archived only, or both.
type Archive string

const (
	ActiveOnly   Archive = ""
	ArchivedOnly Archive = "only"
	IncludeAll   Archive = "all"
)

func (a Archive) Valid() bool { return a == ActiveOnly || a == ArchivedOnly || a == IncludeAll }

type Course struct {
	ID          string
	Name        string
	Code        string
	Description string
	ArchivedAt  *time.Time
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type CourseInput struct{ Name, Code, Description string }

type Batch struct {
	ID         string
	CourseID   string
	CourseName string
	Name       string
	StartDate  string // YYYY-MM-DD or ""
	EndDate    string // YYYY-MM-DD or ""
	ArchivedAt *time.Time
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

type BatchInput struct{ CourseID, Name, StartDate, EndDate string }

type Student struct {
	ID            string
	StudentNumber string // "" when absent
	FullName      string
	Email         string
	Phone         string
	ArchivedAt    *time.Time
	CreatedAt     time.Time
	UpdatedAt     time.Time
	Enrollments   []Enrollment // populated by GetStudent only
}

type StudentInput struct{ StudentNumber, FullName, Email, Phone string }

type EnrollmentStatus string

const (
	Enrolled  EnrollmentStatus = "enrolled"
	Completed EnrollmentStatus = "completed"
	Withdrawn EnrollmentStatus = "withdrawn"
)

func (s EnrollmentStatus) Valid() bool { return s == Enrolled || s == Completed || s == Withdrawn }

// Enrollment links a student to a batch. Completion date and grade live here (per course), not on
// the student, so one student can complete several courses.
type Enrollment struct {
	ID             string
	StudentID      string
	BatchID        string
	BatchName      string
	CourseID       string
	CourseName     string
	Status         EnrollmentStatus
	CompletionDate string // YYYY-MM-DD; required when completed
	Grade          string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// EnrollmentPatch updates an enrollment; nil means "leave unchanged".
type EnrollmentPatch struct {
	Status         *EnrollmentStatus
	CompletionDate *string
	Grade          *string
}

// Page is the common paging/search input. Limit defaults to 50 and is capped at 100.
type Page struct {
	Q        string
	Archived Archive
	Limit    int
	Offset   int
}

func (p Page) Normalized() Page {
	if p.Limit <= 0 {
		p.Limit = 50
	}
	if p.Limit > 100 {
		p.Limit = 100
	}
	if p.Offset < 0 {
		p.Offset = 0
	}
	return p
}

type BatchFilter struct {
	Page
	CourseID string
}

type StudentFilter struct {
	Page
	BatchID  string
	CourseID string
	Status   EnrollmentStatus // matches students having an enrollment in this status
}
