package delivery

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/certificate-os/api/internal/auth"
	"github.com/certificate-os/api/internal/platform/httpx"
	"github.com/certificate-os/api/internal/tenancy"
)

// Handler serves deliveries. Every route goes through tenancy.Handler.Guard (authenticate, resolve the tenant from
// the membership, check the permission; suspended institutes lose write permissions). The institute id is never
// read from the path, query or body. Recipients are masked in every response.
type Handler struct {
	Svc *Service
	// JobCertificates returns the ids of the certificates a bulk job issued (wired from certificates.Jobs).
	JobCertificates func(ctx context.Context, t tenancy.Tenant, jobID string) ([]string, error)
	Log             *slog.Logger
	Tenancy         *tenancy.Handler
}

func (h *Handler) Register(mux *http.ServeMux) {
	g := h.Tenancy.Guard
	mux.Handle("GET /api/v1/email/status", g(auth.PermCertsRead, http.HandlerFunc(h.status)))
	mux.Handle("POST /api/v1/certificates/{id}/deliveries", g(auth.PermCertsDeliver, http.HandlerFunc(h.send)))
	mux.Handle("GET /api/v1/certificates/{id}/deliveries", g(auth.PermCertsRead, http.HandlerFunc(h.listForCertificate)))
	mux.Handle("GET /api/v1/deliveries", g(auth.PermCertsRead, http.HandlerFunc(h.list)))
	mux.Handle("GET /api/v1/deliveries/{id}", g(auth.PermCertsRead, http.HandlerFunc(h.get)))
	mux.Handle("POST /api/v1/deliveries/{id}/retry", g(auth.PermCertsDeliver, http.HandlerFunc(h.retry)))
	if h.JobCertificates != nil {
		mux.Handle("POST /api/v1/bulk-jobs/{id}/deliveries", g(auth.PermCertsDeliver, http.HandlerFunc(h.sendJob)))
	}
}

type deliveryDTO struct {
	ID                string       `json:"id"`
	CertificateID     string       `json:"certificate_id"`
	CertificateNumber string       `json:"certificate_number"`
	Channel           string       `json:"channel"`
	Recipient         string       `json:"recipient"` // masked
	Status            Status       `json:"status"`
	Attempts          int          `json:"attempts"`
	MaxAttempts       int          `json:"max_attempts"`
	NextAttemptAt     *time.Time   `json:"next_attempt_at"`
	LastError         string       `json:"last_error,omitempty"`
	CreatedAt         time.Time    `json:"created_at"`
	FinishedAt        *time.Time   `json:"finished_at"`
	AttemptLog        []attemptDTO `json:"attempt_log,omitempty"`
}

type attemptDTO struct {
	No         int       `json:"attempt"`
	Status     string    `json:"status"`
	ErrorCode  string    `json:"error_code,omitempty"`
	FinishedAt time.Time `json:"finished_at"`
}

// MaskEmail keeps the first character and the domain: a***@example.com.
func MaskEmail(e string) string {
	at := strings.LastIndex(e, "@")
	if at < 1 {
		return "***"
	}
	return e[:1] + "***" + e[at:]
}

func view(d Delivery) deliveryDTO {
	return deliveryDTO{ID: d.ID, CertificateID: d.CertificateID, CertificateNumber: d.CertificateNumber, Channel: d.Channel,
		Recipient: MaskEmail(d.Recipient), Status: d.Status, Attempts: d.Attempts, MaxAttempts: d.MaxAttempts, NextAttemptAt: d.NextAttemptAt,
		LastError: d.LastError, CreatedAt: d.CreatedAt, FinishedAt: d.FinishedAt}
}

func tenant(r *http.Request) tenancy.Tenant { t, _ := tenancy.TenantFrom(r.Context()); return t }
func rid(r *http.Request) string            { return httpx.RequestID(r.Context()) }

func (h *Handler) fail(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		httpx.NotFound(w, r)
	case errors.Is(err, ErrNoRecipient):
		httpx.WriteError(w, r, http.StatusUnprocessableEntity, httpx.CodeValidation, "This student has no email address on file. Add one to the student record first.", map[string]string{"email": "No email address on file."})
	case errors.Is(err, ErrActive):
		httpx.WriteError(w, r, http.StatusConflict, httpx.CodeConflict, "This certificate is already being sent.", nil)
	case errors.Is(err, ErrLimit):
		httpx.WriteError(w, r, http.StatusConflict, httpx.CodeConflict, "This certificate has already been sent too many times.", nil)
	case errors.Is(err, ErrState):
		httpx.WriteError(w, r, http.StatusConflict, httpx.CodeConflict, "That delivery is not in a state that allows this.", nil)
	case errors.Is(err, ErrDisabled):
		httpx.WriteError(w, r, http.StatusServiceUnavailable, "email_unavailable", "Email sending is not set up on this server yet. Ask your administrator to configure it. Nothing was sent.", nil)
	default:
		h.Log.Error("delivery request failed", "request_id", rid(r), "err", err)
		httpx.WriteError(w, r, http.StatusInternalServerError, httpx.CodeInternal, "An unexpected error occurred.", nil)
	}
}

func (h *Handler) status(w http.ResponseWriter, r *http.Request) {
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"enabled": h.Svc.Enabled()})
}

func (h *Handler) send(w http.ResponseWriter, r *http.Request) {
	d, err := h.Svc.Send(r.Context(), tenant(r), r.PathValue("id"), rid(r))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	// 202: queued, not sent. The status only becomes "accepted" when the provider takes the message.
	httpx.WriteJSON(w, http.StatusAccepted, map[string]any{"delivery": view(d)})
}

func (h *Handler) retry(w http.ResponseWriter, r *http.Request) {
	d, err := h.Svc.Retry(r.Context(), tenant(r), r.PathValue("id"), rid(r))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusAccepted, map[string]any{"delivery": view(d)})
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	t := tenant(r)
	d, err := h.Svc.Get(r.Context(), t, r.PathValue("id"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	at, err := h.Svc.Attempts(r.Context(), t, d.ID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	dto := view(d)
	for _, a := range at {
		dto.AttemptLog = append(dto.AttemptLog, attemptDTO{No: a.No, Status: a.Status, ErrorCode: a.ErrorCode, FinishedAt: a.FinishedAt})
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"delivery": dto})
}

func (h *Handler) writeList(w http.ResponseWriter, r *http.Request, f Filter) {
	items, err := h.Svc.List(r.Context(), tenant(r), f)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if f.Limit <= 0 || f.Limit > 100 {
		f.Limit = 50
	}
	more := len(items) > f.Limit
	if more {
		items = items[:f.Limit]
	}
	out := make([]deliveryDTO, 0, len(items))
	for _, d := range items {
		out = append(out, view(d))
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"deliveries": out, "has_more": more, "limit": f.Limit})
}

func bad(w http.ResponseWriter, r *http.Request, field, msg string) {
	httpx.WriteError(w, r, http.StatusUnprocessableEntity, httpx.CodeValidation, "Please correct the highlighted fields.", map[string]string{field: msg})
}

func (h *Handler) filterFrom(w http.ResponseWriter, r *http.Request) (Filter, bool) {
	q := r.URL.Query()
	f := Filter{Status: Status(q.Get("status"))}
	if f.Status != "" && !f.Status.Valid() {
		bad(w, r, "status", "Use queued, sending, accepted or failed.")
		return f, false
	}
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 100 {
			bad(w, r, "limit", "Use a number from 1 to 100.")
			return f, false
		}
		f.Limit = n
	}
	return f, true
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	if f, ok := h.filterFrom(w, r); ok {
		h.writeList(w, r, f)
	}
}

func (h *Handler) listForCertificate(w http.ResponseWriter, r *http.Request) {
	if f, ok := h.filterFrom(w, r); ok {
		f.CertificateID = r.PathValue("id")
		h.writeList(w, r, f)
	}
}

// sendJob queues deliveries for every certificate a bulk job issued. Students without an email address, with a
// delivery in progress or already delivered are skipped and counted.
func (h *Handler) sendJob(w http.ResponseWriter, r *http.Request) {
	t := tenant(r)
	if !h.Svc.Enabled() {
		h.fail(w, r, ErrDisabled)
		return
	}
	ids, err := h.JobCertificates(r.Context(), t, r.PathValue("id"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	res, err := h.Svc.SendMany(r.Context(), t, ids, rid(r))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusAccepted, map[string]any{"queued": res.Queued, "skipped_no_email": res.NoEmail,
		"skipped_already_delivered": res.AlreadyDelivered, "skipped_in_progress": res.InProgress,
		"skipped_not_deliverable": res.NotDeliverable + res.TooMany})
}
