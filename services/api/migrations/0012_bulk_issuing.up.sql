-- 0012_bulk_issuing (M4c): bulk certificate jobs and their per-enrollment items. TENANT-OWNED (institute_id,
-- FORCE row-level security, composite foreign keys). Jobs and items are never deleted.
--
-- A job issues one template version to every eligible enrollment of one batch. The eligible enrollments are
-- fixed when the job is created (one item each), so progress is exact and a retry never widens the job.
-- At most ONE job per institute is queued/processing at a time (partial unique index): a double click or a
-- second browser tab cannot start overlapping jobs. Duplicate certificates are impossible regardless, because
-- every item goes through the normal issuing path with its one-live-certificate-per-enrollment index (0011).

CREATE TABLE bulk_jobs (
    id                 uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    institute_id       uuid        NOT NULL REFERENCES institutes (id) ON DELETE RESTRICT,
    batch_id           uuid        NOT NULL,
    template_id        uuid        NOT NULL,
    template_version   integer     NOT NULL CHECK (template_version >= 1),
    status             text        NOT NULL DEFAULT 'queued'
                       CHECK (status IN ('queued', 'processing', 'completed', 'completed_with_errors', 'failed', 'cancelled')),
    total              integer     NOT NULL CHECK (total BETWEEN 1 AND 500),
    skipped_issued     integer     NOT NULL DEFAULT 0 CHECK (skipped_issued >= 0),
    skipped_ineligible integer     NOT NULL DEFAULT 0 CHECK (skipped_ineligible >= 0),
    created_by         uuid        NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
    created_at         timestamptz NOT NULL DEFAULT now(),
    started_at         timestamptz,
    finished_at        timestamptz,
    runner_token       text        NOT NULL DEFAULT '' CHECK (char_length(runner_token) <= 64),
    lease_until        timestamptz,
    CONSTRAINT bulk_jobs_institute_id_id_key UNIQUE (institute_id, id),
    CONSTRAINT bulk_jobs_batch_fk    FOREIGN KEY (institute_id, batch_id) REFERENCES batches (institute_id, id) ON DELETE RESTRICT,
    CONSTRAINT bulk_jobs_template_fk FOREIGN KEY (institute_id, template_id, template_version)
        REFERENCES certificate_template_versions (institute_id, template_id, number) ON DELETE RESTRICT,
    CONSTRAINT bulk_jobs_finished_consistent CHECK ((status IN ('queued', 'processing')) = (finished_at IS NULL))
);
CREATE UNIQUE INDEX bulk_jobs_one_active ON bulk_jobs (institute_id) WHERE status IN ('queued', 'processing');
CREATE INDEX bulk_jobs_list_idx ON bulk_jobs (institute_id, created_at DESC, id);

CREATE TABLE bulk_job_items (
    institute_id   uuid        NOT NULL,
    job_id         uuid        NOT NULL,
    enrollment_id  uuid        NOT NULL,
    position       integer     NOT NULL CHECK (position >= 1),
    status         text        NOT NULL DEFAULT 'queued' CHECK (status IN ('queued', 'processing', 'succeeded', 'failed')),
    certificate_id uuid,
    error          text        NOT NULL DEFAULT '' CHECK (char_length(error) <= 200),
    attempts       integer     NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    started_at     timestamptz,
    finished_at    timestamptz,
    PRIMARY KEY (job_id, enrollment_id),
    CONSTRAINT bulk_job_items_job_fk        FOREIGN KEY (institute_id, job_id)        REFERENCES bulk_jobs (institute_id, id)     ON DELETE RESTRICT,
    CONSTRAINT bulk_job_items_enrollment_fk FOREIGN KEY (institute_id, enrollment_id) REFERENCES enrollments (institute_id, id)  ON DELETE RESTRICT,
    CONSTRAINT bulk_job_items_certificate_fk FOREIGN KEY (institute_id, certificate_id) REFERENCES certificates (institute_id, id) ON DELETE RESTRICT,
    CONSTRAINT bulk_job_items_position_key UNIQUE (job_id, position),
    CONSTRAINT bulk_job_items_state_consistent CHECK (
        (status IN ('queued', 'processing') AND finished_at IS NULL) OR
        (status = 'succeeded' AND certificate_id IS NOT NULL AND finished_at IS NOT NULL) OR
        (status = 'failed' AND error <> '' AND finished_at IS NOT NULL))
);
CREATE INDEX bulk_job_items_progress_idx ON bulk_job_items (job_id, status, position);

DO $$
DECLARE t text;
BEGIN
    FOREACH t IN ARRAY ARRAY['bulk_jobs', 'bulk_job_items'] LOOP
        EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
        EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
        EXECUTE format($p$CREATE POLICY tenant_isolation ON %I
            USING (institute_id = nullif(current_setting('app.institute_id', true), '')::uuid)
            WITH CHECK (institute_id = nullif(current_setting('app.institute_id', true), '')::uuid)$p$, t);
    END LOOP;
END
$$;

GRANT SELECT, INSERT, UPDATE ON bulk_jobs, bulk_job_items TO certos_runtime;
