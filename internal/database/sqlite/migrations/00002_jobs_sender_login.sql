-- SPDX-License-Identifier: LicenseRef-DSL-1.0
-- Deferred Source License (DSL)
-- Pacer, Copyright (c) 2026 YouSysAdmin

-- +goose Up

-- sender_login is the GitHub user that triggered the workflow_job
-- (push author, PR opener, manual rerun). Hoisted out of the raw
-- payload column into its own indexed column so /api/stats/top-users
-- can GROUP BY without a per-row JSON parse.
ALTER TABLE jobs ADD COLUMN sender_login TEXT NOT NULL DEFAULT '';
CREATE INDEX idx_jobs_sender_login ON jobs(sender_login);

-- Backfill from the existing raw payload. modernc/sqlite ships JSON1.
-- json_extract returns NULL when the key is missing or the payload is
-- malformed; COALESCE keeps the NOT NULL invariant.
UPDATE jobs SET sender_login = COALESCE(json_extract(payload, '$.sender.login'), '');

-- +goose Down

DROP INDEX IF EXISTS idx_jobs_sender_login;
ALTER TABLE jobs DROP COLUMN sender_login;
