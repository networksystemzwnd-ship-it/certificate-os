package certificates

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

// Handler serves certificates. Every route goes through tenancy.Handler.Guard (authenticate, resolve the
// tenant from membership, check the permission; suspended institutes lose write permissions). The institute
// id is never read from the path, query or body.
type Handler struct {
	Svc     *Service
	Jobs    *Jobs // bulk issuing (optional: the routes are only registered when set)
	Log     *slog.Logger
	Tenancy *tenancy.Handler
}

func (h *Handler) Register(mux *http.ServeMux) {
	g := h.Tenancy.Guard
	mux.Handle("POST /api/v1/certificates", g(auth.PermCertsIssue, http.HandlerFunc(h.issue)))
	mux.Handle("GET /api/v1/certificates", g(auth.PermCertsRead, http.HandlerFunc(h.list)))
	mux.Handle("GET /api/v1/certificates/{id}", g(auth.PermCertsRead, http.HandlerFunc(h.get)))
	mux.Handle("GET /api/v1/certificates/{id}/pdf", g(auth.PermCertsRead, http.HandlerFunc(h.download)))
	mux.Handle("POST /api/v1/certificates/{id}/revoke", g(auth.PermCertsRevoke, http.HandlerFunc(h.revoke)))
	// Replacing revokes the old certificate AND issues a new one, so it needs both permissions.
	mux.Handle("POST /api/v1/certificates/{id}/replace", g(auth.PermCertsRevoke, http.HandlerFunc(h.replace)))
	if h.Jobs != nil {
		h.registerBulk(mux)
	}
}

type certDTO struct {
	ID              string     `json:"id"`
	Number          string     `json:"certificate_number"`
	Status          Status     `json:"status"`
	StudentID       string     `json:"student_id"`
	StudentName     string     `json:"student_name"`
	CourseName      string     `json:"course_name"`
	BatchName       string     `json:"batch_name"`
	CompletionDate  string     `json:"completion_date"`
	Grade           string     `json:"grade"`
	IssueDate       string     `json:"issue_date"`
	EnrollmentID    string     `json:"enrollment_id"`
	TemplateID      string     `json:"template_id"`
	TemplateName    string     `json:"template_name"`
	TemplateVersion int        `json:"template_version"`
	VerifyURL       string     `json:"verify_url"`
	PDFSHA256       string     `json:"pdf_sha256,omitempty"`
	PDFSize         int        `json:"pdf_size,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	IssuedAt        *time.Time `json:"issued_at"`
	Failure         string     `json:"failure,omitempty"`
	RevokedAt       *time.Time `json:"revoked_at"`
	RevokeReason    string     `json:"revoke_reason,omitempty"`
	ReplacesID      string     `json:"replaces_id,omitempty"`
	ReplacedByID    string     `json:"replaced_by_id,omitempty"`
}

func (h *Handler) dto(c Certificate) certDTO {
	sn := c.Snapshot
	return certDTO{ID: c.ID, Number: c.Number, Status: c.Status, StudentID: c.StudentID, StudentName: sn.StudentName,
		CourseName: sn.CourseName, BatchName: sn.BatchName, CompletionDate: sn.CompletionDate, Grade: sn.Grade, IssueDate: sn.IssueDate,
		EnrollmentID: c.EnrollmentID, TemplateID: c.TemplateID, TemplateName: sn.TemplateName, TemplateVersion: c.TemplateVersion,
		VerifyURL: h.Svc.VerifyURL(c.PublicID), PDFSHA256: c.PDFSHA256, PDFSize: c.PDFSize, CreatedAt: c.CreatedAt, IssuedAt: c.IssuedAt,
		Failure: c.Failure, RevokedAt: c.RevokedAt, RevokeReason: c.RevokeReason, ReplacesID: c.ReplacesID, ReplacedByID: c.ReplacedByID}
}

func (h *Handler) fail(w http.ResponseWriter, r *http.Request, err error) {
	var ve *ValidationError
	var ee *EligibilityError
	switch {
	case errors.As(err, &ve):
		httpx.WriteError(w, r, http.StatusUnprocessableEntity, httpx.CodeValidation, "Please correct the highlighted fields.", ve.Problems)
	case errors.As(err, &ee):
		httpx.WriteError(w, r, http.StatusUnprocessableEntity, httpx.CodeValidation, ee.Reason, map[string]string{ee.Field: ee.Reason})
	case errors.Is(err, ErrNotFound):
		httpx.NotFound(w, r)
	case errors.Is(err, ErrReplacing):
		httpx.WriteError(w, r, http.StatusConflict, httpx.CodeConflict, "A replacement of this certificate is already being made. Try again in a few minutes.", nil)
	case errors.Is(err, ErrJobActive):
		httpx.WriteError(w, r, http.StatusConflict, httpx.CodeConflict, "Another bulk job is still running. Wait for it to finish or cancel it first.", nil)
	case errors.Is(err, ErrNothingToIssue):
		httpx.WriteError(w, r, http.StatusUnprocessableEntity, httpx.CodeValidation, "No certificates to issue: every enrollment of this batch is either not completed, archived, or already has a certificate.", map[string]string{"batch_id": "Nothing to issue in this batch."})
	case errors.Is(err, ErrAlreadyIssued):
		httpx.WriteError(w, r, http.StatusConflict, httpx.CodeConflict, "This enrollment already has a certificate. Revoke it first to issue a new one.", nil)
	case errors.Is(err, ErrState):
		httpx.WriteError(w, r, http.StatusConflict, httpx.CodeConflict, "That certificate is not in a state that allows this.", nil)
	case errors.Is(err, ErrRenderUnavailable):
		httpx.WriteError(w, r, http.StatusServiceUnavailable, "render_unavailable", "Certificate PDFs cannot be created on this server yet. Ask your administrator to enable PDF rendering. Nothing was issued.", nil)
	case errors.Is(err, ErrRenderFailed):
		h.Log.Error("certificate rendering failed", "request_id", httpx.RequestID(r.Context()), "err", err)
		httpx.WriteError(w, r, http.StatusBadGateway, "render_failed", "The certificate could not be created. Nothing was issued; please try again.", nil)
	default:
		h.Log.Error("certificates request failed", "request_id", httpx.RequestID(r.Context()), "err", err)
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

func (h *Handler) issue(w http.ResponseWriter, r *http.Request) {
	var b struct {
		EnrollmentID    string `json:"enrollment_id"`
		TemplateID      string `json:"template_id"`
		TemplateVersion int    `json:"template_version"`
	}
	if !h.decode(w, r, &b) {
		return
	}
	c, err := h.Svc.Issue(r.Context(), tenant(r), IssueRequest{EnrollmentID: b.EnrollmentID, TemplateID: b.TemplateID, TemplateVersion: b.TemplateVersion}, rid(r))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{"certificate": h.dto(c)})
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := Filter{Status: Status(q.Get("status")), StudentID: q.Get("student_id"), Q: q.Get("q")}
	if f.Status != "" && !f.Status.Valid() {
		bad(w, r, "status", "Use pending, issued, failed or revoked.")
		return
	}
	var err error
	if v := q.Get("limit"); v != "" {
		if f.Limit, err = strconv.Atoi(v); err != nil || f.Limit < 1 {
			bad(w, r, "limit", "Use a positive number.")
			return
		}
	}
	if v := q.Get("offset"); v != "" {
		if f.Offset, err = strconv.Atoi(v); err != nil || f.Offset < 0 {
			bad(w, r, "offset", "Use zero or a positive number.")
			return
		}
	}
	f = f.Normalized()
	items, err := h.Svc.List(r.Context(), tenant(r), f)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	more := len(items) > f.Limit
	if more {
		items = items[:f.Limit]
	}
	out := make([]certDTO, 0, len(items))
	for _, c := range items {
		out = append(out, h.dto(c))
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"certificates": out, "has_more": more, "limit": f.Limit, "offset": f.Offset})
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	c, err := h.Svc.Get(r.Context(), tenant(r), r.PathValue("id"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"certificate": h.dto(c)})
}

// download sends the stored PDF to an authorised member of the issuing institute. The file is private:
// never cached by shared caches, never sniffed, always a download.
func (h *Handler) download(w http.ResponseWriter, r *http.Request) {
	p, err := h.Svc.PDF(r.Context(), tenant(r), r.PathValue("id"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", `attachment; filename="`+p.Number+`.pdf"`)
	w.Header().Set("Content-Length", strconv.Itoa(len(p.Bytes)))
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("ETag", `"`+p.SHA256+`"`)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(p.Bytes)
}

func (h *Handler) revoke(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Reason string `json:"reason"`
	}
	if !h.decode(w, r, &b) {
		return
	}
	c, err := h.Svc.Revoke(r.Context(), tenant(r), r.PathValue("id"), b.Reason, rid(r))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"certificate": h.dto(c)})
}

func (h *Handler) replace(w http.ResponseWriter, r *http.Request) {
	t := tenant(r)
	if !t.Can(auth.PermCertsIssue) {
		httpx.WriteError(w, r, http.StatusForbidden, httpx.CodeForbidden, "You need permission to issue certificates to replace one.", nil)
		return
	}
	var b struct {
		Reason          string `json:"reason"`
		TemplateID      string `json:"template_id"`
		TemplateVersion int    `json:"template_version"`
	}
	if !h.decode(w, r, &b) {
		return
	}
	c, err := h.Svc.Replace(r.Context(), t, ReplaceRequest{CertificateID: r.PathValue("id"), Reason: b.Reason, TemplateID: b.TemplateID, TemplateVersion: b.TemplateVersion}, rid(r))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{"certificate": h.dto(c)})
}
