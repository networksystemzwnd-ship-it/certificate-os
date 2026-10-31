// Package templates manages certificate templates and their immutable versions (M4a).
//
// A template is a PRESET plus a small set of configurable fields (D-008): there is no free-form HTML,
// CSS or script from users, so a template cannot execute code, read files or reach the network.
// A template version is immutable: editing creates a new version, so a certificate issued from
// version N can always be reproduced from exactly what version N contained. Templates are tenant-owned
// (row-level security, ADR-001). Audit metadata holds ids, preset names and version numbers only.
package templates

import (
	"errors"
	"time"
)

var (
	ErrNotFound        = errors.New("template not found")
	ErrConflict        = errors.New("template conflicts with an existing one")
	ErrArchived        = errors.New("template is archived")
	ErrTooManyVersions = errors.New("template has too many versions")
)

// MaxVersions bounds how many versions one template can have.
const MaxVersions = 100

// ConflictError names the field whose uniqueness was violated.
type ConflictError struct{ Field string }

func (e *ConflictError) Error() string   { return "duplicate " + e.Field }
func (e *ConflictError) Is(t error) bool { return t == ErrConflict }

// ValidationError carries per-field messages for HTTP 422 responses.
type ValidationError struct{ Problems map[string]string }

func (e *ValidationError) Error() string { return "validation failed" }

// Config is everything a template version holds. All of it is validated by NormalizeConfig.
type Config struct {
	Preset            string `json:"preset"`      // classic | modern | minimal
	Orientation       string `json:"orientation"` // landscape | portrait
	AccentColor       string `json:"accent_color"`
	Title             string `json:"title"`
	Body              string `json:"body"` // may contain the placeholders in Placeholders
	SignatoryName     string `json:"signatory_name"`
	SignatoryTitle    string `json:"signatory_title"`
	Footer            string `json:"footer"`
	ShowGrade         bool   `json:"show_grade"`
	ShowCompletion    bool   `json:"show_completion_date"`
	ShowCertificateNo bool   `json:"show_certificate_number"`
	ShowQR            bool   `json:"show_qr"`
}

type Version struct {
	Number    int
	Config    Config
	CreatedBy string // user id
	CreatedAt time.Time
}

type VersionSummary struct {
	Number    int
	CreatedBy string
	CreatedAt time.Time
}

type Template struct {
	ID         string
	Name       string
	Current    Version          // the newest version (config included)
	Versions   []VersionSummary // GetTemplate only, newest first
	ArchivedAt *time.Time
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

type Archive string

const (
	ActiveOnly   Archive = ""
	ArchivedOnly Archive = "only"
	IncludeAll   Archive = "all"
)

func (a Archive) Valid() bool { return a == ActiveOnly || a == ArchivedOnly || a == IncludeAll }

// Page is the paging/search input. Limit defaults to 50 and is capped at 100.
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
