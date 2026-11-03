-- 0017_rate_limits: a shared fixed-window counter so rate limits hold across several API replicas (D-108).
-- Global (not tenant-owned). `key` is "<limiter name>:<sha256 hex of the subject>", never an IP address or
-- email in clear. Rows are only useful for one window, so old ones are deleted by an hourly job.
CREATE TABLE rate_limit_windows (
    key          text        NOT NULL CHECK (char_length(key) <= 200),
    window_start timestamptz NOT NULL,
    hits         integer     NOT NULL CHECK (hits > 0),
    PRIMARY KEY (key, window_start)
);
CREATE INDEX rate_limit_windows_start_idx ON rate_limit_windows (window_start);
GRANT SELECT, INSERT, UPDATE, DELETE ON rate_limit_windows TO certos_runtime;
