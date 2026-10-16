package pgstore_test

import (
	"context"
	"os"
	"testing"

	authpg "github.com/certificate-os/api/internal/auth/pgstore"
	"github.com/certificate-os/api/internal/platform/migrate"
	"github.com/certificate-os/api/internal/tenancy/pgstore"
	"github.com/certificate-os/api/internal/tenancy/tenancytest"
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
	users := authpg.New(pool)
	tenancytest.RunStoreContract(t, func(t *testing.T) (tenancytest.Store, func(string) string) {
		if _, err := pool.Exec(ctx, `TRUNCATE institute_memberships, institutes, users CASCADE`); err != nil {
			t.Fatal(err)
		}
		return pgstore.New(pool), func(email string) string {
			u, err := users.CreateUser(ctx, email, "hash")
			if err != nil {
				t.Fatal(err)
			}
			return u.ID
		}
	})
}
