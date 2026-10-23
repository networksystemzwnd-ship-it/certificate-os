// Package staff manages who may work in an institute: one-time invitation links, accepting them,
// changing a member's role or extra permissions, and removing members. Owner-only (staff.manage).
package staff

import (
	"errors"
	"time"

	"github.com/certificate-os/api/internal/auth"
	"github.com/certificate-os/api/internal/tenancy"
)

var (
	ErrNotFound = errors.New("not found")
	// ErrState: the invitation is no longer pending.
	ErrState = errors.New("invitation is not pending")
	// ErrProtected: owners (and one's own membership) cannot be changed or removed here.
	ErrProtected      = errors.New("this member cannot be changed here")
	ErrAlreadyInvited = errors.New("an invitation for this email is already pending")
	ErrAlreadyMember  = errors.New("this person is already a member")
	ErrTooManyPending = errors.New("too many pending invitations")
	// ErrInvalidInvitation is deliberately the same for unknown, expired, revoked, used, wrong-account and
	// inactive-institute cases so a token cannot be probed.
	ErrInvalidInvitation = errors.New("this invitation is invalid or has expired")
)

// ValidationError carries per-field messages for HTTP 422.
type ValidationError struct{ Problems map[string]string }

func (e *ValidationError) Error() string { return "validation failed" }

const (
	InvitationTTL     = 7 * 24 * time.Hour
	MaxPendingInvites = 50
)

type InvitationStatus string

const (
	InvitationPending  InvitationStatus = "pending"
	InvitationAccepted InvitationStatus = "accepted"
	InvitationRevoked  InvitationStatus = "revoked"
	InvitationExpired  InvitationStatus = "expired" // derived: pending and past expires_at
)

type Invitation struct {
	ID         string
	Email      string
	Role       auth.InstituteRole
	Grants     []auth.Permission
	Status     InvitationStatus // as stored (pending|accepted|revoked); use Effective for expiry
	InvitedBy  string
	CreatedAt  time.Time
	ExpiresAt  time.Time
	AcceptedAt *time.Time
	RevokedAt  *time.Time
}

// Effective returns the status as users should see it (pending invitations past expiry are "expired").
func (i Invitation) Effective(now time.Time) InvitationStatus {
	if i.Status == InvitationPending && !now.Before(i.ExpiresAt) {
		return InvitationExpired
	}
	return i.Status
}

type NewInvitation struct {
	Email     string
	Role      auth.InstituteRole
	Grants    []auth.Permission
	TokenHash string
	InvitedBy string
	ExpiresAt time.Time
}

// Accepted is the result of accepting an invitation.
type Accepted struct {
	Institute tenancy.Institute
	Member    tenancy.MemberView
}
