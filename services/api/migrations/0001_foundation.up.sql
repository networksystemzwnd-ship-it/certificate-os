-- 0001_foundation: baseline extensions and shared helpers. Business tables arrive with COS-007+.
CREATE EXTENSION IF NOT EXISTS pgcrypto; -- gen_random_uuid()

-- Keeps updated_at current on tables that opt in.
CREATE OR REPLACE FUNCTION set_updated_at() RETURNS trigger AS $$
BEGIN
    NEW.updated_at = now();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
