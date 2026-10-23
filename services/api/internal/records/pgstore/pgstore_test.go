package pgstore_test

import (
	"context"
	"os"
	"testing"

	"github.com/certificate-os/api/internal/audit"
	auditpg "github.com/certificate-os/api/internal/audit/pgstore"
	"github.com/certificate-os/api/internal/platform/migrate"
	"github.com/certificate-os/api/internal/platform/tenantdb"
	"github.com/certificate-os/api/internal/records/pgstore"
	"github.com/certificate-os/api/internal/records/recordstest"
	"github.com/certificate-os/api/internal/tenancy"
	tenantpg "github.com/certificate-os/api/internal/tenancy/pgstore"
	"github.com/certificate-os/api/migrations"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Runs only against a disposable database (TEST_DATABASE_URL, owner/superuser). The store itself is
// exercised through a second pool that acts as the RESTRICTED runtime role when possible, so row-level
// security is part of what the contract proves (see TEST_APP_DATABASE_URL).
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
	// The runtime pool: a login role that is a member of certos_runtime (RLS applies to it).
	// CI/dev: TEST_APP_DATABASE_URL=postgres://certos_app:...@host/db. Without it the contract still runs
	// as the owner (application filters only) and a warning is logged.
	rt := owner
	if appURL := os.Getenv("TEST_APP_DATABASE_URL"); appURL != "" {
		rt, err = pgxpool.New(ctx, appURL)
		if err != nil {
			t.Fatal(err)
		}
		defer rt.Close()
	} else {
		t.Log("WARNING: TEST_APP_DATABASE_URL not set; running as the owner, so RLS is NOT exercised by this run")
	}
	tenants, reader := tenantpg.New(owner), auditpg.New(owner)
	n := 0
	recordstest.RunStoreContract(t, func(t *testing.T) recordstest.Harness {
		if _, err := owner.Exec(ctx, `TRUNCATE enrollments, students, batches, courses, institute_memberships, institute_applications, institutes, audit_logs CASCADE`); err != nil {
			t.Fatal(err)
		}
		return recordstest.Harness{
			Store: pgstore.New(rt),
			NewScope: func() tenancy.Scope {
				n++
				inst, err := tenants.CreateInstitute(ctx, "inst-"+itoa(n), "Institute "+itoa(n))
				if err != nil {
					t.Fatal(err)
				}
				return tenancy.TrustedScope(inst.ID)
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

// ADR-001 "filter deliberately removed" test: raw, UNFILTERED SQL as the restricted role must still be
// confined to the institute set by tenantdb.WithTenant. Needs TEST_APP_DATABASE_URL (restricted login role).
func TestRLSConfinesUnfilteredQueries(t *testing.T) {
	ownerURL, appURL := os.Getenv("TEST_DATABASE_URL"), os.Getenv("TEST_APP_DATABASE_URL")
	if ownerURL == "" || appURL == "" {
		t.Skip("TEST_DATABASE_URL and TEST_APP_DATABASE_URL must both be set")
	}
	ctx := context.Background()
	owner, err := pgxpool.New(ctx, ownerURL)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	rt, err := pgxpool.New(ctx, appURL)
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	role, err := tenantdb.InspectRole(ctx, rt)
	if err != nil {
		t.Fatal(err)
	}
	if !role.Restricted() {
		t.Fatalf("TEST_APP_DATABASE_URL must be a restricted role, got %+v", role)
	}
	if _, err := owner.Exec(ctx, `TRUNCATE enrollments, students, batches, courses, institute_memberships, institute_applications, institutes, audit_logs CASCADE`); err != nil {
		t.Fatal(err)
	}
	tenants := tenantpg.New(owner)
	a, _ := tenants.CreateInstitute(ctx, "rls-a", "RLS A")
	b, _ := tenants.CreateInstitute(ctx, "rls-b", "RLS B")
	for _, in := range []struct{ id, name string }{{a.ID, "Course A"}, {b.ID, "Course B"}} {
		if _, err := owner.Exec(ctx, `INSERT INTO courses (institute_id, name) VALUES ($1::uuid, $2)`, in.id, in.name); err != nil {
			t.Fatal(err)
		}
	}
	count := func(sc tenancy.Scope, sql string, args ...any) (n int) {
		t.Helper()
		err := tenantdb.WithTenant(ctx, rt, sc, func(tx pgx.Tx) error {
			return tx.QueryRow(ctx, sql, args...).Scan(&n)
		})
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	if n := count(tenancy.TrustedScope(a.ID), `SELECT count(*) FROM courses`); n != 1 {
		t.Fatalf("scope A must see exactly its own course with no WHERE clause, saw %d", n)
	}
	if n := count(tenancy.TrustedScope(b.ID), `SELECT count(*) FROM courses WHERE institute_id = $1::uuid`, a.ID); n != 0 {
		t.Fatalf("scope B explicitly asking for A's rows must see %d, want 0", n)
	}
	// No tenant context at all: nothing is visible (fail closed), even after a pooled connection was used by a tenant.
	var none int
	if err := rt.QueryRow(ctx, `SELECT count(*) FROM courses`).Scan(&none); err != nil || none != 0 {
		t.Fatalf("without tenant context the runtime role must see 0 rows, saw %d (err %v)", none, err)
	}
	// Writes for another tenant are rejected by WITH CHECK.
	err = tenantdb.WithTenant(ctx, rt, tenancy.TrustedScope(b.ID), func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO courses (institute_id, name) VALUES ($1::uuid, 'Planted')`, a.ID)
		return err
	})
	if err == nil {
		t.Fatal("scope B inserted a row owned by A")
	}
	// An UPDATE aimed at A's rows from scope B changes nothing.
	err = tenantdb.WithTenant(ctx, rt, tenancy.TrustedScope(b.ID), func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE courses SET name = 'Hacked' WHERE institute_id = $1::uuid`, a.ID)
		if err == nil && tag.RowsAffected() != 0 {
			t.Fatalf("scope B updated %d of A's rows", tag.RowsAffected())
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	var name string
	if err := owner.QueryRow(ctx, `SELECT name FROM courses WHERE institute_id = $1::uuid`, a.ID).Scan(&name); err != nil || name != "Course A" {
		t.Fatalf("A's course changed to %q (err %v)", name, err)
	}
}
