-- The journal retains lightweight lifecycle facts across task archival.
-- It intentionally has no FK to the hot findings table, which archival removes.
ALTER TABLE findings ADD COLUMN IF NOT EXISTS business_line TEXT NOT NULL DEFAULT '';
ALTER TABLE findings ADD COLUMN IF NOT EXISTS assignee TEXT NOT NULL DEFAULT '';
ALTER TABLE findings ADD COLUMN IF NOT EXISTS business_line_id BIGINT;
ALTER TABLE findings ADD COLUMN IF NOT EXISTS assignee_id BIGINT;
ALTER TABLE findings ADD COLUMN IF NOT EXISTS assignment_source TEXT NOT NULL DEFAULT 'auto';
ALTER TABLE findings ADD COLUMN IF NOT EXISTS assignment_conflict BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE findings ADD COLUMN IF NOT EXISTS responsibility_sla JSONB;
CREATE TABLE IF NOT EXISTS finding_history (
    id BIGSERIAL PRIMARY KEY,
    finding_id BIGINT NOT NULL,
    event_key TEXT NOT NULL DEFAULT gen_random_uuid()::text,
    kind TEXT NOT NULL,
    old_status TEXT NOT NULL DEFAULT '',
    new_status TEXT NOT NULL DEFAULT '',
    actor TEXT NOT NULL,
    occurred_at TIMESTAMPTZ NOT NULL,
    recorded_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    source TEXT NOT NULL DEFAULT 'recorded',
    metadata JSONB NOT NULL DEFAULT '{}',
    UNIQUE(finding_id, event_key)
);
CREATE INDEX IF NOT EXISTS idx_finding_history_time ON finding_history(occurred_at,id);
CREATE INDEX IF NOT EXISTS idx_finding_history_finding ON finding_history(finding_id,occurred_at,id);

CREATE TABLE IF NOT EXISTS daily_vuln_stats (
    day DATE PRIMARY KEY,
    generated_at TIMESTAMPTZ NOT NULL,
    payload JSONB NOT NULL
);

INSERT INTO settings(key,value) VALUES
    ('finding_sla_days','{"critical":3,"high":7,"medium":30,"low":90}')
ON CONFLICT (key) DO NOTHING;

-- Import known timestamps once. The baseline observes today's state without
-- inventing when an old manual status change happened.
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM settings WHERE key='finding_history_started_at') THEN
        INSERT INTO finding_history(finding_id,event_key,kind,new_status,actor,occurred_at,source,metadata)
        SELECT id,'discovered','discovered','pending','历史记录',created_at,'backfill',
               jsonb_build_object('severity',severity,'vulnclass',vulnclass,'business_line',business_line,'assignee',assignee)
        FROM findings ON CONFLICT DO NOTHING;

        INSERT INTO finding_history(finding_id,event_key,kind,actor,occurred_at,source,metadata)
        SELECT f.id,m.kind,m.kind,'历史记录',m.at,'backfill','{}'
        FROM findings f CROSS JOIN LATERAL (VALUES
            ('dispatched',f.dispatched_at),('developer_fixed',f.developer_fixed_at)
        ) m(kind,at) WHERE m.at IS NOT NULL ON CONFLICT DO NOTHING;

        INSERT INTO finding_history(finding_id,event_key,kind,actor,occurred_at,source,metadata)
        SELECT r.finding_id,'retest:'||r.id||':'||m.kind,m.kind,'历史复测记录',m.at,'backfill',
               jsonb_build_object('retest_id',r.id,'verdict',r.verdict,'status',r.status,'requested_at',r.created_at)
        FROM finding_retests r CROSS JOIN LATERAL (VALUES
            ('retest_requested',r.created_at),('retest_started',r.started_at),('retest_completed',r.finished_at)
        ) m(kind,at) WHERE m.at IS NOT NULL ON CONFLICT DO NOTHING;

        INSERT INTO finding_history(finding_id,event_key,kind,new_status,actor,occurred_at,source,metadata)
        SELECT id,'baseline','baseline',status,'历史记录',now(),'baseline',
               jsonb_build_object('severity',severity,'vulnclass',vulnclass,'business_line',business_line,'assignee',assignee)
        FROM findings ON CONFLICT DO NOTHING;
        INSERT INTO settings(key,value) VALUES ('finding_history_started_at',now()::text);
    END IF;
END $$;

CREATE OR REPLACE FUNCTION journal_finding_changes() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    operator TEXT := COALESCE(NULLIF(current_setting('artex.finding_actor',true),''),'ARTEX 管理员');
BEGIN
    IF TG_OP='DELETE' THEN
        IF COALESCE(current_setting('artex.finding_archiving',true),'') <> 'true' THEN
            INSERT INTO finding_history(finding_id,kind,old_status,new_status,actor,occurred_at)
            VALUES (OLD.id,'removed',OLD.status,'removed',operator,now());
        END IF;
        RETURN OLD;
    END IF;
    IF TG_OP='INSERT' THEN
        INSERT INTO finding_history(finding_id,event_key,kind,new_status,actor,occurred_at,metadata)
        VALUES (NEW.id,'discovered','discovered',NEW.status,COALESCE(NULLIF(NEW.worker,''),'系统'),
                NEW.created_at,jsonb_build_object('severity',NEW.severity,'vulnclass',NEW.vulnclass,
                'business_line',NEW.business_line,'assignee',NEW.assignee,'business_line_id',NEW.business_line_id,
                'assignee_id',NEW.assignee_id,'sla',NEW.responsibility_sla))
        ON CONFLICT DO NOTHING;
        RETURN NEW;
    END IF;
    IF OLD.severity IS DISTINCT FROM NEW.severity OR OLD.vulnclass IS DISTINCT FROM NEW.vulnclass THEN
        INSERT INTO finding_history(finding_id,kind,actor,occurred_at,metadata)
        VALUES (NEW.id,'classification_changed',operator,now(),
                jsonb_build_object('severity',NEW.severity,'vulnclass',NEW.vulnclass));
    END IF;
    IF OLD.status IS DISTINCT FROM NEW.status THEN
        INSERT INTO finding_history(finding_id,kind,old_status,new_status,actor,occurred_at)
        VALUES (NEW.id,'status_changed',OLD.status,NEW.status,operator,now());
    END IF;
    IF OLD.business_line IS DISTINCT FROM NEW.business_line OR OLD.assignee IS DISTINCT FROM NEW.assignee THEN
        INSERT INTO finding_history(finding_id,kind,actor,occurred_at,metadata)
        VALUES (NEW.id,'assignment_changed',operator,now(),
                jsonb_build_object('business_line',NEW.business_line,'assignee',NEW.assignee,
                'old_business_line',OLD.business_line,'old_assignee',OLD.assignee,
                'business_line_id',NEW.business_line_id,'assignee_id',NEW.assignee_id,'sla',NEW.responsibility_sla));
    END IF;
    IF OLD.responsibility_sla IS DISTINCT FROM NEW.responsibility_sla THEN
        INSERT INTO finding_history(finding_id,kind,actor,occurred_at,metadata)
        VALUES (NEW.id,'sla_changed',operator,now(),jsonb_build_object('sla',NEW.responsibility_sla));
    END IF;
    IF NEW.dispatched_at IS NOT NULL AND OLD.dispatched_at IS DISTINCT FROM NEW.dispatched_at THEN
        INSERT INTO finding_history(finding_id,kind,actor,occurred_at)
        VALUES (NEW.id,'dispatched',operator,NEW.dispatched_at);
    END IF;
    IF NEW.developer_fixed_at IS NOT NULL AND OLD.developer_fixed_at IS DISTINCT FROM NEW.developer_fixed_at THEN
        INSERT INTO finding_history(finding_id,kind,actor,occurred_at)
        VALUES (NEW.id,'developer_fixed',operator,NEW.developer_fixed_at);
    END IF;
    RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS trg_finding_history ON findings;
CREATE TRIGGER trg_finding_history AFTER INSERT OR UPDATE OR DELETE ON findings
    FOR EACH ROW EXECUTE FUNCTION journal_finding_changes();

CREATE OR REPLACE FUNCTION journal_finding_retest() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP='INSERT' THEN
        INSERT INTO finding_history(finding_id,event_key,kind,actor,occurred_at,metadata)
        VALUES (NEW.finding_id,'retest:'||NEW.id||':retest_requested','retest_requested','ARTEX 管理员',
                NEW.created_at,jsonb_build_object('retest_id',NEW.id)) ON CONFLICT DO NOTHING;
    END IF;
    IF NEW.started_at IS NOT NULL AND (TG_OP='INSERT' OR OLD.started_at IS NULL) THEN
        INSERT INTO finding_history(finding_id,event_key,kind,actor,occurred_at,metadata)
        VALUES (NEW.finding_id,'retest:'||NEW.id||':retest_started','retest_started','复测 Agent',
                NEW.started_at,jsonb_build_object('retest_id',NEW.id)) ON CONFLICT DO NOTHING;
    END IF;
    IF NEW.finished_at IS NOT NULL AND (TG_OP='INSERT' OR OLD.finished_at IS NULL) THEN
        INSERT INTO finding_history(finding_id,event_key,kind,actor,occurred_at,metadata)
        VALUES (NEW.finding_id,'retest:'||NEW.id||':retest_completed','retest_completed','复测 Agent',
                NEW.finished_at,jsonb_build_object('retest_id',NEW.id,'verdict',NEW.verdict,
                'status',NEW.status,'requested_at',NEW.created_at)) ON CONFLICT DO NOTHING;
    END IF;
    RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS trg_finding_retest_history ON finding_retests;
CREATE TRIGGER trg_finding_retest_history AFTER INSERT OR UPDATE ON finding_retests
    FOR EACH ROW EXECUTE FUNCTION journal_finding_retest();
