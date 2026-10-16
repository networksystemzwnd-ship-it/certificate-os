package httpx

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
)

const CSRFHeader = "X-CSRF-Protection"

// CORS allows exactly one web origin to call the API with credentials. Other origins receive
// no CORS headers (browsers then block the response). Preflights are answered here.
func CORS(allowedOrigin string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" && origin == allowedOrigin {
			h := w.Header()
			h.Add("Vary", "Origin")
			h.Set("Access-Control-Allow-Origin", origin)
			h.Set("Access-Control-Allow-Credentials", "true")
			h.Set("Access-Control-Expose-Headers", RequestIDHeader)
			if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
				h.Set("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
				h.Set("Access-Control-Allow-Headers", "Content-Type, "+RequestIDHeader+", "+CSRFHeader+", X-Institute-ID, Idempotency-Key")
				h.Set("Access-Control-Max-Age", "600")
				w.WriteHeader(http.StatusNoContent)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// CSRF protects cookie-authenticated, state-changing requests. Layered with SameSite=Lax cookies:
//  1. a custom header is required (cross-site forms cannot set it; cross-site fetch triggers a
//     preflight that CORS refuses), and
//  2. if an Origin header is present it must be the allowed web origin.
func CSRF(allowedOrigin string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			next.ServeHTTP(w, r)
			return
		}
		if r.Header.Get(CSRFHeader) != "1" {
			WriteError(w, r, http.StatusForbidden, CodeCSRF, "Request rejected.", nil)
			return
		}
		if o := r.Header.Get("Origin"); o != "" && o != allowedOrigin {
			WriteError(w, r, http.StatusForbidden, CodeCSRF, "Request rejected.", nil)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// DecodeJSON strictly decodes a small JSON body: size-limited, unknown fields rejected (so clients
// cannot smuggle fields such as "role"), and trailing data rejected.
func DecodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(strings.ToLower(ct), "application/json") {
		return errors.New("content type must be application/json")
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return errors.New("request body is not valid for this endpoint")
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return errors.New("request body must contain a single JSON object")
	}
	return nil
}
