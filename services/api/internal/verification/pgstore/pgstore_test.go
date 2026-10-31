package pgstore_test

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/certificate-os/api/internal/platform/migrate"
	"github.com/certificate-os/api/internal/verification"
	"github.com/certificate-os/api/internal/verification/pgstore"
	"github.com/certificate-os/api/migrations"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Runs only against a disposable database. With TEST_APP_DATABASE_URL the lookup runs as the RESTRICTED runtime
// role, which is what proves the narrow public-lookup policy (and not the table owner's rights) does the work.
func TestLookupThroughThePublicPolicy(t *testing.T) {
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
	if u := os.Getenv("TEST_APP_DATABASE_URL"); u != "" {
		if rt, err = pgxpool.New(ctx, u); err != nil {
			t.Fatal(err)
		}
		defer rt.Close()
	} else {
		t.Log("WARNING: TEST_APP_DATABASE_URL not set; running as the owner, so the policy is NOT what is being tested")
	}
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := owner.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`TRUNCATE verification_events, certificate_files, certificates, certificate_counters, certificate_template_versions, certificate_templates,
		enrollments, students, batches, courses, institute_memberships, institute_applications, institutes, users, audit_logs CASCADE`)
	var inst, course, batch, stu, enr, tpl, user string
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(owner.QueryRow(ctx, `INSERT INTO institutes (slug, name) VALUES ('verify-one', 'Verify Institute') RETURNING id::text`).Scan(&inst))
	must(owner.QueryRow(ctx, `INSERT INTO users (email, password_hash) VALUES ('v@t.test', 'x') RETURNING id::text`).Scan(&user))
	must(owner.QueryRow(ctx, `INSERT INTO courses (institute_id, name) VALUES ($1::uuid, 'Web') RETURNING id::text`, inst).Scan(&course))
	must(owner.QueryRow(ctx, `INSERT INTO batches (institute_id, course_id, name) VALUES ($1::uuid, $2::uuid, 'B') RETURNING id::text`, inst, course).Scan(&batch))
	must(owner.QueryRow(ctx, `INSERT INTO students (institute_id, full_name) VALUES ($1::uuid, 'Asha Verma') RETURNING id::text`, inst).Scan(&stu))
	must(owner.QueryRow(ctx, `INSERT INTO enrollments (institute_id, student_id, batch_id, status, completion_date) VALUES ($1::uuid, $2::uuid, $3::uuid, 'completed', '2026-03-15') RETURNING id::text`, inst, stu, batch).Scan(&enr))
	must(owner.QueryRow(ctx, `INSERT INTO certificate_templates (institute_id, name, current_version) VALUES ($1::uuid, 'D', 1) RETURNING id::text`, inst).Scan(&tpl))
	exec(`INSERT INTO certificate_template_versions (institute_id, template_id, number, config) VALUES ($1::uuid, $2::uuid, 1, '{}')`, inst, tpl)
	snap := `{"institute_name":"Verify Institute","student_name":"Asha Verma","course_name":"Web","issue_date":"2026-10-28"}`
	insert := func(number, pub, status string) {
		exec(`INSERT INTO certificates (institute_id, certificate_number, public_id, student_id, enrollment_id, template_id, template_version, snapshot, issued_by, status,
				pdf_sha256, pdf_size, issued_at, failed_at, revoked_at, revoked_by, revoke_reason)
			VALUES ($1::uuid, $2, $3, $4::uuid, $5::uuid, $6::uuid, 1, $7::jsonb, $8::uuid, $9,
				CASE WHEN $9 IN ('issued','revoked') THEN repeat('a',64) END, CASE WHEN $9 IN ('issued','revoked') THEN 5 END,
				CASE WHEN $9 IN ('issued','revoked') THEN now() END, CASE WHEN $9 = 'failed' THEN now() END,
				CASE WHEN $9 = 'revoked' THEN now() END, CASE WHEN $9 = 'revoked' THEN $8::uuid END, CASE WHEN $9 = 'revoked' THEN 'internal reason' ELSE '' END)`,
			inst, number, pub, stu, enr, tpl, snap, user, status)
	}
	issued, revoked, failed := strings.Repeat("i", 43), strings.Repeat("r", 43), strings.Repeat("f", 43)
	insert("CERT-2026-000001", failed, "failed")
	insert("CERT-2026-000002", revoked, "revoked")
	insert("CERT-2026-000003", issued, "issued")

	st := pgstore.New(rt, func(msg string, args ...any) { t.Logf("%s %v", msg, args) })
	rec, err := st.Lookup(ctx, issued)
	must(err)
	if rec.Number != "CERT-2026-000003" || rec.StudentName != "Asha Verma" || rec.InstituteName != "Verify Institute" || verification.StatusOf(rec) != verification.Valid {
		t.Fatalf("issued: %+v", rec)
	}
	rec, err = st.Lookup(ctx, revoked)
	must(err)
	if verification.StatusOf(rec) != verification.Revoked || rec.RevokedAt == nil {
		t.Fatalf("revoked: %+v", rec)
	}
	for _, id := range []string{failed, strings.Repeat("z", 43)} {
		if _, err := st.Lookup(ctx, id); err != verification.ErrNotFound {
			t.Fatalf("%.5s: %v", id, err)
		}
	}
	var n int
	must(owner.QueryRow(ctx, `SELECT count(*) FROM verification_events WHERE result IN ('valid', 'revoked')`).Scan(&n))
	if n != 2 {
		t.Fatalf("events: %d", n)
	}
}
