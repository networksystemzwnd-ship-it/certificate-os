package migrate

import (
	"context"
	"os"
	"testing"
	"testing/fstest"

	"github.com/certificate-os/api/migrations"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestLoadRealMigrations(t *testing.T) {
	migs, err := Load(migrations.FS)
	if err != nil {
		t.Fatal(err)
	}
	if len(migs) == 0 || migs[0].Version != 1 {
		t.Fatalf("unexpected migrations: %+v", migs)
	}
	for _, m := range migs {
		if m.Down == "" {
			t.Errorf("migration %04d_%s has no down file (document why if irreversible)", m.Version, m.Name)
		}
	}
}

func TestLoadRejectsGapsAndOrphans(t *testing.T) {
	gap := fstest.MapFS{
		"0001_a.up.sql": {Data: []byte("select 1")},
		"0003_c.up.sql": {Data: []byte("select 1")},
	}
	if _, err := Load(gap); err == nil {
		t.Error("expected gap error")
	}
	orphan := fstest.MapFS{"0001_a.down.sql": {Data: []byte("select 1")}}
	if _, err := Load(orphan); err == nil {
		t.Error("expected orphan down error")
	}
	conflict := fstest.MapFS{
		"0001_a.up.sql":   {Data: []byte("select 1")},
		"0001_b.down.sql": {Data: []byte("select 1")},
	}
	if _, err := Load(conflict); err == nil {
		t.Error("expected name conflict error")
	}
}

// Integration test: runs only when TEST_DATABASE_URL points at a disposable database.
func TestUpDownStatusAgainstPostgres(t *testing.T) {
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

	// Start from a clean slate.
	_, _ = pool.Exec(ctx, `DROP TABLE IF EXISTS schema_migrations; DROP FUNCTION IF EXISTS set_updated_at()`)

	migs, err := Load(migrations.FS)
	if err != nil {
		t.Fatal(err)
	}
	r := &Runner{Pool: pool, Migs: migs}

	n, err := r.Up(ctx)
	if err != nil || n != len(migs) {
		t.Fatalf("up: n=%d err=%v", n, err)
	}
	if n, err := r.Up(ctx); err != nil || n != 0 {
		t.Fatalf("second up should be a no-op: n=%d err=%v", n, err)
	}
	st, err := r.Status(ctx)
	if err != nil || !st[0].Applied {
		t.Fatalf("status: %+v err=%v", st, err)
	}
	if n, err := r.Down(ctx); err != nil || n != 1 {
		t.Fatalf("down: n=%d err=%v", n, err)
	}
	st, _ = r.Status(ctx)
	if st[len(st)-1].Applied {
		t.Fatal("last migration should be reverted")
	}
	if _, err := r.Up(ctx); err != nil {
		t.Fatalf("re-up: %v", err)
	}
}
