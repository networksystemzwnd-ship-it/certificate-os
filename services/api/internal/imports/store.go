package imports

import (
	"context"

	"github.com/certificate-os/api/internal/audit"
	"github.com/certificate-os/api/internal/tenancy"
)

// Store persists import jobs. Like every tenant-owned store, each method takes the Scope and another
// institute's job is indistinguishable from a missing one (ErrNotFound).
type Store interface {
	// Create stores a new job (the store assigns ID, timestamps) and its audit event atomically.
	Create(ctx context.Context, s tenancy.Scope, imp Import, ev audit.Event) (Import, error)
	Get(ctx context.Context, s tenancy.Scope, id string) (Import, error)
	// Save replaces the mutable state (status, mapping, date format, results, counts, rows, completed_at)
	// of a job only if its stored status is still `from`; otherwise ErrState. ev, when non-nil, is written
	// in the same transaction. The returned Import carries the stored values.
	Save(ctx context.Context, s tenancy.Scope, imp Import, from Status, ev *audit.Event) (Import, error)
}
