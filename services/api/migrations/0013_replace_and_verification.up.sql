-- 0013_replace_and_verification (M5a): certificate replacement and public verification.
--
-- Replacement: a corrected certificate is a NEW certificate (own number, public id, PDF) that points back to the
-- one it replaces. The old one stays valid until the new PDF is stored; completing the replacement revokes the
-- old one and links it to the new one in ONE transaction (old.replaced_by), so the student never has zero or two
-- valid certificates. Nothing is deleted or rewritten.
--
-- Public verification: the public route has no institute context. Instead of weakening row-level security it gets
-- a second, narrow SELECT policy that exposes exactly the row whose public id equals the session setting
-- app.public_id (a 256-bit value the caller already holds), and nothing else.

ALTER TABLE certificates
    ADD COLUMN replaces_id         uuid,
    ADD COLUMN replaced_by         uuid,
    ADD COLUMN replaced_by_public_id text CHECK (replaced_by_public_id ~ '^[A-Za-z0-9_-]{43}$'),
    ADD COLUMN replace_reason      text NOT NULL DEFAULT '' CHECK (char_length(replace_reason) <= 500),
    ADD CONSTRAINT certificates_replaces_fk    FOREIGN KEY (institute_id, replaces_id) REFERENCES certificates (institute_id, id) ON DELETE RESTRICT,
    ADD CONSTRAINT certificates_replaced_by_fk FOREIGN KEY (institute_id, replaced_by) REFERENCES certificates (institute_id, id) ON DELETE RESTRICT,
    ADD CONSTRAINT certificates_replaced_link_consistent CHECK ((replaced_by IS NULL) = (replaced_by_public_id IS NULL)),
    ADD CONSTRAINT certificates_replaced_is_revoked CHECK (replaced_by IS NULL OR status = 'revoked'),
    ADD CONSTRAINT certificates_no_self_replace CHECK (replaces_id IS DISTINCT FROM id AND replaced_by IS DISTINCT FROM id);

-- One valid certificate per enrollment. A PENDING replacement is exempt (the old certificate is still issued while
-- the new PDF is being made); an ISSUED one never is.
DROP INDEX certificates_one_live_per_enrollment;
CREATE UNIQUE INDEX certificates_one_live_per_enrollment ON certificates (institute_id, enrollment_id)
    WHERE status = 'issued' OR (status = 'pending' AND replaces_id IS NULL);
-- ... and an issued certificate can have only one replacement in progress.
CREATE UNIQUE INDEX certificates_one_pending_replacement ON certificates (institute_id, replaces_id)
    WHERE status = 'pending' AND replaces_id IS NOT NULL;
CREATE INDEX certificates_replaces_idx ON certificates (institute_id, replaces_id) WHERE replaces_id IS NOT NULL;

CREATE OR REPLACE FUNCTION certificates_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'certificates are never deleted' USING ERRCODE = '55000';
    END IF;
    IF NEW.id IS DISTINCT FROM OLD.id OR NEW.institute_id IS DISTINCT FROM OLD.institute_id
       OR NEW.certificate_number IS DISTINCT FROM OLD.certificate_number OR NEW.public_id IS DISTINCT FROM OLD.public_id
       OR NEW.student_id IS DISTINCT FROM OLD.student_id OR NEW.enrollment_id IS DISTINCT FROM OLD.enrollment_id
       OR NEW.template_id IS DISTINCT FROM OLD.template_id OR NEW.template_version IS DISTINCT FROM OLD.template_version
       OR NEW.snapshot IS DISTINCT FROM OLD.snapshot OR NEW.created_at IS DISTINCT FROM OLD.created_at
       OR NEW.replaces_id IS DISTINCT FROM OLD.replaces_id OR NEW.replace_reason IS DISTINCT FROM OLD.replace_reason THEN
        RAISE EXCEPTION 'issued certificate data is immutable' USING ERRCODE = '55000';
    END IF;
    -- The replacement link is written once, in the same statement that revokes the old certificate.
    IF (NEW.replaced_by IS DISTINCT FROM OLD.replaced_by OR NEW.replaced_by_public_id IS DISTINCT FROM OLD.replaced_by_public_id)
       AND NOT (OLD.replaced_by IS NULL AND OLD.status = 'issued' AND NEW.status = 'revoked' AND NEW.replaced_by IS NOT NULL) THEN
        RAISE EXCEPTION 'the replacement link of a certificate cannot change' USING ERRCODE = '55000';
    END IF;
    IF NEW.status IS DISTINCT FROM OLD.status AND NOT
       ((OLD.status = 'pending' AND NEW.status IN ('issued', 'failed')) OR (OLD.status = 'issued' AND NEW.status = 'revoked')) THEN
        RAISE EXCEPTION 'invalid certificate status change % -> %', OLD.status, NEW.status USING ERRCODE = '55000';
    END IF;
    IF OLD.status IN ('issued', 'revoked') AND (NEW.pdf_sha256 IS DISTINCT FROM OLD.pdf_sha256 OR NEW.pdf_size IS DISTINCT FROM OLD.pdf_size
       OR NEW.issued_at IS DISTINCT FROM OLD.issued_at OR NEW.issued_by IS DISTINCT FROM OLD.issued_by) THEN
        RAISE EXCEPTION 'issued certificate data is immutable' USING ERRCODE = '55000';
    END IF;
    IF OLD.status IN ('failed', 'revoked') AND NEW.status IS NOT DISTINCT FROM OLD.status AND ROW(NEW.*) IS DISTINCT FROM ROW(OLD.*) THEN
        RAISE EXCEPTION 'a % certificate cannot change', OLD.status USING ERRCODE = '55000';
    END IF;
    RETURN NEW;
END
$$;

-- Public lookup: exactly the certificate whose public id the caller presented, read-only.
CREATE POLICY public_lookup ON certificates FOR SELECT
    USING (public_id = nullif(current_setting('app.public_id', true), ''));

-- Privacy-conscious record of verifications: which certificate, what result, when. No IP address, no user agent.
CREATE TABLE verification_events (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    institute_id   uuid        NOT NULL REFERENCES institutes (id) ON DELETE RESTRICT,
    certificate_id uuid        NOT NULL,
    result         text        NOT NULL CHECK (result IN ('valid', 'revoked', 'replaced')),
    occurred_at    timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT verification_events_certificate_fk FOREIGN KEY (institute_id, certificate_id) REFERENCES certificates (institute_id, id) ON DELETE RESTRICT
);
CREATE INDEX verification_events_cert_idx ON verification_events (institute_id, certificate_id, occurred_at DESC);

ALTER TABLE verification_events ENABLE ROW LEVEL SECURITY;
ALTER TABLE verification_events FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON verification_events
    USING (institute_id = nullif(current_setting('app.institute_id', true), '')::uuid)
    WITH CHECK (institute_id = nullif(current_setting('app.institute_id', true), '')::uuid);

GRANT SELECT, INSERT ON verification_events TO certos_runtime;
