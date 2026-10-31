package pgstore_test

import (
	"context"
	"os"
	"strconv"
	"testing"

	"github.com/certificate-os/api/internal/audit"
	auditpg "github.com/certificate-os/api/internal/audit/pgstore"
	"github.com/certificate-os/api/internal/platform/migrate"
	"github.com/certificate-os/api/internal/templates/pgstore"
	"github.com/certificate-os/api/internal/templates/templatestest"
	"github.com/certificate-os/api/internal/tenancy"
	tenantpg "github.com/certificate-os/api/internal/tenancy/pgstore"
	"github.com/certificate-os/api/migrations"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Runs only against a disposable database (TEST_DATABASE_URL, owner/superuser). The store is exercised
// through a second pool acting as the RESTRICTED runtime role when TEST_APP_DATABASE_URL is set, so
// row-level security and the insert-only grant on versions are part of what the contract proves.
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
	tenants, reader := tenantpg.New(owner), auditpg.New(owner)
	n, u := 0, 0
	templatestest.RunStoreContract(t, func(t *testing.T) templatestest.Harness {
		// TRUNCATE does not fire the immutability trigger (it guards UPDATE/DELETE only).
		if _, err := owner.Exec(ctx, `TRUNCATE certificate_template_versions, certificate_templates, institute_memberships, institute_applications, institutes, users, audit_logs CASCADE`); err != nil {
			t.Fatal(err)
		}
		return templatestest.Harness{
			Store: pgstore.New(rt),
			NewScope: func() tenancy.Scope {
				n++
				inst, err := tenants.CreateInstitute(ctx, "tpl-"+strconv.Itoa(n), "Institute "+strconv.Itoa(n))
				if err != nil {
					t.Fatal(err)
				}
				return tenancy.TrustedScope(inst.ID)
			},
			NewUser: func() string {
				u++
				var id string
				if err := owner.QueryRow(ctx, `INSERT INTO users (email, password_hash) VALUES ($1, 'x') RETURNING id::text`,
					"tpl-user-"+strconv.Itoa(u)+"@t.test").Scan(&id); err != nil {
					t.Fatal(err)
				}
				return id
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
			AuditMeta: func(entityType, id string) map[string]any {
				recs, err := reader.List(ctx, audit.Filter{EntityType: entityType, EntityID: id, Limit: 1})
				if err != nil || len(recs) == 0 {
					return nil
				}
				return recs[0].Metadata
			},
		}
	})
}
