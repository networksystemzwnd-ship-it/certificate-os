// Package recordstest provides an in-memory records.Store and the shared store contract.
package recordstest

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/certificate-os/api/internal/audit"
	"github.com/certificate-os/api/internal/records"
	"github.com/certificate-os/api/internal/tenancy"
)

type tenantData struct {
	courses     map[string]*records.Course
	batches     map[string]*records.Batch
	students    map[string]*records.Student
	enrollments map[string]*records.Enrollment // by enrollment id
}

func newTenantData() *tenantData {
	return &tenantData{courses: map[string]*records.Course{}, batches: map[string]*records.Batch{},
		students: map[string]*records.Student{}, enrollments: map[string]*records.Enrollment{}}
}

type MemStore struct {
	mu    sync.Mutex
	n     int
	data  map[string]*tenantData // keyed by institute id: the in-memory equivalent of RLS
	Audit audit.Writer
	Now   func() time.Time
}

var _ records.Store = (*MemStore)(nil)

func NewMemStore(w audit.Writer) *MemStore {
	return &MemStore{data: map[string]*tenantData{}, Audit: w, Now: time.Now}
}

func (m *MemStore) next(prefix string) string { m.n++; return fmt.Sprintf("%s-%d", prefix, m.n) }

func (m *MemStore) tenant(s tenancy.Scope) *tenantData {
	d := m.data[s.InstituteID()]
	if d == nil {
		d = newTenantData()
		m.data[s.InstituteID()] = d
	}
	return d
}

// record writes the audit event; callers invoke it after all guards passed and before committing state.
func (m *MemStore) record(ctx context.Context, s tenancy.Scope, ev audit.Event, entityID string) error {
	ev.EntityID, ev.InstituteID = entityID, s.InstituteID()
	if m.Audit == nil {
		return nil
	}
	return m.Audit.Record(ctx, ev)
}

func like(q string, fields ...string) bool {
	if q == "" {
		return true
	}
	q = strings.ToLower(q)
	for _, f := range fields {
		if strings.Contains(strings.ToLower(f), q) {
			return true
		}
	}
	return false
}

func archiveOK(a records.Archive, at *time.Time) bool {
	switch a {
	case records.ArchivedOnly:
		return at != nil
	case records.IncludeAll:
		return true
	}
	return at == nil
}

func window[T any](all []T, p records.Page) []T {
	p = p.Normalized()
	if p.Offset >= len(all) {
		return []T{}
	}
	all = all[p.Offset:]
	if len(all) > p.Limit+1 {
		all = all[:p.Limit+1]
	}
	return all
}

// --- courses ---

func (m *MemStore) courseNameTaken(d *tenantData, name, code, exceptID string) error {
	for _, c := range d.courses {
		if c.ID == exceptID || c.ArchivedAt != nil {
			continue
		}
		if strings.EqualFold(c.Name, name) {
			return &records.ConflictError{Field: "name"}
		}
		if code != "" && c.Code == code {
			return &records.ConflictError{Field: "code"}
		}
	}
	return nil
}

func (m *MemStore) CreateCourse(ctx context.Context, s tenancy.Scope, in records.CourseInput, ev audit.Event) (records.Course, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d := m.tenant(s)
	if err := m.courseNameTaken(d, in.Name, in.Code, ""); err != nil {
		return records.Course{}, err
	}
	now := m.Now()
	c := &records.Course{ID: m.next("course"), Name: in.Name, Code: in.Code, Description: in.Description, CreatedAt: now, UpdatedAt: now}
	if err := m.record(ctx, s, ev, c.ID); err != nil {
		return records.Course{}, err
	}
	d.courses[c.ID] = c
	return *c, nil
}

func (m *MemStore) GetCourse(_ context.Context, s tenancy.Scope, id string) (records.Course, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if c, ok := m.tenant(s).courses[id]; ok {
		return *c, nil
	}
	return records.Course{}, records.ErrNotFound
}

func (m *MemStore) ListCourses(_ context.Context, s tenancy.Scope, p records.Page) ([]records.Course, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var all []records.Course
	for _, c := range m.tenant(s).courses {
		if archiveOK(p.Archived, c.ArchivedAt) && like(p.Q, c.Name, c.Code) {
			all = append(all, *c)
		}
	}
	sort.Slice(all, func(i, j int) bool {
		if a, b := strings.ToLower(all[i].Name), strings.ToLower(all[j].Name); a != b {
			return a < b
		}
		return all[i].ID < all[j].ID
	})
	return window(all, p), nil
}

func (m *MemStore) UpdateCourse(ctx context.Context, s tenancy.Scope, id string, in records.CourseInput, ev audit.Event) (records.Course, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d := m.tenant(s)
	c, ok := d.courses[id]
	if !ok {
		return records.Course{}, records.ErrNotFound
	}
	if c.ArchivedAt != nil {
		return records.Course{}, records.ErrArchived
	}
	if err := m.courseNameTaken(d, in.Name, in.Code, id); err != nil {
		return records.Course{}, err
	}
	if err := m.record(ctx, s, ev, id); err != nil {
		return records.Course{}, err
	}
	c.Name, c.Code, c.Description, c.UpdatedAt = in.Name, in.Code, in.Description, m.Now()
	return *c, nil
}

func (m *MemStore) SetCourseArchived(ctx context.Context, s tenancy.Scope, id string, archived bool, ev audit.Event) (records.Course, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d := m.tenant(s)
	c, ok := d.courses[id]
	if !ok {
		return records.Course{}, records.ErrNotFound
	}
	if archived {
		for _, b := range d.batches {
			if b.CourseID == id && b.ArchivedAt == nil {
				return records.Course{}, records.ErrHasActiveItems
			}
		}
	} else if err := m.courseNameTaken(d, c.Name, c.Code, id); err != nil {
		return records.Course{}, err // restoring would collide with a newer active course
	}
	if err := m.record(ctx, s, ev, id); err != nil {
		return records.Course{}, err
	}
	now := m.Now()
	if archived {
		c.ArchivedAt = &now
	} else {
		c.ArchivedAt = nil
	}
	c.UpdatedAt = now
	return *c, nil
}

// --- batches ---

func (m *MemStore) batchView(d *tenantData, b *records.Batch) records.Batch {
	out := *b
	if c := d.courses[b.CourseID]; c != nil {
		out.CourseName = c.Name
	}
	return out
}

func batchNameTaken(d *tenantData, courseID, name, exceptID string) error {
	for _, b := range d.batches {
		if b.ID != exceptID && b.CourseID == courseID && b.ArchivedAt == nil && strings.EqualFold(b.Name, name) {
			return &records.ConflictError{Field: "name"}
		}
	}
	return nil
}

func (m *MemStore) CreateBatch(ctx context.Context, s tenancy.Scope, in records.BatchInput, ev audit.Event) (records.Batch, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d := m.tenant(s)
	c, ok := d.courses[in.CourseID]
	if !ok {
		return records.Batch{}, records.ErrBadReference
	}
	if c.ArchivedAt != nil {
		return records.Batch{}, records.ErrArchived
	}
	if err := batchNameTaken(d, in.CourseID, in.Name, ""); err != nil {
		return records.Batch{}, err
	}
	now := m.Now()
	b := &records.Batch{ID: m.next("batch"), CourseID: in.CourseID, Name: in.Name, StartDate: in.StartDate, EndDate: in.EndDate, CreatedAt: now, UpdatedAt: now}
	if err := m.record(ctx, s, ev, b.ID); err != nil {
		return records.Batch{}, err
	}
	d.batches[b.ID] = b
	return m.batchView(d, b), nil
}

func (m *MemStore) GetBatch(_ context.Context, s tenancy.Scope, id string) (records.Batch, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d := m.tenant(s)
	if b, ok := d.batches[id]; ok {
		return m.batchView(d, b), nil
	}
	return records.Batch{}, records.ErrNotFound
}

func (m *MemStore) ListBatches(_ context.Context, s tenancy.Scope, f records.BatchFilter) ([]records.Batch, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d := m.tenant(s)
	var all []records.Batch
	for _, b := range d.batches {
		v := m.batchView(d, b)
		if (f.CourseID == "" || b.CourseID == f.CourseID) && archiveOK(f.Archived, b.ArchivedAt) && like(f.Q, b.Name, v.CourseName) {
			all = append(all, v)
		}
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].StartDate != all[j].StartDate {
			return all[i].StartDate > all[j].StartDate
		}
		return all[i].ID < all[j].ID
	})
	return window(all, f.Page), nil
}

func (m *MemStore) UpdateBatch(ctx context.Context, s tenancy.Scope, id string, in records.BatchInput, ev audit.Event) (records.Batch, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d := m.tenant(s)
	b, ok := d.batches[id]
	if !ok {
		return records.Batch{}, records.ErrNotFound
	}
	if b.ArchivedAt != nil {
		return records.Batch{}, records.ErrArchived
	}
	if err := batchNameTaken(d, b.CourseID, in.Name, id); err != nil {
		return records.Batch{}, err
	}
	if err := m.record(ctx, s, ev, id); err != nil {
		return records.Batch{}, err
	}
	b.Name, b.StartDate, b.EndDate, b.UpdatedAt = in.Name, in.StartDate, in.EndDate, m.Now()
	return m.batchView(d, b), nil
}

func (m *MemStore) SetBatchArchived(ctx context.Context, s tenancy.Scope, id string, archived bool, ev audit.Event) (records.Batch, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d := m.tenant(s)
	b, ok := d.batches[id]
	if !ok {
		return records.Batch{}, records.ErrNotFound
	}
	if archived {
		for _, e := range d.enrollments {
			if e.BatchID == id && e.Status == records.Enrolled {
				return records.Batch{}, records.ErrHasActiveItems
			}
		}
	} else {
		if c := d.courses[b.CourseID]; c != nil && c.ArchivedAt != nil {
			return records.Batch{}, records.ErrArchived // restore the course first
		}
		if err := batchNameTaken(d, b.CourseID, b.Name, id); err != nil {
			return records.Batch{}, err
		}
	}
	if err := m.record(ctx, s, ev, id); err != nil {
		return records.Batch{}, err
	}
	now := m.Now()
	if archived {
		b.ArchivedAt = &now
	} else {
		b.ArchivedAt = nil
	}
	b.UpdatedAt = now
	return m.batchView(d, b), nil
}

// --- students ---

func studentNumberTaken(d *tenantData, number, exceptID string) error {
	if number == "" {
		return nil
	}
	for _, st := range d.students {
		if st.ID != exceptID && st.StudentNumber == number { // archived students keep their number reserved
			return &records.ConflictError{Field: "student_number"}
		}
	}
	return nil
}

func (m *MemStore) CreateStudent(ctx context.Context, s tenancy.Scope, in records.StudentInput, ev audit.Event) (records.Student, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d := m.tenant(s)
	if err := studentNumberTaken(d, in.StudentNumber, ""); err != nil {
		return records.Student{}, err
	}
	now := m.Now()
	st := &records.Student{ID: m.next("student"), StudentNumber: in.StudentNumber, FullName: in.FullName, Email: in.Email, Phone: in.Phone, CreatedAt: now, UpdatedAt: now}
	if err := m.record(ctx, s, ev, st.ID); err != nil {
		return records.Student{}, err
	}
	d.students[st.ID] = st
	return *st, nil
}

func (m *MemStore) enrollmentView(d *tenantData, e *records.Enrollment) records.Enrollment {
	out := *e
	if b := d.batches[e.BatchID]; b != nil {
		out.BatchName, out.CourseID = b.Name, b.CourseID
		if c := d.courses[b.CourseID]; c != nil {
			out.CourseName = c.Name
		}
	}
	return out
}

func (m *MemStore) GetStudent(_ context.Context, s tenancy.Scope, id string) (records.Student, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d := m.tenant(s)
	st, ok := d.students[id]
	if !ok {
		return records.Student{}, records.ErrNotFound
	}
	out := *st
	out.Enrollments = []records.Enrollment{}
	for _, e := range d.enrollments {
		if e.StudentID == id {
			out.Enrollments = append(out.Enrollments, m.enrollmentView(d, e))
		}
	}
	sort.Slice(out.Enrollments, func(i, j int) bool { return out.Enrollments[i].ID < out.Enrollments[j].ID })
	return out, nil
}

func (m *MemStore) ListStudents(_ context.Context, s tenancy.Scope, f records.StudentFilter) ([]records.Student, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d := m.tenant(s)
	matches := func(st *records.Student) bool {
		if f.BatchID == "" && f.CourseID == "" && f.Status == "" {
			return true
		}
		for _, e := range d.enrollments {
			if e.StudentID != st.ID {
				continue
			}
			v := m.enrollmentView(d, e)
			if (f.BatchID == "" || e.BatchID == f.BatchID) && (f.CourseID == "" || v.CourseID == f.CourseID) && (f.Status == "" || e.Status == f.Status) {
				return true
			}
		}
		return false
	}
	var all []records.Student
	for _, st := range d.students {
		if archiveOK(f.Archived, st.ArchivedAt) && like(f.Q, st.FullName, st.Email, st.StudentNumber) && matches(st) {
			all = append(all, *st)
		}
	}
	sort.Slice(all, func(i, j int) bool {
		if a, b := strings.ToLower(all[i].FullName), strings.ToLower(all[j].FullName); a != b {
			return a < b
		}
		return all[i].ID < all[j].ID
	})
	return window(all, f.Page), nil
}

func (m *MemStore) LookupStudents(_ context.Context, s tenancy.Scope, numbers, emails []string) ([]records.Student, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d := m.tenant(s)
	nums, mails := map[string]bool{}, map[string]bool{}
	for _, n := range numbers {
		nums[strings.ToLower(n)] = true
	}
	for _, e := range emails {
		mails[strings.ToLower(e)] = true
	}
	out := []records.Student{}
	for _, st := range d.students {
		if (st.StudentNumber != "" && nums[strings.ToLower(st.StudentNumber)]) || (st.Email != "" && mails[strings.ToLower(st.Email)]) {
			c := *st
			c.Enrollments = nil
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (m *MemStore) UpdateStudent(ctx context.Context, s tenancy.Scope, id string, in records.StudentInput, ev audit.Event) (records.Student, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d := m.tenant(s)
	st, ok := d.students[id]
	if !ok {
		return records.Student{}, records.ErrNotFound
	}
	if st.ArchivedAt != nil {
		return records.Student{}, records.ErrArchived
	}
	if err := studentNumberTaken(d, in.StudentNumber, id); err != nil {
		return records.Student{}, err
	}
	if err := m.record(ctx, s, ev, id); err != nil {
		return records.Student{}, err
	}
	st.StudentNumber, st.FullName, st.Email, st.Phone, st.UpdatedAt = in.StudentNumber, in.FullName, in.Email, in.Phone, m.Now()
	return *st, nil
}

func (m *MemStore) SetStudentArchived(ctx context.Context, s tenancy.Scope, id string, archived bool, ev audit.Event) (records.Student, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	st, ok := m.tenant(s).students[id]
	if !ok {
		return records.Student{}, records.ErrNotFound
	}
	if err := m.record(ctx, s, ev, id); err != nil {
		return records.Student{}, err
	}
	now := m.Now()
	if archived {
		st.ArchivedAt = &now
	} else {
		st.ArchivedAt = nil
	}
	st.UpdatedAt = now
	return *st, nil
}

// --- enrollments ---

func (m *MemStore) AddEnrollment(ctx context.Context, s tenancy.Scope, studentID, batchID string, ev audit.Event) (records.Enrollment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d := m.tenant(s)
	st, ok := d.students[studentID]
	if !ok {
		return records.Enrollment{}, records.ErrNotFound
	}
	b, ok := d.batches[batchID]
	if !ok {
		return records.Enrollment{}, records.ErrBadReference
	}
	if st.ArchivedAt != nil || b.ArchivedAt != nil {
		return records.Enrollment{}, records.ErrArchived
	}
	for _, e := range d.enrollments {
		if e.StudentID == studentID && e.BatchID == batchID {
			return records.Enrollment{}, &records.ConflictError{Field: "batch_id"}
		}
	}
	now := m.Now()
	e := &records.Enrollment{ID: m.next("enr"), StudentID: studentID, BatchID: batchID, Status: records.Enrolled, CreatedAt: now, UpdatedAt: now}
	if err := m.record(ctx, s, ev, e.ID); err != nil {
		return records.Enrollment{}, err
	}
	d.enrollments[e.ID] = e
	return m.enrollmentView(d, e), nil
}

func (m *MemStore) UpdateEnrollment(ctx context.Context, s tenancy.Scope, studentID, enrollmentID string, p records.EnrollmentPatch, ev audit.Event) (records.Enrollment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d := m.tenant(s)
	e, ok := d.enrollments[enrollmentID]
	if !ok || e.StudentID != studentID {
		return records.Enrollment{}, records.ErrNotFound
	}
	status, date, grade, err := records.ApplyEnrollmentPatch(*e, p)
	if err != nil {
		return records.Enrollment{}, err
	}
	if err := m.record(ctx, s, ev, enrollmentID); err != nil {
		return records.Enrollment{}, err
	}
	e.Status, e.CompletionDate, e.Grade, e.UpdatedAt = status, date, grade, m.Now()
	return m.enrollmentView(d, e), nil
}

func (m *MemStore) EnrollmentsOf(_ context.Context, s tenancy.Scope, studentIDs []string) ([]records.Enrollment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d := m.tenant(s)
	want := map[string]bool{}
	for _, id := range studentIDs {
		want[id] = true
	}
	out := []records.Enrollment{}
	for _, e := range d.enrollments {
		if want[e.StudentID] {
			out = append(out, m.enrollmentView(d, e))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}
