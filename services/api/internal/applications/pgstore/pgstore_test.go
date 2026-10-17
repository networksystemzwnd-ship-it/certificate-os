package pgstore_test

import (
	"context"
	"os"
	"testing"

	"github.com/certificate-os/api/internal/applications/applicationstest"
	"github.com/certificate-os/api/internal/applications/pgstore"
	"github.com/certificate-os/api/internal/audit"
	auditpg "github.com/certificate-os/api/internal/audit/pgstore"
	authpg "github.com/certificate-os/api/internal/auth/pgstore"
	"github.com/certificate-os/api/internal/platform/migrate"
	tenantpg "github.com/certificate-os/api/internal/tenancy/pgstore"
	"github.com/certificate-os/api/migrations"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Runs only against a disposable database (TEST_DATABASE_URL).
func TestPgStoreContract(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	migs, err := migrate.Load(migrations.FS)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (&migrate.Runner{Pool: pool, Migs: migs}).Up(ctx); err != nil {
		t.Fatal(err)
	}
	users, tenants, reader := authpg.New(pool), tenantpg.New(pool), auditpg.New(pool)
	applicationstest.RunStoreContract(t, func(t *testing.T) applicationstest.Harness {
		// audit_logs is append-only (triggers); TRUNCATE is not blocked by row triggers, which is fine for tests.
		if _, err := pool.Exec(ctx, `TRUNCATE institute_applications, institute_memberships, institutes, users, audit_logs CASCADE`); err != nil {
			t.Fatal(err)
		}
		return applicationstest.Harness{
			Store: pgstore.New(pool), Tenants: tenants,
			NewUser: func(email string) string {
				u, err := users.CreateUser(ctx, email, "hash")
				if err != nil {
					t.Fatal(err)
				}
				return u.ID
			},
			AuditActions: func(entityType, id string) []string {
				recs, err := reader.List(ctx, audit.Filter{EntityType: entityType, EntityID: id, Limit: 200})
				if err != nil {
					t.Fatal(err)
				}
				var out []string
				for i := len(recs) - 1; i >= 0; i-- { // List is newest-first
					out = append(out, recs[i].Action)
				}
				return out
			},
		}
	})
}
