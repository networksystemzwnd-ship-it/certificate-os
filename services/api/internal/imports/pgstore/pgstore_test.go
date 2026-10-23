package pgstore_test

import (
	"context"
	"os"
	"testing"

	"github.com/certificate-os/api/internal/audit"
	auditpg "github.com/certificate-os/api/internal/audit/pgstore"
	authpg "github.com/certificate-os/api/internal/auth/pgstore"
	"github.com/certificate-os/api/internal/imports/importstest"
	"github.com/certificate-os/api/internal/imports/pgstore"
	"github.com/certificate-os/api/internal/platform/migrate"
	"github.com/certificate-os/api/internal/tenancy"
	tenantpg "github.com/certificate-os/api/internal/tenancy/pgstore"
	"github.com/certificate-os/api/migrations"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Runs only against a disposable database (TEST_DATABASE_URL, owner). When TEST_APP_DATABASE_URL (the
// restricted login role) is set the store runs as that role, so row-level security is part of what the
// contract proves.
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
		t.Log("WARNING: TEST_APP_DATABASE_URL not set; running as the owner, so RLS is NOT exercised by this run")
	}
	tenants, users, reader := tenantpg.New(owner), authpg.New(owner), auditpg.New(owner)
	n := 0
	importstest.RunStoreContract(t, func(t *testing.T) importstest.Harness {
		if _, err := owner.Exec(ctx, `TRUNCATE student_imports, enrollments, students, batches, courses, institute_memberships, institute_applications, institutes, users, audit_logs CASCADE`); err != nil {
			t.Fatal(err)
		}
		return importstest.Harness{
			Store: pgstore.New(rt),
			NewScope: func() (tenancy.Scope, string) {
				n++
				inst, err := tenants.CreateInstitute(ctx, "imp-"+string(rune('a'+n%26))+itoa(n), "Import Institute "+itoa(n))
				if err != nil {
					t.Fatal(err)
				}
				u, err := users.CreateUser(ctx, "imp"+itoa(n)+"@example.test", "hash")
				if err != nil {
					t.Fatal(err)
				}
				return tenancy.TrustedScope(inst.ID), u.ID
			},
			AuditTotal: func(instituteID string) int {
				recs, err := reader.List(ctx, audit.Filter{Limit: 200})
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

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for ; n > 0; n /= 10 {
		b = append([]byte{byte('0' + n%10)}, b...)
	}
	return string(b)
}
