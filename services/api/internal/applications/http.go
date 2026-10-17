package applications

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/certificate-os/api/internal/audit"
	"github.com/certificate-os/api/internal/auth"
	"github.com/certificate-os/api/internal/auth/authhttp"
	"github.com/certificate-os/api/internal/platform/httpx"
	"github.com/certificate-os/api/internal/tenancy"
)

// Handler serves the applicant API (/api/v1/institute-applications) and the operations review API
// (/api/v1/ops/institute-applications). Applicant routes only ever use the caller's own user id;
// a foreign application id is indistinguishable from a missing one (404).
type Handler struct {
	Svc   *Service
	Log   *slog.Logger
	Authn func(http.Handler) http.Handler // authhttp.Handler.Authenticate
}

func (h *Handler) Register(mux *http.ServeMux) {
	user := func(fn http.HandlerFunc) http.Handler { return h.Authn(authhttp.RequireAuth(fn)) }
	ops := func(perm auth.Permission, fn http.HandlerFunc) http.Handler {
		return h.Authn(authhttp.RequirePlatformPermission(perm, fn))
	}
	const base, opsBase = "/api/v1/institute-applications", "/api/v1/ops/institute-applications"
	mux.Handle("POST "+base, user(h.create))
	mux.Handle("GET "+base, user(h.mine))
	mux.Handle("GET "+base+"/{id}", user(h.mineByID))
	mux.Handle("PATCH "+base+"/{id}", user(h.update))
	mux.Handle("POST "+base+"/{id}/submit", user(h.submit))

	mux.Handle("GET "+opsBase, ops(auth.PermOpsApplicationsRead, h.opsList))
	mux.Handle("GET "+opsBase+"/{id}", ops(auth.PermOpsApplicationsRead, h.opsGet))
	mux.Handle("GET "+opsBase+"/{id}/history", ops(auth.PermOpsApplicationsRead, h.opsHistory))
	mux.Handle("POST "+opsBase+"/{id}/start-review", ops(auth.PermOpsApplicationsReview, h.startReview))
	mux.Handle("POST "+opsBase+"/{id}/approve", ops(auth.PermOpsApplicationsReview, h.approve))
	mux.Handle("POST "+opsBase+"/{id}/reject", ops(auth.PermOpsApplicationsReview, h.reject))
	mux.Handle("POST "+opsBase+"/{id}/request-changes", ops(auth.PermOpsApplicationsReview, h.requestChanges))
}

// --- DTOs ---

type detailsDTO struct {
	Name          string `json:"name"`
	Slug          string `json:"slug"`
	Category      string `json:"category"`
	AddressLine1  string `json:"address_line1"`
	AddressLine2  string `json:"address_line2"`
	City          string `json:"city"`
	Region        string `json:"region"`
	PostalCode    string `json:"postal_code"`
	Country       string `json:"country"`
	OfficialEmail string `json:"official_email"`
	Phone         string `json:"phone"`
	RepName       string `json:"rep_name"`
	RepRole       string `json:"rep_role"`
	Website       string `json:"website"`
	Attested      bool   `json:"attested"`
}

type applicationDTO struct {
	ID              string     `json:"id"`
	Status          Status     `json:"status"`
	Details         detailsDTO `json:"details"`
	Editable        bool       `json:"editable"`
	Version         int        `json:"version"`
	SubmittedAt     *time.Time `json:"submitted_at"`
	ReviewedAt      *time.Time `json:"reviewed_at"`
	DecisionReason  string     `json:"decision_reason,omitempty"`
	InstituteID     string     `json:"institute_id,omitempty"`
	ApplicantUserID string     `json:"applicant_user_id,omitempty"` // operations view only
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

func toDTO(a Application, forOps bool) applicationDTO {
	d := a.Details
	dto := applicationDTO{ID: a.ID, Status: a.Status, Editable: a.Status.Editable(), Version: a.Version,
		SubmittedAt: a.SubmittedAt, ReviewedAt: a.ReviewedAt, DecisionReason: a.DecisionReason,
		InstituteID: a.InstituteID, CreatedAt: a.CreatedAt, UpdatedAt: a.UpdatedAt,
		Details: detailsDTO{Name: d.Name, Slug: d.Slug, Category: d.Category, AddressLine1: d.AddressLine1,
			AddressLine2: d.AddressLine2, City: d.City, Region: d.Region, PostalCode: d.PostalCode, Country: d.Country,
			OfficialEmail: d.OfficialEmail, Phone: d.Phone, RepName: d.RepName, RepRole: d.RepRole,
			Website: d.Website, Attested: d.Attested}}
	if forOps {
		dto.ApplicantUserID = a.ApplicantUserID
	}
	return dto
}

// patchBody mirrors Patch with JSON tags. Unknown fields are rejected by DecodeJSON.
type patchBody struct {
	Name          *string `json:"name"`
	Slug          *string `json:"slug"`
	Category      *string `json:"category"`
	AddressLine1  *string `json:"address_line1"`
	AddressLine2  *string `json:"address_line2"`
	City          *string `json:"city"`
	Region        *string `json:"region"`
	PostalCode    *string `json:"postal_code"`
	Country       *string `json:"country"`
	OfficialEmail *string `json:"official_email"`
	Phone         *string `json:"phone"`
	RepName       *string `json:"rep_name"`
	RepRole       *string `json:"rep_role"`
	Website       *string `json:"website"`
	Attested      *bool   `json:"attested"`
}

func (b patchBody) patch() Patch {
	return Patch{Name: b.Name, Slug: b.Slug, Category: b.Category, AddressLine1: b.AddressLine1, AddressLine2: b.AddressLine2,
		City: b.City, Region: b.Region, PostalCode: b.PostalCode, Country: b.Country, OfficialEmail: b.OfficialEmail,
		Phone: b.Phone, RepName: b.RepName, RepRole: b.RepRole, Website: b.Website, Attested: b.Attested}
}

// --- helpers ---

func (h *Handler) fail(w http.ResponseWriter, r *http.Request, err error) {
	var ve *ValidationError
	switch {
	case errors.As(err, &ve):
		httpx.WriteError(w, r, http.StatusUnprocessableEntity, httpx.CodeValidation, "Please correct the highlighted fields.", ve.Problems)
	case errors.Is(err, ErrNotFound):
		httpx.NotFound(w, r)
	case errors.Is(err, ErrNotEditable):
		httpx.WriteError(w, r, http.StatusConflict, httpx.CodeConflict, "This application can no longer be edited.", nil)
	case errors.Is(err, ErrInvalidTransition):
		httpx.WriteError(w, r, http.StatusConflict, httpx.CodeConflict, "This application is not in a state that allows that action.", nil)
	case errors.Is(err, ErrOpenApplicationExists):
		httpx.WriteError(w, r, http.StatusConflict, httpx.CodeConflict, "You already have an open application.", nil)
	case errors.Is(err, ErrSlugTaken):
		httpx.WriteError(w, r, http.StatusConflict, httpx.CodeConflict, "That workspace address is already taken.", map[string]string{"slug": "That workspace address is already taken."})
	case errors.Is(err, ErrSelfReview):
		httpx.WriteError(w, r, http.StatusForbidden, httpx.CodeForbidden, "You cannot review your own application.", nil)
	case errors.Is(err, ErrApplicantInactive):
		httpx.WriteError(w, r, http.StatusConflict, httpx.CodeConflict, "The applicant's account is not active.", nil)
	default:
		h.Log.Error("applications request failed", "request_id", httpx.RequestID(r.Context()), "err", err)
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

func me(r *http.Request) string  { p, _ := auth.PrincipalFrom(r.Context()); return p.UserID }
func rid(r *http.Request) string { return httpx.RequestID(r.Context()) }

// --- applicant endpoints ---

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	var b patchBody
	if !h.decode(w, r, &b) {
		return
	}
	a, err := h.Svc.Create(r.Context(), me(r), b.patch(), rid(r))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{"application": toDTO(a, false)})
}

func (h *Handler) mine(w http.ResponseWriter, r *http.Request) {
	as, err := h.Svc.Mine(r.Context(), me(r))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	out := make([]applicationDTO, 0, len(as))
	for _, a := range as {
		out = append(out, toDTO(a, false))
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"applications": out})
}

func (h *Handler) mineByID(w http.ResponseWriter, r *http.Request) {
	a, err := h.Svc.MineByID(r.Context(), me(r), r.PathValue("id"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"application": toDTO(a, false)})
}

func (h *Handler) update(w http.ResponseWriter, r *http.Request) {
	var b patchBody
	if !h.decode(w, r, &b) {
		return
	}
	a, err := h.Svc.Update(r.Context(), me(r), r.PathValue("id"), b.patch(), rid(r))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"application": toDTO(a, false)})
}

func (h *Handler) submit(w http.ResponseWriter, r *http.Request) {
	a, err := h.Svc.Submit(r.Context(), me(r), r.PathValue("id"), rid(r))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"application": toDTO(a, false)})
}

// --- operations endpoints ---

func (h *Handler) opsList(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := ListFilter{}
	for _, s := range strings.Split(q.Get("status"), ",") {
		if s = strings.TrimSpace(s); s != "" {
			st := Status(s)
			if !st.Valid() || st == Draft {
				httpx.WriteError(w, r, http.StatusUnprocessableEntity, httpx.CodeValidation, "Unknown status filter.", map[string]string{"status": "Unknown status."})
				return
			}
			f.Statuses = append(f.Statuses, st)
		}
	}
	f.Limit, _ = strconv.Atoi(q.Get("limit"))
	f.Offset, _ = strconv.Atoi(q.Get("offset"))
	as, err := h.Svc.OpsList(r.Context(), f)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	out := make([]applicationDTO, 0, len(as))
	for _, a := range as {
		out = append(out, toDTO(a, true))
	}
	n := f.Normalized()
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"applications": out, "limit": n.Limit, "offset": n.Offset})
}

func (h *Handler) opsGet(w http.ResponseWriter, r *http.Request) {
	a, err := h.Svc.OpsGet(r.Context(), r.PathValue("id"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"application": toDTO(a, true)})
}

type historyDTO struct {
	ID          string         `json:"id"`
	Action      string         `json:"action"`
	ActorType   string         `json:"actor_type"`
	ActorUserID string         `json:"actor_user_id,omitempty"`
	OccurredAt  time.Time      `json:"occurred_at"`
	Metadata    map[string]any `json:"metadata,omitempty"`
}

func (h *Handler) opsHistory(w http.ResponseWriter, r *http.Request) {
	recs, err := h.Svc.OpsHistory(r.Context(), r.PathValue("id"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"history": HistoryDTOs(recs)})
}

// HistoryDTOs converts audit records for API output (newest first, as returned by the reader).
func HistoryDTOs(recs []audit.Record) []historyDTO {
	out := make([]historyDTO, 0, len(recs))
	for _, rec := range recs {
		out = append(out, historyDTO{ID: rec.ID, Action: rec.Action, ActorType: string(rec.ActorType),
			ActorUserID: rec.ActorUserID, OccurredAt: rec.OccurredAt, Metadata: rec.Metadata})
	}
	return out
}

func (h *Handler) startReview(w http.ResponseWriter, r *http.Request) {
	a, err := h.Svc.StartReview(r.Context(), me(r), r.PathValue("id"), rid(r))
	h.respondApp(w, r, a, err)
}

type reasonBody struct {
	Reason string `json:"reason"`
}

type noteBody struct {
	Note string `json:"note"`
}

func (h *Handler) reject(w http.ResponseWriter, r *http.Request) {
	var b reasonBody
	if !h.decode(w, r, &b) {
		return
	}
	a, err := h.Svc.Reject(r.Context(), me(r), r.PathValue("id"), b.Reason, rid(r))
	h.respondApp(w, r, a, err)
}

func (h *Handler) requestChanges(w http.ResponseWriter, r *http.Request) {
	var b reasonBody
	if !h.decode(w, r, &b) {
		return
	}
	a, err := h.Svc.RequestChanges(r.Context(), me(r), r.PathValue("id"), b.Reason, rid(r))
	h.respondApp(w, r, a, err)
}

func (h *Handler) approve(w http.ResponseWriter, r *http.Request) {
	var b noteBody
	if r.ContentLength != 0 { // body is optional
		if !h.decode(w, r, &b) {
			return
		}
	}
	a, inst, err := h.Svc.Approve(r.Context(), me(r), r.PathValue("id"), b.Note, rid(r))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"application": toDTO(a, true), "institute": instituteDTO(inst)})
}

func (h *Handler) respondApp(w http.ResponseWriter, r *http.Request, a Application, err error) {
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"application": toDTO(a, true)})
}

func instituteDTO(i tenancy.Institute) map[string]any {
	return map[string]any{"id": i.ID, "slug": i.Slug, "name": i.Name, "status": string(i.Status)}
}
