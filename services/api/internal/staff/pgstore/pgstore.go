// Package pgstore is the PostgreSQL implementation of staff.Store and staff.Acceptor.
//
// Invitations and memberships are GLOBAL tables (see migration 0009 and ADR-001): the tenant-facing
// methods filter every statement by the Scope's institute id; AcceptInvitation is the one operation that
// starts without a tenant and is authorised by the token hash plus the signed-in user's email.
package pgstore

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/certificate-os/api/internal/audit"
	auditpg "github.com/certificate-os/api/internal/audit/pgstore"
	"github.com/certificate-os/api/internal/auth"
	"github.com/certificate-os/api/internal/platform/tenantdb"
	"github.com/certificate-os/api/internal/staff"
	"github.com/certificate-os/api/internal/tenancy"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct{ Pool *pgxpool.Pool }

func New(pool *pgxpool.Pool) *Store { return &Store{Pool: pool} }

var (
	_ staff.Store    = (*Store)(nil)
	_ staff.Acceptor = (*Store)(nil)
)

var uuidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func pgCode(err error) (code, constraint string) {
	var e *pgconn.PgError
	if errors.As(err, &e) {
		return e.Code, e.ConstraintName
	}
	return "", ""
}

func toGrants(in []string) []auth.Permission {
	out := make([]auth.Permission, len(in))
	for i, g := range in {
		out[i] = auth.Permission(g)
	}
	return out
}

func fromGrants(in []auth.Permission) []string {
	out := make([]string, len(in))
	for i, g := range in {
		out[i] = string(g)
	}
	return out
}

const invCols = `id::text, email, role, grants, status, invited_by::text, created_at, expires_at, accepted_at, revoked_at`

func scanInv(row pgx.Row) (staff.Invitation, error) {
	var i staff.Invitation
	var role, status string
	var grants []string
	err := row.Scan(&i.ID, &i.Email, &role, &grants, &status, &i.InvitedBy, &i.CreatedAt, &i.ExpiresAt, &i.AcceptedAt, &i.RevokedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return i, staff.ErrNotFound
	}
	i.Role, i.Grants, i.Status = auth.InstituteRole(role), toGrants(grants), staff.InvitationStatus(status)
	return i, err
}

func record(ctx context.Context, tx pgx.Tx, instituteID string, ev audit.Event, entityID string) error {
	ev.EntityID, ev.InstituteID = entityID, instituteID
	return auditpg.InsertTx(ctx, tx, ev)
}

func withMeta(m map[string]any, kv ...any) map[string]any {
	out := map[string]any{}
	for k, v := range m {
		out[k] = v
	}
	for i := 0; i+1 < len(kv); i += 2 {
		out[kv[i].(string)] = kv[i+1]
	}
	return out
}

func (s *Store) CreateInvitation(ctx context.Context, sc tenancy.Scope, in staff.NewInvitation, now time.Time, ev audit.Event) (staff.Invitation, error) {
	var out staff.Invitation
	inst := sc.InstituteID()
	err := tenantdb.WithTenant(ctx, s.Pool, sc, func(tx pgx.Tx) error {
		// Serialise invitation creation per institute so the pending limit holds under concurrency.
		if _, err := tx.Exec(ctx, `SELECT 1 FROM institutes WHERE id = $1::uuid FOR UPDATE`, inst); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE institute_invitations SET status = 'revoked', revoked_at = $3
			WHERE institute_id = $1::uuid AND email = $2 AND status = 'pending' AND expires_at <= $3`, inst, in.Email, now); err != nil {
			return err
		}
		var member bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM institute_memberships m JOIN users u ON u.id = m.user_id
			WHERE m.institute_id = $1::uuid AND lower(u.email) = $2 AND m.status = 'active')`, inst, in.Email).Scan(&member); err != nil {
			return err
		}
		if member {
			return staff.ErrAlreadyMember
		}
		var pending int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM institute_invitations
			WHERE institute_id = $1::uuid AND status = 'pending' AND expires_at > $2`, inst, now).Scan(&pending); err != nil {
			return err
		}
		if pending >= staff.MaxPendingInvites {
			return staff.ErrTooManyPending
		}
		var err error
		out, err = scanInv(tx.QueryRow(ctx, `INSERT INTO institute_invitations
			(institute_id, email, role, grants, token_hash, invited_by, created_at, expires_at)
			VALUES ($1::uuid, $2, $3, $4::text[], $5, $6::uuid, $7, $8) RETURNING `+invCols,
			inst, in.Email, string(in.Role), fromGrants(in.Grants), in.TokenHash, in.InvitedBy, now, in.ExpiresAt))
		if code, c := pgCode(err); code == "23505" && c == "invitations_pending_email_key" {
			return staff.ErrAlreadyInvited
		}
		if err != nil {
			return err
		}
		return record(ctx, tx, inst, ev, out.ID)
	})
	return out, err
}

func (s *Store) ListInvitations(ctx context.Context, sc tenancy.Scope, limit int) ([]staff.Invitation, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	out := []staff.Invitation{}
	err := tenantdb.WithTenant(ctx, s.Pool, sc, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT `+invCols+` FROM institute_invitations WHERE institute_id = $1::uuid
			ORDER BY created_at DESC, id DESC LIMIT $2`, sc.InstituteID(), limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			i, err := scanInv(rows)
			if err != nil {
				return err
			}
			out = append(out, i)
		}
		return rows.Err()
	})
	return out, err
}

func (s *Store) RevokeInvitation(ctx context.Context, sc tenancy.Scope, id string, now time.Time, ev audit.Event) (staff.Invitation, error) {
	if !uuidRe.MatchString(id) {
		return staff.Invitation{}, staff.ErrNotFound
	}
	var out staff.Invitation
	err := tenantdb.WithTenant(ctx, s.Pool, sc, func(tx pgx.Tx) error {
		var err error
		out, err = scanInv(tx.QueryRow(ctx, `UPDATE institute_invitations SET status = 'revoked', revoked_at = $3
			WHERE institute_id = $1::uuid AND id = $2::uuid AND status = 'pending' RETURNING `+invCols, sc.InstituteID(), id, now))
		if errors.Is(err, staff.ErrNotFound) {
			var one int
			if e := tx.QueryRow(ctx, `SELECT 1 FROM institute_invitations WHERE institute_id = $1::uuid AND id = $2::uuid`, sc.InstituteID(), id).Scan(&one); e == nil {
				return staff.ErrState
			}
			return staff.ErrNotFound
		}
		if err != nil {
			return err
		}
		return record(ctx, tx, sc.InstituteID(), ev, out.ID)
	})
	return out, err
}

func (s *Store) ChangeMember(ctx context.Context, sc tenancy.Scope, membershipID string, role auth.InstituteRole, grants []auth.Permission, ev audit.Event) (tenancy.MemberView, error) {
	var out tenancy.MemberView
	if !uuidRe.MatchString(membershipID) {
		return out, staff.ErrNotFound
	}
	err := tenantdb.WithTenant(ctx, s.Pool, sc, func(tx pgx.Tx) error {
		var cur string
		err := tx.QueryRow(ctx, `SELECT role FROM institute_memberships
			WHERE institute_id = $1::uuid AND id = $2::uuid AND status = 'active' FOR UPDATE`, sc.InstituteID(), membershipID).Scan(&cur)
		if errors.Is(err, pgx.ErrNoRows) {
			return staff.ErrNotFound
		}
		if err != nil {
			return err
		}
		if auth.InstituteRole(cur) == auth.InstituteOwner {
			return staff.ErrProtected
		}
		var r, status string
		var g []string
		if err := tx.QueryRow(ctx, `UPDATE institute_memberships m SET role = $3, grants = $4::text[]
			FROM users u WHERE m.institute_id = $1::uuid AND m.id = $2::uuid AND u.id = m.user_id
			RETURNING m.id::text, m.user_id::text, u.email, m.role, m.grants, m.status`, sc.InstituteID(), membershipID, string(role), fromGrants(grants)).
			Scan(&out.MembershipID, &out.UserID, &out.Email, &r, &g, &status); err != nil {
			return err
		}
		out.Role, out.Grants, out.Status = auth.InstituteRole(r), toGrants(g), tenancy.MembershipStatus(status)
		ev.Metadata = withMeta(ev.Metadata, "from_role", cur)
		return record(ctx, tx, sc.InstituteID(), ev, membershipID)
	})
	return out, err
}

func (s *Store) RemoveMember(ctx context.Context, sc tenancy.Scope, membershipID string, ev audit.Event) error {
	if !uuidRe.MatchString(membershipID) {
		return staff.ErrNotFound
	}
	return tenantdb.WithTenant(ctx, s.Pool, sc, func(tx pgx.Tx) error {
		var cur string
		err := tx.QueryRow(ctx, `SELECT role FROM institute_memberships
			WHERE institute_id = $1::uuid AND id = $2::uuid AND status = 'active' FOR UPDATE`, sc.InstituteID(), membershipID).Scan(&cur)
		if errors.Is(err, pgx.ErrNoRows) {
			return staff.ErrNotFound
		}
		if err != nil {
			return err
		}
		if auth.InstituteRole(cur) == auth.InstituteOwner {
			return staff.ErrProtected
		}
		if _, err := tx.Exec(ctx, `UPDATE institute_memberships SET status = 'removed', removed_at = now()
			WHERE institute_id = $1::uuid AND id = $2::uuid`, sc.InstituteID(), membershipID); err != nil {
			return err
		}
		return record(ctx, tx, sc.InstituteID(), ev, membershipID)
	})
}

func (s *Store) AcceptInvitation(ctx context.Context, tokenHash, userID, userEmail string, now time.Time, ev audit.Event) (staff.Accepted, error) {
	var out staff.Accepted
	if len(tokenHash) != 64 || !uuidRe.MatchString(userID) {
		return out, staff.ErrInvalidInvitation
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return out, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var invID, instID, email, role, status string
	var grants []string
	var expires time.Time
	err = tx.QueryRow(ctx, `SELECT id::text, institute_id::text, email, role, grants, status, expires_at
		FROM institute_invitations WHERE token_hash = $1 FOR UPDATE`, tokenHash).Scan(&invID, &instID, &email, &role, &grants, &status, &expires)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, staff.ErrInvalidInvitation
	}
	if err != nil {
		return out, err
	}
	if status != "pending" || !now.Before(expires) || !strings.EqualFold(email, userEmail) {
		return out, staff.ErrInvalidInvitation
	}
	var istatus string
	if err := tx.QueryRow(ctx, `SELECT id::text, slug, name, status, created_at FROM institutes WHERE id = $1::uuid`, instID).
		Scan(&out.Institute.ID, &out.Institute.Slug, &out.Institute.Name, &istatus, &out.Institute.CreatedAt); err != nil {
		return out, err
	}
	out.Institute.Status = tenancy.InstituteStatus(istatus)
	if out.Institute.Status != tenancy.InstituteActive {
		return out, staff.ErrInvalidInvitation
	}

	var memID, memStatus string
	err = tx.QueryRow(ctx, `SELECT id::text, status FROM institute_memberships
		WHERE institute_id = $1::uuid AND user_id = $2::uuid FOR UPDATE`, instID, userID).Scan(&memID, &memStatus)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		err = tx.QueryRow(ctx, `INSERT INTO institute_memberships (institute_id, user_id, role, grants)
			VALUES ($1::uuid, $2::uuid, $3, $4::text[]) RETURNING id::text`, instID, userID, role, grants).Scan(&memID)
	case err != nil:
	case memStatus == "active":
		return out, staff.ErrInvalidInvitation
	default: // reactivate a removed membership with the invited role
		_, err = tx.Exec(ctx, `UPDATE institute_memberships SET role = $2, grants = $3::text[], status = 'active', removed_at = NULL
			WHERE id = $1::uuid`, memID, role, grants)
	}
	if err != nil {
		return out, err
	}
	if _, err := tx.Exec(ctx, `UPDATE institute_invitations SET status = 'accepted', accepted_at = $2, accepted_by = $3::uuid
		WHERE id = $1::uuid`, invID, now, userID); err != nil {
		return out, err
	}
	ev.Metadata = withMeta(ev.Metadata, "role", role)
	if err := record(ctx, tx, instID, ev, invID); err != nil {
		return out, err
	}
	if err := tx.Commit(ctx); err != nil {
		return out, err
	}
	out.Member = tenancy.MemberView{MembershipID: memID, UserID: userID, Email: userEmail, Role: auth.InstituteRole(role),
		Grants: toGrants(grants), Status: tenancy.MembershipActive}
	return out, nil
}
