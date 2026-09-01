-- +goose Up
-- runner_instance_id records which machine GitHub actually dispatched
-- this job to, as opposed to instance_id, which records the machine
-- pacer launched FOR it.
--
-- Those are different facts and were sharing one column. Every runner
-- in a pool advertises identical labels - that is what makes a pool a
-- pool - so GitHub hands a queued job to whichever matching runner is
-- free, with no notion of which job the instance was launched for.
-- Whenever a pool runs more than one job at a time the two diverge.
--
-- The reaper acted on the merged column: when a host died it failed,
-- reaped and deregistered the job named there, which could be a job
-- running happily on another host - and 'failed' is terminal, so the
-- conclusion GitHub reported afterwards was dropped on arrival.
--
-- instance_id keeps its meaning so the callbacks a runner makes about
-- itself, and the cost trail (a job pays for the instance it caused to
-- be launched), are unaffected. NULL here means no workflow_job
-- webhook has named a runner yet.
ALTER TABLE jobs ADD COLUMN runner_instance_id TEXT;

-- The reaper resolves "who is running on this host" once per alive
-- instance per sweep.
CREATE INDEX IF NOT EXISTS idx_jobs_runner_instance_id ON jobs(runner_instance_id);

-- +goose Down
DROP INDEX IF EXISTS idx_jobs_runner_instance_id;
ALTER TABLE jobs DROP COLUMN runner_instance_id;
