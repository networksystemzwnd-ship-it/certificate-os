package pgstore

import (
	"context"
	"encoding/json"

	"github.com/certificate-os/api/internal/audit"
)

var _ audit.Reader = (*Writer)(nil)

// List returns matching records newest first.
func (w *Writer) List(ctx context.Context, f audit.Filter) ([]audit.Record, error) {
	rows, err := w.Pool.Query(ctx,
		`SELECT id::text, occurred_at, actor_type, coalesce(actor_user_id::text, ''), action, entity_type, entity_id,
		        coalesce(institute_id::text, ''), coalesce(request_id, ''), metadata::text
		 FROM audit_logs
		 WHERE ($1::text = '' OR entity_type = $1) AND ($2::text = '' OR entity_id = $2) AND ($3::text = '' OR action = $3)
		 ORDER BY seq DESC LIMIT $4`,
		f.EntityType, f.EntityID, f.Action, f.EffectiveLimit())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []audit.Record
	for rows.Next() {
		var r audit.Record
		var actor, meta string
		if err := rows.Scan(&r.ID, &r.OccurredAt, &actor, &r.ActorUserID, &r.Action, &r.EntityType, &r.EntityID, &r.InstituteID, &r.RequestID, &meta); err != nil {
			return nil, err
		}
		r.ActorType = audit.ActorType(actor)
		_ = json.Unmarshal([]byte(meta), &r.Metadata)
		out = append(out, r)
	}
	return out, rows.Err()
}
