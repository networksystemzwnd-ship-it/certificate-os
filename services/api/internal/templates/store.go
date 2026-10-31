package templates

import (
	"context"

	"github.com/certificate-os/api/internal/audit"
	"github.com/certificate-os/api/internal/tenancy"
)

// Store persists templates. EVERY method takes the Scope and may only touch rows of that institute;
// another institute's id is indistinguishable from a missing one (ErrNotFound). Mutations run in one
// transaction with their audit event; the store fills in EntityID and InstituteID, and AddVersion/Create
// add the allocated version number to the event metadata ("version"). A failed call records nothing.
//
// Versions are append-only: there is no method that changes or deletes one.
type Store interface {
	// CreateTemplate creates the template with version 1. Duplicate active name: *ConflictError{Field:"name"}.
	CreateTemplate(ctx context.Context, s tenancy.Scope, name string, cfg Config, createdBy string, ev audit.Event) (Template, error)
	// GetTemplate returns the template with its current config and all version summaries (newest first).
	GetTemplate(ctx context.Context, s tenancy.Scope, id string) (Template, error)
	// ListTemplates returns up to Limit+1 templates (current config included, versions not).
	ListTemplates(ctx context.Context, s tenancy.Scope, p Page) ([]Template, error)
	RenameTemplate(ctx context.Context, s tenancy.Scope, id, name string, ev audit.Event) (Template, error)
	SetArchived(ctx context.Context, s tenancy.Scope, id string, archived bool, ev audit.Event) (Template, error)
	// AddVersion appends the next version (serialised per template). ErrArchived for an archived template,
	// ErrTooManyVersions at MaxVersions.
	AddVersion(ctx context.Context, s tenancy.Scope, id string, cfg Config, createdBy string, ev audit.Event) (Version, error)
	GetVersion(ctx context.Context, s tenancy.Scope, id string, number int) (Version, error)
}
