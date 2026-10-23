// Package tenancy resolves which institute (tenant) a request acts on and exposes the
// compile-time Scope that every tenant-owned repository method must require.
package tenancy

import (
	"errors"
	"regexp"
	"time"

	"github.com/certificate-os/api/internal/auth"
)

var (
	ErrNotFound         = errors.New("not found")
	ErrNotMember        = errors.New("not a member of that institute")
	ErrAmbiguous        = errors.New("institute selection required")
	ErrSlugTaken        = errors.New("slug already in use")
	ErrMembershipExists = errors.New("membership already exists")
	ErrInvalidSlug      = errors.New("invalid slug")
)

type InstituteStatus string

const (
	InstituteActive    InstituteStatus = "active"
	InstituteSuspended InstituteStatus = "suspended" // read-only for members
	InstituteClosed    InstituteStatus = "closed"    // no member access
)

func (s InstituteStatus) Valid() bool {
	return s == InstituteActive || s == InstituteSuspended || s == InstituteClosed
}

type Institute struct {
	ID        string
	Slug      string
	Name      string
	Status    InstituteStatus
	CreatedAt time.Time
}

type MembershipStatus string

const (
	MembershipActive  MembershipStatus = "active"
	MembershipRemoved MembershipStatus = "removed" // soft removal keeps the audit trail
)

// MembershipView is one of a user's active memberships joined with its institute.
type MembershipView struct {
	MembershipID string
	Institute    Institute
	Role         auth.InstituteRole
	Grants       []auth.Permission
}

// MemberView is a row of an institute's staff list.
type MemberView struct {
	MembershipID string
	UserID       string
	Email        string
	Role         auth.InstituteRole
	Grants       []auth.Permission
	Status       MembershipStatus
}

// Scope is proof that a request was resolved to one institute through authenticated membership.
// Its field is unexported, so code outside this package can only obtain one from a resolved
// Tenant. Tenant-owned repository methods take a Scope and must filter every query by it.
type Scope struct{ id string }

func (s Scope) InstituteID() string { return s.id }

// TrustedScope builds a Scope without membership resolution. ONLY for tests and out-of-band admin
// tooling; any other use needs review (grep for it in code review).
func TrustedScope(instituteID string) Scope { return Scope{id: instituteID} }

// Tenant is the resolved tenant context for one request.
type Tenant struct {
	Institute    Institute
	MembershipID string
	UserID       string
	Role         auth.InstituteRole
	perms        map[auth.Permission]struct{}
}

func newTenant(userID string, m MembershipView) Tenant {
	perms := map[auth.Permission]struct{}{}
	for _, p := range auth.InstitutePermissions(m.Role) {
		perms[p] = struct{}{}
	}
	for _, g := range m.Grants {
		// Defense in depth: ignore any stored grant that would not pass validation today.
		if auth.ValidateGrants([]auth.Permission{g}) == nil {
			perms[g] = struct{}{}
		}
	}
	return Tenant{Institute: m.Institute, MembershipID: m.MembershipID, UserID: userID, Role: m.Role, perms: perms}
}

func (t Tenant) Scope() Scope { return Scope{id: t.Institute.ID} }

// Can reports whether the member may use permission p in this institute right now. Fails closed.
// A suspended institute only allows read permissions.
func (t Tenant) Can(p auth.Permission) bool {
	if _, ok := t.perms[p]; !ok {
		return false
	}
	if t.Institute.Status != InstituteActive {
		return auth.IsReadPermission(p)
	}
	return true
}

// Permissions lists effective permissions after status restrictions (for display only).
func (t Tenant) Permissions() []auth.Permission {
	out := []auth.Permission{}
	for p := range t.perms {
		if t.Can(p) {
			out = append(out, p)
		}
	}
	sortPerms(out)
	return out
}

var slugRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,38}[a-z0-9]$`)

var reservedSlugs = map[string]bool{
	"www": true, "api": true, "admin": true, "ops": true, "app": true, "verify": true, "static": true,
	"assets": true, "mail": true, "login": true, "register": true, "support": true, "help": true, "status": true,
}

func ValidateSlug(slug string) error {
	if !slugRe.MatchString(slug) || reservedSlugs[slug] {
		return ErrInvalidSlug
	}
	return nil
}
