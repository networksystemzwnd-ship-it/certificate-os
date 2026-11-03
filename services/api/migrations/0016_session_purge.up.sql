-- 0016_session_purge: expired and revoked sessions are dead credentials (only a token HASH is stored).
-- Remove them on a schedule instead of letting the table grow forever. DELETE is granted on this one table
-- only; it is global (not tenant-owned), holds no personal data beyond the user id, and has no audit value.
GRANT DELETE ON sessions TO certos_runtime;
