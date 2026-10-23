package staff

import (
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/certificate-os/api/internal/auth"
	"github.com/certificate-os/api/internal/auth/authhttp"
	"github.com/certificate-os/api/internal/platform/httpx"
	"github.com/certificate-os/api/internal/tenancy"
)

// Handler serves staff management. Management routes need staff.manage (owner only, and lost while the
// institute is suspended). Accepting an invitation needs only a signed-in user and is rate limited.
type Handler struct {
	Svc     *Service
	Log     *slog.Logger
	Tenancy *tenancy.Handler
	// Accept limits: per user and per client IP.
	AcceptByUser *auth.Limiter
	AcceptByIP   *auth.Limiter
}

func NewHandler(svc *Service, log *slog.Logger, t *tenancy.Handler) *Handler {
	return &Handler{Svc: svc, Log: log, Tenancy: t,
		AcceptByUser: auth.NewLimiter(10, 15*time.Minute), AcceptByIP: auth.NewLimiter(30, 15*time.Minute)}
}

func (h *Handler) Register(mux *http.ServeMux) {
	g := func(f http.HandlerFunc) http.Handler { return h.Tenancy.Guard(auth.PermStaffManage, f) }
	mux.Handle("POST /api/v1/institute/invitations", g(h.invite))
	mux.Handle("GET /api/v1/institute/invitations", g(h.listInvitations))
	mux.Handle("DELETE /api/v1/institute/invitations/{id}", g(h.revoke))
	mux.Handle("PATCH /api/v1/institute/members/{id}", g(h.changeMember))
	mux.Handle("DELETE /api/v1/institute/members/{id}", g(h.removeMember))
	mux.Handle("POST /api/v1/invitations/accept", h.Tenancy.Authn(authhttp.RequireAuth(http.HandlerFunc(h.accept))))
}

type invitationDTO struct {
	ID         string            `json:"id"`
	Email      string            `json:"email"`
	Role       string            `json:"role"`
	Grants     []auth.Permission `json:"grants"`
	Status     string            `json:"status"`
	CreatedAt  time.Time         `json:"created_at"`
	ExpiresAt  time.Time         `json:"expires_at"`
	AcceptedAt *time.Time        `json:"accepted_at"`
	RevokedAt  *time.Time        `json:"revoked_at"`
}

func (h *Handler) dto(i Invitation) invitationDTO {
	g := i.Grants
	if g == nil {
		g = []auth.Permission{}
	}
	return invitationDTO{ID: i.ID, Email: i.Email, Role: string(i.Role), Grants: g, Status: string(i.Effective(h.Svc.Now())),
		CreatedAt: i.CreatedAt, ExpiresAt: i.ExpiresAt, AcceptedAt: i.AcceptedAt, RevokedAt: i.RevokedAt}
}

func tenant(r *http.Request) tenancy.Tenant { t, _ := tenancy.TenantFrom(r.Context()); return t }
func rid(r *http.Request) string            { return httpx.RequestID(r.Context()) }

func (h *Handler) fail(w http.ResponseWriter, r *http.Request, err error) {
	var ve *ValidationError
	switch {
	case errors.As(err, &ve):
		httpx.WriteError(w, r, http.StatusUnprocessableEntity, httpx.CodeValidation, "Please correct the highlighted fields.", ve.Problems)
	case errors.Is(err, ErrForbidden):
		httpx.WriteError(w, r, http.StatusForbidden, httpx.CodeForbidden, "You do not have permission to do that.", nil)
	case errors.Is(err, ErrNotFound):
		httpx.NotFound(w, r)
	case errors.Is(err, ErrAlreadyInvited):
		httpx.WriteError(w, r, http.StatusConflict, httpx.CodeConflict, "That email already has a pending invitation. Revoke it to send a new one.", map[string]string{"email": "Already invited."})
	case errors.Is(err, ErrAlreadyMember):
		httpx.WriteError(w, r, http.StatusConflict, httpx.CodeConflict, "That person is already a member.", map[string]string{"email": "Already a member."})
	case errors.Is(err, ErrTooManyPending):
		httpx.WriteError(w, r, http.StatusConflict, httpx.CodeConflict, "Too many pending invitations. Revoke some before inviting more.", nil)
	case errors.Is(err, ErrState):
		httpx.WriteError(w, r, http.StatusConflict, httpx.CodeConflict, "That invitation is no longer pending.", nil)
	case errors.Is(err, ErrProtected):
		httpx.WriteError(w, r, http.StatusConflict, httpx.CodeConflict, "That member cannot be changed here. Owners and your own access are protected.", nil)
	case errors.Is(err, ErrInvalidInvitation):
		httpx.WriteError(w, r, http.StatusNotFound, httpx.CodeNotFound, ErrInvalidInvitation.Error(), nil)
	default:
		h.Log.Error("staff request failed", "request_id", rid(r), "err", err)
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

func (h *Handler) invite(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Email  string            `json:"email"`
		Role   string            `json:"role"`
		Grants []auth.Permission `json:"grants"`
	}
	if !h.decode(w, r, &in) {
		return
	}
	inv, token, err := h.Svc.Invite(r.Context(), tenant(r), in.Email, auth.InstituteRole(in.Role), in.Grants, rid(r))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	// The token is shown exactly once; only its hash is stored. It travels in the URL fragment so it never
	// reaches server logs or Referer headers.
	w.Header().Set("Cache-Control", "no-store")
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{"invitation": h.dto(inv), "token": token, "accept_path": "/accept-invite#token=" + token})
}

func (h *Handler) listInvitations(w http.ResponseWriter, r *http.Request) {
	list, err := h.Svc.ListInvitations(r.Context(), tenant(r))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	out := make([]invitationDTO, 0, len(list))
	for _, i := range list {
		out = append(out, h.dto(i))
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"invitations": out})
}

func (h *Handler) revoke(w http.ResponseWriter, r *http.Request) {
	inv, err := h.Svc.Revoke(r.Context(), tenant(r), r.PathValue("id"), rid(r))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"invitation": h.dto(inv)})
}

func (h *Handler) changeMember(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Role   string            `json:"role"`
		Grants []auth.Permission `json:"grants"`
	}
	if !h.decode(w, r, &in) {
		return
	}
	m, err := h.Svc.ChangeMember(r.Context(), tenant(r), r.PathValue("id"), auth.InstituteRole(in.Role), in.Grants, rid(r))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	g := m.Grants
	if g == nil {
		g = []auth.Permission{}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"member": map[string]any{"id": m.MembershipID, "user_id": m.UserID, "email": m.Email,
		"role": string(m.Role), "grants": g, "status": string(m.Status)}})
}

func (h *Handler) removeMember(w http.ResponseWriter, r *http.Request) {
	if err := h.Svc.RemoveMember(r.Context(), tenant(r), r.PathValue("id"), rid(r)); err != nil {
		h.fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"removed": true})
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (h *Handler) accept(w http.ResponseWriter, r *http.Request) {
	p, _ := auth.PrincipalFrom(r.Context())
	for _, l := range []struct {
		lim *auth.Limiter
		key string
	}{{h.AcceptByUser, "u:" + p.UserID}, {h.AcceptByIP, "ip:" + clientIP(r)}} {
		if ok, retry := l.lim.Allow(l.key); !ok {
			w.Header().Set("Retry-After", strconv.Itoa(int(retry.Seconds())+1))
			httpx.WriteError(w, r, http.StatusTooManyRequests, httpx.CodeRateLimited, "Too many attempts. Please try again later.", nil)
			return
		}
	}
	var in struct {
		Token string `json:"token"`
	}
	if !h.decode(w, r, &in) {
		return
	}
	res, err := h.Svc.Accept(r.Context(), p, in.Token, rid(r))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"institute": map[string]any{"id": res.Institute.ID, "slug": res.Institute.Slug, "name": res.Institute.Name},
		"role":      string(res.Member.Role),
	})
}
