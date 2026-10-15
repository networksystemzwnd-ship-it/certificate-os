// Package health provides liveness and readiness endpoints.
// Liveness reports only that the process is up; readiness reports dependency health.
package health

import (
	"context"
	"net/http"
	"time"

	"github.com/certificate-os/api/internal/platform/httpx"
)

type Pinger interface {
	Ping(ctx context.Context) error
}

func Register(mux *http.ServeMux, db Pinger) {
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		checks := map[string]string{"database": "ok"}
		status := http.StatusOK
		if err := db.Ping(ctx); err != nil {
			// Do not leak driver error text.
			checks["database"] = "unavailable"
			status = http.StatusServiceUnavailable
		}
		body := map[string]any{"status": "ok", "checks": checks}
		if status != http.StatusOK {
			body["status"] = "unavailable"
		}
		httpx.WriteJSON(w, status, body)
	})
}
