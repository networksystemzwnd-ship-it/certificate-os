// Package tenantdb runs tenant-owned queries inside a transaction that carries the institute ID
// as a Postgres setting (app.institute_id). It is the hook for row-level security as defense in
// depth (docs/adr/ADR-001-row-level-security.md). Application-level Scope filtering remains the
// primary control and is always required.
package tenantdb

import (
	"context"

	"github.com/certificate-os/api/internal/tenancy"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// WithTenant runs fn in a transaction with app.institute_id set (transaction-local).
func WithTenant(ctx context.Context, pool *pgxpool.Pool, scope tenancy.Scope, fn func(tx pgx.Tx) error) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT set_config('app.institute_id', $1, true)`, scope.InstituteID()); err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
