// Package certificates issues, stores, downloads and revokes certificates (M4b).
//
// Issuing is a small state machine: pending -> issued | failed, issued -> revoked. A certificate counts as
// ISSUED only after its PDF (and SHA-256) is durably stored; a record that could not be rendered is kept as
// "failed" and never shown as issued. What was issued is immutable (D-147): number, public id, template
// version and the display snapshot never change; the stored PDF is insert-only. Everything is tenant-owned
// (row-level security, ADR-001). Audit metadata holds ids, numbers, template versions and counts, never
// names, emails or free text.
package certificates

import (
	"errors"
	"strings"
	"time"

	"github.com/certificate-os/api/internal/templates"
)

var (
	ErrNotFound      = errors.New("certificate not found")
	ErrAlreadyIssued = errors.New("this enrollment already has a certificate")
	ErrState         = errors.New("certificate is not in a state that allows this")
	// ErrRenderUnavailable: no PDF renderer is configured, so nothing can be issued (nothing is written).
	ErrRenderUnavailable = errors.New("certificate rendering is not available")
	ErrRenderFailed      = errors.New("certificate rendering failed")
)

// StalePendingAfter is how long a pending certificate may block its enrollment before a new attempt
// marks it as abandoned (failed) and proceeds (e.g. the process died mid-render).
const StalePendingAfter = 5 * time.Minute

// EligibilityError explains why an enrollment/template cannot be used for issuing (HTTP 422).
type EligibilityError struct{ Field, Reason string }

func (e *EligibilityError) Error() string { return e.Reason }

// ValidationError carries per-field messages for HTTP 422 responses.
type ValidationError struct{ Problems map[string]string }

func (e *ValidationError) Error() string { return "validation failed" }

type Status string

const (
	Pending Status = "pending"
	Issued  Status = "issued"
	Failed  Status = "failed"
	Revoked Status = "revoked"
)

func (s Status) Valid() bool { return s == Pending || s == Issued || s == Failed || s == Revoked }

// Source is what the store reads (inside the issuing transaction) to build a snapshot.
type Source struct {
	StudentID, StudentName, StudentNumber string
	StudentArchived                       bool
	EnrollmentID, EnrollmentStatus        string // "completed" is required
	CompletionDate, Grade                 string // YYYY-MM-DD, free text
	BatchName, CourseName                 string
	InstituteName                         string
	TemplateID, TemplateName              string
	TemplateArchived                      bool
	TemplateVersion                       int
	Config                                templates.Config
}

// CheckEligible refuses sources that must not produce a certificate.
func CheckEligible(s Source) error {
	switch {
	case s.EnrollmentStatus != "completed" || s.CompletionDate == "":
		return &EligibilityError{"enrollment_id", "Only a completed enrollment can receive a certificate."}
	case s.StudentArchived:
		return &EligibilityError{"enrollment_id", "This student is archived. Restore the student first."}
	case s.TemplateArchived:
		return &EligibilityError{"template_id", "This template is archived. Restore it or choose another."}
	}
	return nil
}

// Snapshot is the immutable record of exactly what a certificate shows. The template configuration is
// copied in, so the artifact stays explainable even if template storage changes later.
type Snapshot struct {
	CertificateNumber string           `json:"certificate_number"`
	StudentName       string           `json:"student_name"`
	StudentNumber     string           `json:"student_number"`
	CourseName        string           `json:"course_name"`
	BatchName         string           `json:"batch_name"`
	InstituteName     string           `json:"institute_name"`
	CompletionDate    string           `json:"completion_date"` // YYYY-MM-DD
	IssueDate         string           `json:"issue_date"`      // YYYY-MM-DD (UTC)
	Grade             string           `json:"grade"`
	TemplateName      string           `json:"template_name"`
	TemplateVersion   int              `json:"template_version"`
	Config            templates.Config `json:"config"`
}

// BuildSnapshot is a pure function of the source, the allocated number and the issue time.
func BuildSnapshot(s Source, number string, now time.Time) Snapshot {
	return Snapshot{CertificateNumber: number, StudentName: s.StudentName, StudentNumber: s.StudentNumber,
		CourseName: s.CourseName, BatchName: s.BatchName, InstituteName: s.InstituteName,
		CompletionDate: s.CompletionDate, IssueDate: now.UTC().Format("2006-01-02"), Grade: s.Grade,
		TemplateName: s.TemplateName, TemplateVersion: s.TemplateVersion, Config: s.Config}
}

// FormatNumber renders CERT-YYYY-NNNNNN.
func FormatNumber(year, n int) string {
	d := []byte("000000")
	s := itoa(n)
	if len(s) >= len(d) {
		return "CERT-" + itoa(year) + "-" + s
	}
	copy(d[len(d)-len(s):], s)
	return "CERT-" + itoa(year) + "-" + string(d)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var d []byte
	for ; n > 0; n /= 10 {
		d = append([]byte{byte('0' + n%10)}, d...)
	}
	return string(d)
}

type Certificate struct {
	ID, Number, PublicID string
	StudentID            string
	EnrollmentID         string
	TemplateID           string
	TemplateVersion      int
	Snapshot             Snapshot
	Status               Status
	PDFSHA256            string
	PDFSize              int
	IssuedBy             string
	CreatedAt            time.Time
	IssuedAt             *time.Time
	FailedAt             *time.Time
	Failure              string
	RevokedAt            *time.Time
	RevokeReason         string
}

// PDF is a stored certificate file.
type PDF struct {
	Bytes  []byte
	SHA256 string
	Number string
}

// BeginInput starts an issuance.
type BeginInput struct {
	EnrollmentID, TemplateID string
	Version                  int // 0 = the template's current version
	PublicID                 string
	IssuedBy                 string
	Now                      time.Time
}

type Filter struct {
	Status    Status
	StudentID string
	Q         string // matches the certificate number or the student's name
	Limit     int
	Offset    int
}

func (f Filter) Normalized() Filter {
	f.Q = strings.TrimSpace(f.Q)
	if f.Limit <= 0 {
		f.Limit = 50
	}
	if f.Limit > 100 {
		f.Limit = 100
	}
	if f.Offset < 0 {
		f.Offset = 0
	}
	return f
}
