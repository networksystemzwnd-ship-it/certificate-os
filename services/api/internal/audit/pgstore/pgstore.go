// Package pgstore writes audit events to PostgreSQL.
package pgstore

import (
	"context"
	"encoding/json"

	"github.com/certificate-os/api/internal/audit"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Execer is satisfied by *pgxpool.Pool and pgx.Tx, so an event can be written in the SAME
// transaction as the change it describes.
type Execer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

type Writer struct{ Pool *pgxpool.Pool }

var _ audit.Writer = (*Writer)(nil)

func New(pool *pgxpool.Pool) *Writer { return &Writer{Pool: pool} }

const insertSQL = `INSERT INTO audit_logs
	(actor_type, actor_user_id, action, entity_type, entity_id, institute_id, request_id, metadata)
	VALUES ($1, nullif($2, '')::uuid, $3, $4, $5, nullif($6, '')::uuid, nullif($7, ''), $8::jsonb)`

func args(e audit.Event) ([]any, error) {
	if err := e.Validate(); err != nil {
		return nil, err
	}
	meta, err := json.Marshal(audit.Sanitize(e.Metadata))
	if err != nil {
		return nil, err
	}
	return []any{string(e.ActorType), e.ActorUserID, e.Action, e.EntityType, e.EntityID, e.InstituteID, e.RequestID, string(meta)}, nil
}

func (w *Writer) Record(ctx context.Context, e audit.Event) error {
	a, err := args(e)
	if err != nil {
		return err
	}
	_, err = w.Pool.Exec(ctx, insertSQL, a...)
	return err
}

// InsertTx appends an event inside an existing transaction (or pool).
func InsertTx(ctx context.Context, db Execer, e audit.Event) error {
	a, err := args(e)
	if err != nil {
		return err
	}
	_, err = db.Exec(ctx, insertSQL, a...)
	return err
}
