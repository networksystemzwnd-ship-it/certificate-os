package pgstore

import (
	"context"
	"time"

	"github.com/certificate-os/api/internal/auth"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Counter is the PostgreSQL implementation of auth.Counter (migration 0017). Windows are aligned to multiples of
// their length (measured by the database clock, so replicas agree) and one INSERT ... ON CONFLICT counts a hit
// atomically.
type Counter struct{ Pool *pgxpool.Pool }

func NewCounter(pool *pgxpool.Pool) *Counter { return &Counter{Pool: pool} }

var _ auth.Counter = (*Counter)(nil)

func (c *Counter) Hit(ctx context.Context, key string, window time.Duration) (int, time.Duration, error) {
	secs := window.Seconds()
	if secs < 1 {
		secs = 1
	}
	var hits int
	var resetSecs float64
	err := c.Pool.QueryRow(ctx, `
		WITH w AS (SELECT to_timestamp(floor(extract(epoch FROM now()) / $2::double precision) * $2::double precision) AS start)
		INSERT INTO rate_limit_windows (key, window_start, hits)
		SELECT $1::text, w.start, 1 FROM w
		ON CONFLICT (key, window_start) DO UPDATE SET hits = rate_limit_windows.hits + 1
		RETURNING hits, extract(epoch FROM (window_start + make_interval(secs => $2::double precision) - now()))::double precision`, key, secs).Scan(&hits, &resetSecs)
	if err != nil {
		return 0, 0, err
	}
	if resetSecs < 0 {
		resetSecs = 0
	}
	return hits, time.Duration(resetSecs * float64(time.Second)), nil
}

// DeleteOld removes windows that started more than a day ago (the longest window is one hour) and returns how many.
func (c *Counter) DeleteOld(ctx context.Context) (int, error) {
	tag, err := c.Pool.Exec(ctx, `DELETE FROM rate_limit_windows WHERE window_start < now() - interval '1 day'`)
	return int(tag.RowsAffected()), err
}
