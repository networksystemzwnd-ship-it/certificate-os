// Package audit defines the append-only audit trail for sensitive operations (brain §4.1).
//
// Record WHO (actor), WHAT (action + entity), WHEN, and the request correlation ID. Metadata carries
// ids, states and reviewer-entered reasons only. Sanitize strips credential-like keys as a safety net,
// but callers must still never pass secrets or personal data.
//
// NOTE: this file was reconstructed from the contract required by internal/audit/pgstore,
// internal/audit/audittest and migration 0004_audit_log after the original was overwritten
// by mistake (see CHANGELOG). Behavior is covered by audit_test.go.
package audit

import (
	"context"
	"errors"
	"strings"
	"time"
)

type ActorType string

const (
	ActorUser   ActorType = "user"   // an authenticated person (requires ActorUserID)
	ActorSystem ActorType = "system" // background jobs and automated processes
	ActorCLI    ActorType = "cli"    // out-of-band administration (api seed/user/institute commands)
)

type Event struct {
	ActorType   ActorType
	ActorUserID string // required when ActorType == ActorUser
	Action      string // e.g. "application.approved" (3–100 chars, matches the DB CHECK)
	EntityType  string // e.g. "institute_application"
	EntityID    string
	InstituteID string // optional: the tenant the event concerns
	RequestID   string
	Metadata    map[string]any
}

func (e Event) Validate() error {
	switch {
	case len(e.Action) < 3 || len(e.Action) > 100:
		return errors.New("audit event action must be 3-100 characters")
	case e.EntityType == "" || e.EntityID == "":
		return errors.New("audit event needs an entity type and id")
	case e.ActorType != ActorUser && e.ActorType != ActorSystem && e.ActorType != ActorCLI:
		return errors.New("audit event needs a valid actor type")
	case e.ActorType == ActorUser && e.ActorUserID == "":
		return errors.New("user audit events need an actor user id")
	}
	return nil
}

// Record is a stored audit entry.
type Record struct {
	ID         string
	OccurredAt time.Time
	Event
}

type Writer interface {
	Record(ctx context.Context, e Event) error
}

// Filter narrows List results.
type Filter struct {
	EntityType string
	EntityID   string
	Action     string
	Limit      int // default 50, max 200
}

func (f Filter) EffectiveLimit() int {
	if f.Limit <= 0 {
		return 50
	}
	if f.Limit > 200 {
		return 200
	}
	return f.Limit
}

// Reader lists entries, newest first.
type Reader interface {
	List(ctx context.Context, f Filter) ([]Record, error)
}

var sensitiveKeyParts = []string{"password", "passwd", "secret", "token", "authorization", "cookie", "api_key", "apikey", "credential", "private_key"}

func sensitive(key string) bool {
	k := strings.ToLower(key)
	for _, p := range sensitiveKeyParts {
		if strings.Contains(k, p) {
			return true
		}
	}
	return false
}

// Sanitize returns a copy of m without credential-like keys (recursively through nested maps).
// A nil map yields an empty one.
func Sanitize(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		if sensitive(k) {
			continue
		}
		if nested, ok := v.(map[string]any); ok {
			v = Sanitize(nested)
		}
		out[k] = v
	}
	return out
}
