package pgstore_test

import (
	"context"
	"os"
	"strconv"
	"testing"

	"github.com/certificate-os/api/internal/audit"
	auditpg "github.com/certificate-os/api/internal/audit/pgstore"
	"github.com/certificate-os/api/internal/auth"
	authpg "github.com/certificate-os/api/internal/auth/pgstore"
	"github.com/certificate-os/api/internal/platform/migrate"
	"github.com/certificate-os/api/internal/staff/pgstore"
	"github.com/certificate-os/api/internal/staff/stafftest"
	"github.com/certificate-os/api/internal/tenancy"
	tenantpg "github.com/certificate-os/api/internal/tenancy/pgstore"
	"github.com/certificate-os/api/migrations"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Runs only against a disposable database (TEST_DATABASE_URL, owner). With TEST_APP_DATABASE_URL (the
// restricted login role) the store under test runs as that role, proving its privileges suffice.
func TestPgStoreContract(t *testing.T) {
	ownerURL := os.Getenv("TEST_DATABASE_URL")
	if ownerURL == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	owner, err := pgxpool.New(ctx, ownerURL)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	migs, err := migrate.Load(migrations.FS)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (&migrate.Runner{Pool: owner, Migs: migs}).Up(ctx); err != nil {
		t.Fatal(err)
	}
	rt := owner
	if appURL := os.Getenv("TEST_APP_DATABASE_URL"); appURL != "" {
		if rt, err = pgxpool.New(ctx, appURL); err != nil {
			t.Fatal(err)
		}
		defer rt.Close()
	} else {
		t.Log("WARNING: TEST_APP_DATABASE_URL not set; running as the owner, so the runtime role's privileges are NOT exercised")
	}
	tenants, users, reader := tenantpg.New(owner), authpg.New(owner), auditpg.New(owner)
	n := 0
	stafftest.RunStoreContract(t, func(t *testing.T) stafftest.Harness {
		if _, err := owner.Exec(ctx, `TRUNCATE institute_invitations, student_imports, enrollments, students, batches, courses, institute_memberships, institute_applications, institutes, users, audit_logs CASCADE`); err != nil {
			t.Fatal(err)
		}
		st := pgstore.New(rt)
		newUser := func(email string) string {
			u, err := users.CreateUser(ctx, email, "hash")
			if err != nil {
				t.Fatal(err)
			}
			return u.ID
		}
		made := map[string]string{}
		return stafftest.Harness{
			Store: st, Acceptor: st,
			NewUser: func(email string) string {
				if id, ok := made[email]; ok {
					return id
				}
				made[email] = newUser(email)
				return made[email]
			},
			NewInstitute: func() (tenancy.Scope, string) {
				n++
				inst, err := tenants.CreateInstitute(ctx, "stf-"+strconv.Itoa(n), "Staff Institute "+strconv.Itoa(n))
				if err != nil {
					t.Fatal(err)
				}
				uid := newUser("owner" + strconv.Itoa(n) + "@example.test")
				if _, err := tenants.AddMembership(ctx, inst.ID, uid, auth.InstituteOwner, nil); err != nil {
					t.Fatal(err)
				}
				return tenancy.TrustedScope(inst.ID), uid
			},
			AddMember: func(s tenancy.Scope, userID string, role auth.InstituteRole) tenancy.MemberView {
				m, err := tenants.AddMembership(ctx, s.InstituteID(), userID, role, nil)
				if err != nil {
					t.Fatal(err)
				}
				return m
			},
			Members: func(s tenancy.Scope) []tenancy.MemberView {
				l, err := tenants.ListMembers(ctx, s)
				if err != nil {
					t.Fatal(err)
				}
				return l
			},
			SetInstituteStatus: func(id string, s tenancy.InstituteStatus) {
				if err := tenants.SetInstituteStatus(ctx, id, s); err != nil {
					t.Fatal(err)
				}
			},
			AuditActions: func(entityType, id string) []string {
				recs, err := reader.List(ctx, audit.Filter{EntityType: entityType, EntityID: id, Limit: 200})
				if err != nil {
					t.Fatal(err)
				}
				var out []string
				for i := len(recs) - 1; i >= 0; i-- {
					out = append(out, recs[i].Action)
				}
				return out
			},
			AuditTotal: func(instituteID string) int {
				recs, err := reader.List(ctx, audit.Filter{Limit: 500})
				if err != nil {
					t.Fatal(err)
				}
				c := 0
				for _, r := range recs {
					if r.InstituteID == instituteID {
						c++
					}
				}
				return c
			},
		}
	})
}
