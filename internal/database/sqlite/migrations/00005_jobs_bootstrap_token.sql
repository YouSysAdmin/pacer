-- +goose Up
-- bootstrap_token caches the raw HMAC callback token between spawn
-- and the runner's POST /api/runner/bootstrap call. The orchestrator
-- mints the token at spawn time, stores the raw form here and the
-- sha256 hash on callback_token_hash. The runner fetches the raw
-- token from the bootstrap endpoint (authenticated by the global
-- bootstrap API token) and the orchestrator clears this column on
-- read so a second bootstrap call returns 410 Gone.
--
-- Raw token in SQLite is acceptable here: tokens are short-TTL HMACs
-- (max_runtime + 30 min) and the database file lives on pacer's host
-- behind the same trust boundary as the HMAC secret itself.
ALTER TABLE jobs ADD COLUMN bootstrap_token TEXT;

-- +goose Down
ALTER TABLE jobs DROP COLUMN bootstrap_token;
