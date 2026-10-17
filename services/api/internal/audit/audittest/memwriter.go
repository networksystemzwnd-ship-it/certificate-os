// Package audittest provides an in-memory audit writer for tests.
package audittest

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/certificate-os/api/internal/audit"
)

type MemWriter struct {
	mu      sync.Mutex
	records []audit.Record
}

var _ audit.Writer = (*MemWriter)(nil)

func NewMemWriter() *MemWriter { return &MemWriter{} }

func (m *MemWriter) Record(_ context.Context, e audit.Event) error {
	if err := e.Validate(); err != nil {
		return err
	}
	e.Metadata = audit.Sanitize(e.Metadata)
	m.mu.Lock()
	defer m.mu.Unlock()
	m.records = append(m.records, audit.Record{ID: fmt.Sprintf("audit-%d", len(m.records)+1), OccurredAt: time.Now(), Event: e})
	return nil
}

// Records returns a copy of everything recorded, oldest first.
func (m *MemWriter) Records() []audit.Record {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]audit.Record(nil), m.records...)
}

// Actions lists the action names in order (handy in assertions).
func (m *MemWriter) Actions() []string {
	var out []string
	for _, r := range m.Records() {
		out = append(out, r.Action)
	}
	return out
}

// ForEntity returns records about one entity.
func (m *MemWriter) ForEntity(entityType, id string) []audit.Record {
	var out []audit.Record
	for _, r := range m.Records() {
		if r.EntityType == entityType && r.EntityID == id {
			out = append(out, r)
		}
	}
	return out
}
