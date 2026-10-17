package audit

import (
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/certificate-os/api/internal/auth"
	"github.com/certificate-os/api/internal/auth/authhttp"
	"github.com/certificate-os/api/internal/platform/httpx"
)

// Handler serves the read-only audit view for platform administrators (ops.audit.read).
type Handler struct {
	Reader Reader
	Log    *slog.Logger
	Authn  func(http.Handler) http.Handler
}

func (h *Handler) Register(mux *http.ServeMux) {
	mux.Handle("GET /api/v1/ops/audit", h.Authn(authhttp.RequirePlatformPermission(auth.PermOpsAuditRead, http.HandlerFunc(h.list))))
}

type recordDTO struct {
	ID          string         `json:"id"`
	OccurredAt  time.Time      `json:"occurred_at"`
	ActorType   string         `json:"actor_type"`
	ActorUserID string         `json:"actor_user_id,omitempty"`
	Action      string         `json:"action"`
	EntityType  string         `json:"entity_type"`
	EntityID    string         `json:"entity_id"`
	InstituteID string         `json:"institute_id,omitempty"`
	RequestID   string         `json:"request_id,omitempty"`
	Metadata    map[string]any `json:"metadata,omitempty"`
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	f := Filter{EntityType: q.Get("entity_type"), EntityID: q.Get("entity_id"), Action: q.Get("action"), Limit: limit}
	recs, err := h.Reader.List(r.Context(), f)
	if err != nil {
		h.Log.Error("audit list failed", "request_id", httpx.RequestID(r.Context()), "err", err)
		httpx.WriteError(w, r, http.StatusInternalServerError, httpx.CodeInternal, "An unexpected error occurred.", nil)
		return
	}
	out := make([]recordDTO, 0, len(recs))
	for _, x := range recs {
		out = append(out, recordDTO{ID: x.ID, OccurredAt: x.OccurredAt, ActorType: string(x.ActorType), ActorUserID: x.ActorUserID,
			Action: x.Action, EntityType: x.EntityType, EntityID: x.EntityID, InstituteID: x.InstituteID, RequestID: x.RequestID, Metadata: x.Metadata})
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"records": out, "limit": f.EffectiveLimit()})
}
