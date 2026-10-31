-- 0010_certificate_templates (M4a): certificate templates and their IMMUTABLE versions.
-- TENANT-OWNED: institute_id on every table, FORCE row-level security, composite foreign keys so a version
-- can never point at another institute's template.
--
-- A template version is never changed or removed (an issued certificate must stay reproducible): the
-- runtime role gets SELECT and INSERT only on versions, and a trigger blocks UPDATE/DELETE even for the
-- table owner. A template is renamed/archived (UPDATE) but never hard-deleted.

CREATE TABLE certificate_templates (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    institute_id    uuid        NOT NULL REFERENCES institutes (id) ON DELETE RESTRICT,
    name            text        NOT NULL CHECK (char_length(btrim(name)) BETWEEN 2 AND 100),
    current_version integer     NOT NULL DEFAULT 1 CHECK (current_version BETWEEN 1 AND 100),
    archived_at     timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT certificate_templates_institute_id_id_key UNIQUE (institute_id, id)
);
CREATE UNIQUE INDEX certificate_templates_active_name_key ON certificate_templates (institute_id, lower(name)) WHERE archived_at IS NULL;
CREATE TRIGGER certificate_templates_set_updated_at BEFORE UPDATE ON certificate_templates FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE certificate_template_versions (
    institute_id uuid        NOT NULL,
    template_id  uuid        NOT NULL,
    number       integer     NOT NULL CHECK (number BETWEEN 1 AND 100),
    config       jsonb       NOT NULL CHECK (jsonb_typeof(config) = 'object' AND octet_length(config::text) <= 8000),
    created_by   uuid        REFERENCES users (id) ON DELETE RESTRICT,
    created_at   timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (template_id, number),
    CONSTRAINT certificate_template_versions_template_fk FOREIGN KEY (institute_id, template_id)
        REFERENCES certificate_templates (institute_id, id) ON DELETE RESTRICT
);
CREATE INDEX certificate_template_versions_institute_idx ON certificate_template_versions (institute_id, template_id);

CREATE FUNCTION certificate_template_versions_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'certificate template versions are immutable' USING ERRCODE = '55000';
END
$$;
CREATE TRIGGER certificate_template_versions_no_change BEFORE UPDATE OR DELETE ON certificate_template_versions
    FOR EACH ROW EXECUTE FUNCTION certificate_template_versions_immutable();

-- Row-level security (ADR-001). An unset or empty app.institute_id matches nothing (fail closed).
DO $$
DECLARE t text;
BEGIN
    FOREACH t IN ARRAY ARRAY['certificate_templates', 'certificate_template_versions'] LOOP
        EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
        EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
        EXECUTE format($p$CREATE POLICY tenant_isolation ON %I
            USING (institute_id = nullif(current_setting('app.institute_id', true), '')::uuid)
            WITH CHECK (institute_id = nullif(current_setting('app.institute_id', true), '')::uuid)$p$, t);
    END LOOP;
END
$$;

GRANT SELECT, INSERT, UPDATE ON certificate_templates TO certos_runtime;
GRANT SELECT, INSERT ON certificate_template_versions TO certos_runtime;
