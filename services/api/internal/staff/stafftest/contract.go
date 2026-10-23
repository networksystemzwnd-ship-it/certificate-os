package stafftest

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/certificate-os/api/internal/audit"
	"github.com/certificate-os/api/internal/auth"
	"github.com/certificate-os/api/internal/staff"
	"github.com/certificate-os/api/internal/tenancy"
)

// Harness wires one store implementation into the shared contract.
type Harness struct {
	Store    staff.Store
	Acceptor staff.Acceptor
	// NewInstitute returns the Scope of a brand-new active institute and the user id of its owner
	// (who already holds an owner membership).
	NewInstitute func() (tenancy.Scope, string)
	NewUser      func(email string) string
	// AddMember adds an active member and returns the membership.
	AddMember func(s tenancy.Scope, userID string, role auth.InstituteRole) tenancy.MemberView
	// Members lists an institute's memberships through the ordinary tenancy store.
	Members            func(s tenancy.Scope) []tenancy.MemberView
	SetInstituteStatus func(instituteID string, st tenancy.InstituteStatus)
	AuditActions       func(entityType, id string) []string
	AuditTotal         func(instituteID string) int
}

func ev(action, entityType string) audit.Event {
	return audit.Event{ActorType: audit.ActorSystem, Action: action, EntityType: entityType}
}

var t0 = time.Date(2026, 10, 22, 9, 0, 0, 0, time.UTC)

func newInv(email string, role auth.InstituteRole, hash string, grants ...auth.Permission) staff.NewInvitation {
	return staff.NewInvitation{Email: email, Role: role, Grants: grants, TokenHash: hash, InvitedBy: "", ExpiresAt: t0.Add(staff.InvitationTTL)}
}

// RunStoreContract is the executable specification every staff store must satisfy.
func RunStoreContract(t *testing.T, newHarness func(t *testing.T) Harness) {
	ctx := context.Background()
	must := func(t *testing.T, err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	hash := func(n int) string { return fmt.Sprintf("%064d", n) }
	create := func(t *testing.T, h Harness, s tenancy.Scope, owner, email string, n int, role auth.InstituteRole, grants ...auth.Permission) staff.Invitation {
		t.Helper()
		in := newInv(email, role, hash(n), grants...)
		in.InvitedBy = owner
		inv, err := h.Store.CreateInvitation(ctx, s, in, t0, ev("invitation.created", "institute_invitation"))
		must(t, err)
		return inv
	}
	accept := func(h Harness, n int, userID, email string, now time.Time) (staff.Accepted, error) {
		return h.Acceptor.AcceptInvitation(ctx, hash(n), userID, email, now, ev("invitation.accepted", "institute_invitation"))
	}

	t.Run("invitations: create, list newest first, one live invitation per email, revoke", func(t *testing.T) {
		h := newHarness(t)
		a, owner := h.NewInstitute()
		i1 := create(t, h, a, owner, "asha@example.com", 1, auth.InstituteStaff, auth.PermCertsIssue)
		if i1.ID == "" || i1.Status != staff.InvitationPending || i1.Email != "asha@example.com" || len(i1.Grants) != 1 || !i1.ExpiresAt.After(i1.CreatedAt) {
			t.Fatalf("unexpected %+v", i1)
		}
		_, err := h.Store.CreateInvitation(ctx, a, newInv("asha@example.com", auth.InstituteAdmin, hash(2)), t0.Add(time.Hour), ev("invitation.created", "institute_invitation"))
		if err != staff.ErrAlreadyInvited {
			t.Fatalf("second live invitation: got %v", err)
		}
		before := h.AuditTotal(a.InstituteID())
		create(t, h, a, owner, "ravi@example.com", 3, auth.InstituteAdmin)
		list, err := h.Store.ListInvitations(ctx, a, 100)
		must(t, err)
		if len(list) != 2 || list[0].Email != "ravi@example.com" {
			t.Fatalf("list order: %+v", list)
		}
		if h.AuditTotal(a.InstituteID()) != before+1 {
			t.Fatal("each created invitation writes exactly one audit event")
		}
		rv, err := h.Store.RevokeInvitation(ctx, a, i1.ID, t0, ev("invitation.revoked", "institute_invitation"))
		must(t, err)
		if rv.Status != staff.InvitationRevoked || rv.RevokedAt == nil {
			t.Fatalf("revoked: %+v", rv)
		}
		auditBefore := h.AuditTotal(a.InstituteID())
		if _, err := h.Store.RevokeInvitation(ctx, a, i1.ID, t0, ev("invitation.revoked", "institute_invitation")); err != staff.ErrState {
			t.Fatalf("revoking twice: %v", err)
		}
		if h.AuditTotal(a.InstituteID()) != auditBefore {
			t.Fatal("a refused revoke must not write an audit event")
		}
		create(t, h, a, owner, "asha@example.com", 4, auth.InstituteStaff) // a revoked invitation frees the email
	})

	t.Run("invitations: an expired invitation gives way to a new one", func(t *testing.T) {
		h := newHarness(t)
		a, owner := h.NewInstitute()
		create(t, h, a, owner, "late@example.com", 1, auth.InstituteStaff)
		later := t0.Add(staff.InvitationTTL + time.Hour)
		in := newInv("late@example.com", auth.InstituteStaff, hash(2))
		in.InvitedBy, in.ExpiresAt = owner, later.Add(staff.InvitationTTL)
		if _, err := h.Store.CreateInvitation(ctx, a, in, later, ev("invitation.created", "institute_invitation")); err != nil {
			t.Fatalf("re-invite after expiry: %v", err)
		}
		if _, err := accept(h, 1, h.NewUser("late@example.com"), "late@example.com", later); err != staff.ErrInvalidInvitation {
			t.Fatalf("the expired token must stay dead: %v", err)
		}
	})

	t.Run("invitations: already a member, and the pending limit", func(t *testing.T) {
		h := newHarness(t)
		a, owner := h.NewInstitute()
		uid := h.NewUser("member@example.com")
		h.AddMember(a, uid, auth.InstituteStaff)
		in := newInv("member@example.com", auth.InstituteStaff, hash(1))
		in.InvitedBy = owner
		if _, err := h.Store.CreateInvitation(ctx, a, in, t0, ev("invitation.created", "institute_invitation")); err != staff.ErrAlreadyMember {
			t.Fatalf("inviting an active member: %v", err)
		}
		for i := 0; i < staff.MaxPendingInvites; i++ {
			create(t, h, a, owner, fmt.Sprintf("p%d@example.com", i), 100+i, auth.InstituteStaff)
		}
		in = newInv("overflow@example.com", auth.InstituteStaff, hash(999))
		in.InvitedBy = owner
		if _, err := h.Store.CreateInvitation(ctx, a, in, t0, ev("invitation.created", "institute_invitation")); err != staff.ErrTooManyPending {
			t.Fatalf("pending limit: %v", err)
		}
	})

	t.Run("accept: creates the membership with the invited role and grants, once", func(t *testing.T) {
		h := newHarness(t)
		a, owner := h.NewInstitute()
		inv := create(t, h, a, owner, "asha@example.com", 1, auth.InstituteStaff, auth.PermCertsIssue)
		uid := h.NewUser("asha@example.com")
		res, err := accept(h, 1, uid, "ASHA@example.com", t0.Add(time.Hour)) // email match ignores case
		must(t, err)
		if res.Institute.ID != a.InstituteID() || res.Member.UserID != uid || res.Member.Role != auth.InstituteStaff ||
			len(res.Member.Grants) != 1 || res.Member.Grants[0] != auth.PermCertsIssue || res.Member.Status != tenancy.MembershipActive {
			t.Fatalf("accepted: %+v", res)
		}
		list, _ := h.Store.ListInvitations(ctx, a, 10)
		if list[0].Status != staff.InvitationAccepted || list[0].AcceptedAt == nil {
			t.Fatalf("invitation not marked accepted: %+v", list[0])
		}
		got := h.AuditActions("institute_invitation", inv.ID)
		if len(got) != 2 || got[1] != "invitation.accepted" {
			t.Fatalf("audit trail: %v", got)
		}
		before := h.AuditTotal(a.InstituteID())
		if _, err := accept(h, 1, uid, "asha@example.com", t0.Add(time.Hour)); err != staff.ErrInvalidInvitation {
			t.Fatalf("second accept: %v", err)
		}
		if h.AuditTotal(a.InstituteID()) != before || len(h.Members(a)) != 2 {
			t.Fatal("a refused accept must change nothing")
		}
	})

	t.Run("accept: every refusal is the same error and changes nothing", func(t *testing.T) {
		h := newHarness(t)
		a, owner := h.NewInstitute()
		create(t, h, a, owner, "asha@example.com", 1, auth.InstituteStaff)
		revoked := create(t, h, a, owner, "gone@example.com", 2, auth.InstituteStaff)
		_, err := h.Store.RevokeInvitation(ctx, a, revoked.ID, t0, ev("invitation.revoked", "institute_invitation"))
		must(t, err)
		create(t, h, a, owner, "susp@example.com", 3, auth.InstituteStaff)
		members := len(h.Members(a))
		before := h.AuditTotal(a.InstituteID())
		uid := h.NewUser("asha@example.com")
		for name, f := range map[string]func() error{
			"unknown token": func() error { _, e := accept(h, 99, uid, "asha@example.com", t0); return e },
			"wrong account": func() error { _, e := accept(h, 1, h.NewUser("eve@example.com"), "eve@example.com", t0); return e },
			"expired":       func() error { _, e := accept(h, 1, uid, "asha@example.com", t0.Add(staff.InvitationTTL)); return e },
			"revoked":       func() error { _, e := accept(h, 2, h.NewUser("gone@example.com"), "gone@example.com", t0); return e },
			"empty hash":    func() error { _, e := accept(h, 0, uid, "asha@example.com", t0); return e },
		} {
			if err := f(); err != staff.ErrInvalidInvitation {
				t.Errorf("%s: got %v", name, err)
			}
		}
		for _, st := range []tenancy.InstituteStatus{tenancy.InstituteSuspended, tenancy.InstituteClosed} {
			h.SetInstituteStatus(a.InstituteID(), st)
			if _, err := accept(h, 3, h.NewUser("susp@example.com"), "susp@example.com", t0); err != staff.ErrInvalidInvitation {
				t.Errorf("institute %s: got %v", st, err)
			}
		}
		if len(h.Members(a)) != members || h.AuditTotal(a.InstituteID()) != before {
			t.Fatal("refused accepts must add no members and no audit events")
		}
	})

	t.Run("accept: a removed member is reactivated with the new role", func(t *testing.T) {
		h := newHarness(t)
		a, owner := h.NewInstitute()
		uid := h.NewUser("back@example.com")
		old := h.AddMember(a, uid, auth.InstituteAdmin)
		must(t, h.Store.RemoveMember(ctx, a, old.MembershipID, ev("member.removed", "membership")))
		create(t, h, a, owner, "back@example.com", 1, auth.InstituteStaff)
		res, err := accept(h, 1, uid, "back@example.com", t0)
		must(t, err)
		if res.Member.MembershipID != old.MembershipID || res.Member.Role != auth.InstituteStaff || res.Member.Status != tenancy.MembershipActive {
			t.Fatalf("reactivation: %+v", res.Member)
		}
	})

	t.Run("members: change role, protect owners, remove", func(t *testing.T) {
		h := newHarness(t)
		a, owner := h.NewInstitute()
		var ownerMem tenancy.MemberView
		for _, m := range h.Members(a) {
			if m.UserID == owner {
				ownerMem = m
			}
		}
		staffMem := h.AddMember(a, h.NewUser("s@example.com"), auth.InstituteStaff)

		up, err := h.Store.ChangeMember(ctx, a, staffMem.MembershipID, auth.InstituteAdmin, nil, ev("member.role_changed", "membership"))
		must(t, err)
		if up.Role != auth.InstituteAdmin || up.MembershipID != staffMem.MembershipID {
			t.Fatalf("change: %+v", up)
		}
		up, err = h.Store.ChangeMember(ctx, a, staffMem.MembershipID, auth.InstituteStaff, []auth.Permission{auth.PermCertsIssue}, ev("member.role_changed", "membership"))
		must(t, err)
		if up.Role != auth.InstituteStaff || len(up.Grants) != 1 {
			t.Fatalf("change back with grant: %+v", up)
		}
		if got := h.AuditActions("membership", staffMem.MembershipID); len(got) != 2 || got[0] != "member.role_changed" {
			t.Fatalf("audit: %v", got)
		}
		before := h.AuditTotal(a.InstituteID())
		if _, err := h.Store.ChangeMember(ctx, a, ownerMem.MembershipID, auth.InstituteStaff, nil, ev("member.role_changed", "membership")); err != staff.ErrProtected {
			t.Fatalf("owner must be protected: %v", err)
		}
		if err := h.Store.RemoveMember(ctx, a, ownerMem.MembershipID, ev("member.removed", "membership")); err != staff.ErrProtected {
			t.Fatalf("owner removal must be protected: %v", err)
		}
		if h.AuditTotal(a.InstituteID()) != before {
			t.Fatal("refused changes write no audit events")
		}
		must(t, h.Store.RemoveMember(ctx, a, staffMem.MembershipID, ev("member.removed", "membership")))
		if err := h.Store.RemoveMember(ctx, a, staffMem.MembershipID, ev("member.removed", "membership")); err != staff.ErrNotFound {
			t.Fatalf("removing twice: %v", err)
		}
		if _, err := h.Store.ChangeMember(ctx, a, staffMem.MembershipID, auth.InstituteAdmin, nil, ev("member.role_changed", "membership")); err != staff.ErrNotFound {
			t.Fatalf("changing a removed member: %v", err)
		}
		for _, m := range h.Members(a) {
			if m.MembershipID == staffMem.MembershipID && m.Status != tenancy.MembershipRemoved {
				t.Fatalf("member not removed: %+v", m)
			}
		}
	})

	t.Run("isolation: another institute can neither see nor change invitations and members", func(t *testing.T) {
		h := newHarness(t)
		a, ownerA := h.NewInstitute()
		b, ownerB := h.NewInstitute()
		inv := create(t, h, a, ownerA, "asha@example.com", 1, auth.InstituteStaff)
		memA := h.AddMember(a, h.NewUser("m@example.com"), auth.InstituteStaff)
		if list, _ := h.Store.ListInvitations(ctx, b, 100); len(list) != 0 {
			t.Fatalf("B sees A's invitations: %+v", list)
		}
		if _, err := h.Store.RevokeInvitation(ctx, b, inv.ID, t0, ev("invitation.revoked", "institute_invitation")); err != staff.ErrNotFound {
			t.Fatalf("foreign revoke: %v", err)
		}
		if _, err := h.Store.ChangeMember(ctx, b, memA.MembershipID, auth.InstituteAdmin, nil, ev("member.role_changed", "membership")); err != staff.ErrNotFound {
			t.Fatalf("foreign change: %v", err)
		}
		if err := h.Store.RemoveMember(ctx, b, memA.MembershipID, ev("member.removed", "membership")); err != staff.ErrNotFound {
			t.Fatalf("foreign remove: %v", err)
		}
		if _, err := h.Store.RevokeInvitation(ctx, b, "00000000-0000-0000-0000-000000000000", t0, ev("invitation.revoked", "institute_invitation")); err != staff.ErrNotFound {
			t.Fatalf("unknown id: %v", err)
		}
		list, _ := h.Store.ListInvitations(ctx, a, 100)
		if len(list) != 1 || list[0].Status != staff.InvitationPending {
			t.Fatalf("A's invitation was touched: %+v", list)
		}
		// the same email may be invited by both institutes independently
		create(t, h, b, ownerB, "asha@example.com", 2, auth.InstituteStaff)
	})
}
