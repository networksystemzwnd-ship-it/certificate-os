-- Destructive: drops verification history and the replacement links (certificates themselves are kept).
DROP TABLE IF EXISTS verification_events;
DROP POLICY IF EXISTS public_lookup ON certificates;
DROP INDEX IF EXISTS certificates_one_pending_replacement;
DROP INDEX IF EXISTS certificates_replaces_idx;
DROP INDEX IF EXISTS certificates_one_live_per_enrollment;
ALTER TABLE certificates DROP CONSTRAINT IF EXISTS certificates_replaces_fk, DROP CONSTRAINT IF EXISTS certificates_replaced_by_fk,
    DROP CONSTRAINT IF EXISTS certificates_replaced_link_consistent, DROP CONSTRAINT IF EXISTS certificates_replaced_is_revoked,
    DROP CONSTRAINT IF EXISTS certificates_no_self_replace;
ALTER TABLE certificates DISABLE TRIGGER certificates_guard;
ALTER TABLE certificates DROP COLUMN IF EXISTS replaces_id, DROP COLUMN IF EXISTS replaced_by, DROP COLUMN IF EXISTS replaced_by_public_id, DROP COLUMN IF EXISTS replace_reason;
ALTER TABLE certificates ENABLE TRIGGER certificates_guard;
CREATE UNIQUE INDEX certificates_one_live_per_enrollment ON certificates (institute_id, enrollment_id) WHERE status IN ('pending', 'issued');
CREATE OR REPLACE FUNCTION certificates_guard() RETURNS trigger LANGUAGE plpgsql AS $$
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
