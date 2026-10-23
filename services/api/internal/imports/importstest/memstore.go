// Package importstest provides an in-memory imports.Store and the shared store contract.
package importstest

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/certificate-os/api/internal/audit"
	"github.com/certificate-os/api/internal/imports"
	"github.com/certificate-os/api/internal/tenancy"
)

type MemStore struct {
	mu    sync.Mutex
	n     int
	data  map[string]map[string]imports.Import // institute id -> import id: the in-memory equivalent of RLS
	Audit audit.Writer
	Now   func() time.Time
}

var _ imports.Store = (*MemStore)(nil)

func NewMemStore(w audit.Writer) *MemStore {
	return &MemStore{data: map[string]map[string]imports.Import{}, Audit: w, Now: time.Now}
}

func clone(i imports.Import) imports.Import {
	c := i
	c.Headers = append([]string(nil), i.Headers...)
	c.Rows = make([][]string, len(i.Rows))
	for k, r := range i.Rows {
		c.Rows[k] = append([]string{}, r...)
	}
	c.Mapping = imports.Mapping{}
	for k, v := range i.Mapping {
		c.Mapping[k] = v
	}
	c.Results = append([]imports.RowResult{}, i.Results...)
	if i.CompletedAt != nil {
		t := *i.CompletedAt
		c.CompletedAt = &t
	}
	return c
}

func (m *MemStore) record(ctx context.Context, s tenancy.Scope, ev audit.Event, id string) error {
	ev.EntityID, ev.InstituteID = id, s.InstituteID()
	if m.Audit == nil {
		return nil
	}
	return m.Audit.Record(ctx, ev)
}

func (m *MemStore) Create(ctx context.Context, s tenancy.Scope, imp imports.Import, ev audit.Event) (imports.Import, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.n++
	imp = clone(imp)
	imp.ID = fmt.Sprintf("import-%d", m.n)
	imp.CreatedAt = m.Now()
	imp.UpdatedAt = imp.CreatedAt
	if err := m.record(ctx, s, ev, imp.ID); err != nil {
		return imports.Import{}, err
	}
	if m.data[s.InstituteID()] == nil {
		m.data[s.InstituteID()] = map[string]imports.Import{}
	}
	m.data[s.InstituteID()][imp.ID] = imp
	return clone(imp), nil
}

func (m *MemStore) Get(_ context.Context, s tenancy.Scope, id string) (imports.Import, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	imp, ok := m.data[s.InstituteID()][id]
	if !ok {
		return imports.Import{}, imports.ErrNotFound
	}
	return clone(imp), nil
}

func (m *MemStore) Save(ctx context.Context, s tenancy.Scope, imp imports.Import, from imports.Status, ev *audit.Event) (imports.Import, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, ok := m.data[s.InstituteID()][imp.ID]
	if !ok {
		return imports.Import{}, imports.ErrNotFound
	}
	if cur.Status != from {
		return imports.Import{}, imports.ErrState
	}
	if ev != nil {
		if err := m.record(ctx, s, *ev, imp.ID); err != nil {
			return imports.Import{}, err
		}
	}
	imp = clone(imp)
	imp.CreatedAt, imp.CreatedBy, imp.Filename, imp.Headers = cur.CreatedAt, cur.CreatedBy, cur.Filename, cur.Headers
	imp.UpdatedAt = m.Now()
	m.data[s.InstituteID()][imp.ID] = imp
	return clone(imp), nil
}
