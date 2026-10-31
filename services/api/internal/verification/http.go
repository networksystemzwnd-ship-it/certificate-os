package verification

import (
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/certificate-os/api/internal/auth"
	"github.com/certificate-os/api/internal/platform/httpx"
)

// Handler serves the public verification API. There is no authentication, so every request is rate limited
// per client address (the TCP peer only: X-Forwarded-For is not trusted until a trusted-proxy setting exists).
type Handler struct {
	Svc  *Service
	Log  *slog.Logger
	ByIP *auth.Limiter
}

func NewHandler(svc *Service, log *slog.Logger) *Handler {
	return &Handler{Svc: svc, Log: log, ByIP: auth.NewLimiter(60, time.Minute)}
}

func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/public/certificates/{public_id}", h.verify)
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

type dto struct {
	Status             Status     `json:"status"`
	CertificateNumber  string     `json:"certificate_number"`
	InstituteName      string     `json:"institute_name"`
	StudentName        string     `json:"student_name"`
	CourseName         string     `json:"course_name"`
	IssueDate          string     `json:"issue_date"`
	RevokedAt          *time.Time `json:"revoked_at,omitempty"`
	ReplacedByPublicID string     `json:"replaced_by,omitempty"`
}

func (h *Handler) verify(w http.ResponseWriter, r *http.Request) {
	// The answer changes when a certificate is revoked: never cache it, never index it.
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Robots-Tag", "noindex, nofollow")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if ok, retry := h.ByIP.Allow(clientIP(r)); !ok {
		w.Header().Set("Retry-After", strconv.Itoa(int(retry.Seconds())+1))
		httpx.WriteError(w, r, http.StatusTooManyRequests, httpx.CodeRateLimited, "Too many verification requests. Please wait a moment and try again.", nil)
		return
	}
	res, err := h.Svc.Verify(r.Context(), r.PathValue("public_id"))
	if errors.Is(err, ErrNotFound) {
		httpx.WriteError(w, r, http.StatusNotFound, httpx.CodeNotFound, "No certificate matches this link.", nil)
		return
	}
	if err != nil {
		h.Log.Error("verification failed", "request_id", httpx.RequestID(r.Context()), "err", err)
		httpx.WriteError(w, r, http.StatusInternalServerError, httpx.CodeInternal, "An unexpected error occurred.", nil)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"verification": dto{Status: res.Status, CertificateNumber: res.CertificateNumber,
		InstituteName: res.InstituteName, StudentName: res.StudentName, CourseName: res.CourseName, IssueDate: res.IssueDate,
		RevokedAt: res.RevokedAt, ReplacedByPublicID: res.ReplacedByPublicID}})
}
