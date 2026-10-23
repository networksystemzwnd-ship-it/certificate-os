-- 0008_student_imports (M3b): spreadsheet import jobs.
-- Holds the uploaded rows ONLY until the job finishes: when an import completes or is cancelled, rows that
-- need no further attention are blanked and the rest are kept for the downloadable error report.
CREATE TABLE student_imports (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    institute_id uuid        NOT NULL REFERENCES institutes (id) ON DELETE RESTRICT,
    created_by   uuid        NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
    status       text        NOT NULL CHECK (status IN ('uploaded', 'validated', 'confirming', 'completed', 'cancelled')),
    filename     text        NOT NULL DEFAULT '' CHECK (char_length(filename) <= 255),
    headers      jsonb       NOT NULL,                       -- ["Name","Email",...]
    rows         jsonb       NOT NULL,                       -- [["Asha","a@x"],...]; purged rows are []
    mapping      jsonb       NOT NULL DEFAULT '{}'::jsonb,   -- {"full_name":0,...}
    date_format  text        NOT NULL DEFAULT 'iso' CHECK (date_format IN ('iso', 'dmy', 'mdy')),
    results      jsonb       NOT NULL DEFAULT '[]'::jsonb,   -- [{"row":1,"state":"valid","message":""},...]
    counts       jsonb       NOT NULL DEFAULT '{}'::jsonb,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    completed_at timestamptz,
    CONSTRAINT student_imports_institute_id_id_key UNIQUE (institute_id, id),
    CONSTRAINT student_imports_json_shapes CHECK (
        jsonb_typeof(headers) = 'array' AND jsonb_typeof(rows) = 'array' AND jsonb_typeof(mapping) = 'object'
        AND jsonb_typeof(results) = 'array' AND jsonb_typeof(counts) = 'object')
);
CREATE INDEX student_imports_institute_idx ON student_imports (institute_id, created_at DESC);
CREATE TRIGGER student_imports_set_updated_at BEFORE UPDATE ON student_imports FOR EACH ROW EXECUTE FUNCTION set_updated_at();

ALTER TABLE student_imports ENABLE ROW LEVEL SECURITY;
ALTER TABLE student_imports FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON student_imports
    USING (institute_id = nullif(current_setting('app.institute_id', true), '')::uuid)
    WITH CHECK (institute_id = nullif(current_setting('app.institute_id', true), '')::uuid);

GRANT SELECT, INSERT, UPDATE ON student_imports TO certos_runtime;
