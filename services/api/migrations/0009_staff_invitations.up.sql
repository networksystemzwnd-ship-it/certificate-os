-- 0009_staff_invitations (M3c): invite staff to an institute by one-time link.
--
-- GLOBAL table (listed in globalTables in tenantdb/rls_test.go, like institute_memberships): accepting an
-- invitation happens BEFORE the invitee has any tenant context, so a tenant-scoped RLS policy cannot apply to
-- that one operation. Isolation here rests on (a) every tenant-facing statement filtering by the Scope's
-- institute id, (b) the token being a 256-bit random secret stored only as a SHA-256 hash, and (c) acceptance
-- requiring the signed-in user's email to equal the invited email.
CREATE TABLE institute_invitations (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    institute_id uuid        NOT NULL REFERENCES institutes (id) ON DELETE RESTRICT,
    email        text        NOT NULL CHECK (char_length(email) BETWEEN 3 AND 254 AND email = lower(email)),
    role         text        NOT NULL CHECK (role IN ('admin', 'staff')),   -- owners are never invited
    grants       text[]      NOT NULL DEFAULT '{}',
    token_hash   text        NOT NULL CHECK (char_length(token_hash) = 64),
    invited_by   uuid        NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
    status       text        NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'accepted', 'revoked')),
    created_at   timestamptz NOT NULL DEFAULT now(),
    expires_at   timestamptz NOT NULL,
    accepted_at  timestamptz,
    accepted_by  uuid        REFERENCES users (id) ON DELETE RESTRICT,
    revoked_at   timestamptz,
    CONSTRAINT invitations_token_hash_key UNIQUE (token_hash),
    CONSTRAINT invitations_expiry_after_creation CHECK (expires_at > created_at),
    CONSTRAINT invitations_state_consistent CHECK (
        (status = 'accepted') = (accepted_at IS NOT NULL AND accepted_by IS NOT NULL)
        AND (status = 'revoked') = (revoked_at IS NOT NULL)),
    CONSTRAINT invitations_grants_safe CHECK (
        NOT (grants && ARRAY['staff.manage', 'institute.manage', 'platform.admin',
                             'ops.applications.read', 'ops.applications.review',
                             'ops.institutes.manage', 'ops.audit.read']::text[])
    ),
    CONSTRAINT invitations_admin_has_no_grants CHECK (role <> 'admin' OR cardinality(grants) = 0)
);
-- One open invitation per (institute, email); expired ones are revoked when a new one is created.
CREATE UNIQUE INDEX invitations_pending_email_key ON institute_invitations (institute_id, email) WHERE status = 'pending';
CREATE INDEX invitations_institute_idx ON institute_invitations (institute_id, created_at DESC);

GRANT SELECT, INSERT, UPDATE ON institute_invitations TO certos_runtime;
