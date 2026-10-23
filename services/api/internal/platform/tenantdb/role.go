package tenantdb

import (
	"context"
	"errors"
	"fmt"

	"github.com/certificate-os/api/internal/platform/config"
	"github.com/jackc/pgx/v5/pgxpool"
)

// RuntimeGroup is the NOLOGIN role (migration 0006) that carries the application's privileges.
const RuntimeGroup = "certos_runtime"

// RoleInfo describes the database role a connection pool runs as.
type RoleInfo struct {
	User      string
	Superuser bool
	BypassRLS bool
}

// Restricted reports whether row-level security actually applies to this role.
// Superusers and BYPASSRLS roles ignore every policy, so RLS would protect nothing.
func (r RoleInfo) Restricted() bool { return !r.Superuser && !r.BypassRLS }

func InspectRole(ctx context.Context, pool *pgxpool.Pool) (RoleInfo, error) {
	var r RoleInfo
	err := pool.QueryRow(ctx,
		`SELECT current_user::text, rolsuper, rolbypassrls FROM pg_roles WHERE rolname = current_user`).
		Scan(&r.User, &r.Superuser, &r.BypassRLS)
	return r, err
}

// ProvisionRuntimeLogin creates (or updates the password of) the login role the API/worker connect as
// and makes it a member of the runtime group. Run it on the OWNER connection. The password is placed
// in the statement server-side with format(%L), so quoting is done by PostgreSQL; never log the statement.
// Note: with `log_statement = all` PostgreSQL would log the password; keep that off for this operation.
func ProvisionRuntimeLogin(ctx context.Context, owner *pgxpool.Pool, user, password string) error {
	if !config.ValidRoleName(user) {
		return errors.New("invalid role name")
	}
	if len(password) < 16 {
		return errors.New("APP_DB_PASSWORD must be at least 16 characters")
	}
	var exists bool
	if err := owner.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = $1)`, user).Scan(&exists); err != nil {
		return err
	}
	verb := "CREATE"
	if exists {
		verb = "ALTER"
	}
	var stmt string
	if err := owner.QueryRow(ctx,
		`SELECT format('%s ROLE %I LOGIN PASSWORD %L NOSUPERUSER NOCREATEDB NOCREATEROLE NOBYPASSRLS', $1::text, $2::text, $3::text)`,
		verb, user, password).Scan(&stmt); err != nil {
		return err
	}
	if _, err := owner.Exec(ctx, stmt); err != nil {
		return fmt.Errorf("provision role: %w", err) // never include stmt: it contains the password
	}
	var grant string
	if err := owner.QueryRow(ctx, `SELECT format('GRANT %I TO %I', $1::text, $2::text)`, RuntimeGroup, user).Scan(&grant); err != nil {
		return err
	}
	_, err := owner.Exec(ctx, grant)
	return err
}
