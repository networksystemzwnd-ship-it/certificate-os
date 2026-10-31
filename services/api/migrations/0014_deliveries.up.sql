-- 0014_deliveries (M5b): email delivery of issued certificates. TENANT-OWNED (institute_id, FORCE row-level
-- security, composite foreign keys). Delivery is independent of issuing: a failed delivery never touches the
-- certificate, and a certificate can be delivered (or re-delivered) many times.
--
-- Statuses are honest: 'accepted' means the mail provider ACCEPTED the message for delivery (SMTP 250), not that
-- it reached an inbox. There is no 'delivered' status because nothing here can know that.
--
-- No personal data beyond the recipient address is stored; provider responses (which can echo addresses) are
-- reduced to a short code before they reach this schema. Rows are never deleted (the runtime role has no DELETE).

CREATE TABLE deliveries (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    institute_id    uuid        NOT NULL REFERENCES institutes (id) ON DELETE RESTRICT,
    certificate_id  uuid        NOT NULL,
    channel         text        NOT NULL DEFAULT 'email' CHECK (channel = 'email'),
    recipient       text        NOT NULL CHECK (char_length(recipient) BETWEEN 3 AND 254 AND recipient = lower(recipient)),
    status          text        NOT NULL DEFAULT 'queued' CHECK (status IN ('queued', 'sending', 'accepted', 'failed')),
    attempts        integer     NOT NULL DEFAULT 0 CHECK (attempts BETWEEN 0 AND 50),
    max_attempts    integer     NOT NULL DEFAULT 5 CHECK (max_attempts BETWEEN 1 AND 50),
    next_attempt_at timestamptz,
    last_error      text        NOT NULL DEFAULT '' CHECK (char_length(last_error) <= 40),
    created_by      uuid        NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    finished_at     timestamptz,
    runner_token    text        NOT NULL DEFAULT '' CHECK (char_length(runner_token) <= 64),
    lease_until     timestamptz,
    CONSTRAINT deliveries_institute_id_id_key UNIQUE (institute_id, id),
    CONSTRAINT deliveries_certificate_fk FOREIGN KEY (institute_id, certificate_id) REFERENCES certificates (institute_id, id) ON DELETE RESTRICT,
    CONSTRAINT deliveries_finished_consistent CHECK ((status IN ('queued', 'sending')) = (finished_at IS NULL)),
    CONSTRAINT deliveries_queued_has_due CHECK ((status = 'queued') = (next_attempt_at IS NOT NULL)),
    CONSTRAINT deliveries_failed_has_error CHECK (status <> 'failed' OR last_error <> ''),
    CONSTRAINT deliveries_attempts_within_max CHECK (attempts <= max_attempts)
);
-- A certificate has at most one delivery in flight: a double click cannot send two copies.
CREATE UNIQUE INDEX deliveries_one_active ON deliveries (institute_id, certificate_id) WHERE status IN ('queued', 'sending');
CREATE INDEX deliveries_certificate_idx ON deliveries (institute_id, certificate_id, created_at DESC, id);
CREATE INDEX deliveries_list_idx ON deliveries (institute_id, created_at DESC, id);

-- Every attempt, kept for good (insert-only).
CREATE TABLE delivery_attempts (
    institute_id uuid        NOT NULL,
    delivery_id  uuid        NOT NULL,
    attempt_no   integer     NOT NULL CHECK (attempt_no >= 1),
    provider     text        NOT NULL CHECK (char_length(provider) BETWEEN 1 AND 30),
    status       text        NOT NULL CHECK (status IN ('accepted', 'failed')),
    error_code   text        NOT NULL DEFAULT '' CHECK (char_length(error_code) <= 40),
    started_at   timestamptz NOT NULL,
    finished_at  timestamptz NOT NULL,
    PRIMARY KEY (delivery_id, attempt_no),
    CONSTRAINT delivery_attempts_delivery_fk FOREIGN KEY (institute_id, delivery_id) REFERENCES deliveries (institute_id, id) ON DELETE RESTRICT,
    CONSTRAINT delivery_attempts_error_consistent CHECK ((status = 'failed') = (error_code <> ''))
);

-- Work queue for the background worker. It holds NO personal data (ids and times only), so the worker may scan it
-- across institutes through the narrow worker_scan policy below; everything else goes through tenant_isolation.
-- One row per delivery; "active" turns false when the delivery is finished (rows are not deleted).
CREATE TABLE delivery_queue (
    delivery_id  uuid PRIMARY KEY,
    institute_id uuid        NOT NULL,
    active       boolean     NOT NULL DEFAULT true,
    due_at       timestamptz NOT NULL,
    lease_until  timestamptz,
    CONSTRAINT delivery_queue_delivery_fk FOREIGN KEY (institute_id, delivery_id) REFERENCES deliveries (institute_id, id) ON DELETE RESTRICT
);
CREATE INDEX delivery_queue_due_idx ON delivery_queue (due_at) WHERE active;

-- Guard: identity columns never change, an accepted delivery is final, status moves only along the state machine.
CREATE OR REPLACE FUNCTION deliveries_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.id IS DISTINCT FROM OLD.id OR NEW.institute_id IS DISTINCT FROM OLD.institute_id
       OR NEW.certificate_id IS DISTINCT FROM OLD.certificate_id OR NEW.channel IS DISTINCT FROM OLD.channel
       OR NEW.created_by IS DISTINCT FROM OLD.created_by OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
        RAISE EXCEPTION 'delivery identity is immutable' USING ERRCODE = '55000';
    END IF;
    IF OLD.status = 'accepted' THEN
        RAISE EXCEPTION 'an accepted delivery cannot change' USING ERRCODE = '55000';
    END IF;
    IF NEW.status IS DISTINCT FROM OLD.status AND NOT
       ((OLD.status = 'queued' AND NEW.status IN ('sending', 'failed'))
     OR (OLD.status = 'sending' AND NEW.status IN ('queued', 'accepted', 'failed'))
     OR (OLD.status = 'failed' AND NEW.status = 'queued')) THEN
        RAISE EXCEPTION 'invalid delivery status change % -> %', OLD.status, NEW.status USING ERRCODE = '55000';
    END IF;
    IF NEW.attempts < OLD.attempts THEN
        RAISE EXCEPTION 'delivery attempts never decrease' USING ERRCODE = '55000';
    END IF;
    RETURN NEW;
END
$$;
CREATE TRIGGER deliveries_guard BEFORE UPDATE ON deliveries FOR EACH ROW EXECUTE FUNCTION deliveries_guard();

DO $$
DECLARE t text;
BEGIN
    FOREACH t IN ARRAY ARRAY['deliveries', 'delivery_attempts', 'delivery_queue'] LOOP
        EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
        EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
        EXECUTE format($p$CREATE POLICY tenant_isolation ON %I
            USING (institute_id = nullif(current_setting('app.institute_id', true), '')::uuid)
            WITH CHECK (institute_id = nullif(current_setting('app.institute_id', true), '')::uuid)$p$, t);
    END LOOP;
END
$$;

-- The worker finds due work across institutes. SELECT only, only this PII-free table, only while the transaction
-- has set app.worker = 'scan'. This is defence in depth like public_lookup (0013): the application sets the GUC,
-- but what it unlocks is ids and timestamps.
CREATE POLICY worker_scan ON delivery_queue FOR SELECT
    USING (current_setting('app.worker', true) = 'scan');

GRANT SELECT, INSERT, UPDATE ON deliveries, delivery_queue TO certos_runtime;
GRANT SELECT, INSERT ON delivery_attempts TO certos_runtime;
