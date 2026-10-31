-- Destructive: deletes every template and version. Back up first.
DROP TABLE IF EXISTS certificate_template_versions;
DROP FUNCTION IF EXISTS certificate_template_versions_immutable();
DROP TABLE IF EXISTS certificate_templates;
