package authhttp

import (
	"errors"
	"net/http"

	"github.com/certificate-os/api/internal/auth"
	"github.com/certificate-os/api/internal/platform/httpx"
)

// Authenticate attaches the principal to the context when a valid session cookie is present.
// It never rejects by itself; use RequireAuth for that. A store failure yields 500 (fail closed).
func (h *Handler) Authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if tok := h.Cookies.token(r); tok != "" {
			p, err := h.Svc.Authenticate(r.Context(), tok)
			switch {
			case err == nil:
				r = r.WithContext(auth.WithPrincipal(r.Context(), p))
			case errors.Is(err, auth.ErrUnauthenticated):
				// treated as anonymous
			default:
				h.Log.Error("authenticate error", "request_id", httpx.RequestID(r.Context()), "err", err)
				httpx.WriteError(w, r, http.StatusInternalServerError, httpx.CodeInternal, "An unexpected error occurred.", nil)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// RequireAuth returns 401 unless a principal is present.
func RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := auth.PrincipalFrom(r.Context()); !ok {
			httpx.WriteError(w, r, http.StatusUnauthorized, httpx.CodeUnauthorized, "Authentication required.", nil)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// RequirePlatformPermission returns 401 when anonymous and 403 when the permission is missing.
func RequirePlatformPermission(perm auth.Permission, next http.Handler) http.Handler {
	return RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, _ := auth.PrincipalFrom(r.Context())
		if !p.HasPlatformPermission(perm) {
			httpx.WriteError(w, r, http.StatusForbidden, httpx.CodeForbidden, "You do not have permission to do that.", nil)
			return
		}
		next.ServeHTTP(w, r)
	}))
}
