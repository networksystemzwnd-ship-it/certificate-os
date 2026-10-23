package records

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/certificate-os/api/internal/auth"
	"github.com/certificate-os/api/internal/platform/httpx"
	"github.com/certificate-os/api/internal/tenancy"
)

// Handler serves courses, batches, students and enrollments. Every route goes through
// tenancy.Handler.Guard, which authenticates, resolves the tenant from membership and checks the
// permission (suspended institutes lose write permissions automatically). The institute id is never
// read from the path, query or body.
type Handler struct {
	Svc     *Service
	Log     *slog.Logger
	Tenancy *tenancy.Handler
}

func (h *Handler) Register(mux *http.ServeMux) {
	g := h.Tenancy.Guard
	r, w := auth.PermCoursesRead, auth.PermCoursesWrite
	sr, sw := auth.PermStudentsRead, auth.PermStudentsWrite
	mux.Handle("GET /api/v1/courses", g(r, http.HandlerFunc(h.listCourses)))
	mux.Handle("POST /api/v1/courses", g(w, http.HandlerFunc(h.createCourse)))
	mux.Handle("GET /api/v1/courses/{id}", g(r, http.HandlerFunc(h.getCourse)))
	mux.Handle("PATCH /api/v1/courses/{id}", g(w, http.HandlerFunc(h.updateCourse)))
	mux.Handle("DELETE /api/v1/courses/{id}", g(w, h.archive(h.archiveCourse, true)))
	mux.Handle("POST /api/v1/courses/{id}/restore", g(w, h.archive(h.archiveCourse, false)))

	mux.Handle("GET /api/v1/batches", g(r, http.HandlerFunc(h.listBatches)))
	mux.Handle("POST /api/v1/batches", g(w, http.HandlerFunc(h.createBatch)))
	mux.Handle("GET /api/v1/batches/{id}", g(r, http.HandlerFunc(h.getBatch)))
	mux.Handle("PATCH /api/v1/batches/{id}", g(w, http.HandlerFunc(h.updateBatch)))
	mux.Handle("DELETE /api/v1/batches/{id}", g(w, h.archive(h.archiveBatch, true)))
	mux.Handle("POST /api/v1/batches/{id}/restore", g(w, h.archive(h.archiveBatch, false)))

	mux.Handle("GET /api/v1/students", g(sr, http.HandlerFunc(h.listStudents)))
	mux.Handle("POST /api/v1/students", g(sw, http.HandlerFunc(h.createStudent)))
	mux.Handle("GET /api/v1/students/{id}", g(sr, http.HandlerFunc(h.getStudent)))
	mux.Handle("PATCH /api/v1/students/{id}", g(sw, http.HandlerFunc(h.updateStudent)))
	mux.Handle("DELETE /api/v1/students/{id}", g(sw, h.archive(h.archiveStudent, true)))
	mux.Handle("POST /api/v1/students/{id}/restore", g(sw, h.archive(h.archiveStudent, false)))
	mux.Handle("POST /api/v1/students/{id}/enrollments", g(sw, http.HandlerFunc(h.enroll)))
	mux.Handle("PATCH /api/v1/students/{id}/enrollments/{eid}", g(sw, http.HandlerFunc(h.updateEnrollment)))
}

// --- DTOs ---

type courseDTO struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Code        string     `json:"code"`
	Description string     `json:"description"`
	ArchivedAt  *time.Time `json:"archived_at"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

func toCourse(c Course) courseDTO {
	return courseDTO{c.ID, c.Name, c.Code, c.Description, c.ArchivedAt, c.CreatedAt, c.UpdatedAt}
}

type batchDTO struct {
	ID         string     `json:"id"`
	CourseID   string     `json:"course_id"`
	CourseName string     `json:"course_name"`
	Name       string     `json:"name"`
	StartDate  string     `json:"start_date"`
	EndDate    string     `json:"end_date"`
	ArchivedAt *time.Time `json:"archived_at"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
}

func toBatch(b Batch) batchDTO {
	return batchDTO{b.ID, b.CourseID, b.CourseName, b.Name, b.StartDate, b.EndDate, b.ArchivedAt, b.CreatedAt, b.UpdatedAt}
}

type enrollmentDTO struct {
	ID             string    `json:"id"`
	StudentID      string    `json:"student_id"`
	BatchID        string    `json:"batch_id"`
	BatchName      string    `json:"batch_name"`
	CourseID       string    `json:"course_id"`
	CourseName     string    `json:"course_name"`
	Status         string    `json:"status"`
	CompletionDate string    `json:"completion_date"`
	Grade          string    `json:"grade"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

func toEnrollment(e Enrollment) enrollmentDTO {
	return enrollmentDTO{e.ID, e.StudentID, e.BatchID, e.BatchName, e.CourseID, e.CourseName, string(e.Status),
		e.CompletionDate, e.Grade, e.CreatedAt, e.UpdatedAt}
}

type studentDTO struct {
	ID            string          `json:"id"`
	StudentNumber string          `json:"student_number"`
	FullName      string          `json:"full_name"`
	Email         string          `json:"email"`
	Phone         string          `json:"phone"`
	ArchivedAt    *time.Time      `json:"archived_at"`
	CreatedAt     time.Time       `json:"created_at"`
	UpdatedAt     time.Time       `json:"updated_at"`
	Enrollments   []enrollmentDTO `json:"enrollments,omitempty"`
}

func toStudent(s Student) studentDTO {
	d := studentDTO{ID: s.ID, StudentNumber: s.StudentNumber, FullName: s.FullName, Email: s.Email, Phone: s.Phone,
		ArchivedAt: s.ArchivedAt, CreatedAt: s.CreatedAt, UpdatedAt: s.UpdatedAt}
	for _, e := range s.Enrollments {
		d.Enrollments = append(d.Enrollments, toEnrollment(e))
	}
	return d
}

// --- helpers ---

// fail maps domain errors to HTTP. refField names the body field a bad reference belongs to.
func (h *Handler) fail(w http.ResponseWriter, r *http.Request, err error, refField string) {
	var ve *ValidationError
	var ce *ConflictError
	switch {
	case errors.As(err, &ve):
		httpx.WriteError(w, r, http.StatusUnprocessableEntity, httpx.CodeValidation, "Please correct the highlighted fields.", ve.Problems)
	case errors.As(err, &ce):
		httpx.WriteError(w, r, http.StatusConflict, httpx.CodeConflict, "That value is already in use.",
			map[string]string{ce.Field: "That value is already in use."})
	case errors.Is(err, ErrNotFound):
		httpx.NotFound(w, r)
	case errors.Is(err, ErrBadReference):
		if refField == "" {
			refField = "reference"
		}
		httpx.WriteError(w, r, http.StatusUnprocessableEntity, httpx.CodeValidation, "Please correct the highlighted fields.",
			map[string]string{refField: "That record does not exist."})
	case errors.Is(err, ErrArchived):
		httpx.WriteError(w, r, http.StatusConflict, httpx.CodeConflict, "That record is archived. Restore it first.", nil)
	case errors.Is(err, ErrHasActiveItems):
		httpx.WriteError(w, r, http.StatusConflict, httpx.CodeConflict, "That record still has active items. Archive or complete them first.", nil)
	default:
		h.Log.Error("records request failed", "request_id", httpx.RequestID(r.Context()), "err", err)
		httpx.WriteError(w, r, http.StatusInternalServerError, httpx.CodeInternal, "An unexpected error occurred.", nil)
	}
}

func (h *Handler) decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	if err := httpx.DecodeJSON(w, r, dst); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, httpx.CodeBadRequest, err.Error(), nil)
		return false
	}
	return true
}

func tenant(r *http.Request) tenancy.Tenant { t, _ := tenancy.TenantFrom(r.Context()); return t }
func rid(r *http.Request) string            { return httpx.RequestID(r.Context()) }

func bad(w http.ResponseWriter, r *http.Request, field, msg string) {
	httpx.WriteError(w, r, http.StatusUnprocessableEntity, httpx.CodeValidation, "Please correct the highlighted fields.", map[string]string{field: msg})
}

// page parses q, archived, limit and offset.
func page(w http.ResponseWriter, r *http.Request) (Page, bool) {
	q := r.URL.Query()
	p := Page{Q: q.Get("q"), Archived: Archive(q.Get("archived"))}
	if !p.Archived.Valid() {
		bad(w, r, "archived", "Use only or all.")
		return p, false
	}
	var err error
	if v := q.Get("limit"); v != "" {
		if p.Limit, err = strconv.Atoi(v); err != nil || p.Limit < 1 {
			bad(w, r, "limit", "Use a positive number.")
			return p, false
		}
	}
	if v := q.Get("offset"); v != "" {
		if p.Offset, err = strconv.Atoi(v); err != nil || p.Offset < 0 {
			bad(w, r, "offset", "Use zero or a positive number.")
			return p, false
		}
	}
	return p.Normalized(), true
}

// trim cuts the Limit+1 probe row and reports whether more exist.
func trim[T any](items []T, limit int) ([]T, bool) {
	if len(items) > limit {
		return items[:limit], true
	}
	return items, false
}

func (h *Handler) archive(fn func(w http.ResponseWriter, r *http.Request, archived bool), archived bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fn(w, r, archived) })
}

// --- courses ---

type courseBody struct {
	Name        string `json:"name"`
	Code        string `json:"code"`
	Description string `json:"description"`
}

func (h *Handler) listCourses(w http.ResponseWriter, r *http.Request) {
	p, ok := page(w, r)
	if !ok {
		return
	}
	cs, err := h.Svc.ListCourses(r.Context(), tenant(r), p)
	if err != nil {
		h.fail(w, r, err, "")
		return
	}
	cs, more := trim(cs, p.Limit)
	out := make([]courseDTO, 0, len(cs))
	for _, c := range cs {
		out = append(out, toCourse(c))
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"courses": out, "has_more": more, "limit": p.Limit, "offset": p.Offset})
}

func (h *Handler) createCourse(w http.ResponseWriter, r *http.Request) {
	var b courseBody
	if !h.decode(w, r, &b) {
		return
	}
	c, err := h.Svc.CreateCourse(r.Context(), tenant(r), CourseInput(b), rid(r))
	if err != nil {
		h.fail(w, r, err, "")
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{"course": toCourse(c)})
}

func (h *Handler) getCourse(w http.ResponseWriter, r *http.Request) {
	c, err := h.Svc.GetCourse(r.Context(), tenant(r), r.PathValue("id"))
	if err != nil {
		h.fail(w, r, err, "")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"course": toCourse(c)})
}

func (h *Handler) updateCourse(w http.ResponseWriter, r *http.Request) {
	var b courseBody
	if !h.decode(w, r, &b) {
		return
	}
	c, err := h.Svc.UpdateCourse(r.Context(), tenant(r), r.PathValue("id"), CourseInput(b), rid(r))
	if err != nil {
		h.fail(w, r, err, "")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"course": toCourse(c)})
}

func (h *Handler) archiveCourse(w http.ResponseWriter, r *http.Request, archived bool) {
	c, err := h.Svc.ArchiveCourse(r.Context(), tenant(r), r.PathValue("id"), archived, rid(r))
	if err != nil {
		h.fail(w, r, err, "")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"course": toCourse(c)})
}

// --- batches ---

type batchBody struct {
	CourseID  string `json:"course_id"`
	Name      string `json:"name"`
	StartDate string `json:"start_date"`
	EndDate   string `json:"end_date"`
}

func (h *Handler) listBatches(w http.ResponseWriter, r *http.Request) {
	p, ok := page(w, r)
	if !ok {
		return
	}
	bs, err := h.Svc.ListBatches(r.Context(), tenant(r), BatchFilter{Page: p, CourseID: r.URL.Query().Get("course_id")})
	if err != nil {
		h.fail(w, r, err, "")
		return
	}
	bs, more := trim(bs, p.Limit)
	out := make([]batchDTO, 0, len(bs))
	for _, b := range bs {
		out = append(out, toBatch(b))
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"batches": out, "has_more": more, "limit": p.Limit, "offset": p.Offset})
}

func (h *Handler) createBatch(w http.ResponseWriter, r *http.Request) {
	var b batchBody
	if !h.decode(w, r, &b) {
		return
	}
	bt, err := h.Svc.CreateBatch(r.Context(), tenant(r), BatchInput(b), rid(r))
	if err != nil {
		h.fail(w, r, err, "course_id")
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{"batch": toBatch(bt)})
}

func (h *Handler) getBatch(w http.ResponseWriter, r *http.Request) {
	b, err := h.Svc.GetBatch(r.Context(), tenant(r), r.PathValue("id"))
	if err != nil {
		h.fail(w, r, err, "")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"batch": toBatch(b)})
}

func (h *Handler) updateBatch(w http.ResponseWriter, r *http.Request) {
	var b batchBody
	if !h.decode(w, r, &b) {
		return
	}
	bt, err := h.Svc.UpdateBatch(r.Context(), tenant(r), r.PathValue("id"), BatchInput(b), rid(r))
	if err != nil {
		h.fail(w, r, err, "")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"batch": toBatch(bt)})
}

func (h *Handler) archiveBatch(w http.ResponseWriter, r *http.Request, archived bool) {
	b, err := h.Svc.ArchiveBatch(r.Context(), tenant(r), r.PathValue("id"), archived, rid(r))
	if err != nil {
		h.fail(w, r, err, "")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"batch": toBatch(b)})
}

// --- students ---

type studentBody struct {
	StudentNumber string `json:"student_number"`
	FullName      string `json:"full_name"`
	Email         string `json:"email"`
	Phone         string `json:"phone"`
}

func (h *Handler) listStudents(w http.ResponseWriter, r *http.Request) {
	p, ok := page(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	f := StudentFilter{Page: p, BatchID: q.Get("batch_id"), CourseID: q.Get("course_id"), Status: EnrollmentStatus(q.Get("status"))}
	if f.Status != "" && !f.Status.Valid() {
		bad(w, r, "status", "Choose enrolled, completed or withdrawn.")
		return
	}
	ss, err := h.Svc.ListStudents(r.Context(), tenant(r), f)
	if err != nil {
		h.fail(w, r, err, "")
		return
	}
	ss, more := trim(ss, p.Limit)
	out := make([]studentDTO, 0, len(ss))
	for _, s := range ss {
		out = append(out, toStudent(s))
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"students": out, "has_more": more, "limit": p.Limit, "offset": p.Offset})
}

func (h *Handler) createStudent(w http.ResponseWriter, r *http.Request) {
	var b studentBody
	if !h.decode(w, r, &b) {
		return
	}
	s, err := h.Svc.CreateStudent(r.Context(), tenant(r), StudentInput(b), rid(r))
	if err != nil {
		h.fail(w, r, err, "")
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{"student": toStudent(s)})
}

func (h *Handler) getStudent(w http.ResponseWriter, r *http.Request) {
	s, err := h.Svc.GetStudent(r.Context(), tenant(r), r.PathValue("id"))
	if err != nil {
		h.fail(w, r, err, "")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"student": toStudent(s)})
}

func (h *Handler) updateStudent(w http.ResponseWriter, r *http.Request) {
	var b studentBody
	if !h.decode(w, r, &b) {
		return
	}
	s, err := h.Svc.UpdateStudent(r.Context(), tenant(r), r.PathValue("id"), StudentInput(b), rid(r))
	if err != nil {
		h.fail(w, r, err, "")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"student": toStudent(s)})
}

func (h *Handler) archiveStudent(w http.ResponseWriter, r *http.Request, archived bool) {
	s, err := h.Svc.ArchiveStudent(r.Context(), tenant(r), r.PathValue("id"), archived, rid(r))
	if err != nil {
		h.fail(w, r, err, "")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"student": toStudent(s)})
}

// --- enrollments ---

func (h *Handler) enroll(w http.ResponseWriter, r *http.Request) {
	var b struct {
		BatchID string `json:"batch_id"`
	}
	if !h.decode(w, r, &b) {
		return
	}
	e, err := h.Svc.Enroll(r.Context(), tenant(r), r.PathValue("id"), b.BatchID, rid(r))
	if err != nil {
		h.fail(w, r, err, "batch_id")
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{"enrollment": toEnrollment(e)})
}

func (h *Handler) updateEnrollment(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Status         *string `json:"status"`
		CompletionDate *string `json:"completion_date"`
		Grade          *string `json:"grade"`
	}
	if !h.decode(w, r, &b) {
		return
	}
	p := EnrollmentPatch{CompletionDate: b.CompletionDate, Grade: b.Grade}
	if b.Status != nil {
		st := EnrollmentStatus(*b.Status)
		if !st.Valid() {
			bad(w, r, "status", "Choose enrolled, completed or withdrawn.")
			return
		}
		p.Status = &st
	}
	e, err := h.Svc.UpdateEnrollment(r.Context(), tenant(r), r.PathValue("id"), r.PathValue("eid"), p, rid(r))
	if err != nil {
		h.fail(w, r, err, "")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"enrollment": toEnrollment(e)})
}
