// Package templatestest provides an in-memory templates.Store and the shared store contract.
package templatestest

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/certificate-os/api/internal/audit"
	"github.com/certificate-os/api/internal/templates"
	"github.com/certificate-os/api/internal/tenancy"
)

type tpl struct {
	t        templates.Template
	versions []templates.Version // index = number-1
}

type MemStore struct {
	mu    sync.Mutex
	n     int
	data  map[string]map[string]*tpl // institute id -> template id (the in-memory equivalent of RLS)
	Audit audit.Writer
	Now   func() time.Time
}

var _ templates.Store = (*MemStore)(nil)

func NewMemStore(w audit.Writer) *MemStore {
	return &MemStore{data: map[string]map[string]*tpl{}, Audit: w, Now: time.Now}
}

func (m *MemStore) tenant(s tenancy.Scope) map[string]*tpl {
	d := m.data[s.InstituteID()]
	if d == nil {
		d = map[string]*tpl{}
		m.data[s.InstituteID()] = d
	}
	return d
}

func (m *MemStore) record(ctx context.Context, s tenancy.Scope, ev audit.Event, id string, version int) error {
	ev.EntityID, ev.InstituteID = id, s.InstituteID()
	if version > 0 {
		meta := map[string]any{}
		for k, v := range ev.Metadata {
			meta[k] = v
		}
		meta["version"] = version
		ev.Metadata = meta
	}
	if m.Audit == nil {
		return nil
	}
	return m.Audit.Record(ctx, ev)
}

func (m *MemStore) nameTaken(d map[string]*tpl, name, except string) bool {
	for id, x := range d {
		if id != except && x.t.ArchivedAt == nil && strings.EqualFold(x.t.Name, name) {
			return true
		}
	}
	return false
}

func view(x *tpl, withVersions bool) templates.Template {
	out := x.t
	out.Current = x.versions[len(x.versions)-1]
	if withVersions {
		for i := len(x.versions) - 1; i >= 0; i-- {
			v := x.versions[i]
			out.Versions = append(out.Versions, templates.VersionSummary{Number: v.Number, CreatedBy: v.CreatedBy, CreatedAt: v.CreatedAt})
		}
	}
	return out
}

func (m *MemStore) CreateTemplate(ctx context.Context, s tenancy.Scope, name string, cfg templates.Config, by string, ev audit.Event) (templates.Template, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d := m.tenant(s)
	if m.nameTaken(d, name, "") {
		return templates.Template{}, &templates.ConflictError{Field: "name"}
	}
	m.n++
	now := m.Now()
	x := &tpl{t: templates.Template{ID: fmt.Sprintf("tpl-%d", m.n), Name: name, CreatedAt: now, UpdatedAt: now},
		versions: []templates.Version{{Number: 1, Config: cfg, CreatedBy: by, CreatedAt: now}}}
	if err := m.record(ctx, s, ev, x.t.ID, 1); err != nil {
		return templates.Template{}, err
	}
	d[x.t.ID] = x
	return view(x, true), nil
}

func (m *MemStore) GetTemplate(_ context.Context, s tenancy.Scope, id string) (templates.Template, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if x, ok := m.tenant(s)[id]; ok {
		return view(x, true), nil
	}
	return templates.Template{}, templates.ErrNotFound
}

func (m *MemStore) ListTemplates(_ context.Context, s tenancy.Scope, p templates.Page) ([]templates.Template, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var all []templates.Template
	q := strings.ToLower(p.Q)
	for _, x := range m.tenant(s) {
		archived := x.t.ArchivedAt != nil
		if (p.Archived == templates.ActiveOnly && archived) || (p.Archived == templates.ArchivedOnly && !archived) {
			continue
		}
		if q != "" && !strings.Contains(strings.ToLower(x.t.Name), q) {
			continue
		}
		all = append(all, view(x, false))
	}
	sort.Slice(all, func(i, j int) bool {
		a, b := strings.ToLower(all[i].Name), strings.ToLower(all[j].Name)
		if a != b {
			return a < b
		}
		return all[i].ID < all[j].ID
	})
	if p.Offset >= len(all) {
		return nil, nil
	}
	all = all[p.Offset:]
	if len(all) > p.Limit+1 {
		all = all[:p.Limit+1]
	}
	return all, nil
}

func (m *MemStore) RenameTemplate(ctx context.Context, s tenancy.Scope, id, name string, ev audit.Event) (templates.Template, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d := m.tenant(s)
	x, ok := d[id]
	if !ok {
		return templates.Template{}, templates.ErrNotFound
	}
	if x.t.ArchivedAt == nil && m.nameTaken(d, name, id) {
		return templates.Template{}, &templates.ConflictError{Field: "name"}
	}
	if err := m.record(ctx, s, ev, id, 0); err != nil {
		return templates.Template{}, err
	}
	x.t.Name, x.t.UpdatedAt = name, m.Now()
	return view(x, true), nil
}

func (m *MemStore) SetArchived(ctx context.Context, s tenancy.Scope, id string, archived bool, ev audit.Event) (templates.Template, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d := m.tenant(s)
	x, ok := d[id]
	if !ok {
		return templates.Template{}, templates.ErrNotFound
	}
	if !archived && x.t.ArchivedAt != nil && m.nameTaken(d, x.t.Name, id) {
		return templates.Template{}, &templates.ConflictError{Field: "name"}
	}
	if err := m.record(ctx, s, ev, id, 0); err != nil {
		return templates.Template{}, err
	}
	now := m.Now()
	if archived {
		if x.t.ArchivedAt == nil {
			x.t.ArchivedAt = &now
		}
	} else {
		x.t.ArchivedAt = nil
	}
	x.t.UpdatedAt = now
	return view(x, true), nil
}

func (m *MemStore) AddVersion(ctx context.Context, s tenancy.Scope, id string, cfg templates.Config, by string, ev audit.Event) (templates.Version, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	x, ok := m.tenant(s)[id]
	if !ok {
		return templates.Version{}, templates.ErrNotFound
	}
	if x.t.ArchivedAt != nil {
		return templates.Version{}, templates.ErrArchived
	}
	if len(x.versions) >= templates.MaxVersions {
		return templates.Version{}, templates.ErrTooManyVersions
	}
	v := templates.Version{Number: len(x.versions) + 1, Config: cfg, CreatedBy: by, CreatedAt: m.Now()}
	if err := m.record(ctx, s, ev, id, v.Number); err != nil {
		return templates.Version{}, err
	}
	x.versions = append(x.versions, v)
	x.t.UpdatedAt = v.CreatedAt
	return v, nil
}

func (m *MemStore) GetVersion(_ context.Context, s tenancy.Scope, id string, number int) (templates.Version, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	x, ok := m.tenant(s)[id]
	if !ok || number < 1 || number > len(x.versions) {
		return templates.Version{}, templates.ErrNotFound
	}
	return x.versions[number-1], nil
}
