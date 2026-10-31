-- 0011_certificates (M4b): issued certificates, their per-institute numbering and their stored PDFs.
-- TENANT-OWNED: institute_id everywhere, FORCE row-level security, composite foreign keys.
--
-- Lifecycle: pending -> issued | failed ; issued -> revoked. A certificate is only "issued" once its PDF
-- and checksum are stored. What was issued never changes: number, public id, links, template version and
-- the display snapshot are immutable (trigger), rows and PDFs are never deleted (no DELETE grant + trigger),
-- and the PDF table is insert-only. Revocation only flips the status and records who/when/why.

-- Composite targets for the new foreign keys.
ALTER TABLE enrollments ADD CONSTRAINT enrollments_institute_id_id_key UNIQUE (institute_id, id);
ALTER TABLE certificate_template_versions ADD CONSTRAINT certificate_template_versions_inst_tpl_num_key UNIQUE (institute_id, template_id, number);

-- Gapless-by-design counter per institute and calendar year (UTC). Numbers are allocated with a row lock.
CREATE TABLE certificate_counters (
    institute_id uuid    NOT NULL REFERENCES institutes (id) ON DELETE RESTRICT,
    year         integer NOT NULL CHECK (year BETWEEN 2000 AND 9999),
    last_number  integer NOT NULL CHECK (last_number >= 0),
    PRIMARY KEY (institute_id, year)
);

CREATE TABLE certificates (
    id                 uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    institute_id       uuid        NOT NULL REFERENCES institutes (id) ON DELETE RESTRICT,
    certificate_number text        NOT NULL CHECK (certificate_number ~ '^CERT-[0-9]{4}-[0-9]{6,}$'),
    -- High-entropy token for the public verification link (not an authorisation secret, D-146); 32 random bytes, base64url.
    public_id          text        NOT NULL UNIQUE CHECK (public_id ~ '^[A-Za-z0-9_-]{43}$'),
    student_id         uuid        NOT NULL,
    enrollment_id      uuid        NOT NULL,
    template_id        uuid        NOT NULL,
    template_version   integer     NOT NULL,
    snapshot           jsonb       NOT NULL CHECK (jsonb_typeof(snapshot) = 'object' AND octet_length(snapshot::text) <= 16000),
    status             text        NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'issued', 'failed', 'revoked')),
    pdf_sha256         text        CHECK (pdf_sha256 ~ '^[0-9a-f]{64}$'),
    pdf_size           integer     CHECK (pdf_size > 0),
    issued_by          uuid        REFERENCES users (id) ON DELETE RESTRICT,
    created_at         timestamptz NOT NULL DEFAULT now(),
    issued_at          timestamptz,
    failed_at          timestamptz,
    failure            text        NOT NULL DEFAULT '' CHECK (char_length(failure) <= 200),
    revoked_at         timestamptz,
    revoked_by         uuid        REFERENCES users (id) ON DELETE RESTRICT,
    revoke_reason      text        NOT NULL DEFAULT '' CHECK (char_length(revoke_reason) <= 500),
    CONSTRAINT certificates_institute_id_id_key UNIQUE (institute_id, id),
    CONSTRAINT certificates_number_key UNIQUE (institute_id, certificate_number),
    CONSTRAINT certificates_student_fk    FOREIGN KEY (institute_id, student_id)    REFERENCES students (institute_id, id) ON DELETE RESTRICT,
    CONSTRAINT certificates_enrollment_fk FOREIGN KEY (institute_id, enrollment_id) REFERENCES enrollments (institute_id, id) ON DELETE RESTRICT,
    CONSTRAINT certificates_template_fk   FOREIGN KEY (institute_id, template_id, template_version)
        REFERENCES certificate_template_versions (institute_id, template_id, number) ON DELETE RESTRICT,
    CONSTRAINT certificates_state_consistent CHECK (
        (status = 'pending' AND pdf_sha256 IS NULL AND issued_at IS NULL AND failed_at IS NULL AND revoked_at IS NULL) OR
        (status = 'issued'  AND pdf_sha256 IS NOT NULL AND pdf_size IS NOT NULL AND issued_at IS NOT NULL AND failed_at IS NULL AND revoked_at IS NULL) OR
        (status = 'failed'  AND pdf_sha256 IS NULL AND failed_at IS NOT NULL AND issued_at IS NULL AND revoked_at IS NULL) OR
        (status = 'revoked' AND pdf_sha256 IS NOT NULL AND issued_at IS NOT NULL AND revoked_at IS NOT NULL AND revoked_by IS NOT NULL))
);
-- One live certificate per enrollment: a double click or retry cannot issue twice. failed/revoked free the slot.
CREATE UNIQUE INDEX certificates_one_live_per_enrollment ON certificates (institute_id, enrollment_id) WHERE status IN ('pending', 'issued');
CREATE INDEX certificates_list_idx ON certificates (institute_id, created_at DESC, id);
CREATE INDEX certificates_student_idx ON certificates (institute_id, student_id);

-- The PDF bytes live apart so list queries never load them. Insert-only.
CREATE TABLE certificate_files (
    institute_id   uuid        NOT NULL,
    certificate_id uuid        PRIMARY KEY,
    content        bytea       NOT NULL CHECK (octet_length(content) BETWEEN 1 AND 5242880),
    created_at     timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT certificate_files_certificate_fk FOREIGN KEY (institute_id, certificate_id)
        REFERENCES certificates (institute_id, id) ON DELETE RESTRICT
);

CREATE FUNCTION certificates_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'certificates are never deleted' USING ERRCODE = '55000';
    END IF;
    IF NEW.id IS DISTINCT FROM OLD.id OR NEW.institute_id IS DISTINCT FROM OLD.institute_id
       OR NEW.certificate_number IS DISTINCT FROM OLD.certificate_number OR NEW.public_id IS DISTINCT FROM OLD.public_id
       OR NEW.student_id IS DISTINCT FROM OLD.student_id OR NEW.enrollment_id IS DISTINCT FROM OLD.enrollment_id
       OR NEW.template_id IS DISTINCT FROM OLD.template_id OR NEW.template_version IS DISTINCT FROM OLD.template_version
       OR NEW.snapshot IS DISTINCT FROM OLD.snapshot OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
        RAISE EXCEPTION 'issued certificate data is immutable' USING ERRCODE = '55000';
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
CREATE TRIGGER certificates_guard BEFORE UPDATE OR DELETE ON certificates FOR EACH ROW EXECUTE FUNCTION certificates_guard();

CREATE FUNCTION certificate_files_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'stored certificate PDFs are immutable' USING ERRCODE = '55000';
END
$$;
CREATE TRIGGER certificate_files_no_change BEFORE UPDATE OR DELETE ON certificate_files FOR EACH ROW EXECUTE FUNCTION certificate_files_immutable();

-- Row-level security (ADR-001). An unset or empty app.institute_id matches nothing (fail closed).
DO $$
DECLARE t text;
BEGIN
    FOREACH t IN ARRAY ARRAY['certificate_counters', 'certificates', 'certificate_files'] LOOP
        EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
        EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
        EXECUTE format($p$CREATE POLICY tenant_isolation ON %I
            USING (institute_id = nullif(current_setting('app.institute_id', true), '')::uuid)
            WITH CHECK (institute_id = nullif(current_setting('app.institute_id', true), '')::uuid)$p$, t);
    END LOOP;
END
$$;

GRANT SELECT, INSERT, UPDATE ON certificate_counters, certificates TO certos_runtime;
GRANT SELECT, INSERT ON certificate_files TO certos_runtime;
