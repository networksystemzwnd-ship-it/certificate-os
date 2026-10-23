package tenantdb_test

import (
	"context"
	"errors"
	"os"
	"sort"
	"testing"

	"github.com/certificate-os/api/internal/platform/migrate"
	"github.com/certificate-os/api/internal/platform/tenantdb"
	"github.com/certificate-os/api/migrations"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// globalTables are intentionally NOT tenant-owned: identity, onboarding, platform bookkeeping and the
// append-only audit trail. Adding a table here is a security-review decision (ADR-001). Every other table
// must carry institute_id and FORCE row-level security.
var globalTables = map[string]bool{
	"schema_migrations": true, "users": true, "sessions": true, "institutes": true,
	"institute_memberships": true, "institute_applications": true, "audit_logs": true,
}

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	migs, err := migrate.Load(migrations.FS)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (&migrate.Runner{Pool: pool, Migs: migs}).Up(ctx); err != nil {
		t.Fatal(err)
	}
	return pool
}

// Fails the build when someone adds a tenant-owned table without RLS, or a global table without review.
func TestEveryTableIsClassified(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	rows, err := pool.Query(ctx, `
		SELECT c.relname, c.relrowsecurity, c.relforcerowsecurity,
		       EXISTS (SELECT 1 FROM information_schema.columns k
		               WHERE k.table_schema = 'public' AND k.table_name = c.relname AND k.column_name = 'institute_id' AND k.is_nullable = 'NO'),
		       (SELECT count(*) FROM pg_policies p WHERE p.schemaname = 'public' AND p.tablename = c.relname)
		FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = 'public' AND c.relkind IN ('r', 'p')`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var problems []string
	for rows.Next() {
		var name string
		var rls, force, hasInstitute bool
		var policies int
		if err := rows.Scan(&name, &rls, &force, &hasInstitute, &policies); err != nil {
			t.Fatal(err)
		}
		if globalTables[name] {
			continue
		}
		switch {
		case !hasInstitute:
			problems = append(problems, name+": not in globalTables and has no NOT NULL institute_id column")
		case !rls || !force:
			problems = append(problems, name+": tenant-owned table must ENABLE and FORCE ROW LEVEL SECURITY")
		case policies == 0:
			problems = append(problems, name+": RLS enabled but no policy (nothing would ever match)")
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	sort.Strings(problems)
	for _, p := range problems {
		t.Error(p)
	}
}

// The runtime role carries only what the application needs, and the audit trail stays append-only for it.
func TestRuntimeRolePrivileges(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	var super, bypass bool
	if err := pool.QueryRow(ctx, `SELECT rolsuper, rolbypassrls FROM pg_roles WHERE rolname = $1`, tenantdb.RuntimeGroup).Scan(&super, &bypass); err != nil {
		t.Fatal(err)
	}
	if super || bypass {
		t.Fatal("runtime group must be neither superuser nor BYPASSRLS")
	}
	deny := []struct{ table, priv string }{
		{"audit_logs", "UPDATE"}, {"audit_logs", "DELETE"}, {"audit_logs", "TRUNCATE"},
		{"schema_migrations", "SELECT"}, {"users", "DELETE"}, {"users", "TRUNCATE"},
	}
	for _, d := range deny {
		var has bool
		if err := pool.QueryRow(ctx, `SELECT has_table_privilege($1, $2, $3)`, tenantdb.RuntimeGroup, d.table, d.priv).Scan(&has); err != nil {
			t.Fatal(err)
		}
		if has {
			t.Errorf("runtime role must not have %s on %s", d.priv, d.table)
		}
	}
	// Behavioural proof: act as the runtime role inside a rolled-back transaction.
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SET LOCAL ROLE `+tenantdb.RuntimeGroup); err != nil {
		t.Skipf("cannot SET ROLE %s (connect as a superuser or a member of the role): %v", tenantdb.RuntimeGroup, err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_logs (actor_type, action, entity_type, entity_id) VALUES ('system', 'test.privilege', 'test', 'priv')`); err != nil {
		t.Fatalf("runtime role must be able to append audit events: %v", err)
	}
	_, err = tx.Exec(ctx, `UPDATE audit_logs SET action = 'tampered'`)
	var pe *pgconn.PgError
	if err == nil || !asPg(err, &pe) || pe.Code != "42501" {
		t.Fatalf("UPDATE on audit_logs must fail with permission denied (42501), got %v", err)
	}
}

func asPg(err error, target **pgconn.PgError) bool { return errors.As(err, target) }
