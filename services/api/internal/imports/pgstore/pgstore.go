// Package pgstore is the PostgreSQL implementation of imports.Store.
//
// Like the records store it runs every statement inside tenantdb.WithTenant (row-level security applies to
// the restricted runtime role) AND filters by the Scope's institute id. Mutations write their audit event in
// the same transaction.
package pgstore

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"time"

	"github.com/certificate-os/api/internal/audit"
	auditpg "github.com/certificate-os/api/internal/audit/pgstore"
	"github.com/certificate-os/api/internal/imports"
	"github.com/certificate-os/api/internal/platform/tenantdb"
	"github.com/certificate-os/api/internal/tenancy"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct{ Pool *pgxpool.Pool }

func New(pool *pgxpool.Pool) *Store { return &Store{Pool: pool} }

var _ imports.Store = (*Store)(nil)

var uuidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// JSON columns are passed as text and cast ($n::jsonb) and read back as text (col::text), which behaves
// identically under every pgx query mode.
const cols = `id::text, created_by::text, status, filename, headers::text, rows::text, mapping::text, date_format,
	results::text, counts::text, created_at, updated_at, completed_at`

func scan(row pgx.Row) (imports.Import, error) {
	var imp imports.Import
	var status, headers, rows, mapping, results, counts string
	err := row.Scan(&imp.ID, &imp.CreatedBy, &status, &imp.Filename, &headers, &rows, &mapping, &imp.DateFormat,
		&results, &counts, &imp.CreatedAt, &imp.UpdatedAt, &imp.CompletedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return imp, imports.ErrNotFound
	}
	if err != nil {
		return imp, err
	}
	imp.Status = imports.Status(status)
	for _, d := range []struct {
		src string
		dst any
	}{{headers, &imp.Headers}, {rows, &imp.Rows}, {mapping, &imp.Mapping}, {results, &imp.Results}, {counts, &imp.Counts}} {
		if err := json.Unmarshal([]byte(d.src), d.dst); err != nil {
			return imp, err
		}
	}
	if imp.Mapping == nil {
		imp.Mapping = imports.Mapping{}
	}
	if imp.Results == nil {
		imp.Results = []imports.RowResult{}
	}
	return imp, nil
}

func js(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "null"
	}
	return string(b)
}

func audited(ctx context.Context, tx pgx.Tx, sc tenancy.Scope, ev audit.Event, id string) error {
	ev.EntityID, ev.InstituteID = id, sc.InstituteID()
	return auditpg.InsertTx(ctx, tx, ev)
}

func (s *Store) Create(ctx context.Context, sc tenancy.Scope, imp imports.Import, ev audit.Event) (imports.Import, error) {
	var out imports.Import
	rows := imp.Rows
	if rows == nil {
		rows = [][]string{}
	}
	err := tenantdb.WithTenant(ctx, s.Pool, sc, func(tx pgx.Tx) error {
		var err error
		out, err = scan(tx.QueryRow(ctx, `INSERT INTO student_imports
			(institute_id, created_by, status, filename, headers, rows, mapping, date_format, results, counts)
			VALUES ($1::uuid, $2::uuid, $3, $4, $5::jsonb, $6::jsonb, $7::jsonb, $8, $9::jsonb, $10::jsonb)
			RETURNING `+cols, sc.InstituteID(), imp.CreatedBy, string(imp.Status), imp.Filename, js(imp.Headers), js(rows),
			js(imp.Mapping), imp.DateFormat, js(imp.Results), js(imp.Counts)))
		if err != nil {
			return err
		}
		return audited(ctx, tx, sc, ev, out.ID)
	})
	return out, err
}

func (s *Store) Get(ctx context.Context, sc tenancy.Scope, id string) (imports.Import, error) {
	if !uuidRe.MatchString(id) {
		return imports.Import{}, imports.ErrNotFound
	}
	var out imports.Import
	err := tenantdb.WithTenant(ctx, s.Pool, sc, func(tx pgx.Tx) error {
		var err error
		out, err = scan(tx.QueryRow(ctx, `SELECT `+cols+` FROM student_imports WHERE institute_id = $1::uuid AND id = $2::uuid`, sc.InstituteID(), id))
		return err
	})
	return out, err
}

func (s *Store) Save(ctx context.Context, sc tenancy.Scope, imp imports.Import, from imports.Status, ev *audit.Event) (imports.Import, error) {
	if !uuidRe.MatchString(imp.ID) {
		return imports.Import{}, imports.ErrNotFound
	}
	rows := imp.Rows
	if rows == nil {
		rows = [][]string{}
	}
	var completed *time.Time = imp.CompletedAt
	var out imports.Import
	err := tenantdb.WithTenant(ctx, s.Pool, sc, func(tx pgx.Tx) error {
		var err error
		out, err = scan(tx.QueryRow(ctx, `UPDATE student_imports SET status = $3, mapping = $4::jsonb, date_format = $5,
			results = $6::jsonb, counts = $7::jsonb, rows = $8::jsonb, completed_at = $9
			WHERE institute_id = $1::uuid AND id = $2::uuid AND status = $10
			RETURNING `+cols, sc.InstituteID(), imp.ID, string(imp.Status), js(imp.Mapping), imp.DateFormat, js(imp.Results),
			js(imp.Counts), js(rows), completed, string(from)))
		if errors.Is(err, imports.ErrNotFound) { // no row updated: missing, or in another state?
			var one int
			if e := tx.QueryRow(ctx, `SELECT 1 FROM student_imports WHERE institute_id = $1::uuid AND id = $2::uuid`, sc.InstituteID(), imp.ID).Scan(&one); e == nil {
				return imports.ErrState
			}
			return imports.ErrNotFound
		}
		if err != nil {
			return err
		}
		if ev != nil {
			return audited(ctx, tx, sc, *ev, out.ID)
		}
		return nil
	})
	return out, err
}

// PurgeAbandoned runs with app.worker = 'purge'. Migration 0015's policies let that mode see and update ONLY
// unfinished imports idle for more than a day, and only to an emptied, cancelled state. No RETURNING: the new
// row is intentionally invisible to the purge policy, so the affected ids are read first, in the same transaction.
func (s *Store) PurgeAbandoned(ctx context.Context, olderThan time.Time) ([]imports.Purged, error) {
	var out []imports.Purged
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT set_config('app.worker', 'purge', true)`); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT id::text, institute_id::text FROM student_imports
		WHERE status IN ('uploaded', 'validated') AND updated_at < $1 AND rows <> '[]'::jsonb
		ORDER BY updated_at LIMIT 500 FOR UPDATE`, olderThan)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var p imports.Purged
		if err := rows.Scan(&p.ImportID, &p.InstituteID); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, p := range out {
		tag, err := tx.Exec(ctx, `UPDATE student_imports SET status = 'cancelled', rows = '[]'::jsonb
			WHERE id = $1::uuid AND institute_id = $2::uuid AND status IN ('uploaded', 'validated')`, p.ImportID, p.InstituteID)
		if err != nil {
			return nil, err
		}
		if tag.RowsAffected() != 1 {
			return nil, errors.New("imports: purge lost a row it had just locked")
		}
		if err := auditpg.InsertTx(ctx, tx, audit.Event{ActorType: audit.ActorSystem, Action: "student_import.purged",
			EntityType: "student_import", EntityID: p.ImportID, InstituteID: p.InstituteID}); err != nil {
			return nil, err
		}
	}
	return out, tx.Commit(ctx)
}
