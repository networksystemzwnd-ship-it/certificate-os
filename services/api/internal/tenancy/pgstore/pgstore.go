// Package pgstore is the PostgreSQL implementation of tenancy.Store and tenancy.AdminStore.
package pgstore

import (
	"context"
	"errors"
	"regexp"
	"strings"

	"github.com/certificate-os/api/internal/audit"
	auditpg "github.com/certificate-os/api/internal/audit/pgstore"
	"github.com/certificate-os/api/internal/auth"
	"github.com/certificate-os/api/internal/tenancy"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct{ Pool *pgxpool.Pool }

func New(pool *pgxpool.Pool) *Store { return &Store{Pool: pool} }

var (
	_ tenancy.Store      = (*Store)(nil)
	_ tenancy.AdminStore = (*Store)(nil)
	_ tenancy.OpsStore   = (*Store)(nil)
)

func pgCode(err error) string {
	var e *pgconn.PgError
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

func toGrants(ss []string) []auth.Permission {
	out := make([]auth.Permission, len(ss))
	for i, s := range ss {
		out[i] = auth.Permission(s)
	}
	return out
}

func fromGrants(gs []auth.Permission) []string {
	out := make([]string, len(gs)) // non-nil: the column is NOT NULL
	for i, g := range gs {
		out[i] = string(g)
	}
	return out
}

// MembershipsForUser: active memberships only, closed institutes excluded.
func (s *Store) MembershipsForUser(ctx context.Context, userID string) ([]tenancy.MembershipView, error) {
	rows, err := s.Pool.Query(ctx,
		`SELECT m.id::text, m.role, m.grants, i.id::text, i.slug, i.name, i.status, i.created_at
		 FROM institute_memberships m JOIN institutes i ON i.id = m.institute_id
		 WHERE m.user_id = $1::uuid AND m.status = 'active' AND i.status <> 'closed'
		 ORDER BY i.name`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []tenancy.MembershipView
	for rows.Next() {
		var v tenancy.MembershipView
		var role, status string
		var grants []string
		if err := rows.Scan(&v.MembershipID, &role, &grants, &v.Institute.ID, &v.Institute.Slug, &v.Institute.Name, &status, &v.Institute.CreatedAt); err != nil {
			return nil, err
		}
		v.Role, v.Grants, v.Institute.Status = auth.InstituteRole(role), toGrants(grants), tenancy.InstituteStatus(status)
		out = append(out, v)
	}
	return out, rows.Err()
}

// GetInstitute is tenant-scoped: the only identifier it accepts comes from a resolved Scope.
func (s *Store) GetInstitute(ctx context.Context, scope tenancy.Scope) (tenancy.Institute, error) {
	var i tenancy.Institute
	var status string
	err := s.Pool.QueryRow(ctx,
		`SELECT id::text, slug, name, status, created_at FROM institutes WHERE id = $1::uuid`,
		scope.InstituteID()).Scan(&i.ID, &i.Slug, &i.Name, &status, &i.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return tenancy.Institute{}, tenancy.ErrNotFound
	}
	i.Status = tenancy.InstituteStatus(status)
	return i, err
}

func (s *Store) ListMembers(ctx context.Context, scope tenancy.Scope) ([]tenancy.MemberView, error) {
	rows, err := s.Pool.Query(ctx,
		`SELECT id::text, user_id::text, role, grants, status
		 FROM institute_memberships WHERE institute_id = $1::uuid ORDER BY created_at, id`, scope.InstituteID())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []tenancy.MemberView
	for rows.Next() {
		var m tenancy.MemberView
		var role, status string
		var grants []string
		if err := rows.Scan(&m.MembershipID, &m.UserID, &role, &grants, &status); err != nil {
			return nil, err
		}
		m.Role, m.Grants, m.Status = auth.InstituteRole(role), toGrants(grants), tenancy.MembershipStatus(status)
		out = append(out, m)
	}
	return out, rows.Err()
}

// --- AdminStore (out-of-band; never reachable from tenant handlers) ---

func (s *Store) CreateInstitute(ctx context.Context, slug, name string) (tenancy.Institute, error) {
	if err := tenancy.ValidateSlug(slug); err != nil { // also covers reserved slugs, which the DB regex does not
		return tenancy.Institute{}, err
	}
	var i tenancy.Institute
	var status string
	err := s.Pool.QueryRow(ctx,
		`INSERT INTO institutes (slug, name) VALUES ($1, $2) RETURNING id::text, slug, name, status, created_at`,
		slug, name).Scan(&i.ID, &i.Slug, &i.Name, &status, &i.CreatedAt)
	if pgCode(err) == "23505" {
		return tenancy.Institute{}, tenancy.ErrSlugTaken
	}
	i.Status = tenancy.InstituteStatus(status)
	return i, err
}

func (s *Store) InstituteBySlug(ctx context.Context, slug string) (tenancy.Institute, error) {
	var i tenancy.Institute
	var status string
	err := s.Pool.QueryRow(ctx,
		`SELECT id::text, slug, name, status, created_at FROM institutes WHERE slug = $1`, slug).
		Scan(&i.ID, &i.Slug, &i.Name, &status, &i.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return tenancy.Institute{}, tenancy.ErrNotFound
	}
	i.Status = tenancy.InstituteStatus(status)
	return i, err
}

func (s *Store) InstituteByID(ctx context.Context, id string) (tenancy.Institute, error) {
	var i tenancy.Institute
	var status string
	err := s.Pool.QueryRow(ctx,
		`SELECT id::text, slug, name, status, created_at FROM institutes WHERE id = $1::uuid`, id).
		Scan(&i.ID, &i.Slug, &i.Name, &status, &i.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) || pgCode(err) == "22P02" { // 22P02: not a valid uuid
		return tenancy.Institute{}, tenancy.ErrNotFound
	}
	i.Status = tenancy.InstituteStatus(status)
	return i, err
}

func (s *Store) SetInstituteStatus(ctx context.Context, id string, st tenancy.InstituteStatus) error {
	if !st.Valid() {
		return errors.New("invalid institute status")
	}
	tag, err := s.Pool.Exec(ctx, `UPDATE institutes SET status = $2 WHERE id = $1::uuid`, id, string(st))
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return tenancy.ErrNotFound
	}
	return nil
}

func (s *Store) AddMembership(ctx context.Context, instituteID, userID string, role auth.InstituteRole, grants []auth.Permission) (tenancy.MemberView, error) {
	if !role.Valid() {
		return tenancy.MemberView{}, errors.New("invalid role")
	}
	if err := auth.ValidateGrants(grants); err != nil {
		return tenancy.MemberView{}, err
	}
	m := tenancy.MemberView{UserID: userID, Role: role, Grants: grants, Status: tenancy.MembershipActive}
	err := s.Pool.QueryRow(ctx,
		`INSERT INTO institute_memberships (institute_id, user_id, role, grants)
		 VALUES ($1::uuid, $2::uuid, $3, $4) RETURNING id::text`,
		instituteID, userID, string(role), fromGrants(grants)).Scan(&m.MembershipID)
	switch pgCode(err) {
	case "23505":
		return tenancy.MemberView{}, tenancy.ErrMembershipExists
	case "23503":
		return tenancy.MemberView{}, tenancy.ErrNotFound
	}
	return m, err
}

func (s *Store) RemoveMembership(ctx context.Context, instituteID, userID string) error {
	tag, err := s.Pool.Exec(ctx,
		`UPDATE institute_memberships SET status = 'removed', removed_at = now()
		 WHERE institute_id = $1::uuid AND user_id = $2::uuid AND status = 'active'`, instituteID, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return tenancy.ErrNotFound
	}
	return nil
}

var uuidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// likeEscape neutralizes LIKE wildcards in operator-typed search text.
func likeEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

func (s *Store) ListInstitutes(ctx context.Context, q string, limit, offset int) ([]tenancy.Institute, error) {
	rows, err := s.Pool.Query(ctx,
		`SELECT id::text, slug, name, status, created_at FROM institutes
		 WHERE $1 = '' OR slug LIKE $2 ESCAPE '\' OR lower(name) LIKE $2 ESCAPE '\'
		 ORDER BY slug LIMIT $3 OFFSET $4`, q, "%"+likeEscape(strings.ToLower(q))+"%", limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []tenancy.Institute{}
	for rows.Next() {
		var i tenancy.Institute
		var st string
		if err := rows.Scan(&i.ID, &i.Slug, &i.Name, &st, &i.CreatedAt); err != nil {
			return nil, err
		}
		i.Status = tenancy.InstituteStatus(st)
		out = append(out, i)
	}
	return out, rows.Err()
}

func (s *Store) ChangeInstituteStatus(ctx context.Context, id string, from []tenancy.InstituteStatus, to tenancy.InstituteStatus, ev audit.Event) (tenancy.Institute, error) {
	var out tenancy.Institute
	if !uuidRe.MatchString(id) {
		return out, tenancy.ErrNotFound
	}
	if !to.Valid() {
		return out, tenancy.ErrInvalidStatusChange
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return out, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var cur string
	err = tx.QueryRow(ctx, `SELECT status FROM institutes WHERE id = $1::uuid FOR UPDATE`, id).Scan(&cur)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, tenancy.ErrNotFound
	}
	if err != nil {
		return out, err
	}
	allowed := false
	for _, f := range from {
		allowed = allowed || string(f) == cur
	}
	if !allowed {
		return out, tenancy.ErrInvalidStatusChange
	}
	var st string
	if err := tx.QueryRow(ctx, `UPDATE institutes SET status = $2 WHERE id = $1::uuid RETURNING id::text, slug, name, status, created_at`,
		id, string(to)).Scan(&out.ID, &out.Slug, &out.Name, &st, &out.CreatedAt); err != nil {
		return tenancy.Institute{}, err
	}
	out.Status = tenancy.InstituteStatus(st)
	if err := auditpg.InsertTx(ctx, tx, ev); err != nil {
		return tenancy.Institute{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return tenancy.Institute{}, err
	}
	return out, nil
}
