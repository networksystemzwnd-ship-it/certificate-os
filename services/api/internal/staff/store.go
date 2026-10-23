package staff

import (
	"context"
	"time"

	"github.com/certificate-os/api/internal/audit"
	"github.com/certificate-os/api/internal/auth"
	"github.com/certificate-os/api/internal/tenancy"
)

// Store is the tenant-scoped part: every method takes the Scope and another institute's ids are
// indistinguishable from missing ones (ErrNotFound). Mutations write their audit event in the same
// transaction (the store fills in EntityID and InstituteID); a refused call records nothing.
type Store interface {
	// CreateInvitation first revokes this email's expired pending invitation, then inserts. It returns
	// ErrAlreadyInvited (a live pending one exists), ErrAlreadyMember (an ACTIVE member has that email) or
	// ErrTooManyPending (MaxPendingInvites live pending invitations).
	CreateInvitation(ctx context.Context, s tenancy.Scope, in NewInvitation, now time.Time, ev audit.Event) (Invitation, error)
	// ListInvitations returns the newest invitations first (any status), at most limit.
	ListInvitations(ctx context.Context, s tenancy.Scope, limit int) ([]Invitation, error)
	// RevokeInvitation revokes a pending invitation (ErrState otherwise).
	RevokeInvitation(ctx context.Context, s tenancy.Scope, id string, now time.Time, ev audit.Event) (Invitation, error)
	// ChangeMember sets the role and grants of an ACTIVE member. Owners are protected (ErrProtected).
	// The store adds from_role and to_role to the audit metadata.
	ChangeMember(ctx context.Context, s tenancy.Scope, membershipID string, role auth.InstituteRole, grants []auth.Permission, ev audit.Event) (tenancy.MemberView, error)
	// RemoveMember soft-removes an ACTIVE non-owner member.
	RemoveMember(ctx context.Context, s tenancy.Scope, membershipID string, ev audit.Event) error
}

// Acceptor is deliberately NOT tenant scoped: the invitee has no tenant context yet. It is reachable only
// from the accept endpoint and authorises by token hash plus the signed-in user's email.
type Acceptor interface {
	// AcceptInvitation succeeds only for a pending, unexpired invitation whose email equals userEmail
	// (case-insensitive) and whose institute is active. It creates the membership (or
	// reactivates a removed one with the invited role and grants) and marks the invitation accepted in one
	// transaction with its audit event. Every failure is ErrInvalidInvitation and changes nothing.
	AcceptInvitation(ctx context.Context, tokenHash, userID, userEmail string, now time.Time, ev audit.Event) (Accepted, error)
}
