-- +goose Up
-- settings is a generic key-value table for pacer-managed config that
-- needs to be DB-backed rather than YAML-frozen. Today it holds the
-- bootstrap API token (auto-generated on first start, rotatable via
-- UI). Future single-row config items live here too.
--
-- Single-row entries are accessed by key (PK), so this is effectively
-- a typed dictionary; we don't need a separate table per setting.
CREATE TABLE settings (
    key        TEXT PRIMARY KEY,
    value      TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

-- +goose Down
DROP TABLE settings;
