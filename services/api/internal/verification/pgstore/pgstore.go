// Package pgstore is the PostgreSQL implementation of verification.Store.
//
// The public route has no institute. Instead of weakening row-level security it relies on one narrow SELECT
// policy (migration 0013): the session setting app.public_id exposes exactly the certificate with that public id
// to the restricted runtime role and nothing else. The verification event is then written under the institute of
// the row that was found, inside a savepoint, so a failure to record it never breaks the answer.
package pgstore

import (
	"context"
	"errors"
	"time"

	"github.com/certificate-os/api/internal/verification"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct {
	Pool *pgxpool.Pool
	Log  func(msg string, args ...any)
}

func New(pool *pgxpool.Pool, log func(msg string, args ...any)) *Store {
	return &Store{Pool: pool, Log: log}
}

var _ verification.Store = (*Store)(nil)

func (s *Store) Lookup(ctx context.Context, publicID string) (verification.Record, error) {
	var rec verification.Record
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return rec, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT set_config('app.public_id', $1, true)`, publicID); err != nil {
		return rec, err
	}
	var instID, certID, status string
	var revokedAt *time.Time
	err = tx.QueryRow(ctx, `SELECT c.institute_id::text, c.id::text, c.status, c.certificate_number,
			coalesce(c.snapshot->>'institute_name', ''), coalesce(c.snapshot->>'student_name', ''), coalesce(c.snapshot->>'course_name', ''),
			coalesce(c.snapshot->>'issue_date', ''), c.revoked_at, coalesce(c.replaced_by_public_id, '')
		FROM certificates c WHERE c.public_id = $1 AND c.status IN ('issued', 'revoked')`, publicID).
		Scan(&instID, &certID, &status, &rec.Number, &rec.InstituteName, &rec.StudentName, &rec.CourseName, &rec.IssueDate, &revokedAt, &rec.ReplacedByPublicID)
	if errors.Is(err, pgx.ErrNoRows) {
		return rec, verification.ErrNotFound
	}
	if err != nil {
		return rec, err
	}
	rec.Revoked, rec.RevokedAt = status == "revoked", revokedAt
	s.record(ctx, tx, instID, certID, verification.StatusOf(rec))
	if err := tx.Commit(ctx); err != nil {
		return rec, err
	}
	return rec, nil
}

// record writes the event (certificate and result only: no address, no user agent) inside a savepoint.
func (s *Store) record(ctx context.Context, tx pgx.Tx, instID, certID string, st verification.Status) {
	fail := func(err error) {
		if s.Log != nil {
			s.Log("could not record a verification event", "err", err)
		}
		_, _ = tx.Exec(ctx, `ROLLBACK TO SAVEPOINT verification_event`)
	}
	if _, err := tx.Exec(ctx, `SAVEPOINT verification_event`); err != nil {
		return
	}
	if _, err := tx.Exec(ctx, `SELECT set_config('app.institute_id', $1, true)`, instID); err != nil {
		fail(err)
		return
	}
	if _, err := tx.Exec(ctx, `INSERT INTO verification_events (institute_id, certificate_id, result) VALUES ($1::uuid, $2::uuid, $3)`,
		instID, certID, string(st)); err != nil {
		fail(err)
		return
	}
	_, _ = tx.Exec(ctx, `RELEASE SAVEPOINT verification_event`)
}
