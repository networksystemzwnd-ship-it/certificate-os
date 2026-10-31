// Package pgstore is the PostgreSQL implementation of certificates.Store.
//
// Defense in depth (ADR-001): every method runs inside tenantdb.WithTenant (row-level security applies to
// the restricted runtime role) AND every statement still filters by the Scope's institute id. Certificates
// are never deleted and the stored PDF is insert-only (grants plus triggers, migration 0011). Issuing
// allocates its number under a row lock in the same transaction that inserts the certificate, so numbers
// are unique and a rolled-back attempt leaves no gap.
package pgstore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"strings"

	"github.com/certificate-os/api/internal/audit"
	auditpg "github.com/certificate-os/api/internal/audit/pgstore"
	"github.com/certificate-os/api/internal/certificates"
	"github.com/certificate-os/api/internal/platform/tenantdb"
	"github.com/certificate-os/api/internal/tenancy"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct{ Pool *pgxpool.Pool }

func New(pool *pgxpool.Pool) *Store { return &Store{Pool: pool} }

var _ certificates.Store = (*Store)(nil)

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
		switch e.ConstraintName {
		case "certificates_one_live_per_enrollment":
			return certificates.ErrAlreadyIssued
		case "certificates_one_pending_replacement":
			return certificates.ErrReplacing
		}
	case "22P02":
		return certificates.ErrNotFound
	}
	return err
}

func (s *Store) tx(ctx context.Context, sc tenancy.Scope, fn func(tx pgx.Tx) error) error {
	return tenantdb.WithTenant(ctx, s.Pool, sc, fn)
}

func audited(ctx context.Context, tx pgx.Tx, sc tenancy.Scope, ev audit.Event, id, number string) error {
	ev.EntityID, ev.InstituteID = id, sc.InstituteID()
	meta := map[string]any{}
	for k, v := range ev.Metadata {
		meta[k] = v
	}
	if number != "" {
		meta["number"] = number
	}
	ev.Metadata = meta
	return auditpg.InsertTx(ctx, tx, ev)
}

const certCols = `c.id::text, c.certificate_number, c.public_id, c.student_id::text, c.enrollment_id::text, c.template_id::text,
	c.template_version, c.snapshot::text, c.status, coalesce(c.pdf_sha256, ''), coalesce(c.pdf_size, 0), coalesce(c.issued_by::text, ''),
	c.created_at, c.issued_at, c.failed_at, c.failure, c.revoked_at, c.revoke_reason,
	coalesce(c.replaces_id::text, ''), coalesce(c.replaced_by::text, ''), coalesce(c.replaced_by_public_id, ''), c.replace_reason`

func scanCert(row pgx.Row) (certificates.Certificate, error) {
	var c certificates.Certificate
	var snap, status string
	err := row.Scan(&c.ID, &c.Number, &c.PublicID, &c.StudentID, &c.EnrollmentID, &c.TemplateID, &c.TemplateVersion, &snap, &status,
		&c.PDFSHA256, &c.PDFSize, &c.IssuedBy, &c.CreatedAt, &c.IssuedAt, &c.FailedAt, &c.Failure, &c.RevokedAt, &c.RevokeReason,
		&c.ReplacesID, &c.ReplacedByID, &c.ReplacedByPublicID, &c.ReplaceReason)
	if errors.Is(err, pgx.ErrNoRows) {
		return c, certificates.ErrNotFound
	}
	if err != nil {
		return c, err
	}
	c.Status = certificates.Status(status)
	if err := json.Unmarshal([]byte(snap), &c.Snapshot); err != nil {
		return c, err
	}
	return c, nil
}

func (s *Store) BeginIssue(ctx context.Context, sc tenancy.Scope, in certificates.BeginInput, ev audit.Event) (certificates.Certificate, error) {
	if !okID(in.EnrollmentID) || !okID(in.TemplateID) || in.Version < 0 || (in.Replaces != "" && !okID(in.Replaces)) {
		return certificates.Certificate{}, certificates.ErrNotFound
	}
	var out certificates.Certificate
	err := s.tx(ctx, sc, func(tx pgx.Tx) error {
		if in.Replaces != "" {
			// The certificate being replaced must be issued and belong to the same enrollment; lock it so it
			// cannot be revoked while the replacement is being created.
			old, err := lock(ctx, tx, sc, in.Replaces)
			if err != nil {
				return err
			}
			if old.Status != certificates.Issued || old.EnrollmentID != in.EnrollmentID {
				return certificates.ErrState
			}
		}
		var src certificates.Source
		// FOR SHARE keeps the enrollment and student stable until this transaction commits.
		err := tx.QueryRow(ctx, `SELECT s.id::text, s.full_name, coalesce(s.student_number, ''), s.archived_at IS NOT NULL,
				e.id::text, e.status, coalesce(to_char(e.completion_date, 'YYYY-MM-DD'), ''), e.grade, b.name, c.name, i.name
			FROM enrollments e
			JOIN students s ON s.institute_id = e.institute_id AND s.id = e.student_id
			JOIN batches b ON b.institute_id = e.institute_id AND b.id = e.batch_id
			JOIN courses c ON c.institute_id = b.institute_id AND c.id = b.course_id
			JOIN institutes i ON i.id = e.institute_id
			WHERE e.institute_id = $1::uuid AND e.id = $2::uuid FOR SHARE OF e, s`, sc.InstituteID(), in.EnrollmentID).
			Scan(&src.StudentID, &src.StudentName, &src.StudentNumber, &src.StudentArchived, &src.EnrollmentID, &src.EnrollmentStatus,
				&src.CompletionDate, &src.Grade, &src.BatchName, &src.CourseName, &src.InstituteName)
		if errors.Is(err, pgx.ErrNoRows) {
			return certificates.ErrNotFound
		}
		if err != nil {
			return err
		}
		var current int
		err = tx.QueryRow(ctx, `SELECT t.id::text, t.name, t.archived_at IS NOT NULL, t.current_version FROM certificate_templates t
			WHERE t.institute_id = $1::uuid AND t.id = $2::uuid`, sc.InstituteID(), in.TemplateID).
			Scan(&src.TemplateID, &src.TemplateName, &src.TemplateArchived, &current)
		if errors.Is(err, pgx.ErrNoRows) {
			return certificates.ErrNotFound
		}
		if err != nil {
			return err
		}
		src.TemplateVersion = in.Version
		if src.TemplateVersion == 0 {
			src.TemplateVersion = current
		}
		var cfg string
		err = tx.QueryRow(ctx, `SELECT config::text FROM certificate_template_versions
			WHERE institute_id = $1::uuid AND template_id = $2::uuid AND number = $3`, sc.InstituteID(), in.TemplateID, src.TemplateVersion).Scan(&cfg)
		if errors.Is(err, pgx.ErrNoRows) {
			return certificates.ErrNotFound
		}
		if err != nil {
			return err
		}
		if err := json.Unmarshal([]byte(cfg), &src.Config); err != nil {
			return err
		}
		if err := certificates.CheckEligible(src); err != nil {
			return err
		}
		// Abandon stale pending certificates of this enrollment so a crashed attempt cannot block it forever.
		if _, err := tx.Exec(ctx, `UPDATE certificates SET status = 'failed', failed_at = $3, failure = 'abandoned'
			WHERE institute_id = $1::uuid AND enrollment_id = $2::uuid AND status = 'pending' AND created_at < $3::timestamptz - $4::int * interval '1 second'`,
			sc.InstituteID(), in.EnrollmentID, in.Now, int(certificates.StalePendingAfter.Seconds())); err != nil {
			return err
		}
		// The counter row lock serialises numbering per institute and year until commit.
		year := in.Now.UTC().Year()
		var last int
		if err := tx.QueryRow(ctx, `INSERT INTO certificate_counters (institute_id, year, last_number) VALUES ($1::uuid, $2, 1)
			ON CONFLICT (institute_id, year) DO UPDATE SET last_number = certificate_counters.last_number + 1 RETURNING last_number`,
			sc.InstituteID(), year).Scan(&last); err != nil {
			return err
		}
		number := certificates.FormatNumber(year, last)
		snap, err := json.Marshal(certificates.BuildSnapshot(src, number, in.Now))
		if err != nil {
			return err
		}
		var id string
		if err := tx.QueryRow(ctx, `INSERT INTO certificates (institute_id, certificate_number, public_id, student_id, enrollment_id,
				template_id, template_version, snapshot, issued_by, created_at, replaces_id, replace_reason)
			VALUES ($1::uuid, $2, $3, $4::uuid, $5::uuid, $6::uuid, $7, $8::jsonb, nullif($9, '')::uuid, $10, nullif($11, '')::uuid, $12) RETURNING id::text`,
			sc.InstituteID(), number, in.PublicID, src.StudentID, in.EnrollmentID, in.TemplateID, src.TemplateVersion, string(snap), in.IssuedBy, in.Now,
			in.Replaces, in.ReplaceReason).
			Scan(&id); err != nil {
			return mapErr(err)
		}
		c, err := scanCert(tx.QueryRow(ctx, `SELECT `+certCols+` FROM certificates c WHERE c.institute_id = $1::uuid AND c.id = $2::uuid`, sc.InstituteID(), id))
		if err != nil {
			return err
		}
		out = c
		return audited(ctx, tx, sc, ev, id, number)
	})
	return out, err
}

// lockStatus loads the certificate row FOR UPDATE and returns it.
func lock(ctx context.Context, tx pgx.Tx, sc tenancy.Scope, id string) (certificates.Certificate, error) {
	return scanCert(tx.QueryRow(ctx, `SELECT `+certCols+` FROM certificates c WHERE c.institute_id = $1::uuid AND c.id = $2::uuid FOR UPDATE`, sc.InstituteID(), id))
}

func reload(ctx context.Context, tx pgx.Tx, sc tenancy.Scope, id string) (certificates.Certificate, error) {
	return scanCert(tx.QueryRow(ctx, `SELECT `+certCols+` FROM certificates c WHERE c.institute_id = $1::uuid AND c.id = $2::uuid`, sc.InstituteID(), id))
}

func (s *Store) Complete(ctx context.Context, sc tenancy.Scope, id string, pdf []byte, ev audit.Event) (certificates.Certificate, error) {
	if !okID(id) {
		return certificates.Certificate{}, certificates.ErrNotFound
	}
	var out certificates.Certificate
	err := s.tx(ctx, sc, func(tx pgx.Tx) error {
		cur, err := lock(ctx, tx, sc, id)
		if err != nil {
			return err
		}
		if cur.Status != certificates.Pending {
			return certificates.ErrState
		}
		if len(pdf) == 0 {
			return errors.New("certificates: empty pdf")
		}
		var old certificates.Certificate
		if cur.ReplacesID != "" {
			// Lock and revoke the certificate being replaced FIRST (the one-valid-certificate index would refuse two
			// issued ones), linking it to this one. If it is no longer issued the replacement cannot complete.
			if old, err = lock(ctx, tx, sc, cur.ReplacesID); err != nil {
				return err
			}
			if old.Status != certificates.Issued {
				return certificates.ErrState
			}
			if _, err := tx.Exec(ctx, `UPDATE certificates SET status = 'revoked', revoked_at = now(), revoked_by = nullif($3, '')::uuid, revoke_reason = $4,
					replaced_by = $5::uuid, replaced_by_public_id = $6
				WHERE institute_id = $1::uuid AND id = $2::uuid`, sc.InstituteID(), old.ID, cur.IssuedBy, cur.ReplaceReason, cur.ID, cur.PublicID); err != nil {
				return err
			}
			oe := audit.Event{ActorType: ev.ActorType, ActorUserID: ev.ActorUserID, RequestID: ev.RequestID, Action: "certificate.replaced",
				EntityType: "certificate", Metadata: map[string]any{"replaced_by": cur.ID}}
			if err := audited(ctx, tx, sc, oe, old.ID, old.Number); err != nil {
				return err
			}
		}
		sum := sha256.Sum256(pdf)
		if _, err := tx.Exec(ctx, `INSERT INTO certificate_files (institute_id, certificate_id, content) VALUES ($1::uuid, $2::uuid, $3)`,
			sc.InstituteID(), id, pdf); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE certificates SET status = 'issued', pdf_sha256 = $3, pdf_size = $4, issued_at = now()
			WHERE institute_id = $1::uuid AND id = $2::uuid`, sc.InstituteID(), id, hex.EncodeToString(sum[:]), len(pdf)); err != nil {
			return err
		}
		if out, err = reload(ctx, tx, sc, id); err != nil {
			return err
		}
		return audited(ctx, tx, sc, ev, id, out.Number)
	})
	return out, err
}

func (s *Store) Fail(ctx context.Context, sc tenancy.Scope, id, reason string, ev audit.Event) (certificates.Certificate, error) {
	if !okID(id) {
		return certificates.Certificate{}, certificates.ErrNotFound
	}
	if r := []rune(reason); len(r) > 200 {
		reason = string(r[:200])
	}
	var out certificates.Certificate
	err := s.tx(ctx, sc, func(tx pgx.Tx) error {
		cur, err := lock(ctx, tx, sc, id)
		if err != nil {
			return err
		}
		if cur.Status != certificates.Pending {
			return certificates.ErrState
		}
		if _, err := tx.Exec(ctx, `UPDATE certificates SET status = 'failed', failed_at = now(), failure = $3
			WHERE institute_id = $1::uuid AND id = $2::uuid`, sc.InstituteID(), id, reason); err != nil {
			return err
		}
		if out, err = reload(ctx, tx, sc, id); err != nil {
			return err
		}
		return audited(ctx, tx, sc, ev, id, out.Number)
	})
	return out, err
}

func (s *Store) Get(ctx context.Context, sc tenancy.Scope, id string) (certificates.Certificate, error) {
	if !okID(id) {
		return certificates.Certificate{}, certificates.ErrNotFound
	}
	var out certificates.Certificate
	err := s.tx(ctx, sc, func(tx pgx.Tx) error {
		c, err := reload(ctx, tx, sc, id)
		out = c
		return err
	})
	return out, err
}

func (s *Store) List(ctx context.Context, sc tenancy.Scope, f certificates.Filter) ([]certificates.Certificate, error) {
	f = f.Normalized()
	out := []certificates.Certificate{}
	if strings.ContainsRune(f.Q, 0) || (f.Status != "" && !f.Status.Valid()) || (f.StudentID != "" && !okID(f.StudentID)) {
		return out, nil
	}
	err := s.tx(ctx, sc, func(tx pgx.Tx) error {
		q := `SELECT ` + certCols + ` FROM certificates c WHERE c.institute_id = $1::uuid`
		args := []any{sc.InstituteID()}
		add := func(v any) string { args = append(args, v); return "$" + itoa(len(args)) }
		if f.Status != "" {
			q += ` AND c.status = ` + add(string(f.Status))
		}
		if f.StudentID != "" {
			q += ` AND c.student_id = ` + add(f.StudentID) + `::uuid`
		}
		if f.Q != "" {
			n := add(like(f.Q))
			q += ` AND (lower(c.certificate_number) LIKE ` + n + ` ESCAPE '\' OR lower(c.snapshot->>'student_name') LIKE ` + n + ` ESCAPE '\')`
		}
		q += ` ORDER BY c.created_at DESC, c.id DESC LIMIT ` + add(f.Limit+1) + ` OFFSET ` + add(f.Offset)
		rows, err := tx.Query(ctx, q, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			c, err := scanCert(rows)
			if err != nil {
				return err
			}
			out = append(out, c)
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

func (s *Store) GetPDF(ctx context.Context, sc tenancy.Scope, id string) (certificates.PDF, error) {
	if !okID(id) {
		return certificates.PDF{}, certificates.ErrNotFound
	}
	var out certificates.PDF
	err := s.tx(ctx, sc, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `SELECT f.content, c.pdf_sha256, c.certificate_number FROM certificates c
			JOIN certificate_files f ON f.institute_id = c.institute_id AND f.certificate_id = c.id
			WHERE c.institute_id = $1::uuid AND c.id = $2::uuid AND c.status IN ('issued', 'revoked')`, sc.InstituteID(), id).
			Scan(&out.Bytes, &out.SHA256, &out.Number)
		if errors.Is(err, pgx.ErrNoRows) {
			return certificates.ErrNotFound
		}
		return err
	})
	return out, err
}

func (s *Store) Revoke(ctx context.Context, sc tenancy.Scope, id, reason, by string, ev audit.Event) (certificates.Certificate, error) {
	if !okID(id) {
		return certificates.Certificate{}, certificates.ErrNotFound
	}
	var out certificates.Certificate
	err := s.tx(ctx, sc, func(tx pgx.Tx) error {
		cur, err := lock(ctx, tx, sc, id)
		if err != nil {
			return err
		}
		if cur.Status != certificates.Issued {
			return certificates.ErrState
		}
		if _, err := tx.Exec(ctx, `UPDATE certificates SET status = 'revoked', revoked_at = now(), revoked_by = nullif($3, '')::uuid, revoke_reason = $4
			WHERE institute_id = $1::uuid AND id = $2::uuid`, sc.InstituteID(), id, by, reason); err != nil {
			return err
		}
		if out, err = reload(ctx, tx, sc, id); err != nil {
			return err
		}
		return audited(ctx, tx, sc, ev, id, out.Number)
	})
	return out, err
}
