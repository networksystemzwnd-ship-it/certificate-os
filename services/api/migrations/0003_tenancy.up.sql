-- 0003_tenancy: institutes and memberships (COS-008).
-- Application state (draft/submitted/approved...) is a separate concept and arrives with COS-009;
-- an institute row represents an approved workspace and carries only its operational state.
CREATE TABLE institutes (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    slug       text        NOT NULL,
    name       text        NOT NULL,
    status     text        NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'suspended', 'closed')),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT institutes_slug_format CHECK (slug ~ '^[a-z0-9][a-z0-9-]{1,38}[a-z0-9]$'),
    CONSTRAINT institutes_name_length CHECK (char_length(btrim(name)) BETWEEN 2 AND 200)
);
CREATE UNIQUE INDEX institutes_slug_key ON institutes (slug);
CREATE TRIGGER institutes_set_updated_at BEFORE UPDATE ON institutes
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- Removal is soft (status='removed') so role changes and removals stay auditable.
-- ON DELETE RESTRICT: institutes and users are never hard-deleted through normal workflows.
CREATE TABLE institute_memberships (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    institute_id uuid        NOT NULL REFERENCES institutes (id) ON DELETE RESTRICT,
    user_id      uuid        NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
    role         text        NOT NULL CHECK (role IN ('owner', 'admin', 'staff')),
    grants       text[]      NOT NULL DEFAULT '{}',
    status       text        NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'removed')),
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    removed_at   timestamptz,
    CONSTRAINT memberships_unique UNIQUE (institute_id, user_id),
    -- Grants can never carry owner-only or platform permissions (mirrors auth.ValidateGrants).
    CONSTRAINT memberships_grants_safe CHECK (
        NOT (grants && ARRAY['staff.manage', 'institute.manage', 'platform.admin',
                             'ops.applications.read', 'ops.applications.review',
                             'ops.institutes.manage', 'ops.audit.read']::text[])
    )
);
CREATE INDEX memberships_user_active_idx ON institute_memberships (user_id) WHERE status = 'active';
CREATE TRIGGER memberships_set_updated_at BEFORE UPDATE ON institute_memberships
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();
