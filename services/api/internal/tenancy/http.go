package tenancy

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/certificate-os/api/internal/auth"
	"github.com/certificate-os/api/internal/auth/authhttp"
	"github.com/certificate-os/api/internal/platform/httpx"
)

// InstituteHeader lets a user with several memberships pick one. It is only ever matched against
// the caller's own memberships (see Service.Resolve).
const InstituteHeader = "X-Institute-ID"

type Handler struct {
	Svc   *Service
	Log   *slog.Logger
	Authn func(http.Handler) http.Handler // authhttp.Handler.Authenticate
}

func (h *Handler) internal(w http.ResponseWriter, r *http.Request, what string, err error) {
	h.Log.Error(what, "request_id", httpx.RequestID(r.Context()), "err", err)
	httpx.WriteError(w, r, http.StatusInternalServerError, httpx.CodeInternal, "An unexpected error occurred.", nil)
}

// RequireTenant authenticates, resolves the tenant from membership and stores it in the context.
// Same response for "not a member" and "no such institute" (no enumeration).
func (h *Handler) RequireTenant(next http.Handler) http.Handler {
	return h.Authn(authhttp.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, _ := auth.PrincipalFrom(r.Context())
		t, err := h.Svc.Resolve(r.Context(), p, r.Header.Get(InstituteHeader))
		switch {
		case err == nil:
			next.ServeHTTP(w, r.WithContext(WithTenant(r.Context(), t)))
		case errors.Is(err, ErrNotMember):
			httpx.WriteError(w, r, http.StatusForbidden, httpx.CodeForbidden, "You do not have access to this institute.", nil)
		case errors.Is(err, ErrAmbiguous):
			httpx.WriteError(w, r, http.StatusBadRequest, httpx.CodeInstituteRequired,
				"You belong to several institutes; send the "+InstituteHeader+" header.", nil)
		default:
			h.internal(w, r, "resolve tenant", err)
		}
	})))
}

// RequirePermission must be wrapped INSIDE RequireTenant. Missing tenant context fails closed.
func RequirePermission(perm auth.Permission, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t, ok := TenantFrom(r.Context())
		if !ok || !t.Can(perm) {
			httpx.WriteError(w, r, http.StatusForbidden, httpx.CodeForbidden, "You do not have permission to do that.", nil)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// Guard composes RequireTenant + RequirePermission: the standard wrapper for tenant routes.
func (h *Handler) Guard(perm auth.Permission, next http.Handler) http.Handler {
	return h.RequireTenant(RequirePermission(perm, next))
}

func (h *Handler) Register(mux *http.ServeMux) {
	mux.Handle("GET /api/v1/me/institutes", h.Authn(authhttp.RequireAuth(http.HandlerFunc(h.myInstitutes))))
	mux.Handle("GET /api/v1/institute", h.Guard(auth.PermInstituteRead, http.HandlerFunc(h.institute)))
	mux.Handle("GET /api/v1/institute/members", h.Guard(auth.PermStaffManage, http.HandlerFunc(h.members)))
}

type instituteDTO struct {
	ID          string            `json:"id"`
	Slug        string            `json:"slug"`
	Name        string            `json:"name"`
	Status      string            `json:"status"`
	Role        string            `json:"role"`
	Permissions []auth.Permission `json:"permissions,omitempty"`
}

func (h *Handler) myInstitutes(w http.ResponseWriter, r *http.Request) {
	p, _ := auth.PrincipalFrom(r.Context())
	ms, err := h.Svc.Memberships(r.Context(), p)
	if err != nil {
		h.internal(w, r, "list memberships", err)
		return
	}
	out := make([]instituteDTO, 0, len(ms))
	for _, m := range ms {
		out = append(out, instituteDTO{ID: m.Institute.ID, Slug: m.Institute.Slug, Name: m.Institute.Name,
			Status: string(m.Institute.Status), Role: string(m.Role)})
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"institutes": out})
}

func (h *Handler) institute(w http.ResponseWriter, r *http.Request) {
	t, _ := TenantFrom(r.Context())
	// Read through the Scope, never through a client-supplied ID.
	inst, err := h.Svc.Store.GetInstitute(r.Context(), t.Scope())
	if err != nil {
		h.internal(w, r, "get institute", err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"institute": instituteDTO{
		ID: inst.ID, Slug: inst.Slug, Name: inst.Name, Status: string(inst.Status),
		Role: string(t.Role), Permissions: t.Permissions(),
	}})
}

func (h *Handler) members(w http.ResponseWriter, r *http.Request) {
	t, _ := TenantFrom(r.Context())
	ms, err := h.Svc.Store.ListMembers(r.Context(), t.Scope())
	if err != nil {
		h.internal(w, r, "list members", err)
		return
	}
	type dto struct {
		ID     string            `json:"id"`
		UserID string            `json:"user_id"`
		Role   string            `json:"role"`
		Grants []auth.Permission `json:"grants"`
		Status string            `json:"status"`
	}
	out := make([]dto, 0, len(ms))
	for _, m := range ms {
		g := m.Grants
		if g == nil {
			g = []auth.Permission{}
		}
		out = append(out, dto{ID: m.MembershipID, UserID: m.UserID, Role: string(m.Role), Grants: g, Status: string(m.Status)})
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"members": out})
}
