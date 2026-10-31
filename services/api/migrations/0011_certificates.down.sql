-- Destructive: deletes every issued certificate and stored PDF. Back up first (brain §10.3).
DROP TABLE IF EXISTS certificate_files;
DROP TABLE IF EXISTS certificates;
DROP TABLE IF EXISTS certificate_counters;
DROP FUNCTION IF EXISTS certificates_guard();
DROP FUNCTION IF EXISTS certificate_files_immutable();
ALTER TABLE certificate_template_versions DROP CONSTRAINT IF EXISTS certificate_template_versions_inst_tpl_num_key;
ALTER TABLE enrollments DROP CONSTRAINT IF EXISTS enrollments_institute_id_id_key;
