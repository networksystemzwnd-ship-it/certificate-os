package records

import (
	"context"
	"strings"

	"github.com/certificate-os/api/internal/audit"
	"github.com/certificate-os/api/internal/tenancy"
)

// Service holds the business rules. Callers pass the resolved tenancy.Tenant, so the scope and the
// acting user always come from authenticated membership. Permission checks happen in the HTTP layer.
type Service struct{ Store Store }

func NewService(s Store) *Service { return &Service{Store: s} }

func event(t tenancy.Tenant, action, entityType, requestID string, meta map[string]any) audit.Event {
	return audit.Event{ActorType: audit.ActorUser, ActorUserID: t.UserID, Action: action, EntityType: entityType,
		RequestID: requestID, Metadata: meta}
}

// diff collects the NAMES (never values) of fields that changed: audit metadata must not hold personal data.
type diff struct{ fields []string }

func (d *diff) add(name, a, b string) {
	if a != b {
		d.fields = append(d.fields, name)
	}
}

func (d *diff) meta() map[string]any {
	f := d.fields
	if f == nil {
		f = []string{}
	}
	return map[string]any{"changed_fields": f}
}

// --- courses ---

func (s *Service) CreateCourse(ctx context.Context, t tenancy.Tenant, in CourseInput, reqID string) (Course, error) {
	in, err := NormalizeCourse(in)
	if err != nil {
		return Course{}, err
	}
	return s.Store.CreateCourse(ctx, t.Scope(), in, event(t, "course.created", "course", reqID, nil))
}

func (s *Service) GetCourse(ctx context.Context, t tenancy.Tenant, id string) (Course, error) {
	return s.Store.GetCourse(ctx, t.Scope(), id)
}

func (s *Service) ListCourses(ctx context.Context, t tenancy.Tenant, p Page) ([]Course, error) {
	p.Q = strings.TrimSpace(p.Q)
	return s.Store.ListCourses(ctx, t.Scope(), p.Normalized())
}

func (s *Service) UpdateCourse(ctx context.Context, t tenancy.Tenant, id string, in CourseInput, reqID string) (Course, error) {
	in, err := NormalizeCourse(in)
	if err != nil {
		return Course{}, err
	}
	cur, err := s.Store.GetCourse(ctx, t.Scope(), id)
	if err != nil {
		return Course{}, err
	}
	var d diff
	d.add("name", cur.Name, in.Name)
	d.add("code", cur.Code, in.Code)
	d.add("description", cur.Description, in.Description)
	return s.Store.UpdateCourse(ctx, t.Scope(), id, in, event(t, "course.updated", "course", reqID, d.meta()))
}

func (s *Service) ArchiveCourse(ctx context.Context, t tenancy.Tenant, id string, archived bool, reqID string) (Course, error) {
	action := "course.archived"
	if !archived {
		action = "course.restored"
	}
	return s.Store.SetCourseArchived(ctx, t.Scope(), id, archived, event(t, action, "course", reqID, nil))
}

// --- batches ---

func (s *Service) CreateBatch(ctx context.Context, t tenancy.Tenant, in BatchInput, reqID string) (Batch, error) {
	in, err := NormalizeBatch(in)
	if err != nil {
		return Batch{}, err
	}
	if strings.TrimSpace(in.CourseID) == "" {
		return Batch{}, &ValidationError{Problems: map[string]string{"course_id": "Choose a course."}}
	}
	return s.Store.CreateBatch(ctx, t.Scope(), in, event(t, "batch.created", "batch", reqID, map[string]any{"course_id": in.CourseID}))
}

func (s *Service) GetBatch(ctx context.Context, t tenancy.Tenant, id string) (Batch, error) {
	return s.Store.GetBatch(ctx, t.Scope(), id)
}

func (s *Service) ListBatches(ctx context.Context, t tenancy.Tenant, f BatchFilter) ([]Batch, error) {
	f.Q = strings.TrimSpace(f.Q)
	f.Page = f.Page.Normalized()
	return s.Store.ListBatches(ctx, t.Scope(), f)
}

func (s *Service) UpdateBatch(ctx context.Context, t tenancy.Tenant, id string, in BatchInput, reqID string) (Batch, error) {
	in, err := NormalizeBatch(in)
	if err != nil {
		return Batch{}, err
	}
	cur, err := s.Store.GetBatch(ctx, t.Scope(), id)
	if err != nil {
		return Batch{}, err
	}
	var d diff
	d.add("name", cur.Name, in.Name)
	d.add("start_date", cur.StartDate, in.StartDate)
	d.add("end_date", cur.EndDate, in.EndDate)
	return s.Store.UpdateBatch(ctx, t.Scope(), id, in, event(t, "batch.updated", "batch", reqID, d.meta()))
}

func (s *Service) ArchiveBatch(ctx context.Context, t tenancy.Tenant, id string, archived bool, reqID string) (Batch, error) {
	action := "batch.archived"
	if !archived {
		action = "batch.restored"
	}
	return s.Store.SetBatchArchived(ctx, t.Scope(), id, archived, event(t, action, "batch", reqID, nil))
}

// --- students ---

func (s *Service) CreateStudent(ctx context.Context, t tenancy.Tenant, in StudentInput, reqID string) (Student, error) {
	in, err := NormalizeStudent(in)
	if err != nil {
		return Student{}, err
	}
	return s.Store.CreateStudent(ctx, t.Scope(), in, event(t, "student.created", "student", reqID, nil))
}

func (s *Service) GetStudent(ctx context.Context, t tenancy.Tenant, id string) (Student, error) {
	return s.Store.GetStudent(ctx, t.Scope(), id)
}

func (s *Service) ListStudents(ctx context.Context, t tenancy.Tenant, f StudentFilter) ([]Student, error) {
	f.Q = strings.TrimSpace(f.Q)
	f.Page = f.Page.Normalized()
	return s.Store.ListStudents(ctx, t.Scope(), f)
}

func (s *Service) UpdateStudent(ctx context.Context, t tenancy.Tenant, id string, in StudentInput, reqID string) (Student, error) {
	in, err := NormalizeStudent(in)
	if err != nil {
		return Student{}, err
	}
	cur, err := s.Store.GetStudent(ctx, t.Scope(), id)
	if err != nil {
		return Student{}, err
	}
	var d diff
	d.add("student_number", cur.StudentNumber, in.StudentNumber)
	d.add("full_name", cur.FullName, in.FullName)
	d.add("email", cur.Email, in.Email)
	d.add("phone", cur.Phone, in.Phone)
	return s.Store.UpdateStudent(ctx, t.Scope(), id, in, event(t, "student.updated", "student", reqID, d.meta()))
}

func (s *Service) ArchiveStudent(ctx context.Context, t tenancy.Tenant, id string, archived bool, reqID string) (Student, error) {
	action := "student.archived"
	if !archived {
		action = "student.restored"
	}
	return s.Store.SetStudentArchived(ctx, t.Scope(), id, archived, event(t, action, "student", reqID, nil))
}

// LookupStudents finds existing students by student number or email, in chunks.
func (s *Service) LookupStudents(ctx context.Context, t tenancy.Tenant, numbers, emails []string) ([]Student, error) {
	var out []Student
	seen := map[string]bool{}
	for len(numbers) > 0 || len(emails) > 0 {
		n, e := numbers, emails
		if len(n) > MaxLookupValues {
			n = n[:MaxLookupValues]
		}
		if room := MaxLookupValues - len(n); len(e) > room {
			e = e[:room]
		}
		numbers, emails = numbers[len(n):], emails[len(e):]
		got, err := s.Store.LookupStudents(ctx, t.Scope(), n, e)
		if err != nil {
			return nil, err
		}
		for _, st := range got {
			if !seen[st.ID] {
				seen[st.ID] = true
				out = append(out, st)
			}
		}
	}
	return out, nil
}

// --- enrollments ---

func (s *Service) Enroll(ctx context.Context, t tenancy.Tenant, studentID, batchID, reqID string) (Enrollment, error) {
	if strings.TrimSpace(batchID) == "" {
		return Enrollment{}, &ValidationError{Problems: map[string]string{"batch_id": "Choose a batch."}}
	}
	return s.Store.AddEnrollment(ctx, t.Scope(), studentID, batchID,
		event(t, "enrollment.created", "enrollment", reqID, map[string]any{"student_id": studentID, "batch_id": batchID}))
}

func (s *Service) UpdateEnrollment(ctx context.Context, t tenancy.Tenant, studentID, enrollmentID string, p EnrollmentPatch, reqID string) (Enrollment, error) {
	if p.Grade != nil {
		g := strings.TrimSpace(*p.Grade)
		p.Grade = &g
	}
	if p.CompletionDate != nil {
		c := strings.TrimSpace(*p.CompletionDate)
		p.CompletionDate = &c
	}
	var fields []string
	if p.Status != nil {
		fields = append(fields, "status")
	}
	if p.CompletionDate != nil {
		fields = append(fields, "completion_date")
	}
	if p.Grade != nil {
		fields = append(fields, "grade")
	}
	meta := map[string]any{"student_id": studentID, "changed_fields": append([]string{}, fields...)}
	return s.Store.UpdateEnrollment(ctx, t.Scope(), studentID, enrollmentID, p, event(t, "enrollment.updated", "enrollment", reqID, meta))
}
