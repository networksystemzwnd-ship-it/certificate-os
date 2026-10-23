-- 0007_records (M3a): courses, batches, students, enrollments. TENANT-OWNED: every table carries
-- institute_id, has FORCE row-level security, and parent links are composite (institute_id, id) foreign
-- keys, so a child row can never point at another institute's parent even if application code is wrong.
--
-- Nothing is hard-deleted by the application: records are archived (archived_at), so the runtime role
-- gets SELECT, INSERT and UPDATE only.

CREATE TABLE courses (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    institute_id uuid        NOT NULL REFERENCES institutes (id) ON DELETE RESTRICT,
    name         text        NOT NULL CHECK (char_length(btrim(name)) BETWEEN 2 AND 200),
    code         text        NOT NULL DEFAULT '' CHECK (char_length(code) <= 40),
    description  text        NOT NULL DEFAULT '' CHECK (char_length(description) <= 2000),
    archived_at  timestamptz,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT courses_institute_id_id_key UNIQUE (institute_id, id)
);
CREATE UNIQUE INDEX courses_active_name_key ON courses (institute_id, lower(name)) WHERE archived_at IS NULL;
CREATE UNIQUE INDEX courses_active_code_key ON courses (institute_id, code) WHERE archived_at IS NULL AND code <> '';
CREATE TRIGGER courses_set_updated_at BEFORE UPDATE ON courses FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE batches (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    institute_id uuid        NOT NULL,
    course_id    uuid        NOT NULL,
    name         text        NOT NULL CHECK (char_length(btrim(name)) BETWEEN 2 AND 200),
    start_date   date,
    end_date     date,
    archived_at  timestamptz,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT batches_institute_id_id_key UNIQUE (institute_id, id),
    CONSTRAINT batches_course_fk FOREIGN KEY (institute_id, course_id) REFERENCES courses (institute_id, id) ON DELETE RESTRICT,
    CONSTRAINT batches_institute_fk FOREIGN KEY (institute_id) REFERENCES institutes (id) ON DELETE RESTRICT,
    CONSTRAINT batches_dates_ordered CHECK (start_date IS NULL OR end_date IS NULL OR end_date >= start_date)
);
CREATE UNIQUE INDEX batches_active_name_key ON batches (institute_id, course_id, lower(name)) WHERE archived_at IS NULL;
CREATE INDEX batches_course_idx ON batches (institute_id, course_id);
CREATE TRIGGER batches_set_updated_at BEFORE UPDATE ON batches FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE students (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    institute_id   uuid        NOT NULL REFERENCES institutes (id) ON DELETE RESTRICT,
    student_number text        CHECK (student_number IS NULL OR char_length(student_number) BETWEEN 1 AND 40),
    full_name      text        NOT NULL CHECK (char_length(btrim(full_name)) BETWEEN 2 AND 200),
    email          text        NOT NULL DEFAULT '' CHECK (char_length(email) <= 254 AND email = lower(email)),
    phone          text        NOT NULL DEFAULT '' CHECK (char_length(phone) <= 20),
    archived_at    timestamptz,
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT students_institute_id_id_key UNIQUE (institute_id, id)
);
-- Unique when present; archived students keep their number reserved (no accidental reuse).
CREATE UNIQUE INDEX students_number_key ON students (institute_id, student_number) WHERE student_number IS NOT NULL;
CREATE INDEX students_name_idx ON students (institute_id, lower(full_name), id);
CREATE TRIGGER students_set_updated_at BEFORE UPDATE ON students FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE enrollments (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    institute_id    uuid        NOT NULL REFERENCES institutes (id) ON DELETE RESTRICT,
    student_id      uuid        NOT NULL,
    batch_id        uuid        NOT NULL,
    status          text        NOT NULL DEFAULT 'enrolled' CHECK (status IN ('enrolled', 'completed', 'withdrawn')),
    completion_date date,
    grade           text        NOT NULL DEFAULT '' CHECK (char_length(grade) <= 40),
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT enrollments_student_fk FOREIGN KEY (institute_id, student_id) REFERENCES students (institute_id, id) ON DELETE RESTRICT,
    CONSTRAINT enrollments_batch_fk   FOREIGN KEY (institute_id, batch_id)   REFERENCES batches (institute_id, id)   ON DELETE RESTRICT,
    CONSTRAINT enrollments_unique_student_batch UNIQUE (student_id, batch_id),
    -- completed needs a completion date; only completed has one.
    CONSTRAINT enrollments_completion_consistent CHECK ((status = 'completed') = (completion_date IS NOT NULL))
);
CREATE INDEX enrollments_batch_idx ON enrollments (institute_id, batch_id, status);
CREATE INDEX enrollments_student_idx ON enrollments (institute_id, student_id);
CREATE TRIGGER enrollments_set_updated_at BEFORE UPDATE ON enrollments FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- Row-level security (ADR-001). An unset or empty app.institute_id matches nothing (fail closed).
-- nullif(...,'') matters: after set_config(..., true) the setting reverts to '' (not NULL) on the same connection.
DO $$
DECLARE t text;
BEGIN
    FOREACH t IN ARRAY ARRAY['courses', 'batches', 'students', 'enrollments'] LOOP
        EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
        EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
        EXECUTE format($p$CREATE POLICY tenant_isolation ON %I
            USING (institute_id = nullif(current_setting('app.institute_id', true), '')::uuid)
            WITH CHECK (institute_id = nullif(current_setting('app.institute_id', true), '')::uuid)$p$, t);
    END LOOP;
END
$$;

GRANT SELECT, INSERT, UPDATE ON courses, batches, students, enrollments TO certos_runtime;
