package tenancytest

import (
	"context"
	"errors"
	"testing"

	"github.com/certificate-os/api/internal/auth"
	"github.com/certificate-os/api/internal/tenancy"
)

// Store combines both ports; MemStore and pgstore implement it.
type Store interface {
	tenancy.Store
	tenancy.AdminStore
}

// RunStoreContract verifies a tenancy store. newStore must return an empty store plus a function
// that makes a real user ID (Postgres enforces a foreign key to users; memory stores can invent one).
func RunStoreContract(t *testing.T, newStore func(t *testing.T) (Store, func(email string) string)) {
	ctx := context.Background()
	t.Run("institutes", func(t *testing.T) {
		s, _ := newStore(t)
		a, err := s.CreateInstitute(ctx, "alpha-academy", "Alpha Academy")
		if err != nil || a.ID == "" || a.Status != tenancy.InstituteActive {
			t.Fatalf("create: %+v %v", a, err)
		}
		if _, err := s.CreateInstitute(ctx, "alpha-academy", "Dup"); !errors.Is(err, tenancy.ErrSlugTaken) {
			t.Fatalf("dup slug: %v", err)
		}
		if _, err := s.CreateInstitute(ctx, "Bad Slug!", "X"); err == nil {
			t.Fatal("invalid slug accepted")
		}
		got, err := s.InstituteBySlug(ctx, "alpha-academy")
		if err != nil || got.ID != a.ID {
			t.Fatalf("by slug: %v", err)
		}
		if _, err := s.InstituteBySlug(ctx, "missing-one"); !errors.Is(err, tenancy.ErrNotFound) {
			t.Fatalf("missing: %v", err)
		}
	})

	t.Run("memberships and scope isolation", func(t *testing.T) {
		s, uid := newStore(t)
		a, _ := s.CreateInstitute(ctx, "alpha-academy", "Alpha Academy")
		b, _ := s.CreateInstitute(ctx, "beta-institute", "Beta Institute")
		u1, u2, u3 := uid("u1@x.com"), uid("u2@x.com"), uid("u3@x.com")

		if _, err := s.AddMembership(ctx, a.ID, u1, auth.InstituteOwner, nil); err != nil {
			t.Fatal(err)
		}
		if _, err := s.AddMembership(ctx, a.ID, u1, auth.InstituteAdmin, nil); !errors.Is(err, tenancy.ErrMembershipExists) {
			t.Fatalf("duplicate membership: %v", err)
		}
		if _, err := s.AddMembership(ctx, b.ID, u2, auth.InstituteStaff, []auth.Permission{auth.PermCertsIssue}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.AddMembership(ctx, b.ID, u3, auth.InstituteStaff, []auth.Permission{auth.PermStaffManage}); err == nil {
			t.Fatal("owner-only grant accepted")
		}

		// A scope only ever sees its own institute and its own members.
		sa, sb := tenancy.TrustedScope(a.ID), tenancy.TrustedScope(b.ID)
		if i, _ := s.GetInstitute(ctx, sa); i.ID != a.ID {
			t.Fatal("scope A returned another institute")
		}
		ma, _ := s.ListMembers(ctx, sa)
		mb, _ := s.ListMembers(ctx, sb)
		if len(ma) != 1 || ma[0].UserID != u1 || len(mb) != 1 || mb[0].UserID != u2 {
			t.Fatalf("member leakage: A=%+v B=%+v", ma, mb)
		}
		if len(mb[0].Grants) != 1 || mb[0].Grants[0] != auth.PermCertsIssue {
			t.Fatalf("grants not stored: %+v", mb[0].Grants)
		}

		ms, _ := s.MembershipsForUser(ctx, u1)
		if len(ms) != 1 || ms[0].Institute.ID != a.ID || ms[0].Role != auth.InstituteOwner {
			t.Fatalf("memberships: %+v", ms)
		}
	})

	t.Run("removed memberships and closed institutes disappear", func(t *testing.T) {
		s, uid := newStore(t)
		a, _ := s.CreateInstitute(ctx, "alpha-academy", "Alpha Academy")
		b, _ := s.CreateInstitute(ctx, "beta-institute", "Beta Institute")
		u := uid("u@x.com")
		_, _ = s.AddMembership(ctx, a.ID, u, auth.InstituteStaff, nil)
		_, _ = s.AddMembership(ctx, b.ID, u, auth.InstituteAdmin, nil)

		if err := s.RemoveMembership(ctx, a.ID, u); err != nil {
			t.Fatal(err)
		}
		if ms, _ := s.MembershipsForUser(ctx, u); len(ms) != 1 || ms[0].Institute.ID != b.ID {
			t.Fatalf("removed membership still listed: %+v", ms)
		}
		// Removal is soft: the row stays in the institute's own list for audit.
		if ml, _ := s.ListMembers(ctx, tenancy.TrustedScope(a.ID)); len(ml) != 1 || ml[0].Status != tenancy.MembershipRemoved {
			t.Fatalf("expected soft-removed row: %+v", ml)
		}

		if err := s.SetInstituteStatus(ctx, b.ID, tenancy.InstituteSuspended); err != nil {
			t.Fatal(err)
		}
		if ms, _ := s.MembershipsForUser(ctx, u); len(ms) != 1 || ms[0].Institute.Status != tenancy.InstituteSuspended {
			t.Fatalf("suspended institute should still be listed (read-only): %+v", ms)
		}
		_ = s.SetInstituteStatus(ctx, b.ID, tenancy.InstituteClosed)
		if ms, _ := s.MembershipsForUser(ctx, u); len(ms) != 0 {
			t.Fatalf("closed institute still listed: %+v", ms)
		}
	})
}
