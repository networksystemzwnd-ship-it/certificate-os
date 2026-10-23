// Package imports implements the student spreadsheet import (M3b): upload a CSV, map its columns,
// preview row-level validation, then confirm. Nothing is written to the student tables until confirm,
// existing students are never overwritten (duplicates are skipped and reported), and the uploaded rows
// are discarded once the job finishes except those needed for the downloadable error report.
package imports

import (
	"errors"
	"time"
)

var (
	ErrNotFound = errors.New("import not found")
	// ErrState: the import is not in a state that allows the operation (e.g. confirming twice).
	ErrState = errors.New("import is not in the right state for that")
)

// FileError describes an unusable upload; the message is safe to show to the user.
type FileError struct{ Msg string }

func (e *FileError) Error() string { return e.Msg }

// MappingError carries per-field problems with a column mapping.
type MappingError struct{ Problems map[string]string }

func (e *MappingError) Error() string { return "invalid column mapping" }

type Status string

const (
	Uploaded   Status = "uploaded"
	Validated  Status = "validated"
	Confirming Status = "confirming"
	Completed  Status = "completed"
	Cancelled  Status = "cancelled"
)

type RowState string

const (
	RowValid     RowState = "valid"     // will be imported on confirm
	RowError     RowState = "error"     // invalid; fix in the spreadsheet and upload again
	RowDuplicate RowState = "duplicate" // already exists (or repeated in the file); skipped, never overwritten
	RowImported  RowState = "imported"
	RowFailed    RowState = "failed" // passed validation but could not be saved
)

// Importable fields. full_name is required; everything else is optional.
const (
	FieldFullName       = "full_name"
	FieldStudentNumber  = "student_number"
	FieldEmail          = "email"
	FieldPhone          = "phone"
	FieldCourse         = "course"
	FieldBatch          = "batch"
	FieldStatus         = "status"
	FieldCompletionDate = "completion_date"
	FieldGrade          = "grade"
)

// Fields lists importable fields in display order.
var Fields = []string{FieldFullName, FieldStudentNumber, FieldEmail, FieldPhone, FieldCourse, FieldBatch, FieldStatus, FieldCompletionDate, FieldGrade}

// Mapping maps an importable field to a zero-based column index; unmapped fields are absent.
type Mapping map[string]int

type RowResult struct {
	Row     int      `json:"row"` // spreadsheet row: the header is row 1
	State   RowState `json:"state"`
	Message string   `json:"message"`
}

type Counts struct {
	Total      int `json:"total"`
	Valid      int `json:"valid"`
	Errors     int `json:"errors"`
	Duplicates int `json:"duplicates"`
	Imported   int `json:"imported"`
	Failed     int `json:"failed"`
}

type Import struct {
	ID          string
	CreatedBy   string
	Status      Status
	Filename    string
	Headers     []string
	Rows        [][]string // aligned with Results; a purged row is empty
	Mapping     Mapping
	DateFormat  string // iso | dmy | mdy
	Results     []RowResult
	Counts      Counts
	CreatedAt   time.Time
	UpdatedAt   time.Time
	CompletedAt *time.Time
}

func ValidDateFormat(f string) bool { return f == "iso" || f == "dmy" || f == "mdy" }
