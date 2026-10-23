// Package pgstore is the PostgreSQL implementation of records.Store.
//
// Defense in depth (ADR-001): every method runs inside tenantdb.WithTenant (row-level security applies
// to the restricted runtime role) AND every statement still filters by the Scope's institute id.
// Mutations write their audit event in the same transaction.
package pgstore

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/certificate-os/api/internal/audit"
	auditpg "github.com/certificate-os/api/internal/audit/pgstore"
	"github.com/certificate-os/api/internal/platform/tenantdb"
	"github.com/certificate-os/api/internal/records"
	"github.com/certificate-os/api/internal/tenancy"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct{ Pool *pgxpool.Pool }

func New(pool *pgxpool.Pool) *Store { return &Store{Pool: pool} }

var _ records.Store = (*Store)(nil)

var uuidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// like turns operator-typed search text into a LIKE pattern with wildcards neutralized.
func like(q string) string {
	return "%" + strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(strings.ToLower(q)) + "%"
}

func pgErr(err error) *pgconn.PgError {
	var e *pgconn.PgError
	if errors.As(err, &e) {
		return e
	}
	return nil
}

// mapErr turns constraint violations into domain errors.
func mapErr(err error) error {
	e := pgErr(err)
	if e == nil {
		return err
	}
	switch e.Code {
	case "23505":
		switch e.ConstraintName {
		case "courses_active_name_key", "batches_active_name_key":
			return &records.ConflictError{Field: "name"}
		case "courses_active_code_key":
			return &records.ConflictError{Field: "code"}
		case "students_number_key":
			return &records.ConflictError{Field: "student_number"}
		case "enrollments_unique_student_batch":
			return &records.ConflictError{Field: "batch_id"}
		}
	case "23503":
		return records.ErrBadReference
	case "22P02": // malformed uuid that slipped past the guards
		return records.ErrNotFound
	}
	return err
}

func (s *Store) tx(ctx context.Context, sc tenancy.Scope, fn func(tx pgx.Tx) error) error {
	return tenantdb.WithTenant(ctx, s.Pool, sc, fn)
}

func audited(ctx context.Context, tx pgx.Tx, sc tenancy.Scope, ev audit.Event, entityID string) error {
	ev.EntityID, ev.InstituteID = entityID, sc.InstituteID()
	return auditpg.InsertTx(ctx, tx, ev)
}

// --- scanning ---

const courseCols = `id::text, name, code, description, archived_at, created_at, updated_at`

func scanCourse(row pgx.Row) (records.Course, error) {
	var c records.Course
	err := row.Scan(&c.ID, &c.Name, &c.Code, &c.Description, &c.ArchivedAt, &c.CreatedAt, &c.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return c, records.ErrNotFound
	}
	return c, err
}

const batchSel = `SELECT b.id::text, b.course_id::text, c.name, b.name,
	coalesce(to_char(b.start_date, 'YYYY-MM-DD'), ''), coalesce(to_char(b.end_date, 'YYYY-MM-DD'), ''),
	b.archived_at, b.created_at, b.updated_at
	FROM batches b JOIN courses c ON c.institute_id = b.institute_id AND c.id = b.course_id`

func scanBatch(row pgx.Row) (records.Batch, error) {
	var b records.Batch
	err := row.Scan(&b.ID, &b.CourseID, &b.CourseName, &b.Name, &b.StartDate, &b.EndDate, &b.ArchivedAt, &b.CreatedAt, &b.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return b, records.ErrNotFound
	}
	return b, err
}

const studentCols = `id::text, coalesce(student_number, ''), full_name, email, phone, archived_at, created_at, updated_at`

func scanStudent(row pgx.Row) (records.Student, error) {
	var st records.Student
	err := row.Scan(&st.ID, &st.StudentNumber, &st.FullName, &st.Email, &st.Phone, &st.ArchivedAt, &st.CreatedAt, &st.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return st, records.ErrNotFound
	}
	return st, err
}

const enrollmentSel = `SELECT e.id::text, e.student_id::text, e.batch_id::text, b.name, c.id::text, c.name, e.status,
	coalesce(to_char(e.completion_date, 'YYYY-MM-DD'), ''), e.grade, e.created_at, e.updated_at
	FROM enrollments e
	JOIN batches b ON b.institute_id = e.institute_id AND b.id = e.batch_id
	JOIN courses c ON c.institute_id = b.institute_id AND c.id = b.course_id`

func scanEnrollment(row pgx.Row) (records.Enrollment, error) {
	var e records.Enrollment
	var status string
	err := row.Scan(&e.ID, &e.StudentID, &e.BatchID, &e.BatchName, &e.CourseID, &e.CourseName, &status, &e.CompletionDate, &e.Grade, &e.CreatedAt, &e.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return e, records.ErrNotFound
	}
	e.Status = records.EnrollmentStatus(status)
	return e, err
}

func archiveClause(a records.Archive, col string) string {
	switch a {
	case records.ArchivedOnly:
		return " AND " + col + " IS NOT NULL"
	case records.IncludeAll:
		return ""
	}
	return " AND " + col + " IS NULL"
}

// qArgs holds positional arguments for dynamically built queries.
type qArgs []any

func (a *qArgs) add(v any) string {
	*a = append(*a, v)
	return "$" + itoa(len(*a))
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

func nullDate(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func okID(id string) bool { return uuidRe.MatchString(id) }

// --- courses ---

func (s *Store) CreateCourse(ctx context.Context, sc tenancy.Scope, in records.CourseInput, ev audit.Event) (records.Course, error) {
	var out records.Course
	err := s.tx(ctx, sc, func(tx pgx.Tx) error {
		c, err := scanCourse(tx.QueryRow(ctx, `INSERT INTO courses (institute_id, name, code, description)
			VALUES ($1::uuid, $2, $3, $4) RETURNING `+courseCols, sc.InstituteID(), in.Name, in.Code, in.Description))
		if err != nil {
			return mapErr(err)
		}
		out = c
		return audited(ctx, tx, sc, ev, c.ID)
	})
	return out, err
}

func (s *Store) GetCourse(ctx context.Context, sc tenancy.Scope, id string) (records.Course, error) {
	if !okID(id) {
		return records.Course{}, records.ErrNotFound
	}
	var out records.Course
	err := s.tx(ctx, sc, func(tx pgx.Tx) error {
		c, err := scanCourse(tx.QueryRow(ctx, `SELECT `+courseCols+` FROM courses WHERE institute_id = $1::uuid AND id = $2::uuid`, sc.InstituteID(), id))
		out = c
		return err
	})
	return out, err
}

func (s *Store) ListCourses(ctx context.Context, sc tenancy.Scope, p records.Page) ([]records.Course, error) {
	p = p.Normalized()
	out := []records.Course{}
	if strings.ContainsRune(p.Q, 0) {
		return out, nil
	}
	err := s.tx(ctx, sc, func(tx pgx.Tx) error {
		var a qArgs
		q := `SELECT ` + courseCols + ` FROM courses WHERE institute_id = ` + a.add(sc.InstituteID()) + `::uuid` + archiveClause(p.Archived, "archived_at")
		if p.Q != "" {
			n := a.add(like(p.Q))
			q += ` AND (lower(name) LIKE ` + n + ` ESCAPE '\' OR lower(code) LIKE ` + n + ` ESCAPE '\')`
		}
		q += ` ORDER BY lower(name), id LIMIT ` + a.add(p.Limit+1) + ` OFFSET ` + a.add(p.Offset)
		rows, err := tx.Query(ctx, q, a...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			c, err := scanCourse(rows)
			if err != nil {
				return err
			}
			out = append(out, c)
		}
		return rows.Err()
	})
	return out, err
}

func (s *Store) UpdateCourse(ctx context.Context, sc tenancy.Scope, id string, in records.CourseInput, ev audit.Event) (records.Course, error) {
	if !okID(id) {
		return records.Course{}, records.ErrNotFound
	}
	var out records.Course
	err := s.tx(ctx, sc, func(tx pgx.Tx) error {
		cur, err := scanCourse(tx.QueryRow(ctx, `SELECT `+courseCols+` FROM courses WHERE institute_id = $1::uuid AND id = $2::uuid FOR UPDATE`, sc.InstituteID(), id))
		if err != nil {
			return err
		}
		if cur.ArchivedAt != nil {
			return records.ErrArchived
		}
		c, err := scanCourse(tx.QueryRow(ctx, `UPDATE courses SET name = $3, code = $4, description = $5
			WHERE institute_id = $1::uuid AND id = $2::uuid RETURNING `+courseCols, sc.InstituteID(), id, in.Name, in.Code, in.Description))
		if err != nil {
			return mapErr(err)
		}
		out = c
		return audited(ctx, tx, sc, ev, id)
	})
	return out, err
}

func (s *Store) SetCourseArchived(ctx context.Context, sc tenancy.Scope, id string, archived bool, ev audit.Event) (records.Course, error) {
	if !okID(id) {
		return records.Course{}, records.ErrNotFound
	}
	var out records.Course
	err := s.tx(ctx, sc, func(tx pgx.Tx) error {
		// FOR UPDATE serializes with CreateBatch (which takes FOR SHARE on the course).
		if _, err := scanCourse(tx.QueryRow(ctx, `SELECT `+courseCols+` FROM courses WHERE institute_id = $1::uuid AND id = $2::uuid FOR UPDATE`, sc.InstituteID(), id)); err != nil {
			return err
		}
		if archived {
			var busy bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM batches WHERE institute_id = $1::uuid AND course_id = $2::uuid AND archived_at IS NULL)`, sc.InstituteID(), id).Scan(&busy); err != nil {
				return err
			}
			if busy {
				return records.ErrHasActiveItems
			}
		}
		c, err := scanCourse(tx.QueryRow(ctx, `UPDATE courses SET archived_at = CASE WHEN $3 THEN now() ELSE NULL END
			WHERE institute_id = $1::uuid AND id = $2::uuid RETURNING `+courseCols, sc.InstituteID(), id, archived))
		if err != nil {
			return mapErr(err) // restoring over a newer active course with the same name/code
		}
		out = c
		return audited(ctx, tx, sc, ev, id)
	})
	return out, err
}

// --- batches ---

func (s *Store) CreateBatch(ctx context.Context, sc tenancy.Scope, in records.BatchInput, ev audit.Event) (records.Batch, error) {
	if !okID(in.CourseID) {
		return records.Batch{}, records.ErrBadReference
	}
	var out records.Batch
	err := s.tx(ctx, sc, func(tx pgx.Tx) error {
		var archivedAt *time.Time
		err := tx.QueryRow(ctx, `SELECT archived_at FROM courses WHERE institute_id = $1::uuid AND id = $2::uuid FOR SHARE`, sc.InstituteID(), in.CourseID).Scan(&archivedAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return records.ErrBadReference
		}
		if err != nil {
			return err
		}
		if archivedAt != nil {
			return records.ErrArchived
		}
		var id string
		if err := tx.QueryRow(ctx, `INSERT INTO batches (institute_id, course_id, name, start_date, end_date)
			VALUES ($1::uuid, $2::uuid, $3, $4::date, $5::date) RETURNING id::text`,
			sc.InstituteID(), in.CourseID, in.Name, nullDate(in.StartDate), nullDate(in.EndDate)).Scan(&id); err != nil {
			return mapErr(err)
		}
		b, err := scanBatch(tx.QueryRow(ctx, batchSel+` WHERE b.institute_id = $1::uuid AND b.id = $2::uuid`, sc.InstituteID(), id))
		if err != nil {
			return err
		}
		out = b
		return audited(ctx, tx, sc, ev, id)
	})
	return out, err
}

func (s *Store) GetBatch(ctx context.Context, sc tenancy.Scope, id string) (records.Batch, error) {
	if !okID(id) {
		return records.Batch{}, records.ErrNotFound
	}
	var out records.Batch
	err := s.tx(ctx, sc, func(tx pgx.Tx) error {
		b, err := scanBatch(tx.QueryRow(ctx, batchSel+` WHERE b.institute_id = $1::uuid AND b.id = $2::uuid`, sc.InstituteID(), id))
		out = b
		return err
	})
	return out, err
}

func (s *Store) ListBatches(ctx context.Context, sc tenancy.Scope, f records.BatchFilter) ([]records.Batch, error) {
	f.Page = f.Page.Normalized()
	out := []records.Batch{}
	if strings.ContainsRune(f.Q, 0) || (f.CourseID != "" && !okID(f.CourseID)) {
		return out, nil
	}
	err := s.tx(ctx, sc, func(tx pgx.Tx) error {
		var a qArgs
		q := batchSel + ` WHERE b.institute_id = ` + a.add(sc.InstituteID()) + `::uuid` + archiveClause(f.Archived, "b.archived_at")
		if f.CourseID != "" {
			q += ` AND b.course_id = ` + a.add(f.CourseID) + `::uuid`
		}
		if f.Q != "" {
			n := a.add(like(f.Q))
			q += ` AND (lower(b.name) LIKE ` + n + ` ESCAPE '\' OR lower(c.name) LIKE ` + n + ` ESCAPE '\')`
		}
		q += ` ORDER BY b.start_date DESC NULLS LAST, b.id LIMIT ` + a.add(f.Limit+1) + ` OFFSET ` + a.add(f.Offset)
		rows, err := tx.Query(ctx, q, a...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			b, err := scanBatch(rows)
			if err != nil {
				return err
			}
			out = append(out, b)
		}
		return rows.Err()
	})
	return out, err
}

func (s *Store) UpdateBatch(ctx context.Context, sc tenancy.Scope, id string, in records.BatchInput, ev audit.Event) (records.Batch, error) {
	if !okID(id) {
		return records.Batch{}, records.ErrNotFound
	}
	var out records.Batch
	err := s.tx(ctx, sc, func(tx pgx.Tx) error {
		var archivedAt *time.Time
		err := tx.QueryRow(ctx, `SELECT archived_at FROM batches WHERE institute_id = $1::uuid AND id = $2::uuid FOR UPDATE`, sc.InstituteID(), id).Scan(&archivedAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return records.ErrNotFound
		}
		if err != nil {
			return err
		}
		if archivedAt != nil {
			return records.ErrArchived
		}
		if _, err := tx.Exec(ctx, `UPDATE batches SET name = $3, start_date = $4::date, end_date = $5::date
			WHERE institute_id = $1::uuid AND id = $2::uuid`, sc.InstituteID(), id, in.Name, nullDate(in.StartDate), nullDate(in.EndDate)); err != nil {
			return mapErr(err)
		}
		b, err := scanBatch(tx.QueryRow(ctx, batchSel+` WHERE b.institute_id = $1::uuid AND b.id = $2::uuid`, sc.InstituteID(), id))
		if err != nil {
			return err
		}
		out = b
		return audited(ctx, tx, sc, ev, id)
	})
	return out, err
}

func (s *Store) SetBatchArchived(ctx context.Context, sc tenancy.Scope, id string, archived bool, ev audit.Event) (records.Batch, error) {
	if !okID(id) {
		return records.Batch{}, records.ErrNotFound
	}
	var out records.Batch
	err := s.tx(ctx, sc, func(tx pgx.Tx) error {
		var courseID string
		var courseArchived bool
		err := tx.QueryRow(ctx, `SELECT b.course_id::text, c.archived_at IS NOT NULL
			FROM batches b JOIN courses c ON c.institute_id = b.institute_id AND c.id = b.course_id
			WHERE b.institute_id = $1::uuid AND b.id = $2::uuid FOR UPDATE OF b`, sc.InstituteID(), id).Scan(&courseID, &courseArchived)
		if errors.Is(err, pgx.ErrNoRows) {
			return records.ErrNotFound
		}
		if err != nil {
			return err
		}
		if archived {
			var busy bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM enrollments WHERE institute_id = $1::uuid AND batch_id = $2::uuid AND status = 'enrolled')`, sc.InstituteID(), id).Scan(&busy); err != nil {
				return err
			}
			if busy {
				return records.ErrHasActiveItems
			}
		} else if courseArchived {
			return records.ErrArchived // restore the course first
		}
		if _, err := tx.Exec(ctx, `UPDATE batches SET archived_at = CASE WHEN $3 THEN now() ELSE NULL END
			WHERE institute_id = $1::uuid AND id = $2::uuid`, sc.InstituteID(), id, archived); err != nil {
			return mapErr(err)
		}
		b, err := scanBatch(tx.QueryRow(ctx, batchSel+` WHERE b.institute_id = $1::uuid AND b.id = $2::uuid`, sc.InstituteID(), id))
		if err != nil {
			return err
		}
		out = b
		return audited(ctx, tx, sc, ev, id)
	})
	return out, err
}

// --- students ---

func nullNumber(n string) any {
	if n == "" {
		return nil
	}
	return n
}

func (s *Store) CreateStudent(ctx context.Context, sc tenancy.Scope, in records.StudentInput, ev audit.Event) (records.Student, error) {
	var out records.Student
	err := s.tx(ctx, sc, func(tx pgx.Tx) error {
		st, err := scanStudent(tx.QueryRow(ctx, `INSERT INTO students (institute_id, student_number, full_name, email, phone)
			VALUES ($1::uuid, $2, $3, $4, $5) RETURNING `+studentCols, sc.InstituteID(), nullNumber(in.StudentNumber), in.FullName, in.Email, in.Phone))
		if err != nil {
			return mapErr(err)
		}
		out = st
		return audited(ctx, tx, sc, ev, st.ID)
	})
	return out, err
}

func (s *Store) GetStudent(ctx context.Context, sc tenancy.Scope, id string) (records.Student, error) {
	if !okID(id) {
		return records.Student{}, records.ErrNotFound
	}
	var out records.Student
	err := s.tx(ctx, sc, func(tx pgx.Tx) error {
		st, err := scanStudent(tx.QueryRow(ctx, `SELECT `+studentCols+` FROM students WHERE institute_id = $1::uuid AND id = $2::uuid`, sc.InstituteID(), id))
		if err != nil {
			return err
		}
		st.Enrollments = []records.Enrollment{}
		rows, err := tx.Query(ctx, enrollmentSel+` WHERE e.institute_id = $1::uuid AND e.student_id = $2::uuid ORDER BY e.created_at, e.id`, sc.InstituteID(), id)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			e, err := scanEnrollment(rows)
			if err != nil {
				return err
			}
			st.Enrollments = append(st.Enrollments, e)
		}
		out = st
		return rows.Err()
	})
	return out, err
}

func (s *Store) ListStudents(ctx context.Context, sc tenancy.Scope, f records.StudentFilter) ([]records.Student, error) {
	f.Page = f.Page.Normalized()
	out := []records.Student{}
	if strings.ContainsRune(f.Q, 0) || (f.BatchID != "" && !okID(f.BatchID)) || (f.CourseID != "" && !okID(f.CourseID)) || (f.Status != "" && !f.Status.Valid()) {
		return out, nil
	}
	err := s.tx(ctx, sc, func(tx pgx.Tx) error {
		var a qArgs
		q := `SELECT ` + studentCols + ` FROM students s WHERE s.institute_id = ` + a.add(sc.InstituteID()) + `::uuid` + archiveClause(f.Archived, "s.archived_at")
		if f.Q != "" {
			n := a.add(like(f.Q))
			q += ` AND (lower(s.full_name) LIKE ` + n + ` ESCAPE '\' OR lower(s.email) LIKE ` + n + ` ESCAPE '\' OR lower(coalesce(s.student_number, '')) LIKE ` + n + ` ESCAPE '\')`
		}
		if f.BatchID != "" || f.CourseID != "" || f.Status != "" {
			q += ` AND EXISTS (SELECT 1 FROM enrollments e JOIN batches b ON b.institute_id = e.institute_id AND b.id = e.batch_id
				WHERE e.institute_id = s.institute_id AND e.student_id = s.id`
			if f.BatchID != "" {
				q += ` AND e.batch_id = ` + a.add(f.BatchID) + `::uuid`
			}
			if f.CourseID != "" {
				q += ` AND b.course_id = ` + a.add(f.CourseID) + `::uuid`
			}
			if f.Status != "" {
				q += ` AND e.status = ` + a.add(string(f.Status))
			}
			q += `)`
		}
		q += ` ORDER BY lower(s.full_name), s.id LIMIT ` + a.add(f.Limit+1) + ` OFFSET ` + a.add(f.Offset)
		q = strings.Replace(q, studentCols, "s.id::text, coalesce(s.student_number, ''), s.full_name, s.email, s.phone, s.archived_at, s.created_at, s.updated_at", 1)
		rows, err := tx.Query(ctx, q, a...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			st, err := scanStudent(rows)
			if err != nil {
				return err
			}
			out = append(out, st)
		}
		return rows.Err()
	})
	return out, err
}

func (s *Store) UpdateStudent(ctx context.Context, sc tenancy.Scope, id string, in records.StudentInput, ev audit.Event) (records.Student, error) {
	if !okID(id) {
		return records.Student{}, records.ErrNotFound
	}
	var out records.Student
	err := s.tx(ctx, sc, func(tx pgx.Tx) error {
		var archivedAt *time.Time
		err := tx.QueryRow(ctx, `SELECT archived_at FROM students WHERE institute_id = $1::uuid AND id = $2::uuid FOR UPDATE`, sc.InstituteID(), id).Scan(&archivedAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return records.ErrNotFound
		}
		if err != nil {
			return err
		}
		if archivedAt != nil {
			return records.ErrArchived
		}
		st, err := scanStudent(tx.QueryRow(ctx, `UPDATE students SET student_number = $3, full_name = $4, email = $5, phone = $6
			WHERE institute_id = $1::uuid AND id = $2::uuid RETURNING `+studentCols, sc.InstituteID(), id, nullNumber(in.StudentNumber), in.FullName, in.Email, in.Phone))
		if err != nil {
			return mapErr(err)
		}
		out = st
		return audited(ctx, tx, sc, ev, id)
	})
	return out, err
}

func (s *Store) SetStudentArchived(ctx context.Context, sc tenancy.Scope, id string, archived bool, ev audit.Event) (records.Student, error) {
	if !okID(id) {
		return records.Student{}, records.ErrNotFound
	}
	var out records.Student
	err := s.tx(ctx, sc, func(tx pgx.Tx) error {
		st, err := scanStudent(tx.QueryRow(ctx, `UPDATE students SET archived_at = CASE WHEN $3 THEN now() ELSE NULL END
			WHERE institute_id = $1::uuid AND id = $2::uuid RETURNING `+studentCols, sc.InstituteID(), id, archived))
		if err != nil {
			return mapErr(err)
		}
		out = st
		return audited(ctx, tx, sc, ev, id)
	})
	return out, err
}

// --- enrollments ---

func (s *Store) AddEnrollment(ctx context.Context, sc tenancy.Scope, studentID, batchID string, ev audit.Event) (records.Enrollment, error) {
	if !okID(studentID) {
		return records.Enrollment{}, records.ErrNotFound
	}
	if !okID(batchID) {
		return records.Enrollment{}, records.ErrBadReference
	}
	var out records.Enrollment
	err := s.tx(ctx, sc, func(tx pgx.Tx) error {
		var studentArchived *time.Time
		err := tx.QueryRow(ctx, `SELECT archived_at FROM students WHERE institute_id = $1::uuid AND id = $2::uuid FOR SHARE`, sc.InstituteID(), studentID).Scan(&studentArchived)
		if errors.Is(err, pgx.ErrNoRows) {
			return records.ErrNotFound
		}
		if err != nil {
			return err
		}
		// FOR SHARE serializes with SetBatchArchived (FOR UPDATE).
		var batchArchived *time.Time
		err = tx.QueryRow(ctx, `SELECT archived_at FROM batches WHERE institute_id = $1::uuid AND id = $2::uuid FOR SHARE`, sc.InstituteID(), batchID).Scan(&batchArchived)
		if errors.Is(err, pgx.ErrNoRows) {
			return records.ErrBadReference
		}
		if err != nil {
			return err
		}
		if studentArchived != nil || batchArchived != nil {
			return records.ErrArchived
		}
		var id string
		if err := tx.QueryRow(ctx, `INSERT INTO enrollments (institute_id, student_id, batch_id) VALUES ($1::uuid, $2::uuid, $3::uuid) RETURNING id::text`,
			sc.InstituteID(), studentID, batchID).Scan(&id); err != nil {
			return mapErr(err)
		}
		e, err := scanEnrollment(tx.QueryRow(ctx, enrollmentSel+` WHERE e.institute_id = $1::uuid AND e.id = $2::uuid`, sc.InstituteID(), id))
		if err != nil {
			return err
		}
		out = e
		return audited(ctx, tx, sc, ev, id)
	})
	return out, err
}

func (s *Store) UpdateEnrollment(ctx context.Context, sc tenancy.Scope, studentID, enrollmentID string, p records.EnrollmentPatch, ev audit.Event) (records.Enrollment, error) {
	if !okID(studentID) || !okID(enrollmentID) {
		return records.Enrollment{}, records.ErrNotFound
	}
	var out records.Enrollment
	err := s.tx(ctx, sc, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT 1 FROM enrollments WHERE institute_id = $1::uuid AND id = $2::uuid AND student_id = $3::uuid FOR UPDATE`,
			sc.InstituteID(), enrollmentID, studentID); err != nil {
			return err
		}
		cur, err := scanEnrollment(tx.QueryRow(ctx, enrollmentSel+` WHERE e.institute_id = $1::uuid AND e.id = $2::uuid AND e.student_id = $3::uuid`,
			sc.InstituteID(), enrollmentID, studentID))
		if err != nil {
			return err
		}
		status, date, grade, err := records.ApplyEnrollmentPatch(cur, p)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE enrollments SET status = $3, completion_date = $4::date, grade = $5
			WHERE institute_id = $1::uuid AND id = $2::uuid`, sc.InstituteID(), enrollmentID, string(status), nullDate(date), grade); err != nil {
			return mapErr(err)
		}
		e, err := scanEnrollment(tx.QueryRow(ctx, enrollmentSel+` WHERE e.institute_id = $1::uuid AND e.id = $2::uuid`, sc.InstituteID(), enrollmentID))
		if err != nil {
			return err
		}
		out = e
		return audited(ctx, tx, sc, ev, enrollmentID)
	})
	return out, err
}
