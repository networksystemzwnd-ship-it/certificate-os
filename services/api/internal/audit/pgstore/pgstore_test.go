package pgstore_test

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/certificate-os/api/internal/audit"
	"github.com/certificate-os/api/internal/audit/pgstore"
	"github.com/certificate-os/api/internal/platform/migrate"
	"github.com/certificate-os/api/migrations"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Runs only against a disposable database (TEST_DATABASE_URL).
func TestAuditIsAppendOnly(t *testing.T) {
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
	migs, _ := migrate.Load(migrations.FS)
	if _, err := (&migrate.Runner{Pool: pool, Migs: migs}).Up(ctx); err != nil {
		t.Fatal(err)
	}
	w := pgstore.New(pool)
	err = w.Record(ctx, audit.Event{ActorType: audit.ActorSystem, Action: "test.event", EntityType: "test", EntityID: "t-append-only",
		Metadata: map[string]any{"password": "must-not-be-stored", "ok": "yes"}})
	if err != nil {
		t.Fatal(err)
	}
	var meta string
	if err := pool.QueryRow(ctx, `SELECT metadata::text FROM audit_logs WHERE entity_id = 't-append-only'`).Scan(&meta); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(meta, "must-not-be-stored") || !strings.Contains(meta, "yes") {
		t.Fatalf("metadata not sanitized: %s", meta)
	}
	if _, err := pool.Exec(ctx, `UPDATE audit_logs SET action = 'tampered' WHERE entity_id = 't-append-only'`); err == nil {
		t.Fatal("UPDATE on audit_logs succeeded")
	}
	if _, err := pool.Exec(ctx, `DELETE FROM audit_logs WHERE entity_id = 't-append-only'`); err == nil {
		t.Fatal("DELETE on audit_logs succeeded")
	}
}
