package tenancy

import (
	"context"

	"github.com/certificate-os/api/internal/auth"
)

// Store is the tenant-facing persistence port. Anything returning tenant-owned data takes a Scope.
type Store interface {
	// MembershipsForUser returns only ACTIVE memberships of institutes that are not closed.
	MembershipsForUser(ctx context.Context, userID string) ([]MembershipView, error)
	GetInstitute(ctx context.Context, scope Scope) (Institute, error)
	ListMembers(ctx context.Context, scope Scope) ([]MemberView, error)
}

// AdminStore is for out-of-band tooling (CLI) and, later, platform operations. It is deliberately
// NOT tenant-scoped and must never be reachable from tenant request handlers.
type AdminStore interface {
	CreateInstitute(ctx context.Context, slug, name string) (Institute, error)
	InstituteBySlug(ctx context.Context, slug string) (Institute, error)
	SetInstituteStatus(ctx context.Context, instituteID string, status InstituteStatus) error
	AddMembership(ctx context.Context, instituteID, userID string, role auth.InstituteRole, grants []auth.Permission) (MemberView, error)
	RemoveMembership(ctx context.Context, instituteID, userID string) error
}
