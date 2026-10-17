package tenancy

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/certificate-os/api/internal/auth"
	"github.com/certificate-os/api/internal/auth/authhttp"
	"github.com/certificate-os/api/internal/platform/httpx"
)

// OpsHandler serves /api/v1/ops/institutes. Every route requires a platform permission.
type OpsHandler struct {
	Svc   *OpsService
	Log   *slog.Logger
	Authn func(http.Handler) http.Handler
}

func (h *OpsHandler) Register(mux *http.ServeMux) {
	ops := func(perm auth.Permission, fn http.HandlerFunc) http.Handler {
		return h.Authn(authhttp.RequirePlatformPermission(perm, fn))
	}
	mux.Handle("GET /api/v1/ops/institutes", ops(auth.PermOpsInstitutesManage, h.list))
	mux.Handle("POST /api/v1/ops/institutes/{id}/suspend", ops(auth.PermOpsInstitutesManage, h.suspend))
	mux.Handle("POST /api/v1/ops/institutes/{id}/reactivate", ops(auth.PermOpsInstitutesManage, h.reactivate))
}

type opsInstituteDTO struct {
	ID        string    `json:"id"`
	Slug      string    `json:"slug"`
	Name      string    `json:"name"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
}

func dto(i Institute) opsInstituteDTO {
	return opsInstituteDTO{ID: i.ID, Slug: i.Slug, Name: i.Name, Status: string(i.Status), CreatedAt: i.CreatedAt}
}

func (h *OpsHandler) fail(w http.ResponseWriter, r *http.Request, err error) {
	var re *ReasonError
	switch {
	case errors.As(err, &re):
		httpx.WriteError(w, r, http.StatusUnprocessableEntity, httpx.CodeValidation, "Please correct the highlighted fields.", map[string]string{"reason": re.Message})
	case errors.Is(err, ErrNotFound):
		httpx.NotFound(w, r)
	case errors.Is(err, ErrInvalidStatusChange):
		httpx.WriteError(w, r, http.StatusConflict, httpx.CodeConflict, "The institute is not in a state that allows that action.", nil)
	default:
		h.Log.Error("ops institutes request failed", "request_id", httpx.RequestID(r.Context()), "err", err)
		httpx.WriteError(w, r, http.StatusInternalServerError, httpx.CodeInternal, "An unexpected error occurred.", nil)
	}
}

func (h *OpsHandler) list(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	offset, _ := strconv.Atoi(q.Get("offset"))
	is, err := h.Svc.List(r.Context(), q.Get("q"), limit, offset)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	out := make([]opsInstituteDTO, 0, len(is))
	for _, i := range is {
		out = append(out, dto(i))
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"institutes": out})
}

func (h *OpsHandler) suspend(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Reason string `json:"reason"`
	}
	if err := httpx.DecodeJSON(w, r, &b); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, httpx.CodeBadRequest, err.Error(), nil)
		return
	}
	p, _ := auth.PrincipalFrom(r.Context())
	i, err := h.Svc.Suspend(r.Context(), p.UserID, r.PathValue("id"), b.Reason, httpx.RequestID(r.Context()))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"institute": dto(i)})
}

func (h *OpsHandler) reactivate(w http.ResponseWriter, r *http.Request) {
	p, _ := auth.PrincipalFrom(r.Context())
	i, err := h.Svc.Reactivate(r.Context(), p.UserID, r.PathValue("id"), httpx.RequestID(r.Context()))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"institute": dto(i)})
}
