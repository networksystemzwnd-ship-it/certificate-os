package stafftest

import (
	"context"
	"fmt"
	"testing"

	"github.com/certificate-os/api/internal/audit/audittest"
	"github.com/certificate-os/api/internal/auth"
	"github.com/certificate-os/api/internal/tenancy"
	"github.com/certificate-os/api/internal/tenancy/tenancytest"
)

func TestMemStoreContract(t *testing.T) {
	RunStoreContract(t, func(t *testing.T) Harness {
		ctx := context.Background()
		w := audittest.NewMemWriter()
		tm := tenancytest.NewMemStore()
		ms := NewMemStore(tm, w)
		users := map[string]string{} // email -> id
		n := 0
		ms.UserIDByEmail = func(e string) string { return users[e] }
		return Harness{
			Store: ms, Acceptor: ms,
			NewUser: func(email string) string {
				if id, ok := users[email]; ok {
					return id
				}
				n++
				users[email] = fmt.Sprintf("user-%d", n)
				return users[email]
			},
			NewInstitute: func() (tenancy.Scope, string) {
				n++
				inst, err := tm.CreateInstitute(ctx, fmt.Sprintf("inst-%d", n), fmt.Sprintf("Institute %d", n))
				if err != nil {
					t.Fatal(err)
				}
				owner := fmt.Sprintf("owner-%d", n)
				if _, err := tm.AddMembership(ctx, inst.ID, owner, auth.InstituteOwner, nil); err != nil {
					t.Fatal(err)
				}
				return tenancy.TrustedScope(inst.ID), owner
			},
			AddMember: func(s tenancy.Scope, userID string, role auth.InstituteRole) tenancy.MemberView {
				m, err := tm.AddMembership(ctx, s.InstituteID(), userID, role, nil)
				if err != nil {
					t.Fatal(err)
				}
				return m
			},
			Members: func(s tenancy.Scope) []tenancy.MemberView {
				l, _ := tm.ListMembers(ctx, s)
				return l
			},
			SetInstituteStatus: func(id string, st tenancy.InstituteStatus) { _ = tm.SetInstituteStatus(ctx, id, st) },
			AuditActions: func(et, id string) []string {
				var out []string
				for _, r := range w.ForEntity(et, id) {
					out = append(out, r.Action)
				}
				return out
			},
			AuditTotal: func(inst string) int {
				c := 0
				for _, r := range w.Records() {
					if r.InstituteID == inst {
						c++
					}
				}
				return c
			},
		}
	})
}
