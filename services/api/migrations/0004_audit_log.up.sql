-- 0004_audit_log: append-only audit trail (COS-009).
-- No foreign keys on purpose: audit rows must survive (and never block) changes to other tables.
CREATE TABLE audit_logs (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    -- seq gives a stable total order (now() is identical for every row of one transaction).
    seq           bigint GENERATED ALWAYS AS IDENTITY,
    occurred_at   timestamptz NOT NULL DEFAULT now(),
    actor_type    text        NOT NULL CHECK (actor_type IN ('user', 'system', 'cli')),
    actor_user_id uuid,
    action        text        NOT NULL CHECK (char_length(action) BETWEEN 3 AND 100),
    entity_type   text        NOT NULL,
    entity_id     text        NOT NULL,
    institute_id  uuid,
    request_id    text,
    metadata      jsonb       NOT NULL DEFAULT '{}',
    CONSTRAINT audit_user_actor_has_id CHECK (actor_type <> 'user' OR actor_user_id IS NOT NULL)
);
CREATE INDEX audit_logs_entity_idx    ON audit_logs (entity_type, entity_id, occurred_at);
CREATE INDEX audit_logs_actor_idx     ON audit_logs (actor_user_id, occurred_at) WHERE actor_user_id IS NOT NULL;
CREATE INDEX audit_logs_institute_idx ON audit_logs (institute_id, occurred_at) WHERE institute_id IS NOT NULL;

-- Append-only: rows can never be changed or removed through SQL, even by the table owner.
-- (Defense in depth: the runtime role will also lose UPDATE/DELETE/TRUNCATE — see ADR-001.)
CREATE FUNCTION audit_logs_block_mutation() RETURNS trigger AS $$
BEGIN
    RAISE EXCEPTION 'audit_logs is append-only';
END;
$$ LANGUAGE plpgsql;
CREATE TRIGGER audit_logs_no_update BEFORE UPDATE ON audit_logs
    FOR EACH ROW EXECUTE FUNCTION audit_logs_block_mutation();
CREATE TRIGGER audit_logs_no_delete BEFORE DELETE ON audit_logs
    FOR EACH ROW EXECUTE FUNCTION audit_logs_block_mutation();
