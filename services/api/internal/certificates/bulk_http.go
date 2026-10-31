package certificates

import (
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/certificate-os/api/internal/auth"
	"github.com/certificate-os/api/internal/platform/httpx"
)

func (h *Handler) registerBulk(mux *http.ServeMux) {
	g := h.Tenancy.Guard
	mux.Handle("GET /api/v1/certificates/bulk-preview", g(auth.PermCertsIssue, http.HandlerFunc(h.bulkPreview)))
	mux.Handle("POST /api/v1/certificates/bulk", g(auth.PermCertsIssue, http.HandlerFunc(h.bulkStart)))
	mux.Handle("GET /api/v1/bulk-jobs", g(auth.PermCertsRead, http.HandlerFunc(h.jobList)))
	mux.Handle("GET /api/v1/bulk-jobs/{id}", g(auth.PermCertsRead, http.HandlerFunc(h.jobGet)))
	mux.Handle("GET /api/v1/bulk-jobs/{id}/items", g(auth.PermCertsRead, http.HandlerFunc(h.jobItems)))
	mux.Handle("POST /api/v1/bulk-jobs/{id}/retry-failed", g(auth.PermCertsIssue, http.HandlerFunc(h.jobRetry)))
	mux.Handle("POST /api/v1/bulk-jobs/{id}/resume", g(auth.PermCertsIssue, http.HandlerFunc(h.jobResume)))
	mux.Handle("POST /api/v1/bulk-jobs/{id}/cancel", g(auth.PermCertsIssue, http.HandlerFunc(h.jobCancel)))
	// Exports are not read permissions: a suspended institute cannot export (D-114).
	mux.Handle("GET /api/v1/bulk-jobs/{id}/export", g(auth.PermCertsExport, http.HandlerFunc(h.jobExport)))
}

type jobDTO struct {
	ID                string     `json:"id"`
	Status            JobStatus  `json:"status"`
	BatchID           string     `json:"batch_id"`
	BatchName         string     `json:"batch_name"`
	TemplateID        string     `json:"template_id"`
	TemplateName      string     `json:"template_name"`
	TemplateVersion   int        `json:"template_version"`
	Total             int        `json:"total"`
	Succeeded         int        `json:"succeeded"`
	Failed            int        `json:"failed"`
	Remaining         int        `json:"remaining"`
	SkippedIssued     int        `json:"skipped_already_issued"`
	SkippedIneligible int        `json:"skipped_not_eligible"`
	CreatedAt         time.Time  `json:"created_at"`
	StartedAt         *time.Time `json:"started_at"`
	FinishedAt        *time.Time `json:"finished_at"`
}

func jobView(j Job) jobDTO {
	return jobDTO{ID: j.ID, Status: j.Status, BatchID: j.BatchID, BatchName: j.BatchName, TemplateID: j.TemplateID, TemplateName: j.TemplateName,
		TemplateVersion: j.TemplateVersion, Total: j.Total, Succeeded: j.Succeeded, Failed: j.Failed, Remaining: j.Remaining,
		SkippedIssued: j.SkippedIssued, SkippedIneligible: j.SkippedIneligible, CreatedAt: j.CreatedAt, StartedAt: j.StartedAt, FinishedAt: j.FinishedAt}
}

type itemDTO struct {
	EnrollmentID      string     `json:"enrollment_id"`
	StudentName       string     `json:"student_name"`
	Position          int        `json:"position"`
	Status            ItemStatus `json:"status"`
	Attempts          int        `json:"attempts"`
	CertificateID     string     `json:"certificate_id,omitempty"`
	CertificateNumber string     `json:"certificate_number,omitempty"`
	Error             string     `json:"error,omitempty"`
}

func (h *Handler) bulkPreview(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	p, err := h.Jobs.Preview(r.Context(), tenant(r), q.Get("batch_id"), q.Get("template_id"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"preview": map[string]any{"batch_name": p.BatchName, "eligible": p.Eligible,
		"already_issued": p.AlreadyIssued, "not_eligible": p.NotEligible, "max_per_job": MaxJobItems}})
}

func (h *Handler) bulkStart(w http.ResponseWriter, r *http.Request) {
	var b struct {
		BatchID         string `json:"batch_id"`
		TemplateID      string `json:"template_id"`
		TemplateVersion int    `json:"template_version"`
	}
	if !h.decode(w, r, &b) {
		return
	}
	j, err := h.Jobs.Start(r.Context(), tenant(r), BulkRequest{BatchID: b.BatchID, TemplateID: b.TemplateID, TemplateVersion: b.TemplateVersion}, rid(r))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	// 202: the work continues in the background; poll GET /api/v1/bulk-jobs/{id}.
	httpx.WriteJSON(w, http.StatusAccepted, map[string]any{"job": jobView(j)})
}

func page(w http.ResponseWriter, r *http.Request, def int) (limit, offset int, ok bool) {
	q := r.URL.Query()
	limit = def
	var err error
	if v := q.Get("limit"); v != "" {
		if limit, err = strconv.Atoi(v); err != nil || limit < 1 {
			bad(w, r, "limit", "Use a positive number.")
			return 0, 0, false
		}
	}
	if v := q.Get("offset"); v != "" {
		if offset, err = strconv.Atoi(v); err != nil || offset < 0 {
			bad(w, r, "offset", "Use zero or a positive number.")
			return 0, 0, false
		}
	}
	return limit, offset, true
}

func (h *Handler) jobList(w http.ResponseWriter, r *http.Request) {
	limit, offset, ok := page(w, r, 20)
	if !ok {
		return
	}
	if limit > 100 {
		limit = 100
	}
	jobs, err := h.Jobs.List(r.Context(), tenant(r), limit, offset)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	more := len(jobs) > limit
	if more {
		jobs = jobs[:limit]
	}
	out := make([]jobDTO, 0, len(jobs))
	for _, j := range jobs {
		out = append(out, jobView(j))
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"jobs": out, "has_more": more, "limit": limit, "offset": offset})
}

func (h *Handler) jobGet(w http.ResponseWriter, r *http.Request) {
	j, err := h.Jobs.Get(r.Context(), tenant(r), r.PathValue("id"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"job": jobView(j)})
}

func (h *Handler) jobItems(w http.ResponseWriter, r *http.Request) {
	status := ItemStatus(r.URL.Query().Get("status"))
	if status != "" && !status.Valid() {
		bad(w, r, "status", "Use queued, processing, succeeded or failed.")
		return
	}
	limit, offset, ok := page(w, r, 100)
	if !ok {
		return
	}
	if limit > 200 {
		limit = 200
	}
	items, err := h.Jobs.Items(r.Context(), tenant(r), r.PathValue("id"), status, limit, offset)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	more := len(items) > limit
	if more {
		items = items[:limit]
	}
	out := make([]itemDTO, 0, len(items))
	for _, it := range items {
		out = append(out, itemDTO{EnrollmentID: it.EnrollmentID, StudentName: it.StudentName, Position: it.Position, Status: it.Status,
			Attempts: it.Attempts, CertificateID: it.CertificateID, CertificateNumber: it.CertificateNumber, Error: it.Error})
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": out, "has_more": more, "limit": limit, "offset": offset})
}

func (h *Handler) jobAction(w http.ResponseWriter, r *http.Request, do func(id string) (Job, error)) {
	j, err := do(r.PathValue("id"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"job": jobView(j)})
}

func (h *Handler) jobRetry(w http.ResponseWriter, r *http.Request) {
	h.jobAction(w, r, func(id string) (Job, error) { return h.Jobs.RetryFailed(r.Context(), tenant(r), id, rid(r)) })
}

func (h *Handler) jobResume(w http.ResponseWriter, r *http.Request) {
	h.jobAction(w, r, func(id string) (Job, error) { return h.Jobs.Resume(r.Context(), tenant(r), id, rid(r)) })
}

func (h *Handler) jobCancel(w http.ResponseWriter, r *http.Request) {
	h.jobAction(w, r, func(id string) (Job, error) { return h.Jobs.Cancel(r.Context(), tenant(r), id, rid(r)) })
}

// jobExport streams a ZIP of the job's issued PDFs. Everything that can fail is checked before the first
// byte; a failure after that aborts the connection so the download cannot look complete.
func (h *Handler) jobExport(w http.ResponseWriter, r *http.Request) {
	started := false
	err := h.Jobs.ExportZIP(r.Context(), tenant(r), r.PathValue("id"), func(name string) io.Writer {
		started = true
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
		w.Header().Set("Cache-Control", "private, no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.WriteHeader(http.StatusOK)
		return w
	}, rid(r))
	if err == nil {
		return
	}
	if !started {
		h.fail(w, r, err)
		return
	}
	h.Log.Error("bulk export aborted", "request_id", rid(r), "err", err)
	panic(http.ErrAbortHandler)
}
