// Package pgstore is the PostgreSQL implementation of applications.Store.
//
// Every mutation runs in one transaction that locks the row (FOR UPDATE), checks the guard, changes
// state and appends the audit event(s). Nothing is committed unless all of it succeeds.
package pgstore

import (
	"context"
	"errors"
	"regexp"

	"github.com/certificate-os/api/internal/applications"
	"github.com/certificate-os/api/internal/audit"
	auditpg "github.com/certificate-os/api/internal/audit/pgstore"
	"github.com/certificate-os/api/internal/tenancy"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct{ Pool *pgxpool.Pool }

func New(pool *pgxpool.Pool) *Store { return &Store{Pool: pool} }

var _ applications.Store = (*Store)(nil)

var uuidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

const cols = `id::text, applicant_user_id::text, status, name, slug, category, address_line1, address_line2, city, region,
	postal_code, country, official_email, phone, rep_name, rep_role, website, attested, version, submitted_at,
	coalesce(reviewed_by::text, ''), reviewed_at, decision_reason, coalesce(institute_id::text, ''), created_at, updated_at`

func pgErr(err error) *pgconn.PgError {
	var e *pgconn.PgError
	if errors.As(err, &e) {
		return e
	}
	return nil
}

// mapErr turns unique violations into domain errors by constraint name.
func mapErr(err error) error {
	if e := pgErr(err); e != nil && e.Code == "23505" {
		switch e.ConstraintName {
		case "applications_one_open_per_user":
			return applications.ErrOpenApplicationExists
		case "applications_slug_claim", "institutes_slug_key":
			return applications.ErrSlugTaken
		}
	}
	return err
}

func scan(row pgx.Row) (applications.Application, error) {
	var a applications.Application
	var status string
	d := &a.Details
	err := row.Scan(&a.ID, &a.ApplicantUserID, &status, &d.Name, &d.Slug, &d.Category, &d.AddressLine1, &d.AddressLine2,
		&d.City, &d.Region, &d.PostalCode, &d.Country, &d.OfficialEmail, &d.Phone, &d.RepName, &d.RepRole, &d.Website,
		&d.Attested, &a.Version, &a.SubmittedAt, &a.ReviewedBy, &a.ReviewedAt, &a.DecisionReason, &a.InstituteID,
		&a.CreatedAt, &a.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return a, applications.ErrNotFound
	}
	a.Status = applications.Status(status)
	return a, err
}

// inTx runs fn in a transaction, rolling back on any error.
func (s *Store) inTx(ctx context.Context, fn func(tx pgx.Tx) error) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) Create(ctx context.Context, applicantID string, d applications.Details, ev audit.Event) (applications.Application, error) {
	var out applications.Application
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		a, err := scan(tx.QueryRow(ctx, `INSERT INTO institute_applications
			(applicant_user_id, name, slug, category, address_line1, address_line2, city, region, postal_code, country,
			 official_email, phone, rep_name, rep_role, website, attested)
			VALUES ($1::uuid,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16) RETURNING `+cols,
			applicantID, d.Name, d.Slug, d.Category, d.AddressLine1, d.AddressLine2, d.City, d.Region, d.PostalCode,
			d.Country, d.OfficialEmail, d.Phone, d.RepName, d.RepRole, d.Website, d.Attested))
		if err != nil {
			return mapErr(err)
		}
		ev.EntityID = a.ID
		if err := auditpg.InsertTx(ctx, tx, ev); err != nil {
			return err
		}
		out = a
		return nil
	})
	return out, err
}

func (s *Store) GetForApplicant(ctx context.Context, applicantID, id string) (applications.Application, error) {
	if !uuidRe.MatchString(id) {
		return applications.Application{}, applications.ErrNotFound
	}
	return scan(s.Pool.QueryRow(ctx, `SELECT `+cols+` FROM institute_applications WHERE id=$1::uuid AND applicant_user_id=$2::uuid`, id, applicantID))
}

func (s *Store) ListForApplicant(ctx context.Context, applicantID string) ([]applications.Application, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+cols+` FROM institute_applications WHERE applicant_user_id=$1::uuid ORDER BY created_at DESC, id`, applicantID)
	return collect(rows, err)
}

func collect(rows pgx.Rows, err error) ([]applications.Application, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []applications.Application{}
	for rows.Next() {
		a, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) UpdateDetails(ctx context.Context, applicantID, id string, d applications.Details, ev audit.Event) (applications.Application, error) {
	if !uuidRe.MatchString(id) {
		return applications.Application{}, applications.ErrNotFound
	}
	var out applications.Application
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		cur, err := scan(tx.QueryRow(ctx, `SELECT `+cols+` FROM institute_applications WHERE id=$1::uuid AND applicant_user_id=$2::uuid FOR UPDATE`, id, applicantID))
		if err != nil {
			return err
		}
		if !cur.Status.Editable() {
			return applications.ErrNotEditable
		}
		a, err := scan(tx.QueryRow(ctx, `UPDATE institute_applications SET
			name=$2, slug=$3, category=$4, address_line1=$5, address_line2=$6, city=$7, region=$8, postal_code=$9,
			country=$10, official_email=$11, phone=$12, rep_name=$13, rep_role=$14, website=$15, attested=$16,
			version=version+1 WHERE id=$1::uuid RETURNING `+cols,
			id, d.Name, d.Slug, d.Category, d.AddressLine1, d.AddressLine2, d.City, d.Region, d.PostalCode,
			d.Country, d.OfficialEmail, d.Phone, d.RepName, d.RepRole, d.Website, d.Attested))
		if err != nil {
			return mapErr(err)
		}
		ev.EntityID = a.ID
		if err := auditpg.InsertTx(ctx, tx, ev); err != nil {
			return err
		}
		out = a
		return nil
	})
	return out, err
}

func (s *Store) GetForReview(ctx context.Context, id string) (applications.Application, error) {
	if !uuidRe.MatchString(id) {
		return applications.Application{}, applications.ErrNotFound
	}
	return scan(s.Pool.QueryRow(ctx, `SELECT `+cols+` FROM institute_applications WHERE id=$1::uuid AND status <> 'draft'`, id))
}

func (s *Store) ListForReview(ctx context.Context, f applications.ListFilter) ([]applications.Application, error) {
	f = f.Normalized()
	statuses := make([]string, 0, len(f.Statuses))
	for _, st := range f.Statuses {
		if st.Valid() && st != applications.Draft {
			statuses = append(statuses, string(st))
		}
	}
	rows, err := s.Pool.Query(ctx, `SELECT `+cols+` FROM institute_applications
		WHERE status <> 'draft' AND (cardinality($1::text[]) = 0 OR status = ANY($1::text[]))
		ORDER BY submitted_at ASC NULLS LAST, id LIMIT $2 OFFSET $3`, statuses, f.Limit, f.Offset)
	return collect(rows, err)
}

func (s *Store) Transition(ctx context.Context, t applications.Transition) (applications.Application, error) {
	if !uuidRe.MatchString(t.ID) {
		return applications.Application{}, applications.ErrNotFound
	}
	var out applications.Application
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		cur, err := scan(tx.QueryRow(ctx, `SELECT `+cols+` FROM institute_applications WHERE id=$1::uuid FOR UPDATE`, t.ID))
		if err != nil {
			return err
		}
		if (t.ApplicantID != "" && cur.ApplicantUserID != t.ApplicantID) || (t.ApplicantID == "" && cur.Status == applications.Draft) {
			return applications.ErrNotFound
		}
		ok := false
		for _, f := range t.From {
			ok = ok || f == cur.Status
		}
		if !ok {
			return applications.ErrInvalidTransition
		}
		d := cur.Details
		if t.Details != nil {
			d = *t.Details
		}
		if t.To == applications.Submitted {
			var taken bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM institutes WHERE slug=$1)`, d.Slug).Scan(&taken); err != nil {
				return err
			}
			if taken {
				return applications.ErrSlugTaken
			}
		}
		a, err := scan(tx.QueryRow(ctx, `UPDATE institute_applications SET status=$2,
			name=$3, slug=$4, category=$5, address_line1=$6, address_line2=$7, city=$8, region=$9, postal_code=$10,
			country=$11, official_email=$12, phone=$13, rep_name=$14, rep_role=$15, website=$16, attested=$17,
			submitted_at = CASE WHEN $2 = 'submitted' THEN now() ELSE submitted_at END,
			reviewed_by  = CASE WHEN $2 IN ('under_review','rejected','changes_requested') THEN $18::uuid ELSE reviewed_by END,
			reviewed_at  = CASE WHEN $2 IN ('rejected','changes_requested') THEN now() ELSE reviewed_at END,
			decision_reason = CASE WHEN $2 IN ('rejected','changes_requested') THEN $19 ELSE decision_reason END,
			version=version+1 WHERE id=$1::uuid RETURNING `+cols,
			t.ID, string(t.To), d.Name, d.Slug, d.Category, d.AddressLine1, d.AddressLine2, d.City, d.Region, d.PostalCode,
			d.Country, d.OfficialEmail, d.Phone, d.RepName, d.RepRole, d.Website, d.Attested, nullable(t.ActorUserID), t.Reason))
		if err != nil {
			return mapErr(err)
		}
		t.Event.EntityID = a.ID
		if err := auditpg.InsertTx(ctx, tx, t.Event); err != nil {
			return err
		}
		out = a
		return nil
	})
	return out, err
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func (s *Store) Approve(ctx context.Context, in applications.ApproveInput) (applications.Application, tenancy.Institute, error) {
	var app applications.Application
	var inst tenancy.Institute
	if !uuidRe.MatchString(in.ID) {
		return app, inst, applications.ErrNotFound
	}
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		cur, err := scan(tx.QueryRow(ctx, `SELECT `+cols+` FROM institute_applications WHERE id=$1::uuid AND status <> 'draft' FOR UPDATE`, in.ID))
		if err != nil {
			return err
		}
		if cur.Status != applications.UnderReview {
			return applications.ErrInvalidTransition
		}
		var active bool
		if err := tx.QueryRow(ctx, `SELECT status = 'active' FROM users WHERE id=$1::uuid`, cur.ApplicantUserID).Scan(&active); err != nil {
			return err
		}
		if !active {
			return applications.ErrApplicantInactive
		}
		var status string
		if err := tx.QueryRow(ctx, `INSERT INTO institutes (slug, name) VALUES ($1, $2) RETURNING id::text, slug, name, status, created_at`,
			cur.Details.Slug, cur.Details.Name).Scan(&inst.ID, &inst.Slug, &inst.Name, &status, &inst.CreatedAt); err != nil {
			return mapErr(err)
		}
		inst.Status = tenancy.InstituteStatus(status)
		if _, err := tx.Exec(ctx, `INSERT INTO institute_memberships (institute_id, user_id, role) VALUES ($1::uuid, $2::uuid, 'owner')`,
			inst.ID, cur.ApplicantUserID); err != nil {
			return err
		}
		a, err := scan(tx.QueryRow(ctx, `UPDATE institute_applications SET status='approved', reviewed_by=$2::uuid, reviewed_at=now(),
			institute_id=$3::uuid, version=version+1 WHERE id=$1::uuid RETURNING `+cols, in.ID, in.ReviewerID, inst.ID))
		if err != nil {
			return mapErr(err)
		}
		for _, ev := range applications.ApprovalEvents(in, a, inst) {
			if err := auditpg.InsertTx(ctx, tx, ev); err != nil {
				return err
			}
		}
		app = a
		return nil
	})
	if err != nil {
		return applications.Application{}, tenancy.Institute{}, err
	}
	return app, inst, nil
}
