// Package migrate is a small versioned SQL migration runner.
//
// Files are named NNNN_description.up.sql and NNNN_description.down.sql. Applied versions are
// tracked in schema_migrations. A Postgres advisory lock serializes concurrent runners.
package migrate

import (
	"context"
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const lockKey int64 = 7_463_920_118 // arbitrary app-specific advisory lock id

var fileRe = regexp.MustCompile(`^(\d{4})_([a-z0-9_]+)\.(up|down)\.sql$`)

type Migration struct {
	Version int
	Name    string
	Up      string
	Down    string // may be empty if irreversible
}

type Status struct {
	Version int
	Name    string
	Applied bool
}

// Load parses and validates migration files from fsys (root directory).
func Load(fsys fs.FS) ([]Migration, error) {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, err
	}
	byVersion := map[int]*Migration{}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		m := fileRe.FindStringSubmatch(e.Name())
		if m == nil {
			continue // ignore non-migration files (e.g. embed.go)
		}
		v, _ := strconv.Atoi(m[1])
		body, err := fs.ReadFile(fsys, e.Name())
		if err != nil {
			return nil, err
		}
		mig := byVersion[v]
		if mig == nil {
			mig = &Migration{Version: v, Name: m[2]}
			byVersion[v] = mig
		} else if mig.Name != m[2] {
			return nil, fmt.Errorf("migration %04d has conflicting names %q and %q", v, mig.Name, m[2])
		}
		if m[3] == "up" {
			if mig.Up != "" {
				return nil, fmt.Errorf("duplicate up migration for version %04d", v)
			}
			mig.Up = string(body)
		} else {
			mig.Down = string(body)
		}
	}
	out := make([]Migration, 0, len(byVersion))
	for _, m := range byVersion {
		if m.Up == "" {
			return nil, fmt.Errorf("migration %04d_%s has a down file but no up file", m.Version, m.Name)
		}
		out = append(out, *m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Version < out[j].Version })
	for i, m := range out {
		if m.Version != i+1 {
			return nil, fmt.Errorf("migration versions must be contiguous from 0001; found %04d at position %d", m.Version, i+1)
		}
	}
	return out, nil
}

type Runner struct {
	Pool *pgxpool.Pool
	Migs []Migration
}

func (r *Runner) withLock(ctx context.Context, fn func(conn *pgxpool.Conn) error) error {
	conn, err := r.Pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, lockKey); err != nil {
		return err
	}
	defer func() { _, _ = conn.Exec(context.Background(), `SELECT pg_advisory_unlock($1)`, lockKey) }()
	if _, err := conn.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version int PRIMARY KEY,
		name text NOT NULL,
		applied_at timestamptz NOT NULL DEFAULT now())`); err != nil {
		return err
	}
	return fn(conn)
}

func applied(ctx context.Context, conn *pgxpool.Conn) (map[int]bool, error) {
	rows, err := conn.Query(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	set := map[int]bool{}
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		set[v] = true
	}
	return set, rows.Err()
}

// Up applies all pending migrations, each in its own transaction.
func (r *Runner) Up(ctx context.Context) (int, error) {
	count := 0
	err := r.withLock(ctx, func(conn *pgxpool.Conn) error {
		done, err := applied(ctx, conn)
		if err != nil {
			return err
		}
		for _, m := range r.Migs {
			if done[m.Version] {
				continue
			}
			if err := execTx(ctx, conn, m.Up, `INSERT INTO schema_migrations(version, name) VALUES ($1,$2)`, m.Version, m.Name); err != nil {
				return fmt.Errorf("apply %04d_%s: %w", m.Version, m.Name, err)
			}
			count++
		}
		return nil
	})
	return count, err
}

// Down reverts the most recently applied migration. Refuses if it has no down file.
func (r *Runner) Down(ctx context.Context) (int, error) {
	reverted := 0
	err := r.withLock(ctx, func(conn *pgxpool.Conn) error {
		done, err := applied(ctx, conn)
		if err != nil {
			return err
		}
		for i := len(r.Migs) - 1; i >= 0; i-- {
			m := r.Migs[i]
			if !done[m.Version] {
				continue
			}
			if m.Down == "" {
				return fmt.Errorf("migration %04d_%s is irreversible (no down file)", m.Version, m.Name)
			}
			if err := execTx(ctx, conn, m.Down, `DELETE FROM schema_migrations WHERE version=$1`, m.Version); err != nil {
				return fmt.Errorf("revert %04d_%s: %w", m.Version, m.Name, err)
			}
			reverted = 1
			return nil
		}
		return nil
	})
	return reverted, err
}

// Status lists every known migration and whether it has been applied.
func (r *Runner) Status(ctx context.Context) ([]Status, error) {
	var out []Status
	err := r.withLock(ctx, func(conn *pgxpool.Conn) error {
		done, err := applied(ctx, conn)
		if err != nil {
			return err
		}
		for _, m := range r.Migs {
			out = append(out, Status{Version: m.Version, Name: m.Name, Applied: done[m.Version]})
		}
		return nil
	})
	return out, err
}

func execTx(ctx context.Context, conn *pgxpool.Conn, sql, bookkeeping string, args ...any) error {
	tx, err := conn.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, sql, pgx.QueryExecModeSimpleProtocol); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, bookkeeping, args...); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
