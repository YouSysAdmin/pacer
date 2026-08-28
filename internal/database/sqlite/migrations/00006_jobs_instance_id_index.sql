-- +goose Up
-- ConsumeBootstrap and the runner callbacks look a job up by
-- instance_id on every call. Without an index that is a full scan of
-- jobs, which grows with retention.
CREATE INDEX IF NOT EXISTS idx_jobs_instance_id ON jobs(instance_id);

-- +goose Down
DROP INDEX IF EXISTS idx_jobs_instance_id;
