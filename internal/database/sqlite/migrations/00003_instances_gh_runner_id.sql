-- SPDX-License-Identifier: LicenseRef-DSL-1.0
-- Deferred Source License (DSL)
-- Pacer, Copyright (c) 2026 YouSysAdmin

-- +goose Up

-- gh_runner_id is the integer runner identity returned by
-- POST /actions/runners/generate-jitconfig at register time. We store
-- it so the reaper can call DELETE /actions/runners/{id} when the
-- instance is lost (spot reclaim, host failure) -- that fast-fails
-- the workflow_job on GitHub's side instead of waiting ~10 min for
-- their heartbeat timeout. NULL when the runner never came online.
ALTER TABLE instances ADD COLUMN gh_runner_id INTEGER;

-- +goose Down

ALTER TABLE instances DROP COLUMN gh_runner_id;
