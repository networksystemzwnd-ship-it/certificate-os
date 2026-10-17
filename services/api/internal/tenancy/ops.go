package tenancy

import (
	"context"
	"errors"
	"strings"

	"github.com/certificate-os/api/internal/audit"
)

var ErrInvalidStatusChange = errors.New("institute status cannot be changed that way")

// OpsStore is the platform-operations view of institutes. It is NOT tenant-scoped and must only be
// wired behind platform-permission middleware. Status changes and their audit event commit together.
type OpsStore interface {
	ListInstitutes(ctx context.Context, q string, limit, offset int) ([]Institute, error)
	// ChangeInstituteStatus moves the institute from one of `from` to `to` under a row lock.
	// ErrNotFound for unknown/malformed ids; ErrInvalidStatusChange if the current status isn't in from.
	ChangeInstituteStatus(ctx context.Context, id string, from []InstituteStatus, to InstituteStatus, ev audit.Event) (Institute, error)
}

// OpsService implements suspend/reactivate (D-114: suspended = read-only, exports blocked).
// Closing is deliberately not available here: it is an out-of-band, irreversible-by-default action.
type OpsService struct{ Store OpsStore }

type ReasonError struct{ Message string }

func (e *ReasonError) Error() string { return e.Message }

func (s *OpsService) List(ctx context.Context, q string, limit, offset int) ([]Institute, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 100 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}
	return s.Store.ListInstitutes(ctx, strings.TrimSpace(q), limit, offset)
}

func (s *OpsService) event(actor, action, id, reqID string, meta map[string]any) audit.Event {
	return audit.Event{ActorType: audit.ActorUser, ActorUserID: actor, Action: action, EntityType: "institute",
		EntityID: id, InstituteID: id, RequestID: reqID, Metadata: meta}
}

func (s *OpsService) Suspend(ctx context.Context, actorID, id, reason, reqID string) (Institute, error) {
	reason = strings.TrimSpace(reason)
	if n := len([]rune(reason)); n < 10 || n > 2000 {
		return Institute{}, &ReasonError{"Give a reason of 10–2000 characters; it is recorded in the audit log."}
	}
	return s.Store.ChangeInstituteStatus(ctx, id, []InstituteStatus{InstituteActive}, InstituteSuspended,
		s.event(actorID, "institute.suspended", id, reqID, map[string]any{"from": "active", "to": "suspended", "reason": reason}))
}

func (s *OpsService) Reactivate(ctx context.Context, actorID, id, reqID string) (Institute, error) {
	return s.Store.ChangeInstituteStatus(ctx, id, []InstituteStatus{InstituteSuspended}, InstituteActive,
		s.event(actorID, "institute.reactivated", id, reqID, map[string]any{"from": "suspended", "to": "active"}))
}
