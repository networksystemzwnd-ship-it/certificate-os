// Package pgstore is the PostgreSQL implementation of templates.Store.
//
// Defense in depth (ADR-001): every method runs inside tenantdb.WithTenant (row-level security applies to
// the restricted runtime role) AND every statement still filters by the Scope's institute id. Versions are
// insert-only (no grant, and a trigger blocks UPDATE/DELETE). Mutations write their audit event in the
// same transaction.
package pgstore

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strings"

	"github.com/certificate-os/api/internal/audit"
	auditpg "github.com/certificate-os/api/internal/audit/pgstore"
	"github.com/certificate-os/api/internal/platform/tenantdb"
	"github.com/certificate-os/api/internal/templates"
	"github.com/certificate-os/api/internal/tenancy"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct{ Pool *pgxpool.Pool }

func New(pool *pgxpool.Pool) *Store { return &Store{Pool: pool} }

var _ templates.Store = (*Store)(nil)

var uuidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func okID(id string) bool { return uuidRe.MatchString(id) }

func like(q string) string {
	return "%" + strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(strings.ToLower(q)) + "%"
}

func mapErr(err error) error {
	var e *pgconn.PgError
	if !errors.As(err, &e) {
		return err
	}
	switch e.Code {
	case "23505":
		if e.ConstraintName == "certificate_templates_active_name_key" {
			return &templates.ConflictError{Field: "name"}
		}
	case "22P02":
		return templates.ErrNotFound
	}
	return err
}

func (s *Store) tx(ctx context.Context, sc tenancy.Scope, fn func(tx pgx.Tx) error) error {
	return tenantdb.WithTenant(ctx, s.Pool, sc, fn)
}

func audited(ctx context.Context, tx pgx.Tx, sc tenancy.Scope, ev audit.Event, entityID string, version int) error {
	ev.EntityID, ev.InstituteID = entityID, sc.InstituteID()
	if version > 0 {
		meta := map[string]any{}
		for k, v := range ev.Metadata {
			meta[k] = v
		}
		meta["version"] = version
		ev.Metadata = meta
	}
	return auditpg.InsertTx(ctx, tx, ev)
}

// templateSel selects a template with its CURRENT version (config as text).
const templateSel = `SELECT t.id::text, t.name, t.archived_at, t.created_at, t.updated_at,
	v.number, v.config::text, coalesce(v.created_by::text, ''), v.created_at
	FROM certificate_templates t
	JOIN certificate_template_versions v ON v.institute_id = t.institute_id AND v.template_id = t.id AND v.number = t.current_version`

func scanTemplate(row pgx.Row) (templates.Template, error) {
	var t templates.Template
	var cfg string
	err := row.Scan(&t.ID, &t.Name, &t.ArchivedAt, &t.CreatedAt, &t.UpdatedAt,
		&t.Current.Number, &cfg, &t.Current.CreatedBy, &t.Current.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return t, templates.ErrNotFound
	}
	if err != nil {
		return t, err
	}
	if err := json.Unmarshal([]byte(cfg), &t.Current.Config); err != nil {
		return t, err
	}
	return t, nil
}

func (s *Store) loadVersions(ctx context.Context, tx pgx.Tx, sc tenancy.Scope, t *templates.Template) error {
	rows, err := tx.Query(ctx, `SELECT number, coalesce(created_by::text, ''), created_at FROM certificate_template_versions
		WHERE institute_id = $1::uuid AND template_id = $2::uuid ORDER BY number DESC`, sc.InstituteID(), t.ID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var v templates.VersionSummary
		if err := rows.Scan(&v.Number, &v.CreatedBy, &v.CreatedAt); err != nil {
			return err
		}
		t.Versions = append(t.Versions, v)
	}
	return rows.Err()
}

func (s *Store) CreateTemplate(ctx context.Context, sc tenancy.Scope, name string, cfg templates.Config, by string, ev audit.Event) (templates.Template, error) {
	raw, err := json.Marshal(cfg)
	if err != nil {
		return templates.Template{}, err
	}
	var out templates.Template
	err = s.tx(ctx, sc, func(tx pgx.Tx) error {
		var id string
		if err := tx.QueryRow(ctx, `INSERT INTO certificate_templates (institute_id, name) VALUES ($1::uuid, $2) RETURNING id::text`,
			sc.InstituteID(), name).Scan(&id); err != nil {
			return mapErr(err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO certificate_template_versions (institute_id, template_id, number, config, created_by)
			VALUES ($1::uuid, $2::uuid, 1, $3::jsonb, nullif($4, '')::uuid)`, sc.InstituteID(), id, string(raw), by); err != nil {
			return err
		}
		t, err := scanTemplate(tx.QueryRow(ctx, templateSel+` WHERE t.institute_id = $1::uuid AND t.id = $2::uuid`, sc.InstituteID(), id))
		if err != nil {
			return err
		}
		if err := s.loadVersions(ctx, tx, sc, &t); err != nil {
			return err
		}
		out = t
		return audited(ctx, tx, sc, ev, id, 1)
	})
	return out, err
}

func (s *Store) GetTemplate(ctx context.Context, sc tenancy.Scope, id string) (templates.Template, error) {
	if !okID(id) {
		return templates.Template{}, templates.ErrNotFound
	}
	var out templates.Template
	err := s.tx(ctx, sc, func(tx pgx.Tx) error {
		t, err := scanTemplate(tx.QueryRow(ctx, templateSel+` WHERE t.institute_id = $1::uuid AND t.id = $2::uuid`, sc.InstituteID(), id))
		if err != nil {
			return err
		}
		out = t
		return s.loadVersions(ctx, tx, sc, &out)
	})
	return out, err
}

func (s *Store) ListTemplates(ctx context.Context, sc tenancy.Scope, p templates.Page) ([]templates.Template, error) {
	p = p.Normalized()
	out := []templates.Template{}
	if strings.ContainsRune(p.Q, 0) {
		return out, nil
	}
	err := s.tx(ctx, sc, func(tx pgx.Tx) error {
		q := templateSel + ` WHERE t.institute_id = $1::uuid`
		args := []any{sc.InstituteID()}
		switch p.Archived {
		case templates.ArchivedOnly:
			q += ` AND t.archived_at IS NOT NULL`
		case templates.IncludeAll:
		default:
			q += ` AND t.archived_at IS NULL`
		}
		if p.Q != "" {
			args = append(args, like(p.Q))
			q += ` AND lower(t.name) LIKE $2 ESCAPE '\'`
		}
		args = append(args, p.Limit+1, p.Offset)
		q += ` ORDER BY lower(t.name), t.id LIMIT $` + itoa(len(args)-1) + ` OFFSET $` + itoa(len(args))
		rows, err := tx.Query(ctx, q, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			t, err := scanTemplate(rows)
			if err != nil {
				return err
			}
			out = append(out, t)
		}
		return rows.Err()
	})
	return out, err
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for ; n > 0; n /= 10 {
		b = append([]byte{byte('0' + n%10)}, b...)
	}
	return string(b)
}

func (s *Store) RenameTemplate(ctx context.Context, sc tenancy.Scope, id, name string, ev audit.Event) (templates.Template, error) {
	if !okID(id) {
		return templates.Template{}, templates.ErrNotFound
	}
	var out templates.Template
	err := s.tx(ctx, sc, func(tx pgx.Tx) error {
		ct, err := tx.Exec(ctx, `UPDATE certificate_templates SET name = $3 WHERE institute_id = $1::uuid AND id = $2::uuid`, sc.InstituteID(), id, name)
		if err != nil {
			return mapErr(err)
		}
		if ct.RowsAffected() == 0 {
			return templates.ErrNotFound
		}
		t, err := scanTemplate(tx.QueryRow(ctx, templateSel+` WHERE t.institute_id = $1::uuid AND t.id = $2::uuid`, sc.InstituteID(), id))
		if err != nil {
			return err
		}
		out = t
		if err := s.loadVersions(ctx, tx, sc, &out); err != nil {
			return err
		}
		return audited(ctx, tx, sc, ev, id, 0)
	})
	return out, err
}

func (s *Store) SetArchived(ctx context.Context, sc tenancy.Scope, id string, archived bool, ev audit.Event) (templates.Template, error) {
	if !okID(id) {
		return templates.Template{}, templates.ErrNotFound
	}
	var out templates.Template
	err := s.tx(ctx, sc, func(tx pgx.Tx) error {
		// Archiving an already archived template keeps its original archive time.
		ct, err := tx.Exec(ctx, `UPDATE certificate_templates
			SET archived_at = CASE WHEN $3 THEN coalesce(archived_at, now()) ELSE NULL END
			WHERE institute_id = $1::uuid AND id = $2::uuid`, sc.InstituteID(), id, archived)
		if err != nil {
			return mapErr(err) // restoring over a newer active template with the same name
		}
		if ct.RowsAffected() == 0 {
			return templates.ErrNotFound
		}
		t, err := scanTemplate(tx.QueryRow(ctx, templateSel+` WHERE t.institute_id = $1::uuid AND t.id = $2::uuid`, sc.InstituteID(), id))
		if err != nil {
			return err
		}
		out = t
		if err := s.loadVersions(ctx, tx, sc, &out); err != nil {
			return err
		}
		return audited(ctx, tx, sc, ev, id, 0)
	})
	return out, err
}

func (s *Store) AddVersion(ctx context.Context, sc tenancy.Scope, id string, cfg templates.Config, by string, ev audit.Event) (templates.Version, error) {
	if !okID(id) {
		return templates.Version{}, templates.ErrNotFound
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		return templates.Version{}, err
	}
	var out templates.Version
	err = s.tx(ctx, sc, func(tx pgx.Tx) error {
		// The row lock serialises concurrent AddVersion calls so numbers are gapless and unique.
		var cur int
		var archived bool
		if err := tx.QueryRow(ctx, `SELECT current_version, archived_at IS NOT NULL FROM certificate_templates
			WHERE institute_id = $1::uuid AND id = $2::uuid FOR UPDATE`, sc.InstituteID(), id).Scan(&cur, &archived); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return templates.ErrNotFound
			}
			return err
		}
		if archived {
			return templates.ErrArchived
		}
		if cur >= templates.MaxVersions {
			return templates.ErrTooManyVersions
		}
		next := cur + 1
		var summary struct{ by string }
		if err := tx.QueryRow(ctx, `INSERT INTO certificate_template_versions (institute_id, template_id, number, config, created_by)
			VALUES ($1::uuid, $2::uuid, $3, $4::jsonb, nullif($5, '')::uuid)
			RETURNING coalesce(created_by::text, ''), created_at`, sc.InstituteID(), id, next, string(raw), by).Scan(&summary.by, &out.CreatedAt); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE certificate_templates SET current_version = $3 WHERE institute_id = $1::uuid AND id = $2::uuid`,
			sc.InstituteID(), id, next); err != nil {
			return err
		}
		out.Number, out.Config, out.CreatedBy = next, cfg, summary.by
		return audited(ctx, tx, sc, ev, id, next)
	})
	return out, err
}

func (s *Store) GetVersion(ctx context.Context, sc tenancy.Scope, id string, number int) (templates.Version, error) {
	if !okID(id) || number < 1 {
		return templates.Version{}, templates.ErrNotFound
	}
	var out templates.Version
	err := s.tx(ctx, sc, func(tx pgx.Tx) error {
		var cfg string
		err := tx.QueryRow(ctx, `SELECT number, config::text, coalesce(created_by::text, ''), created_at FROM certificate_template_versions
			WHERE institute_id = $1::uuid AND template_id = $2::uuid AND number = $3`, sc.InstituteID(), id, number).
			Scan(&out.Number, &cfg, &out.CreatedBy, &out.CreatedAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return templates.ErrNotFound
		}
		if err != nil {
			return err
		}
		return json.Unmarshal([]byte(cfg), &out.Config)
	})
	return out, err
}
