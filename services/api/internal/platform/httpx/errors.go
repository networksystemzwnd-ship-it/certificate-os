// Package httpx holds HTTP plumbing: request IDs, logging, panic recovery and the error envelope.
package httpx

import (
	"encoding/json"
	"net/http"
)

// Stable machine-readable error codes.
const (
	CodeInternal     = "internal_error"
	CodeNotFound     = "not_found"
	CodeValidation   = "validation_failed"
	CodeUnauthorized = "unauthenticated"
	CodeForbidden    = "forbidden"
	CodeConflict     = "conflict"
	CodeRateLimited  = "rate_limited"
	CodeMethod       = "method_not_allowed"
	CodeCSRF         = "csrf_rejected"
	CodeBadRequest   = "bad_request"
)

type ErrorBody struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"request_id,omitempty"`
	Details   any    `json:"details,omitempty"`
}

type errorEnvelope struct {
	Error ErrorBody `json:"error"`
}

func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// WriteError emits the consistent error envelope. Never pass internal error text as message.
func WriteError(w http.ResponseWriter, r *http.Request, status int, code, message string, details any) {
	WriteJSON(w, status, errorEnvelope{Error: ErrorBody{
		Code: code, Message: message, RequestID: RequestID(r.Context()), Details: details,
	}})
}

func NotFound(w http.ResponseWriter, r *http.Request) {
	WriteError(w, r, http.StatusNotFound, CodeNotFound, "The requested resource was not found.", nil)
}
