// Package pgstore is the PostgreSQL implementation of delivery.Store (migration 0014).
//
// Like the other tenant stores, every call except Due runs inside tenantdb.WithTenant and still filters by the
// Scope's institute id. Due is the one cross-institute call: it sets app.worker = 'scan', which unlocks the
// PII-free delivery_queue table for SELECT and nothing else.
package pgstore

import (
	"context"
	"errors"
	"regexp"
	"time"

	"github.com/certificate-os/api/internal/audit"
	auditpg "github.com/certificate-os/api/internal/audit/pgstore"
	"github.com/certificate-os/api/internal/delivery"
	"github.com/certificate-os/api/internal/platform/tenantdb"
	"github.com/certificate-os/api/internal/tenancy"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct{ Pool *pgxpool.Pool }

func New(pool *pgxpool.Pool) *Store { return &Store{Pool: pool} }

var _ delivery.Store = (*Store)(nil)

var uuidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func okID(id string) bool { return uuidRe.MatchString(id) }

func mapErr(err error) error {
	var e *pgconn.PgError
	if !errors.As(err, &e) {
		return err
	}
	switch {
	case e.Code == "23505" && e.ConstraintName == "deliveries_one_active":
		return delivery.ErrActive
	case e.Code == "22P02":
		return delivery.ErrNotFound
	}
	return err
}

func (s *Store) tx(ctx context.Context, sc tenancy.Scope, fn func(tx pgx.Tx) error) error {
	return mapErr(tenantdb.WithTenant(ctx, s.Pool, sc, fn))
}

func audited(ctx context.Context, tx pgx.Tx, sc tenancy.Scope, ev audit.Event, d delivery.Delivery) error {
	ev.EntityID, ev.InstituteID = d.ID, sc.InstituteID()
	meta := map[string]any{"certificate_id": d.CertificateID, "number": d.CertificateNumber}
	for k, v := range ev.Metadata {
		meta[k] = v
	}
	ev.Metadata = meta
	return auditpg.InsertTx(ctx, tx, ev)
}

const cols = `d.id::text, d.certificate_id::text, c.certificate_number, d.channel, d.recipient, d.status, d.attempts, d.max_attempts,
	d.next_attempt_at, d.last_error, d.created_by::text, d.created_at, d.updated_at, d.finished_at`

const from = ` FROM deliveries d JOIN certificates c ON c.institute_id = d.institute_id AND c.id = d.certificate_id`

func scan(row pgx.Row) (delivery.Delivery, error) {
	var d delivery.Delivery
	var status string
	err := row.Scan(&d.ID, &d.CertificateID, &d.CertificateNumber, &d.Channel, &d.Recipient, &status, &d.Attempts, &d.MaxAttempts,
		&d.NextAttemptAt, &d.LastError, &d.CreatedBy, &d.CreatedAt, &d.UpdatedAt, &d.FinishedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return d, delivery.ErrNotFound
	}
	if err != nil {
		return d, err
	}
	d.Status = delivery.Status(status)
	return d, nil
}

func load(ctx context.Context, tx pgx.Tx, sc tenancy.Scope, id string) (delivery.Delivery, error) {
	return scan(tx.QueryRow(ctx, `SELECT `+cols+from+` WHERE d.institute_id = $1::uuid AND d.id = $2::uuid`, sc.InstituteID(), id))
}

// lockRow locks the delivery row and returns the raw state the state machine needs.
type raw struct {
	status, token string
	attempts, max int
	next, lease   *time.Time
	certID        string
}

func lockRow(ctx context.Context, tx pgx.Tx, sc tenancy.Scope, id string) (raw, error) {
	var r raw
	err := tx.QueryRow(ctx, `SELECT status, runner_token, attempts, max_attempts, next_attempt_at, lease_until, certificate_id::text
		FROM deliveries WHERE institute_id = $1::uuid AND id = $2::uuid FOR UPDATE`, sc.InstituteID(), id).
		Scan(&r.status, &r.token, &r.attempts, &r.max, &r.next, &r.lease, &r.certID)
	if errors.Is(err, pgx.ErrNoRows) {
		err = delivery.ErrNotFound
	}
	return r, err
}

// recipient returns the student's current address for an ISSUED certificate, locking the certificate row so
// it cannot be revoked until this transaction ends. ErrNotFound unless issued.
func recipient(ctx context.Context, tx pgx.Tx, sc tenancy.Scope, certID string) (string, error) {
	var status, email string
	err := tx.QueryRow(ctx, `SELECT c.status, s.email FROM certificates c
		JOIN students s ON s.institute_id = c.institute_id AND s.id = c.student_id
		WHERE c.institute_id = $1::uuid AND c.id = $2::uuid FOR SHARE OF c`, sc.InstituteID(), certID).Scan(&status, &email)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && status != "issued") {
		return "", delivery.ErrNotFound
	}
	if err != nil {
		return "", err
	}
	if email == "" {
		return "", delivery.ErrNoRecipient
	}
	return email, nil
}

func (s *Store) Request(ctx context.Context, sc tenancy.Scope, in delivery.RequestInput, ev audit.Event) (delivery.Delivery, error) {
	if !okID(in.CertificateID) {
		return delivery.Delivery{}, delivery.ErrNotFound
	}
	var out delivery.Delivery
	err := s.tx(ctx, sc, func(tx pgx.Tx) error {
		email, err := recipient(ctx, tx, sc, in.CertificateID)
		if err != nil {
			return err
		}
		var total, active, accepted int
		if err := tx.QueryRow(ctx, `SELECT count(*), count(*) FILTER (WHERE status IN ('queued', 'sending')), count(*) FILTER (WHERE status = 'accepted')
			FROM deliveries WHERE institute_id = $1::uuid AND certificate_id = $2::uuid`, sc.InstituteID(), in.CertificateID).Scan(&total, &active, &accepted); err != nil {
			return err
		}
		switch {
		case active > 0:
			return delivery.ErrActive
		case accepted > 0 && in.SkipAccepted:
			return delivery.ErrAlreadyAccepted
		case total >= delivery.MaxPerCertificate:
			return delivery.ErrLimit
		}
		var id string
		if err := tx.QueryRow(ctx, `INSERT INTO deliveries (institute_id, certificate_id, recipient, status, max_attempts, next_attempt_at, created_by, created_at, updated_at)
			VALUES ($1::uuid, $2::uuid, $3, 'queued', $4, $5, $6::uuid, $5, $5) RETURNING id::text`,
			sc.InstituteID(), in.CertificateID, email, delivery.MaxAttempts, in.Now, in.CreatedBy).Scan(&id); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO delivery_queue (delivery_id, institute_id, due_at) VALUES ($1::uuid, $2::uuid, $3)`, id, sc.InstituteID(), in.Now); err != nil {
			return err
		}
		if out, err = load(ctx, tx, sc, id); err != nil {
			return err
		}
		return audited(ctx, tx, sc, ev, out)
	})
	return out, err
}

func (s *Store) Get(ctx context.Context, sc tenancy.Scope, id string) (delivery.Delivery, error) {
	if !okID(id) {
		return delivery.Delivery{}, delivery.ErrNotFound
	}
	var out delivery.Delivery
	err := s.tx(ctx, sc, func(tx pgx.Tx) (err error) { out, err = load(ctx, tx, sc, id); return })
	return out, err
}

func (s *Store) List(ctx context.Context, sc tenancy.Scope, f delivery.Filter) ([]delivery.Delivery, error) {
	if f.CertificateID != "" && !okID(f.CertificateID) {
		return nil, nil
	}
	limit := f.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	var out []delivery.Delivery
	err := s.tx(ctx, sc, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT `+cols+from+` WHERE d.institute_id = $1::uuid
			AND ($2 = '' OR d.certificate_id = nullif($2, '')::uuid) AND ($3 = '' OR d.status = $3)
			ORDER BY d.created_at DESC, d.id DESC LIMIT $4`, sc.InstituteID(), f.CertificateID, string(f.Status), limit+1)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			d, err := scan(rows)
			if err != nil {
				return err
			}
			out = append(out, d)
		}
		return rows.Err()
	})
	return out, err
}

func (s *Store) Attempts(ctx context.Context, sc tenancy.Scope, id string) ([]delivery.Attempt, error) {
	if !okID(id) {
		return nil, delivery.ErrNotFound
	}
	var out []delivery.Attempt
	err := s.tx(ctx, sc, func(tx pgx.Tx) error {
		var one int
		if err := tx.QueryRow(ctx, `SELECT 1 FROM deliveries WHERE institute_id = $1::uuid AND id = $2::uuid`, sc.InstituteID(), id).Scan(&one); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return delivery.ErrNotFound
			}
			return err
		}
		rows, err := tx.Query(ctx, `SELECT attempt_no, provider, status, error_code, started_at, finished_at FROM delivery_attempts
			WHERE institute_id = $1::uuid AND delivery_id = $2::uuid ORDER BY attempt_no`, sc.InstituteID(), id)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var a delivery.Attempt
			if err := rows.Scan(&a.No, &a.Provider, &a.Status, &a.ErrorCode, &a.StartedAt, &a.FinishedAt); err != nil {
				return err
			}
			out = append(out, a)
		}
		return rows.Err()
	})
	return out, err
}

func (s *Store) Retry(ctx context.Context, sc tenancy.Scope, id string, now time.Time, ev audit.Event) (delivery.Delivery, error) {
	if !okID(id) {
		return delivery.Delivery{}, delivery.ErrNotFound
	}
	var out delivery.Delivery
	err := s.tx(ctx, sc, func(tx pgx.Tx) error {
		r, err := lockRow(ctx, tx, sc, id)
		if err != nil {
			return err
		}
		if r.status != string(delivery.Failed) {
			return delivery.ErrState
		}
		email, err := recipient(ctx, tx, sc, r.certID)
		if err != nil {
			return err
		}
		if r.attempts+delivery.MaxAttempts > 50 {
			return delivery.ErrLimit
		}
		if _, err := tx.Exec(ctx, `UPDATE deliveries SET recipient = $3, status = 'queued', finished_at = NULL, next_attempt_at = $4, max_attempts = attempts + $5,
				runner_token = '', lease_until = NULL, updated_at = $4 WHERE institute_id = $1::uuid AND id = $2::uuid`,
			sc.InstituteID(), id, email, now, delivery.MaxAttempts); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE delivery_queue SET active = true, due_at = $3, lease_until = NULL WHERE institute_id = $1::uuid AND delivery_id = $2::uuid`,
			sc.InstituteID(), id, now); err != nil {
			return err
		}
		if out, err = load(ctx, tx, sc, id); err != nil {
			return err
		}
		return audited(ctx, tx, sc, ev, out)
	})
	return out, err
}

func (s *Store) Due(ctx context.Context, now time.Time, limit int) ([]delivery.DueRef, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT set_config('app.worker', 'scan', true)`); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT institute_id::text, delivery_id::text FROM delivery_queue
		WHERE active AND due_at <= $1 AND (lease_until IS NULL OR lease_until <= $1) ORDER BY due_at, delivery_id LIMIT $2`, now, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []delivery.DueRef
	for rows.Next() {
		var r delivery.DueRef
		if err := rows.Scan(&r.InstituteID, &r.DeliveryID); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) Claim(ctx context.Context, sc tenancy.Scope, id, token string, now time.Time, lease time.Duration) (delivery.Claim, error) {
	if !okID(id) {
		return delivery.Claim{}, delivery.ErrNotFound
	}
	var out delivery.Claim
	exhausted := false
	err := s.tx(ctx, sc, func(tx pgx.Tx) error {
		r, err := lockRow(ctx, tx, sc, id)
		if err != nil {
			return err
		}
		var inst string
		if err := tx.QueryRow(ctx, `SELECT status FROM institutes WHERE id = $1::uuid`, sc.InstituteID()).Scan(&inst); err != nil {
			return err
		}
		if inst != "active" {
			return delivery.ErrSuspended
		}
		stale := r.status == string(delivery.Sending) && r.lease != nil && !r.lease.After(now)
		ready := r.status == string(delivery.Queued) && r.next != nil && !r.next.After(now)
		if !ready && !stale {
			return delivery.ErrState
		}
		until := now.Add(lease)
		if stale && r.attempts >= r.max {
			// A worker keeps dying on this delivery: stop instead of sending forever. The failure is committed.
			if _, err := tx.Exec(ctx, `UPDATE deliveries SET status = 'failed', last_error = $3, finished_at = $4, updated_at = $4, runner_token = '', lease_until = NULL
				WHERE institute_id = $1::uuid AND id = $2::uuid`, sc.InstituteID(), id, delivery.CodeInterrupted, now); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE delivery_queue SET active = false, lease_until = NULL WHERE institute_id = $1::uuid AND delivery_id = $2::uuid`, sc.InstituteID(), id); err != nil {
				return err
			}
			d, err := load(ctx, tx, sc, id)
			if err != nil {
				return err
			}
			exhausted = true
			return audited(ctx, tx, sc, audit.Event{ActorType: audit.ActorSystem, Action: "delivery.failed", EntityType: "delivery",
				Metadata: map[string]any{"code": delivery.CodeInterrupted}}, d)
		}
		if _, err := tx.Exec(ctx, `UPDATE deliveries SET status = 'sending', attempts = attempts + 1, next_attempt_at = NULL, updated_at = $3, runner_token = $4, lease_until = $5
			WHERE institute_id = $1::uuid AND id = $2::uuid`, sc.InstituteID(), id, now, token, until); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE delivery_queue SET lease_until = $3 WHERE institute_id = $1::uuid AND delivery_id = $2::uuid`, sc.InstituteID(), id, until); err != nil {
			return err
		}
		d, err := load(ctx, tx, sc, id)
		out = delivery.Claim{Delivery: d, Token: token}
		return err
	})
	if err == nil && exhausted {
		return delivery.Claim{}, delivery.ErrState
	}
	return out, err
}

func (s *Store) Finish(ctx context.Context, sc tenancy.Scope, id, token string, in delivery.FinishInput, ev audit.Event) (delivery.Delivery, error) {
	if !okID(id) {
		return delivery.Delivery{}, delivery.ErrNotFound
	}
	var out delivery.Delivery
	err := s.tx(ctx, sc, func(tx pgx.Tx) error {
		r, err := lockRow(ctx, tx, sc, id)
		if err != nil {
			return err
		}
		if r.status != string(delivery.Sending) || token == "" || r.token != token {
			return delivery.ErrState
		}
		status, code := "accepted", ""
		if in.Outcome != delivery.OutcomeAccepted {
			status, code = "failed", in.Code
		}
		if _, err := tx.Exec(ctx, `INSERT INTO delivery_attempts (institute_id, delivery_id, attempt_no, provider, status, error_code, started_at, finished_at)
			VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6, $7, $8)`, sc.InstituteID(), id, r.attempts, in.Provider, status, code, in.StartedAt, in.At); err != nil {
			return err
		}
		switch in.Outcome {
		case delivery.OutcomeRetry:
			if _, err := tx.Exec(ctx, `UPDATE deliveries SET status = 'queued', last_error = $3, next_attempt_at = $4, updated_at = $5, runner_token = '', lease_until = NULL
				WHERE institute_id = $1::uuid AND id = $2::uuid`, sc.InstituteID(), id, code, in.NextAttemptAt, in.At); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE delivery_queue SET due_at = $3, lease_until = NULL WHERE institute_id = $1::uuid AND delivery_id = $2::uuid`,
				sc.InstituteID(), id, in.NextAttemptAt); err != nil {
				return err
			}
		default:
			if _, err := tx.Exec(ctx, `UPDATE deliveries SET status = $3, last_error = $4, next_attempt_at = NULL, finished_at = $5, updated_at = $5, runner_token = '', lease_until = NULL
				WHERE institute_id = $1::uuid AND id = $2::uuid`, sc.InstituteID(), id, status, code, in.At); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE delivery_queue SET active = false, lease_until = NULL WHERE institute_id = $1::uuid AND delivery_id = $2::uuid`, sc.InstituteID(), id); err != nil {
				return err
			}
		}
		if out, err = load(ctx, tx, sc, id); err != nil {
			return err
		}
		return audited(ctx, tx, sc, ev, out)
	})
	return out, err
}

func (s *Store) Postpone(ctx context.Context, sc tenancy.Scope, id string, until time.Time) error {
	if !okID(id) {
		return delivery.ErrNotFound
	}
	return s.tx(ctx, sc, func(tx pgx.Tx) error {
		r, err := lockRow(ctx, tx, sc, id)
		if err != nil {
			return err
		}
		if r.status != string(delivery.Queued) && r.status != string(delivery.Sending) {
			return delivery.ErrState
		}
		if r.status == string(delivery.Queued) {
			if _, err := tx.Exec(ctx, `UPDATE deliveries SET next_attempt_at = $3 WHERE institute_id = $1::uuid AND id = $2::uuid`, sc.InstituteID(), id, until); err != nil {
				return err
			}
		}
		_, err = tx.Exec(ctx, `UPDATE delivery_queue SET due_at = $3 WHERE institute_id = $1::uuid AND delivery_id = $2::uuid`, sc.InstituteID(), id, until)
		return err
	})
}
