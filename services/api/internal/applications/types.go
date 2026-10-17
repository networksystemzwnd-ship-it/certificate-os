// Package applications implements institute onboarding: the application lifecycle
// (draft → submitted → under_review → approved | rejected | changes_requested), validation,
// and the atomic approval that creates the institute workspace and its owner.
//
// The Store contract (applicationstest.RunStoreContract) is the executable specification of the
// persistence rules; every Store implementation must pass it.
package applications

import (
	"errors"
	"strings"
	"time"

	"github.com/certificate-os/api/internal/audit"
	"github.com/certificate-os/api/internal/auth"
	"github.com/certificate-os/api/internal/tenancy"
)

type Status string

const (
	Draft            Status = "draft"
	Submitted        Status = "submitted"
	UnderReview      Status = "under_review"
	ChangesRequested Status = "changes_requested"
	Approved         Status = "approved"
	Rejected         Status = "rejected"
)

func (s Status) Valid() bool {
	switch s {
	case Draft, Submitted, UnderReview, ChangesRequested, Approved, Rejected:
		return true
	}
	return false
}

// transitions is the complete state machine. Anything not listed is rejected.
var transitions = map[Status][]Status{
	Draft:            {Submitted},
	Submitted:        {UnderReview},
	UnderReview:      {Approved, Rejected, ChangesRequested},
	ChangesRequested: {Submitted},
}

func CanTransition(from, to Status) bool {
	for _, t := range transitions[from] {
		if t == to {
			return true
		}
	}
	return false
}

// SourcesOf lists every status from which `to` may be entered (derived from the state machine).
func SourcesOf(to Status) []Status {
	var out []Status
	for _, from := range []Status{Draft, Submitted, UnderReview, ChangesRequested, Approved, Rejected} {
		if CanTransition(from, to) {
			out = append(out, from)
		}
	}
	return out
}

// Editable: the applicant may change details only before submission or after a change request.
func (s Status) Editable() bool { return s == Draft || s == ChangesRequested }

// Open applications block the applicant from starting another (one open application per user).
func (s Status) Open() bool { return s != Approved && s != Rejected }

// ClaimsSlug: these states reserve the requested workspace slug.
func (s Status) ClaimsSlug() bool {
	return s == Submitted || s == UnderReview || s == ChangesRequested || s == Approved
}

var (
	ErrNotFound              = errors.New("application not found")
	ErrInvalidTransition     = errors.New("invalid status transition")
	ErrNotEditable           = errors.New("application can no longer be edited")
	ErrSlugTaken             = tenancy.ErrSlugTaken
	ErrOpenApplicationExists = errors.New("you already have an open application")
	ErrSelfReview            = errors.New("reviewers cannot decide their own application")
	ErrApplicantInactive     = errors.New("applicant account is not active")
)

// ValidationError carries per-field messages for HTTP 422 responses.
type ValidationError struct{ Problems map[string]string }

func (e *ValidationError) Error() string { return "validation failed" }

// Details are the applicant-entered organization details (brain §3.2). Empty means "not provided".
type Details struct {
	Name          string
	Slug          string // requested workspace slug
	Category      string
	AddressLine1  string
	AddressLine2  string
	City          string
	Region        string
	PostalCode    string
	Country       string // ISO 3166-1 alpha-2
	OfficialEmail string
	Phone         string
	RepName       string
	RepRole       string
	Website       string
	Attested      bool // applicant confirms accuracy and authority to apply
}

// Normalize trims and canonicalizes values (lowercase slug/email, uppercase country).
func (d Details) Normalize() Details {
	t := strings.TrimSpace
	d.Name, d.Slug, d.Category = t(d.Name), strings.ToLower(t(d.Slug)), t(d.Category)
	d.AddressLine1, d.AddressLine2, d.City, d.Region, d.PostalCode = t(d.AddressLine1), t(d.AddressLine2), t(d.City), t(d.Region), t(d.PostalCode)
	d.Country, d.Phone, d.RepName, d.RepRole, d.Website = strings.ToUpper(t(d.Country)), t(d.Phone), t(d.RepName), t(d.RepRole), t(d.Website)
	if e, ok := auth.NormalizeEmail(d.OfficialEmail); ok {
		d.OfficialEmail = e
	} else {
		d.OfficialEmail = t(d.OfficialEmail)
	}
	return d
}

type Application struct {
	ID              string
	ApplicantUserID string
	Status          Status
	Details         Details
	Version         int // increments on every change
	SubmittedAt     *time.Time
	ReviewedBy      string
	ReviewedAt      *time.Time
	DecisionReason  string // reviewer's reason (reject / request changes)
	InstituteID     string // set once approved
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// Transition is one guarded status change. The store applies it only if the CURRENT status is in
// From (compare-and-set under a row lock), and writes Event in the same transaction.
//
// ApplicantID set  → applicant-initiated: ownership is enforced and a foreign/unknown id is ErrNotFound.
// ApplicantID empty → operations-initiated: drafts are invisible (ErrNotFound).
// Details, when set, replaces the stored details atomically (used by submit). Submitting also
// re-checks completeness is the caller's job; the store enforces slug reservation (ErrSlugTaken).
type Transition struct {
	ID          string
	ApplicantID string
	From        []Status
	To          Status
	ActorUserID string
	Details     *Details
	Reason      string
	Event       audit.Event // EntityID is filled in by the store
}

type ApproveInput struct {
	ID         string
	ReviewerID string
	Note       string
	RequestID  string
}

type ListFilter struct {
	Statuses []Status
	Limit    int // default 50, max 100
	Offset   int
}

func (f ListFilter) Normalized() ListFilter {
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

// ApprovalEvents returns the audit trail of an approval: the decision, the institute it created and
// the owner membership. Stores write all of them in the approval transaction.
func ApprovalEvents(in ApproveInput, app Application, inst tenancy.Institute) []audit.Event {
	actor := func(action, entityType, entityID string, meta map[string]any) audit.Event {
		return audit.Event{ActorType: audit.ActorUser, ActorUserID: in.ReviewerID, Action: action, EntityType: entityType,
			EntityID: entityID, InstituteID: inst.ID, RequestID: in.RequestID, Metadata: meta}
	}
	return []audit.Event{
		actor("application.approved", "institute_application", app.ID,
			map[string]any{"institute_id": inst.ID, "slug": inst.Slug, "note": in.Note}),
		actor("institute.created", "institute", inst.ID,
			map[string]any{"application_id": app.ID, "slug": inst.Slug}),
		actor("membership.created", "institute_membership", inst.ID+":"+app.ApplicantUserID,
			map[string]any{"user_id": app.ApplicantUserID, "role": string(auth.InstituteOwner), "via": "application_approval"}),
	}
}

// AuditAction maps a target status to its audit action name.
func AuditAction(to Status) string {
	switch to {
	case Submitted:
		return "application.submitted"
	case UnderReview:
		return "application.review_started"
	case Rejected:
		return "application.rejected"
	case ChangesRequested:
		return "application.changes_requested"
	case Approved:
		return "application.approved"
	}
	return "application.updated"
}
