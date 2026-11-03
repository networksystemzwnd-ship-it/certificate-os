package pgstore_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/certificate-os/api/internal/auth/pgstore"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Runs against a disposable database (TEST_DATABASE_URL) that has migration 0017; as the restricted role when
// TEST_APP_DATABASE_URL is set.
func TestCounter(t *testing.T) {
	url := os.Getenv("TEST_APP_DATABASE_URL")
	if url == "" {
		url = os.Getenv("TEST_DATABASE_URL")
	}
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err := pool.Exec(ctx, `SELECT 1 FROM rate_limit_windows LIMIT 0`); err != nil {
		t.Skipf("rate_limit_windows not available (run the migrations first): %v", err)
	}
	c := pgstore.NewCounter(pool)
	key := "test:" + time.Now().Format("150405.000000")
	for want := 1; want <= 3; want++ {
		n, reset, err := c.Hit(ctx, key, time.Minute)
		if err != nil || n != want || reset <= 0 || reset > time.Minute {
			t.Fatalf("hit %d: got %d, reset %v, err %v", want, n, reset, err)
		}
	}
	if n, _, err := c.Hit(ctx, key+"-other", time.Minute); err != nil || n != 1 {
		t.Fatalf("another key must start at 1: %d %v", n, err)
	}
	if _, err := c.DeleteOld(ctx); err != nil {
		t.Fatal(err)
	}
}
