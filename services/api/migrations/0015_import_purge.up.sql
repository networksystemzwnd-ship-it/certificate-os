-- 0015_import_purge (M6 follow-up): abandoned student-import uploads hold personal data (the CSV rows).
-- A background job blanks the rows of imports nobody finished. Like the delivery worker (0014) it needs to
-- look across institutes, so this adds two NARROW policies instead of bypassing row-level security:
--   * visible/updatable only while the transaction sets app.worker = 'purge',
--   * only imports that are unfinished (uploaded / validated) and idle for more than a day,
--   * the only permitted result of an UPDATE is an emptied, cancelled import.
-- Everything else about student_imports stays tenant-isolated.
CREATE POLICY abandoned_purge_select ON student_imports FOR SELECT
    USING (current_setting('app.worker', true) = 'purge'
           AND status IN ('uploaded', 'validated')
           AND updated_at < now() - interval '1 day');

CREATE POLICY abandoned_purge_update ON student_imports FOR UPDATE
    USING (current_setting('app.worker', true) = 'purge'
           AND status IN ('uploaded', 'validated')
           AND updated_at < now() - interval '1 day')
    WITH CHECK (current_setting('app.worker', true) = 'purge'
           AND status = 'cancelled' AND rows = '[]'::jsonb);
