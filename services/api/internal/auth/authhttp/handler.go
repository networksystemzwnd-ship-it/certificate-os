// Package authhttp exposes authentication over HTTP: register, login, logout, /me,
// plus middleware for authentication and permission checks.
package authhttp

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

type CookieConfig struct {
	Secure bool          // true in production (HTTPS)
	TTL    time.Duration // must match the service session TTL
}

func (c CookieConfig) name() string {
	if c.Secure {
		return "__Host-cos_session" // __Host- prefix: Secure, Path=/, no Domain
	}
	return "cos_session"
}

func (c CookieConfig) set(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name: c.name(), Value: token, Path: "/", MaxAge: int(c.TTL.Seconds()),
		HttpOnly: true, Secure: c.Secure, SameSite: http.SameSiteLaxMode,
	})
}

func (c CookieConfig) clear(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: c.name(), Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: c.Secure, SameSite: http.SameSiteLaxMode,
	})
}

func (c CookieConfig) token(r *http.Request) string {
	ck, err := r.Cookie(c.name())
	if err != nil {
		return ""
	}
	return ck.Value
}

type Handler struct {
	Svc          *auth.Service
	Cookies      CookieConfig
	Log          *slog.Logger
	RegisterByIP *auth.Limiter
	LoginByIP    *auth.Limiter
	LoginByEmail *auth.Limiter
}

// NewHandler wires default rate limits (per-process; see DECISIONS D-108).
func NewHandler(svc *auth.Service, cookies CookieConfig, log *slog.Logger) *Handler {
	return &Handler{
		Svc: svc, Cookies: cookies, Log: log,
		RegisterByIP: auth.NewLimiter(10, time.Hour),
		LoginByIP:    auth.NewLimiter(20, time.Minute),
		LoginByEmail: auth.NewLimiter(10, 15*time.Minute),
	}
}

func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/auth/register", h.register)
	mux.HandleFunc("POST /api/v1/auth/login", h.login)
	mux.HandleFunc("POST /api/v1/auth/logout", h.logout)
	mux.Handle("GET /api/v1/me", h.Authenticate(RequireAuth(http.HandlerFunc(h.me))))
}

// clientIP uses the TCP peer address only. X-Forwarded-For is NOT trusted until a trusted-proxy
// setting exists (behind a reverse proxy every client would otherwise share one limiter key).
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (h *Handler) limited(w http.ResponseWriter, r *http.Request, retry time.Duration) {
	secs := int(retry.Seconds()) + 1
	w.Header().Set("Retry-After", strconv.Itoa(secs))
	httpx.WriteError(w, r, http.StatusTooManyRequests, httpx.CodeRateLimited, "Too many attempts. Please try again later.", nil)
}

type credentials struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type userDTO struct {
	ID           string            `json:"id"`
	Email        string            `json:"email"`
	PlatformRole string            `json:"platform_role,omitempty"`
	Permissions  []auth.Permission `json:"permissions"`
}

func (h *Handler) register(w http.ResponseWriter, r *http.Request) {
	if ok, retry := h.RegisterByIP.Allow(clientIP(r)); !ok {
		h.limited(w, r, retry)
		return
	}
	var in credentials
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, httpx.CodeBadRequest, err.Error(), nil)
		return
	}
	email, okEmail := auth.NormalizeEmail(in.Email)
	problems := map[string]string{}
	if !okEmail {
		problems["email"] = "Enter a valid email address."
	}
	if msg := auth.ValidatePassword(in.Password, email); msg != "" {
		problems["password"] = msg
	}
	if len(problems) > 0 {
		httpx.WriteError(w, r, http.StatusUnprocessableEntity, httpx.CodeValidation, "Please correct the highlighted fields.", problems)
		return
	}
	u, err := h.Svc.Register(r.Context(), email, in.Password)
	switch {
	case errors.Is(err, auth.ErrEmailTaken):
		// Known limitation (D-104): reveals that the email exists until email verification lands.
		httpx.WriteError(w, r, http.StatusConflict, httpx.CodeConflict, "That email cannot be registered.", nil)
	case err != nil:
		h.Log.Error("register failed", "request_id", httpx.RequestID(r.Context()), "err", err)
		httpx.WriteError(w, r, http.StatusInternalServerError, httpx.CodeInternal, "An unexpected error occurred.", nil)
	default:
		h.Log.Info("user registered", "request_id", httpx.RequestID(r.Context()), "user_id", u.ID)
		httpx.WriteJSON(w, http.StatusCreated, map[string]any{"user": toDTO(auth.Principal{UserID: u.ID, Email: u.Email, Status: u.Status})})
	}
}

func (h *Handler) login(w http.ResponseWriter, r *http.Request) {
	if ok, retry := h.LoginByIP.Allow(clientIP(r)); !ok {
		h.limited(w, r, retry)
		return
	}
	var in credentials
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, httpx.CodeBadRequest, err.Error(), nil)
		return
	}
	email, _ := auth.NormalizeEmail(in.Email) // invalid emails simply fail to match anyone
	if ok, retry := h.LoginByEmail.Allow(email); !ok {
		h.limited(w, r, retry)
		return
	}
	token, p, err := h.Svc.Login(r.Context(), email, in.Password)
	switch {
	case errors.Is(err, auth.ErrInvalidLogin):
		h.Log.Info("login failed", "request_id", httpx.RequestID(r.Context()))
		httpx.WriteError(w, r, http.StatusUnauthorized, httpx.CodeUnauthorized, "Incorrect email or password.", nil)
	case err != nil:
		h.Log.Error("login error", "request_id", httpx.RequestID(r.Context()), "err", err)
		httpx.WriteError(w, r, http.StatusInternalServerError, httpx.CodeInternal, "An unexpected error occurred.", nil)
	default:
		h.Cookies.set(w, token)
		h.Log.Info("login succeeded", "request_id", httpx.RequestID(r.Context()), "user_id", p.UserID)
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"user": toDTO(p)})
	}
}

func (h *Handler) logout(w http.ResponseWriter, r *http.Request) {
	if err := h.Svc.Logout(r.Context(), h.Cookies.token(r)); err != nil {
		h.Log.Error("logout error", "request_id", httpx.RequestID(r.Context()), "err", err)
	}
	h.Cookies.clear(w)
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) me(w http.ResponseWriter, r *http.Request) {
	p, _ := auth.PrincipalFrom(r.Context())
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"user": toDTO(p)})
}

// toDTO lists permissions for UI display only; the server remains authoritative.
func toDTO(p auth.Principal) userDTO {
	perms := auth.PlatformPermissions(p.PlatformRole)
	if perms == nil {
		perms = []auth.Permission{}
	}
	return userDTO{ID: p.UserID, Email: p.Email, PlatformRole: string(p.PlatformRole), Permissions: perms}
}
