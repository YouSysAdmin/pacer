-- SPDX-License-Identifier: LicenseRef-DSL-1.0
-- Deferred Source License (DSL)
-- Pacer, Copyright (c) 2026 YouSysAdmin

-- +goose Up

CREATE TABLE users (
    id              TEXT PRIMARY KEY,
    email           TEXT NOT NULL UNIQUE,
    password_hash   TEXT,
    oidc_subject    TEXT UNIQUE,
    role            TEXT NOT NULL DEFAULT 'admin',
    super_user      INTEGER NOT NULL DEFAULT 0,
    disabled        INTEGER NOT NULL DEFAULT 0,
    refresh_version INTEGER NOT NULL DEFAULT 0,
    created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_login_at   DATETIME
);

CREATE TABLE projects (
    id                       TEXT PRIMARY KEY,
    name                     TEXT NOT NULL UNIQUE,
    max_concurrent_runners   INTEGER NOT NULL DEFAULT 0,
    tags                     TEXT NOT NULL DEFAULT '{}',
    scope                    TEXT NOT NULL DEFAULT 'repo',  -- 'repo' (per-repo bindings) | 'org' (route by owner.login, JIT to /orgs/...)
    org_name                 TEXT NOT NULL DEFAULT '',      -- GitHub org login when scope='org'
    runner_group_id          INTEGER NOT NULL DEFAULT 0,    -- 0 = 'Default' group (id=1); meaningful only for org scope
    disabled                 INTEGER NOT NULL DEFAULT 0,
    created_at               DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at               DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
-- One project per (scope='org', org_name) so two org-scoped projects
-- can't fight over the same incoming webhook.
CREATE UNIQUE INDEX idx_projects_org_scope ON projects(org_name) WHERE scope = 'org' AND org_name != '';

CREATE TABLE pools (
    id                       TEXT PRIMARY KEY,
    project_id               TEXT NOT NULL REFERENCES projects(id) ON DELETE RESTRICT,
    name                     TEXT NOT NULL,
    is_default               INTEGER NOT NULL DEFAULT 0,
    priority                 INTEGER NOT NULL DEFAULT 100,
    ami_id                   TEXT NOT NULL,
    instance_types           TEXT NOT NULL,
    subnet_ids               TEXT NOT NULL,
    security_group_ids       TEXT NOT NULL,
    iam_instance_profile     TEXT NOT NULL DEFAULT '',
    root_volume_gb           INTEGER NOT NULL DEFAULT 30,
    max_runtime_minutes      INTEGER NOT NULL DEFAULT 60,
    max_concurrent_runners   INTEGER NOT NULL DEFAULT 5,
    spot                     INTEGER NOT NULL DEFAULT 1,
    spawn_method             TEXT NOT NULL DEFAULT 'fleet',  -- 'fleet' (CreateFleet, multi-type/multi-AZ) | 'run_instances' (single type per call, our serial fallback)
    allocation_strategy      TEXT NOT NULL DEFAULT 'cost',   -- 'cost' (lowest-price / price-capacity-optimized) | 'priority' (prioritized / capacity-optimized-prioritized; honors instance_types list order)
    extra_labels             TEXT NOT NULL DEFAULT '[]',     -- JSON []string; appended to auto-derived runner labels
    runner_version           TEXT NOT NULL DEFAULT '',
    runner_user              TEXT NOT NULL DEFAULT '',
    tags                     TEXT NOT NULL DEFAULT '{}',
    user_data_extra          TEXT,
    launch_template_id       TEXT,
    launch_template_version  INTEGER,
    disabled                 INTEGER NOT NULL DEFAULT 0,
    created_at               DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at               DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (project_id, name)
);
CREATE INDEX idx_pools_project ON pools(project_id);
CREATE UNIQUE INDEX idx_pools_default_per_project ON pools(project_id) WHERE is_default = 1;

CREATE TABLE repos (
    full_name              TEXT PRIMARY KEY,
    project_id             TEXT NOT NULL REFERENCES projects(id) ON DELETE RESTRICT,
    max_concurrent_runners INTEGER,
    tags                   TEXT NOT NULL DEFAULT '{}',
    created_at             DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_repos_project ON repos(project_id);

CREATE TABLE jobs (
    id                  TEXT PRIMARY KEY,
    gh_job_id           INTEGER NOT NULL,
    gh_run_id           INTEGER NOT NULL,
    repo_full_name      TEXT NOT NULL,
    project_id          TEXT NOT NULL REFERENCES projects(id) ON DELETE RESTRICT,
    pool_id             TEXT REFERENCES pools(id) ON DELETE RESTRICT,
    installation_id     INTEGER NOT NULL,
    status              TEXT NOT NULL,
    instance_id         TEXT,
    callback_token_hash TEXT,
    queued_at           DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    claimed_at          DATETIME,
    started_at          DATETIME,
    completed_at        DATETIME,
    failure_stage       TEXT,
    failure_message     TEXT,
    failure_log         TEXT,
    estimated_cost_usd  REAL,
    attempts            INTEGER NOT NULL DEFAULT 0,    -- spawn attempts; bumped on capacity-class reschedule
    next_retry_at       DATETIME,                     -- earliest time the orchestrator may re-claim; NULL = ready now
    payload             TEXT NOT NULL
);
CREATE INDEX idx_jobs_status         ON jobs(status);
CREATE INDEX idx_jobs_project_status ON jobs(project_id, status);
CREATE INDEX idx_jobs_pool_status    ON jobs(pool_id, status);
CREATE INDEX idx_jobs_repo           ON jobs(repo_full_name);
CREATE INDEX idx_jobs_completed_at   ON jobs(completed_at);
CREATE UNIQUE INDEX idx_jobs_gh      ON jobs(gh_job_id);

CREATE TABLE instances (
    id              TEXT PRIMARY KEY,
    job_id          TEXT NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
    project_id      TEXT NOT NULL REFERENCES projects(id) ON DELETE RESTRICT,
    pool_id         TEXT REFERENCES pools(id) ON DELETE RESTRICT,
    instance_type   TEXT,
    az              TEXT,
    state           TEXT NOT NULL,
    spot            INTEGER NOT NULL DEFAULT 0,
    price_per_hour  REAL,
    price_model     TEXT,
    launched_at     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    registered_at   DATETIME,
    terminated_at   DATETIME,
    last_seen_at    DATETIME
);
CREATE INDEX idx_instances_state ON instances(state);
CREATE INDEX idx_instances_job   ON instances(job_id);
CREATE INDEX idx_instances_pool  ON instances(pool_id);

CREATE TABLE audit_log (
    id            TEXT PRIMARY KEY,
    actor_user_id TEXT,
    actor_email   TEXT,
    action        TEXT NOT NULL,
    target_type   TEXT,
    target_id     TEXT,
    detail        TEXT,
    client_ip     TEXT,
    request_id    TEXT,
    occurred_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_audit_occurred ON audit_log(occurred_at);
CREATE INDEX idx_audit_action   ON audit_log(action);

CREATE TABLE webhook_deliveries (
    id          TEXT PRIMARY KEY,
    event       TEXT NOT NULL,
    received_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    payload     TEXT NOT NULL,
    processed   INTEGER NOT NULL DEFAULT 0,
    job_id      TEXT REFERENCES jobs(id) ON DELETE SET NULL,
    error       TEXT
);
CREATE INDEX idx_webhook_received ON webhook_deliveries(received_at);

-- +goose Down

DROP TABLE IF EXISTS webhook_deliveries;
DROP TABLE IF EXISTS audit_log;
DROP TABLE IF EXISTS instances;
DROP TABLE IF EXISTS jobs;
DROP TABLE IF EXISTS repos;
DROP TABLE IF EXISTS pools;
DROP TABLE IF EXISTS projects;
DROP TABLE IF EXISTS users;
