-- 0005_applications: institute onboarding applications (COS-009).
-- Approval creates the institute; until then the requested slug is only RESERVED here.
CREATE TABLE institute_applications (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    applicant_user_id uuid        NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
    status            text        NOT NULL DEFAULT 'draft'
        CHECK (status IN ('draft', 'submitted', 'under_review', 'changes_requested', 'approved', 'rejected')),
    name              text        NOT NULL DEFAULT '',
    slug              text        NOT NULL DEFAULT '',
    category          text        NOT NULL DEFAULT '',
    address_line1     text        NOT NULL DEFAULT '',
    address_line2     text        NOT NULL DEFAULT '',
    city              text        NOT NULL DEFAULT '',
    region            text        NOT NULL DEFAULT '',
    postal_code       text        NOT NULL DEFAULT '',
    country           text        NOT NULL DEFAULT '',
    official_email    text        NOT NULL DEFAULT '',
    phone             text        NOT NULL DEFAULT '',
    rep_name          text        NOT NULL DEFAULT '',
    rep_role          text        NOT NULL DEFAULT '',
    website           text        NOT NULL DEFAULT '',
    attested          boolean     NOT NULL DEFAULT false,
    version           integer     NOT NULL DEFAULT 1,
    submitted_at      timestamptz,
    reviewed_by       uuid        REFERENCES users (id) ON DELETE RESTRICT,
    reviewed_at       timestamptz,
    decision_reason   text        NOT NULL DEFAULT '',
    institute_id      uuid        REFERENCES institutes (id) ON DELETE RESTRICT,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now(),
    -- Anything past draft must be complete and attested (the service checks too; this is the backstop).
    CONSTRAINT applications_complete_when_submitted CHECK (
        status = 'draft' OR (
            attested AND name <> '' AND slug <> '' AND category <> '' AND address_line1 <> '' AND city <> ''
            AND country <> '' AND official_email <> '' AND phone <> '' AND rep_name <> '' AND rep_role <> ''
            AND submitted_at IS NOT NULL)),
    CONSTRAINT applications_approved_has_institute CHECK ((status = 'approved') = (institute_id IS NOT NULL))
);
-- One open application per user.
CREATE UNIQUE INDEX applications_one_open_per_user ON institute_applications (applicant_user_id)
    WHERE status NOT IN ('approved', 'rejected');
-- A slug is reserved while an application that may become an institute holds it.
CREATE UNIQUE INDEX applications_slug_claim ON institute_applications (slug)
    WHERE status IN ('submitted', 'under_review', 'changes_requested', 'approved');
CREATE INDEX applications_status_idx ON institute_applications (status, submitted_at);
CREATE INDEX applications_applicant_idx ON institute_applications (applicant_user_id, created_at DESC);
CREATE TRIGGER applications_set_updated_at BEFORE UPDATE ON institute_applications
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();
