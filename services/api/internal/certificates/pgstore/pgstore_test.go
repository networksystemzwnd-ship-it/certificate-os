package pgstore_test

import (
	"context"
	"encoding/json"
	"os"
	"strconv"
	"testing"

	"github.com/certificate-os/api/internal/audit"
	auditpg "github.com/certificate-os/api/internal/audit/pgstore"
	"github.com/certificate-os/api/internal/certificates/certtest"
	"github.com/certificate-os/api/internal/certificates/pgstore"
	"github.com/certificate-os/api/internal/platform/migrate"
	"github.com/certificate-os/api/internal/tenancy"
	tenantpg "github.com/certificate-os/api/internal/tenancy/pgstore"
	"github.com/certificate-os/api/migrations"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Runs only against a disposable database (TEST_DATABASE_URL, owner/superuser). The store is exercised
// through a second pool acting as the RESTRICTED runtime role when TEST_APP_DATABASE_URL is set, so
// row-level security, the counter/insert-only grants and the immutability triggers are part of what the
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
	tenants, reader := tenantpg.New(owner), auditpg.New(owner)
	n, u, f := 0, 0, 0
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := owner.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	build := func(t *testing.T) certtest.Harness {
		// TRUNCATE does not fire the immutability triggers (they guard UPDATE/DELETE only).
		exec(`TRUNCATE bulk_job_items, bulk_jobs, certificate_files, certificates, certificate_counters, certificate_template_versions, certificate_templates,
			enrollments, students, batches, courses, institute_memberships, institute_applications, institutes, users, audit_logs CASCADE`)
		return certtest.Harness{
			Store: pgstore.New(rt),
			NewScope: func() tenancy.Scope {
				n++
				inst, err := tenants.CreateInstitute(ctx, "cert-"+strconv.Itoa(n), "Institute "+strconv.Itoa(n))
				if err != nil {
					t.Fatal(err)
				}
				return tenancy.TrustedScope(inst.ID)
			},
			NewUser: func() string {
				u++
				var id string
				if err := owner.QueryRow(ctx, `INSERT INTO users (email, password_hash) VALUES ($1, 'x') RETURNING id::text`,
					"cert-user-"+strconv.Itoa(u)+"@t.test").Scan(&id); err != nil {
					t.Fatal(err)
				}
				return id
			},
			NewFixture: func(s tenancy.Scope, o certtest.FixtureOpts) certtest.Refs {
				f++
				k := strconv.Itoa(f)
				if o.StudentName == "" {
					o.StudentName = "Asha Verma"
				}
				if o.Versions < 1 {
					o.Versions = 1
				}
				inst := s.InstituteID()
				var course, batch, student, enrollment, tpl string
				batch, tpl = o.BatchID, o.TemplateID
				if batch == "" {
					if err := owner.QueryRow(ctx, `INSERT INTO courses (institute_id, name) VALUES ($1::uuid, 'Web Design ' || $2) RETURNING id::text`, inst, k).Scan(&course); err != nil {
						t.Fatal(err)
					}
					if err := owner.QueryRow(ctx, `INSERT INTO batches (institute_id, course_id, name) VALUES ($1::uuid, $2::uuid, 'Batch A') RETURNING id::text`, inst, course).Scan(&batch); err != nil {
						t.Fatal(err)
					}
				}
				if err := owner.QueryRow(ctx, `INSERT INTO students (institute_id, student_number, full_name, archived_at)
					VALUES ($1::uuid, 'ST-' || $2, $3, CASE WHEN $4 THEN now() END) RETURNING id::text`, inst, k, o.StudentName, o.StudentArchived).Scan(&student); err != nil {
					t.Fatal(err)
				}
				status, completion := "completed", "2026-03-15"
				if o.Incomplete {
					status, completion = "enrolled", ""
				}
				if err := owner.QueryRow(ctx, `INSERT INTO enrollments (institute_id, student_id, batch_id, status, completion_date, grade)
					VALUES ($1::uuid, $2::uuid, $3::uuid, $4, nullif($5, '')::date, $6) RETURNING id::text`, inst, student, batch, status, completion, o.Grade).Scan(&enrollment); err != nil {
					t.Fatal(err)
				}
				if tpl == "" {
					if err := owner.QueryRow(ctx, `INSERT INTO certificate_templates (institute_id, name, current_version, archived_at)
						VALUES ($1::uuid, 'Diploma ' || $2, $3, CASE WHEN $4 THEN now() END) RETURNING id::text`, inst, k, o.Versions, o.TemplateArchived).Scan(&tpl); err != nil {
						t.Fatal(err)
					}
					for v := 1; v <= o.Versions; v++ {
						cfg, _ := json.Marshal(certtest.TemplateConfig(v))
						exec(`INSERT INTO certificate_template_versions (institute_id, template_id, number, config) VALUES ($1::uuid, $2::uuid, $3, $4::jsonb)`, inst, tpl, v, string(cfg))
					}
				}
				return certtest.Refs{StudentID: student, EnrollmentID: enrollment, TemplateID: tpl, BatchID: batch}
			},
			AuditActions: func(id string) []string {
				recs, err := reader.List(ctx, audit.Filter{EntityType: "certificate", EntityID: id, Limit: 200})
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
			AuditMeta: func(id string) map[string]any {
				recs, err := reader.List(ctx, audit.Filter{EntityType: "certificate", EntityID: id, Limit: 1})
				if err != nil || len(recs) == 0 {
					return nil
				}
				return recs[0].Metadata
			},
		}
	}
	t.Run("certificates", func(t *testing.T) { certtest.RunStoreContract(t, build) })
	t.Run("bulk jobs", func(t *testing.T) {
		certtest.RunJobStoreContract(t, func(t *testing.T) certtest.JobHarness {
			h := build(t)
			return certtest.JobHarness{
				Harness: h,
				Jobs:    pgstore.New(rt),
				SetActive: func(sc tenancy.Scope, active bool) {
					st := "suspended"
					if active {
						st = "active"
					}
					exec(`UPDATE institutes SET status = $2 WHERE id = $1::uuid`, sc.InstituteID(), st)
				},
				JobAudit: func(id string) ([]string, map[string]any) {
					recs, err := reader.List(ctx, audit.Filter{EntityType: "bulk_job", EntityID: id, Limit: 200})
					if err != nil {
						t.Fatal(err)
					}
					var acts []string
					for i := len(recs) - 1; i >= 0; i-- {
						acts = append(acts, recs[i].Action)
					}
					if len(recs) == 0 {
						return acts, nil
					}
					return acts, recs[0].Metadata
				},
			}
		})
	})
}
