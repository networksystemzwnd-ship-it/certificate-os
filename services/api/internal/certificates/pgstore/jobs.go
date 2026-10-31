package pgstore

import (
	"context"
	"errors"
	"time"

	"github.com/certificate-os/api/internal/audit"
	"github.com/certificate-os/api/internal/certificates"
	"github.com/certificate-os/api/internal/tenancy"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Bulk jobs (migration 0012). Same rules as the certificate methods: every call runs inside
// tenantdb.WithTenant and still filters by the Scope's institute id. A job row is locked FOR UPDATE for
// every state change, so a runner, a cancel and a retry can never interleave.

var _ certificates.JobStore = (*Store)(nil)

func (s *Store) run(ctx context.Context, sc tenancy.Scope, fn func(tx pgx.Tx) error) error {
	err := s.tx(ctx, sc, fn)
	var e *pgconn.PgError
	if errors.As(err, &e) {
		switch {
		case e.Code == "23505" && e.ConstraintName == "bulk_jobs_one_active":
			return certificates.ErrJobActive
		case e.Code == "23503":
			return certificates.ErrNotFound
		}
	}
	return mapErr(err)
}

const jobCols = `j.id::text, j.batch_id::text, b.name, j.template_id::text, t.name, j.template_version, j.status,
	j.skipped_issued, j.skipped_ineligible, j.created_by::text, j.created_at, j.started_at, j.finished_at,
	(SELECT count(*) FROM bulk_job_items i WHERE i.institute_id = j.institute_id AND i.job_id = j.id),
	(SELECT count(*) FROM bulk_job_items i WHERE i.institute_id = j.institute_id AND i.job_id = j.id AND i.status = 'succeeded'),
	(SELECT count(*) FROM bulk_job_items i WHERE i.institute_id = j.institute_id AND i.job_id = j.id AND i.status = 'failed')`

const jobFrom = ` FROM bulk_jobs j
	JOIN batches b ON b.institute_id = j.institute_id AND b.id = j.batch_id
	JOIN certificate_templates t ON t.institute_id = j.institute_id AND t.id = j.template_id`

func scanJob(row pgx.Row) (certificates.Job, error) {
	var j certificates.Job
	var status string
	err := row.Scan(&j.ID, &j.BatchID, &j.BatchName, &j.TemplateID, &j.TemplateName, &j.TemplateVersion, &status,
		&j.SkippedIssued, &j.SkippedIneligible, &j.CreatedBy, &j.CreatedAt, &j.StartedAt, &j.FinishedAt, &j.Total, &j.Succeeded, &j.Failed)
	if errors.Is(err, pgx.ErrNoRows) {
		return j, certificates.ErrNotFound
	}
	if err != nil {
		return j, err
	}
	j.Status = certificates.JobStatus(status)
	j.Remaining = j.Total - j.Succeeded - j.Failed
	return j, nil
}

func loadJob(ctx context.Context, tx pgx.Tx, sc tenancy.Scope, id string) (certificates.Job, error) {
	return scanJob(tx.QueryRow(ctx, `SELECT `+jobCols+jobFrom+` WHERE j.institute_id = $1::uuid AND j.id = $2::uuid`, sc.InstituteID(), id))
}

// lockJob locks the job row and returns its raw state.
func lockJob(ctx context.Context, tx pgx.Tx, sc tenancy.Scope, id string) (status, token string, lease *time.Time, err error) {
	err = tx.QueryRow(ctx, `SELECT status, runner_token, lease_until FROM bulk_jobs WHERE institute_id = $1::uuid AND id = $2::uuid FOR UPDATE`,
		sc.InstituteID(), id).Scan(&status, &token, &lease)
	if errors.Is(err, pgx.ErrNoRows) {
		err = certificates.ErrNotFound
	}
	return
}

func instituteActive(ctx context.Context, tx pgx.Tx, sc tenancy.Scope) (bool, error) {
	var st string
	if err := tx.QueryRow(ctx, `SELECT status FROM institutes WHERE id = $1::uuid`, sc.InstituteID()).Scan(&st); err != nil {
		return false, err
	}
	return st == "active", nil
}

// classify returns the enrollments of a batch in issuing order with their outcome: eligible | issued | not.
func classify(ctx context.Context, tx pgx.Tx, sc tenancy.Scope, batchID string) (eligible []string, issued, not int, err error) {
	rows, err := tx.Query(ctx, `SELECT e.id::text,
			CASE WHEN EXISTS (SELECT 1 FROM certificates c WHERE c.institute_id = e.institute_id AND c.enrollment_id = e.id AND c.status IN ('pending', 'issued')) THEN 'issued'
			     WHEN e.status <> 'completed' OR e.completion_date IS NULL OR s.archived_at IS NOT NULL THEN 'not'
			     ELSE 'eligible' END
		FROM enrollments e JOIN students s ON s.institute_id = e.institute_id AND s.id = e.student_id
		WHERE e.institute_id = $1::uuid AND e.batch_id = $2::uuid ORDER BY lower(s.full_name), e.id`, sc.InstituteID(), batchID)
	if err != nil {
		return nil, 0, 0, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, cls string
		if err := rows.Scan(&id, &cls); err != nil {
			return nil, 0, 0, err
		}
		switch cls {
		case "eligible":
			eligible = append(eligible, id)
		case "issued":
			issued++
		default:
			not++
		}
	}
	return eligible, issued, not, rows.Err()
}

type target struct {
	batchName, tplName string
	version            int
}

func resolve(ctx context.Context, tx pgx.Tx, sc tenancy.Scope, batchID, templateID string, version int) (target, error) {
	var t target
	err := tx.QueryRow(ctx, `SELECT name FROM batches WHERE institute_id = $1::uuid AND id = $2::uuid`, sc.InstituteID(), batchID).Scan(&t.batchName)
	if errors.Is(err, pgx.ErrNoRows) {
		return t, certificates.ErrNotFound
	}
	if err != nil {
		return t, err
	}
	var archived bool
	var current int
	err = tx.QueryRow(ctx, `SELECT name, archived_at IS NOT NULL, current_version FROM certificate_templates WHERE institute_id = $1::uuid AND id = $2::uuid`,
		sc.InstituteID(), templateID).Scan(&t.tplName, &archived, &current)
	if errors.Is(err, pgx.ErrNoRows) {
		return t, certificates.ErrNotFound
	}
	if err != nil {
		return t, err
	}
	if archived {
		return t, &certificates.EligibilityError{Field: "template_id", Reason: "This template is archived. Restore it or choose another."}
	}
	t.version = version
	if t.version == 0 {
		t.version = current
	}
	if t.version < 1 || t.version > current {
		return t, certificates.ErrNotFound
	}
	return t, nil
}

func (s *Store) Preview(ctx context.Context, sc tenancy.Scope, batchID, templateID string) (certificates.Preview, error) {
	var out certificates.Preview
	if !okID(batchID) || !okID(templateID) {
		return out, certificates.ErrNotFound
	}
	err := s.run(ctx, sc, func(tx pgx.Tx) error {
		t, err := resolve(ctx, tx, sc, batchID, templateID, 0)
		if err != nil {
			return err
		}
		el, issued, not, err := classify(ctx, tx, sc, batchID)
		if err != nil {
			return err
		}
		out = certificates.Preview{BatchName: t.batchName, Eligible: len(el), AlreadyIssued: issued, NotEligible: not}
		return nil
	})
	return out, err
}

func (s *Store) CreateJob(ctx context.Context, sc tenancy.Scope, in certificates.JobInput, ev audit.Event) (certificates.Job, error) {
	var out certificates.Job
	if !okID(in.BatchID) || !okID(in.TemplateID) || in.Version < 0 {
		return out, certificates.ErrNotFound
	}
	err := s.run(ctx, sc, func(tx pgx.Tx) error {
		t, err := resolve(ctx, tx, sc, in.BatchID, in.TemplateID, in.Version)
		if err != nil {
			return err
		}
		el, issued, not, err := classify(ctx, tx, sc, in.BatchID)
		if err != nil {
			return err
		}
		if len(el) == 0 {
			return certificates.ErrNothingToIssue
		}
		if len(el) > certificates.MaxJobItems {
			return &certificates.EligibilityError{Field: "batch_id", Reason: "A job can hold at most 500 certificates. Issue the batch in smaller groups."}
		}
		var id string
		if err := tx.QueryRow(ctx, `INSERT INTO bulk_jobs (institute_id, batch_id, template_id, template_version, total, skipped_issued, skipped_ineligible, created_by, created_at)
			VALUES ($1::uuid, $2::uuid, $3::uuid, $4, $5, $6, $7, $8::uuid, $9) RETURNING id::text`,
			sc.InstituteID(), in.BatchID, in.TemplateID, t.version, len(el), issued, not, in.CreatedBy, in.Now).Scan(&id); err != nil {
			return err
		}
		pos := make([]int32, len(el))
		for i := range pos {
			pos[i] = int32(i + 1)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO bulk_job_items (institute_id, job_id, enrollment_id, position)
			SELECT $1::uuid, $2::uuid, u.id::uuid, u.pos FROM unnest($3::text[], $4::int4[]) AS u(id, pos)`, sc.InstituteID(), id, el, pos); err != nil {
			return err
		}
		ev.Metadata = merge(ev.Metadata, map[string]any{"total": len(el), "template_version": t.version, "skipped_issued": issued, "skipped_ineligible": not})
		if err := audited(ctx, tx, sc, ev, id, ""); err != nil {
			return err
		}
		out, err = loadJob(ctx, tx, sc, id)
		return err
	})
	return out, err
}

func merge(a, b map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		out[k] = v
	}
	return out
}

func (s *Store) GetJob(ctx context.Context, sc tenancy.Scope, id string) (certificates.Job, error) {
	var out certificates.Job
	if !okID(id) {
		return out, certificates.ErrNotFound
	}
	err := s.run(ctx, sc, func(tx pgx.Tx) (err error) { out, err = loadJob(ctx, tx, sc, id); return })
	return out, err
}

func (s *Store) ListJobs(ctx context.Context, sc tenancy.Scope, limit, offset int) ([]certificates.Job, error) {
	out := []certificates.Job{}
	err := s.run(ctx, sc, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT `+jobCols+jobFrom+` WHERE j.institute_id = $1::uuid ORDER BY j.created_at DESC, j.id DESC LIMIT $2 OFFSET $3`,
			sc.InstituteID(), limit+1, offset)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			j, err := scanJob(rows)
			if err != nil {
				return err
			}
			out = append(out, j)
		}
		return rows.Err()
	})
	return out, err
}

const itemCols = `i.enrollment_id::text, s.full_name, i.position, i.attempts, i.status, coalesce(i.certificate_id::text, ''), coalesce(c.certificate_number, ''), i.error`

const itemFrom = ` FROM bulk_job_items i
	JOIN enrollments e ON e.institute_id = i.institute_id AND e.id = i.enrollment_id
	JOIN students s ON s.institute_id = e.institute_id AND s.id = e.student_id
	LEFT JOIN certificates c ON c.institute_id = i.institute_id AND c.id = i.certificate_id`

func scanItem(row pgx.Row) (certificates.JobItem, error) {
	var it certificates.JobItem
	var st string
	err := row.Scan(&it.EnrollmentID, &it.StudentName, &it.Position, &it.Attempts, &st, &it.CertificateID, &it.CertificateNumber, &it.Error)
	it.Status = certificates.ItemStatus(st)
	return it, err
}

func (s *Store) ListItems(ctx context.Context, sc tenancy.Scope, jobID string, status certificates.ItemStatus, limit, offset int) ([]certificates.JobItem, error) {
	out := []certificates.JobItem{}
	if !okID(jobID) {
		return out, certificates.ErrNotFound
	}
	if status != "" && !status.Valid() {
		return out, nil
	}
	err := s.run(ctx, sc, func(tx pgx.Tx) error {
		if _, err := loadJob(ctx, tx, sc, jobID); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT `+itemCols+itemFrom+` WHERE i.institute_id = $1::uuid AND i.job_id = $2::uuid AND ($3 = '' OR i.status = $3)
			ORDER BY i.position LIMIT $4 OFFSET $5`, sc.InstituteID(), jobID, string(status), limit+1, offset)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			it, err := scanItem(rows)
			if err != nil {
				return err
			}
			out = append(out, it)
		}
		return rows.Err()
	})
	return out, err
}

func (s *Store) ClaimJob(ctx context.Context, sc tenancy.Scope, id, token string, now time.Time, lease time.Duration, ev audit.Event) (certificates.Job, error) {
	var out certificates.Job
	if !okID(id) {
		return out, certificates.ErrNotFound
	}
	err := s.run(ctx, sc, func(tx pgx.Tx) error {
		status, _, until, err := lockJob(ctx, tx, sc, id)
		if err != nil {
			return err
		}
		active, err := instituteActive(ctx, tx, sc)
		if err != nil {
			return err
		}
		stale := status == string(certificates.JobProcessing) && until != nil && until.Before(now)
		if !active || (status != string(certificates.JobQueued) && !stale) {
			return certificates.ErrState
		}
		if _, err := tx.Exec(ctx, `UPDATE bulk_job_items SET status = 'queued' WHERE institute_id = $1::uuid AND job_id = $2::uuid AND status = 'processing'`, sc.InstituteID(), id); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE bulk_jobs SET status = 'processing', runner_token = $3, lease_until = $4, started_at = coalesce(started_at, $5)
			WHERE institute_id = $1::uuid AND id = $2::uuid`, sc.InstituteID(), id, token, now.Add(lease), now); err != nil {
			return err
		}
		if err := audited(ctx, tx, sc, ev, id, ""); err != nil {
			return err
		}
		out, err = loadJob(ctx, tx, sc, id)
		return err
	})
	return out, err
}

// owned locks the job and checks the runner's token.
func owned(ctx context.Context, tx pgx.Tx, sc tenancy.Scope, id, token string) (status string, err error) {
	status, tok, _, err := lockJob(ctx, tx, sc, id)
	if err != nil {
		return "", err
	}
	if token == "" || tok != token {
		return "", certificates.ErrState
	}
	return status, nil
}

func (s *Store) NextItem(ctx context.Context, sc tenancy.Scope, jobID, token string, now time.Time, lease time.Duration) (certificates.JobItem, bool, error) {
	var out certificates.JobItem
	var ok bool
	if !okID(jobID) {
		return out, false, certificates.ErrNotFound
	}
	err := s.run(ctx, sc, func(tx pgx.Tx) error {
		status, err := owned(ctx, tx, sc, jobID, token)
		if err != nil {
			return err
		}
		active, err := instituteActive(ctx, tx, sc)
		if err != nil {
			return err
		}
		if status != string(certificates.JobProcessing) || !active {
			return certificates.ErrState
		}
		if _, err := tx.Exec(ctx, `UPDATE bulk_jobs SET lease_until = $3 WHERE institute_id = $1::uuid AND id = $2::uuid`, sc.InstituteID(), jobID, now.Add(lease)); err != nil {
			return err
		}
		var enr string
		err = tx.QueryRow(ctx, `UPDATE bulk_job_items SET status = 'processing', attempts = attempts + 1, started_at = $3
			WHERE institute_id = $1::uuid AND job_id = $2::uuid AND enrollment_id = (
				SELECT enrollment_id FROM bulk_job_items WHERE institute_id = $1::uuid AND job_id = $2::uuid AND status = 'queued' ORDER BY position LIMIT 1)
			RETURNING enrollment_id::text`, sc.InstituteID(), jobID, now).Scan(&enr)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		out, err = scanItem(tx.QueryRow(ctx, `SELECT `+itemCols+itemFrom+` WHERE i.institute_id = $1::uuid AND i.job_id = $2::uuid AND i.enrollment_id = $3::uuid`, sc.InstituteID(), jobID, enr))
		ok = err == nil
		return err
	})
	return out, ok, err
}

func (s *Store) RecordItem(ctx context.Context, sc tenancy.Scope, jobID, enrollmentID, token string, res certificates.ItemResult, now time.Time) error {
	if !okID(jobID) || !okID(enrollmentID) || (res.CertificateID != "" && !okID(res.CertificateID)) {
		return certificates.ErrNotFound
	}
	if res.CertificateID == "" && res.Error == "" {
		return certificates.ErrState
	}
	if r := []rune(res.Error); len(r) > 200 {
		res.Error = string(r[:200])
	}
	return s.run(ctx, sc, func(tx pgx.Tx) error {
		if _, err := owned(ctx, tx, sc, jobID, token); err != nil {
			return err
		}
		status, cert := "failed", ""
		if res.CertificateID != "" {
			status, cert = "succeeded", res.CertificateID
		}
		tag, err := tx.Exec(ctx, `UPDATE bulk_job_items SET status = $4, certificate_id = nullif($5, '')::uuid, error = $6, finished_at = $7
			WHERE institute_id = $1::uuid AND job_id = $2::uuid AND enrollment_id = $3::uuid AND status = 'processing'`,
			sc.InstituteID(), jobID, enrollmentID, status, cert, res.Error, now)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			var one int
			err := tx.QueryRow(ctx, `SELECT 1 FROM bulk_job_items WHERE institute_id = $1::uuid AND job_id = $2::uuid AND enrollment_id = $3::uuid`, sc.InstituteID(), jobID, enrollmentID).Scan(&one)
			if errors.Is(err, pgx.ErrNoRows) {
				return certificates.ErrNotFound
			}
			if err != nil {
				return err
			}
			return certificates.ErrState
		}
		return nil
	})
}

func (s *Store) FinishJob(ctx context.Context, sc tenancy.Scope, jobID, token string, now time.Time, ev audit.Event) (certificates.Job, error) {
	var out certificates.Job
	if !okID(jobID) {
		return out, certificates.ErrNotFound
	}
	err := s.run(ctx, sc, func(tx pgx.Tx) error {
		status, err := owned(ctx, tx, sc, jobID, token)
		if err != nil {
			return err
		}
		cur, err := loadJob(ctx, tx, sc, jobID)
		if err != nil {
			return err
		}
		if status != string(certificates.JobProcessing) || cur.Remaining > 0 {
			return certificates.ErrState
		}
		final := certificates.JobCompletedWithErrors
		switch {
		case cur.Failed == 0:
			final = certificates.JobCompleted
		case cur.Succeeded == 0:
			final = certificates.JobFailed
		}
		if _, err := tx.Exec(ctx, `UPDATE bulk_jobs SET status = $3, finished_at = $4 WHERE institute_id = $1::uuid AND id = $2::uuid`, sc.InstituteID(), jobID, string(final), now); err != nil {
			return err
		}
		ev.Metadata = merge(ev.Metadata, map[string]any{"status": string(final), "total": cur.Total, "succeeded": cur.Succeeded, "failed": cur.Failed})
		if err := audited(ctx, tx, sc, ev, jobID, ""); err != nil {
			return err
		}
		out, err = loadJob(ctx, tx, sc, jobID)
		return err
	})
	return out, err
}

func (s *Store) CancelJob(ctx context.Context, sc tenancy.Scope, id string, now time.Time, ev audit.Event) (certificates.Job, error) {
	var out certificates.Job
	if !okID(id) {
		return out, certificates.ErrNotFound
	}
	err := s.run(ctx, sc, func(tx pgx.Tx) error {
		status, _, _, err := lockJob(ctx, tx, sc, id)
		if err != nil {
			return err
		}
		if !certificates.JobStatus(status).Active() {
			return certificates.ErrState
		}
		cur, err := loadJob(ctx, tx, sc, id)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE bulk_jobs SET status = 'cancelled', finished_at = $3 WHERE institute_id = $1::uuid AND id = $2::uuid`, sc.InstituteID(), id, now); err != nil {
			return err
		}
		ev.Metadata = merge(ev.Metadata, map[string]any{"succeeded": cur.Succeeded, "failed": cur.Failed, "remaining": cur.Remaining})
		if err := audited(ctx, tx, sc, ev, id, ""); err != nil {
			return err
		}
		out, err = loadJob(ctx, tx, sc, id)
		return err
	})
	return out, err
}

func (s *Store) RetryFailed(ctx context.Context, sc tenancy.Scope, id string, now time.Time, ev audit.Event) (certificates.Job, int, error) {
	var out certificates.Job
	n := 0
	if !okID(id) {
		return out, 0, certificates.ErrNotFound
	}
	err := s.run(ctx, sc, func(tx pgx.Tx) error {
		status, _, _, err := lockJob(ctx, tx, sc, id)
		if err != nil {
			return err
		}
		if status != string(certificates.JobCompletedWithErrors) && status != string(certificates.JobFailed) {
			return certificates.ErrState
		}
		var one int
		err = tx.QueryRow(ctx, `SELECT 1 FROM bulk_jobs WHERE institute_id = $1::uuid AND status IN ('queued', 'processing') LIMIT 1`, sc.InstituteID()).Scan(&one)
		if err == nil {
			return certificates.ErrJobActive
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		tag, err := tx.Exec(ctx, `UPDATE bulk_job_items SET status = 'queued', error = '', certificate_id = NULL, finished_at = NULL
			WHERE institute_id = $1::uuid AND job_id = $2::uuid AND status = 'failed'`, sc.InstituteID(), id)
		if err != nil {
			return err
		}
		n = int(tag.RowsAffected())
		if _, err := tx.Exec(ctx, `UPDATE bulk_jobs SET status = 'queued', finished_at = NULL, runner_token = '', lease_until = NULL
			WHERE institute_id = $1::uuid AND id = $2::uuid`, sc.InstituteID(), id); err != nil {
			return err
		}
		ev.Metadata = merge(ev.Metadata, map[string]any{"requeued": n})
		if err := audited(ctx, tx, sc, ev, id, ""); err != nil {
			return err
		}
		out, err = loadJob(ctx, tx, sc, id)
		return err
	})
	return out, n, err
}

func (s *Store) FindLive(ctx context.Context, sc tenancy.Scope, enrollmentID string) (certificates.Certificate, error) {
	var out certificates.Certificate
	if !okID(enrollmentID) {
		return out, certificates.ErrNotFound
	}
	err := s.run(ctx, sc, func(tx pgx.Tx) (err error) {
		out, err = scanCert(tx.QueryRow(ctx, `SELECT `+certCols+` FROM certificates c WHERE c.institute_id = $1::uuid AND c.enrollment_id = $2::uuid
			AND c.status IN ('pending', 'issued') ORDER BY (c.status = 'issued') DESC LIMIT 1`, sc.InstituteID(), enrollmentID))
		return
	})
	return out, err
}
