package applications

import (
	"context"

	"github.com/certificate-os/api/internal/audit"
	"github.com/certificate-os/api/internal/tenancy"
)

// Store persists applications. Every mutating method runs in ONE transaction: lock the row, verify
// the guard, change state, and write the audit event(s). A failed guard writes nothing.
// The caller-supplied audit.Event has EntityType set; the store fills in EntityID.
type Store interface {
	// Create fails with ErrOpenApplicationExists if the applicant already has an open application.
	Create(ctx context.Context, applicantID string, d Details, ev audit.Event) (Application, error)
	// GetForApplicant / ListForApplicant never expose another user's application (ErrNotFound).
	// A malformed id is ErrNotFound, never an internal error.
	GetForApplicant(ctx context.Context, applicantID, id string) (Application, error)
	ListForApplicant(ctx context.Context, applicantID string) ([]Application, error)
	// UpdateDetails returns ErrNotEditable unless the status is draft or changes_requested.
	UpdateDetails(ctx context.Context, applicantID, id string, d Details, ev audit.Event) (Application, error)
	// GetForReview / ListForReview are for operations and never return drafts.
	GetForReview(ctx context.Context, id string) (Application, error)
	ListForReview(ctx context.Context, f ListFilter) ([]Application, error)
	Transition(ctx context.Context, t Transition) (Application, error)
	// Approve atomically moves under_review → approved, creates the institute and the applicant's
	// owner membership, and writes ApprovalEvents. Repeated approval → ErrInvalidTransition;
	// slug taken meanwhile → ErrSlugTaken with nothing changed; inactive applicant → ErrApplicantInactive.
	Approve(ctx context.Context, in ApproveInput) (Application, tenancy.Institute, error)
}
