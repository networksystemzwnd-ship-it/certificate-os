// Package pgstore is the PostgreSQL implementation of dashboard.Store. Institute figures run inside
// tenantdb.WithTenant and filter by the Scope's institute id (row-level security on top). Platform figures read only
// global tables (applications, institutes) and COUNT audit events by action: no tenant row and no personal data is read.
package pgstore

import (
	"context"
	"errors"
	"time"

	"github.com/certificate-os/api/internal/dashboard"
	"github.com/certificate-os/api/internal/platform/tenantdb"
	"github.com/certificate-os/api/internal/tenancy"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct{ Pool *pgxpool.Pool }

func New(pool *pgxpool.Pool) *Store { return &Store{Pool: pool} }

var _ dashboard.Store = (*Store)(nil)

func (s *Store) Institute(ctx context.Context, sc tenancy.Scope, since time.Time) (dashboard.InstituteStats, error) {
	var st dashboard.InstituteStats
	inst := sc.InstituteID()
	err := tenantdb.WithTenant(ctx, s.Pool, sc, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT count(*) FILTER (WHERE archived_at IS NULL), count(*) FILTER (WHERE archived_at IS NOT NULL)
			FROM students WHERE institute_id = $1::uuid`, inst).Scan(&st.Records.ActiveStudents, &st.Records.ArchivedStudents); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM batches WHERE institute_id = $1::uuid AND archived_at IS NULL`, inst).Scan(&st.Records.ActiveBatches); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT count(*) FILTER (WHERE status = 'issued' AND issued_at >= $2), count(*) FILTER (WHERE status = 'issued'),
				count(*) FILTER (WHERE status = 'revoked'), count(*) FILTER (WHERE status = 'failed')
			FROM certificates WHERE institute_id = $1::uuid`, inst, since).
			Scan(&st.Certificates.IssuedInPeriod, &st.Certificates.Issued, &st.Certificates.Revoked, &st.Certificates.Failed); err != nil {
			return err
		}
		var j dashboard.JobSummary
		err := tx.QueryRow(ctx, `SELECT j.id::text, b.name, j.status, j.total,
				(SELECT count(*) FROM bulk_job_items i WHERE i.institute_id = j.institute_id AND i.job_id = j.id AND i.status = 'succeeded'),
				(SELECT count(*) FROM bulk_job_items i WHERE i.institute_id = j.institute_id AND i.job_id = j.id AND i.status = 'failed'), j.created_at
			FROM bulk_jobs j JOIN batches b ON b.institute_id = j.institute_id AND b.id = j.batch_id
			WHERE j.institute_id = $1::uuid ORDER BY j.created_at DESC, j.id DESC LIMIT 1`, inst).
			Scan(&j.ID, &j.BatchName, &j.Status, &j.Total, &j.Succeeded, &j.Failed, &j.CreatedAt)
		switch {
		case err == nil:
			st.LastJob = &j
		case !errors.Is(err, pgx.ErrNoRows):
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT count(*) FILTER (WHERE status = 'queued'), count(*) FILTER (WHERE status = 'sending'),
				count(*) FILTER (WHERE status = 'accepted'), count(*) FILTER (WHERE status = 'failed')
			FROM deliveries WHERE institute_id = $1::uuid AND created_at >= $2`, inst, since).
			Scan(&st.Deliveries.Queued, &st.Deliveries.Sending, &st.Deliveries.Accepted, &st.Deliveries.Failed); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT c.id::text, c.certificate_number, coalesce(c.snapshot->>'student_name', ''), coalesce(c.snapshot->>'course_name', ''),
				c.status, coalesce(c.issued_at, c.created_at)
			FROM certificates c WHERE c.institute_id = $1::uuid AND c.status IN ('issued', 'revoked') ORDER BY c.created_at DESC, c.id DESC LIMIT 5`, inst)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var r dashboard.RecentCertificate
			if err := rows.Scan(&r.ID, &r.Number, &r.StudentName, &r.CourseName, &r.Status, &r.At); err != nil {
				return err
			}
			st.Recent = append(st.Recent, r)
		}
		return rows.Err()
	})
	return st, err
}

func (s *Store) Platform(ctx context.Context, since time.Time) (dashboard.PlatformStats, error) {
	st := dashboard.PlatformStats{Applications: map[string]int{}, Institutes: map[string]int{}}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return st, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	count := func(q string, args []any, into func(k string, n int)) error {
		rows, err := tx.Query(ctx, q, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var k string
			var n int
			if err := rows.Scan(&k, &n); err != nil {
				return err
			}
			into(k, n)
		}
		return rows.Err()
	}
	if err := count(`SELECT status, count(*) FROM institute_applications GROUP BY status`, nil, func(k string, n int) { st.Applications[k] = n }); err != nil {
		return st, err
	}
	if err := count(`SELECT status, count(*) FROM institutes GROUP BY status`, nil, func(k string, n int) { st.Institutes[k] = n }); err != nil {
		return st, err
	}
	err = count(`SELECT action, count(*) FROM audit_logs WHERE occurred_at >= $1
		AND action IN ('certificate.issued', 'certificate.revoked', 'certificate.bulk_started', 'delivery.accepted', 'delivery.failed') GROUP BY action`,
		[]any{since}, func(k string, n int) {
			switch k {
			case "certificate.issued":
				st.CertificatesIssued = n
			case "certificate.revoked":
				st.CertificatesRevoked = n
			case "certificate.bulk_started":
				st.BulkJobsStarted = n
			case "delivery.accepted":
				st.EmailsAccepted = n
			case "delivery.failed":
				st.EmailsFailed = n
			}
		})
	if err != nil {
		return st, err
	}
	rows, err := tx.Query(ctx, `SELECT action, occurred_at FROM audit_logs
		WHERE action IN ('application.approved', 'application.rejected', 'application.changes_requested') ORDER BY seq DESC LIMIT 10`)
	if err != nil {
		return st, err
	}
	defer rows.Close()
	for rows.Next() {
		var d dashboard.RecentDecision
		if err := rows.Scan(&d.Action, &d.At); err != nil {
			return st, err
		}
		st.RecentDecisions = append(st.RecentDecisions, d)
	}
	return st, rows.Err()
}
