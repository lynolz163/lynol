-- The operator explicitly runs the maintenance command; startup only adds its checkpoint columns.
ALTER TABLE org.sync_state ADD COLUMN IF NOT EXISTS backfill_run_id UUID REFERENCES org.change_operation(id);
ALTER TABLE org.sync_state ADD COLUMN IF NOT EXISTS history_backfill_upper_id BIGINT NOT NULL DEFAULT 0;
CREATE INDEX IF NOT EXISTS idx_finding_history_org_uninitialized ON public.finding_history(id) WHERE attribution_version=0;
