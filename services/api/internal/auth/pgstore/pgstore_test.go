package pgstore_test

import (
	"context"
	"os"
	"testing"

	"github.com/certificate-os/api/internal/auth"
	"github.com/certificate-os/api/internal/auth/authtest"
	"github.com/certificate-os/api/internal/auth/pgstore"
	"github.com/certificate-os/api/internal/platform/migrate"
	"github.com/certificate-os/api/migrations"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Runs only against a disposable database (TEST_DATABASE_URL). Applies all migrations first.
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
	authtest.RunStoreContract(t, func(t *testing.T) auth.Store {
		if _, err := pool.Exec(ctx, `TRUNCATE users CASCADE`); err != nil {
			t.Fatal(err)
		}
		return pgstore.New(pool)
	})
}
