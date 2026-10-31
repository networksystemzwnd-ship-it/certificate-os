package certificates

import (
	"context"

	"github.com/certificate-os/api/internal/audit"
	"github.com/certificate-os/api/internal/tenancy"
)

// Store persists certificates. EVERY method takes the Scope and may only touch rows of that institute;
// another institute's id is indistinguishable from a missing one (ErrNotFound). Mutations run in one
// transaction with their audit event (the store fills in EntityID/InstituteID and adds "number" to the
// metadata); a failed call records nothing.
type Store interface {
	// BeginIssue, in ONE transaction: loads the enrollment/student/template version, checks eligibility
	// (*EligibilityError, ErrNotFound for unknown or foreign ids), abandons stale pending certificates of the
	// enrollment, allocates the next number of the institute's year, builds the snapshot and inserts the
	// certificate as pending. A live (pending/issued) certificate for the enrollment: ErrAlreadyIssued.
	BeginIssue(ctx context.Context, s tenancy.Scope, in BeginInput, ev audit.Event) (Certificate, error)
	// Complete stores the PDF (its SHA-256 is computed by the store) and marks the pending certificate
	// issued, atomically. ErrState unless the certificate is pending.
	Complete(ctx context.Context, s tenancy.Scope, id string, pdf []byte, ev audit.Event) (Certificate, error)
	// Fail marks a pending certificate failed (reason: short, no personal data). ErrState unless pending.
	Fail(ctx context.Context, s tenancy.Scope, id, reason string, ev audit.Event) (Certificate, error)
	Get(ctx context.Context, s tenancy.Scope, id string) (Certificate, error)
	// List returns up to Limit+1 certificates, newest first.
	List(ctx context.Context, s tenancy.Scope, f Filter) ([]Certificate, error)
	// GetPDF returns the stored file of an issued or revoked certificate (ErrNotFound otherwise).
	GetPDF(ctx context.Context, s tenancy.Scope, id string) (PDF, error)
	// Revoke flips an issued certificate to revoked (ErrState otherwise); the PDF stays.
	Revoke(ctx context.Context, s tenancy.Scope, id, reason, by string, ev audit.Event) (Certificate, error)
}
