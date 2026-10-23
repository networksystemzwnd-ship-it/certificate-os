package staff

import (
	"context"
	"errors"
	"net/mail"
	"sort"
	"strings"
	"time"

	"github.com/certificate-os/api/internal/audit"
	"github.com/certificate-os/api/internal/auth"
	"github.com/certificate-os/api/internal/tenancy"
)

// ErrForbidden: the acting member lacks staff.manage (defense in depth; the HTTP guard checks it too).
var ErrForbidden = errors.New("forbidden")

type Service struct {
	Store    Store
	Acceptor Acceptor
	Now      func() time.Time
}

func NewService(st Store, acc Acceptor) *Service {
	return &Service{Store: st, Acceptor: acc, Now: time.Now}
}

func event(userID, action, entityType, reqID string, meta map[string]any) audit.Event {
	return audit.Event{ActorType: audit.ActorUser, ActorUserID: userID, Action: action, EntityType: entityType, RequestID: reqID, Metadata: meta}
}

func normEmail(s string) (string, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	a, err := mail.ParseAddress(s)
	if s == "" || err != nil || a.Address != s || len(s) > 254 || strings.ContainsAny(s, " <>,;") {
		return s, false
	}
	return s, true
}

// roleOK: only admin and staff can be invited or assigned; ownership is never handed out through this API.
func checkRoleAndGrants(role auth.InstituteRole, grants []auth.Permission) (map[string]string, []auth.Permission) {
	p := map[string]string{}
	if role != auth.InstituteAdmin && role != auth.InstituteStaff {
		p["role"] = "Choose admin or staff."
	}
	seen := map[auth.Permission]bool{}
	var clean []auth.Permission
	for _, g := range grants {
		if !seen[g] {
			seen[g] = true
			clean = append(clean, g)
		}
	}
	sort.Slice(clean, func(i, j int) bool { return clean[i] < clean[j] })
	if err := auth.ValidateGrants(clean); err != nil {
		p["grants"] = "Choose only permissions that can be granted to one person."
	} else if role == auth.InstituteAdmin && len(clean) > 0 {
		p["grants"] = "Admins already have these permissions."
	}
	return p, clean
}

// Invite creates a one-time invitation. The returned token is the ONLY copy: only its hash is stored.
func (s *Service) Invite(ctx context.Context, t tenancy.Tenant, email string, role auth.InstituteRole, grants []auth.Permission, reqID string) (Invitation, string, error) {
	if !t.Can(auth.PermStaffManage) {
		return Invitation{}, "", ErrForbidden
	}
	p, clean := checkRoleAndGrants(role, grants)
	email, ok := normEmail(email)
	if !ok {
		p["email"] = "Enter a valid email address."
	}
	if len(p) > 0 {
		return Invitation{}, "", &ValidationError{Problems: p}
	}
	token, hash, err := NewToken()
	if err != nil {
		return Invitation{}, "", err
	}
	now := s.Now()
	names := make([]string, len(clean))
	for i, g := range clean {
		names[i] = string(g)
	}
	inv, err := s.Store.CreateInvitation(ctx, t.Scope(), NewInvitation{Email: email, Role: role, Grants: clean, TokenHash: hash,
		InvitedBy: t.UserID, ExpiresAt: now.Add(InvitationTTL)}, now,
		event(t.UserID, "invitation.created", "institute_invitation", reqID, map[string]any{"role": string(role), "grants": names}))
	if err != nil {
		return Invitation{}, "", err
	}
	return inv, token, nil
}

func (s *Service) ListInvitations(ctx context.Context, t tenancy.Tenant) ([]Invitation, error) {
	if !t.Can(auth.PermStaffManage) {
		return nil, ErrForbidden
	}
	return s.Store.ListInvitations(ctx, t.Scope(), 100)
}

func (s *Service) Revoke(ctx context.Context, t tenancy.Tenant, id, reqID string) (Invitation, error) {
	if !t.Can(auth.PermStaffManage) {
		return Invitation{}, ErrForbidden
	}
	return s.Store.RevokeInvitation(ctx, t.Scope(), id, s.Now(), event(t.UserID, "invitation.revoked", "institute_invitation", reqID, nil))
}

// Accept redeems a token for the signed-in user. The email must match the invitation; every failure is
// the same ErrInvalidInvitation.
func (s *Service) Accept(ctx context.Context, p auth.Principal, token, reqID string) (Accepted, error) {
	token = strings.TrimSpace(token)
	if p.UserID == "" || p.Email == "" || token == "" || len(token) > 200 {
		return Accepted{}, ErrInvalidInvitation
	}
	return s.Acceptor.AcceptInvitation(ctx, HashToken(token), p.UserID, p.Email, s.Now(),
		event(p.UserID, "invitation.accepted", "institute_invitation", reqID, nil))
}

// ChangeMember changes another member's role and extra permissions. Owners and the acting member's own
// membership are protected.
func (s *Service) ChangeMember(ctx context.Context, t tenancy.Tenant, membershipID string, role auth.InstituteRole, grants []auth.Permission, reqID string) (tenancy.MemberView, error) {
	if !t.Can(auth.PermStaffManage) {
		return tenancy.MemberView{}, ErrForbidden
	}
	if membershipID == t.MembershipID {
		return tenancy.MemberView{}, ErrProtected
	}
	p, clean := checkRoleAndGrants(role, grants)
	if len(p) > 0 {
		return tenancy.MemberView{}, &ValidationError{Problems: p}
	}
	names := make([]string, len(clean))
	for i, g := range clean {
		names[i] = string(g)
	}
	return s.Store.ChangeMember(ctx, t.Scope(), membershipID, role, clean,
		event(t.UserID, "member.role_changed", "membership", reqID, map[string]any{"to_role": string(role), "grants": names}))
}

func (s *Service) RemoveMember(ctx context.Context, t tenancy.Tenant, membershipID, reqID string) error {
	if !t.Can(auth.PermStaffManage) {
		return ErrForbidden
	}
	if membershipID == t.MembershipID {
		return ErrProtected
	}
	return s.Store.RemoveMember(ctx, t.Scope(), membershipID, event(t.UserID, "member.removed", "membership", reqID, nil))
}
