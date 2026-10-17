package audittest

import (
	"context"

	"github.com/certificate-os/api/internal/audit"
)

var _ audit.Reader = (*MemWriter)(nil)

// List returns matching records newest first (insertion order reversed).
func (m *MemWriter) List(_ context.Context, f audit.Filter) ([]audit.Record, error) {
	recs := m.Records()
	var out []audit.Record
	for i := len(recs) - 1; i >= 0; i-- {
		r := recs[i]
		if (f.EntityType != "" && r.EntityType != f.EntityType) || (f.EntityID != "" && r.EntityID != f.EntityID) || (f.Action != "" && r.Action != f.Action) {
			continue
		}
		out = append(out, r)
		if len(out) == f.EffectiveLimit() {
			break
		}
	}
	return out, nil
}
