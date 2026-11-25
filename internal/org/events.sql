-- New events only: existing journal rows are never rewritten by this schema.
CREATE OR REPLACE FUNCTION org.write_finding_asset(p_finding BIGINT,p_asset BIGINT,p_at TIMESTAMPTZ,p_operation UUID,p_actor TEXT)
RETURNS BOOLEAN LANGUAGE plpgsql AS $$
DECLARE old_asset BIGINT; had_binding BOOLEAN;
BEGIN
 SELECT asset_id INTO old_asset FROM org.finding_asset_history
 WHERE finding_id=p_finding AND valid_to IS NULL AND voided_at IS NULL;
 had_binding:=FOUND;
 IF had_binding AND old_asset IS NOT DISTINCT FROM p_asset THEN
   UPDATE public.findings SET asset_id=p_asset WHERE id=p_finding AND asset_id IS DISTINCT FROM p_asset;
   RETURN false;
 END IF;
 PERFORM asset_id FROM org.asset_registry WHERE asset_id=ANY(ARRAY[old_asset,p_asset]) ORDER BY asset_id FOR UPDATE;
 UPDATE org.finding_asset_history SET valid_to=p_at WHERE finding_id=p_finding AND valid_to IS NULL AND voided_at IS NULL;
 INSERT INTO org.finding_asset_history(finding_id,asset_id,valid_from,operation_id,created_by)
 VALUES(p_finding,p_asset,p_at,p_operation,p_actor);
 UPDATE public.findings SET asset_id=p_asset WHERE id=p_finding AND asset_id IS DISTINCT FROM p_asset;
 INSERT INTO org.audit_log(operation_id,actor_type,actor_id,actor_label,object_type,object_id,action,effective_at,old_value,new_value)
 SELECT id,actor_type,actor_id,actor_label,'finding',p_finding::text,'primary_asset_changed',p_at,
 jsonb_build_object('asset_id',old_asset),jsonb_build_object('asset_id',p_asset) FROM org.change_operation WHERE id=p_operation;
 -- A binding change is itself a new fact for both involved assets.
 IF had_binding AND old_asset IS NOT NULL THEN
   UPDATE org.asset_registry SET event_revision=event_revision+1 WHERE asset_id=old_asset;
 END IF;
 RETURN true;
END $$;

CREATE OR REPLACE FUNCTION org.bind_initial_finding_asset() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE chosen BIGINT; op UUID; at_time TIMESTAMPTZ; existing_history BOOLEAN; changed BOOLEAN;
BEGIN
 SELECT EXISTS(SELECT 1 FROM public.finding_history WHERE finding_id=NEW.id) INTO existing_history;
 -- Restoring an old, unbound finding must not act as phase-6 history backfill.
 IF existing_history AND NOT EXISTS(SELECT 1 FROM org.finding_asset_history WHERE finding_id=NEW.id AND voided_at IS NULL) THEN RETURN NEW; END IF;
 chosen:=NEW.asset_id;
 IF chosen IS NULL AND jsonb_array_length(NEW.asset_ids)=1 AND (NEW.asset_ids->>0) ~ '^[0-9]{1,18}$' THEN
   chosen:=(NEW.asset_ids->>0)::bigint;
 END IF;
 IF chosen IS NULL OR NOT EXISTS(SELECT 1 FROM public.assets WHERE id=chosen) THEN RETURN NEW; END IF;
 INSERT INTO org.asset_registry(asset_id,asset_type,identifier,first_seen_at,source_updated_at)
 SELECT id,type,COALESCE(NULLIF(url,''),NULLIF(domain,''),NULLIF(ip,''),NULLIF(app_name,''),'资产 #'||id),created_at,updated_at
 FROM public.assets WHERE id=chosen ON CONFLICT DO NOTHING;
 -- An archive identity remap takes effect now; it does not replace old bindings.
 at_time:=CASE WHEN existing_history THEN clock_timestamp() ELSE NEW.created_at END;
 IF EXISTS(SELECT 1 FROM org.finding_asset_history WHERE finding_id=NEW.id AND valid_to IS NULL AND voided_at IS NULL AND asset_id=chosen) THEN
   UPDATE public.findings SET asset_id=chosen WHERE id=NEW.id AND asset_id IS DISTINCT FROM chosen;
   RETURN NEW;
 END IF;
 op:=gen_random_uuid();
 INSERT INTO org.change_operation(id,idempotency_key,request_hash,kind,actor_type,actor_id,actor_label,state,effective_at,finished_at)
 VALUES(op,op,repeat('0',64),'finding_asset_auto',CASE WHEN COALESCE(NEW.worker,'')='' THEN 'system' ELSE 'worker' END,COALESCE(NULLIF(NEW.worker,''),'system'),COALESCE(NULLIF(NEW.worker,''),'系统'),'succeeded',at_time,clock_timestamp());
 changed:=org.write_finding_asset(NEW.id,chosen,at_time,op,COALESCE(NULLIF(NEW.worker,''),'系统'));
 IF existing_history AND changed THEN
   INSERT INTO public.finding_history(finding_id,kind,actor,occurred_at,metadata)
   VALUES(NEW.id,'primary_asset_changed','系统',at_time,jsonb_build_object('reason','archive_restore','asset_id',chosen));
 END IF;
 RETURN NEW;
END $$;

-- Global SLA settings are versioned from activation onwards; old periods remain unknown.
CREATE OR REPLACE FUNCTION org.record_global_sla(p_days JSONB,p_actor TEXT)
RETURNS VOID LANGUAGE plpgsql AS $$
DECLARE at_time TIMESTAMPTZ; op UUID; previous JSONB;
BEGIN
 PERFORM config_revision FROM org.sync_state WHERE singleton FOR UPDATE;
 SELECT days INTO previous FROM org.sla_policy_history WHERE business_line_id IS NULL AND valid_to IS NULL AND voided_at IS NULL;
 IF previous IS NOT DISTINCT FROM p_days THEN RETURN; END IF;
 at_time:=clock_timestamp(); op:=gen_random_uuid();
 INSERT INTO org.change_operation(id,idempotency_key,request_hash,kind,actor_type,actor_id,actor_label,state,effective_at,finished_at)
 VALUES(op,op,repeat('0',64),'global_sla',CASE WHEN p_actor='系统' THEN 'system' ELSE 'admin' END,CASE WHEN p_actor='系统' THEN 'system' ELSE 'admin' END,p_actor,'succeeded',at_time,at_time);
 UPDATE org.sla_policy_history SET valid_to=at_time WHERE business_line_id IS NULL AND valid_to IS NULL AND voided_at IS NULL;
 INSERT INTO org.sla_policy_history(days,valid_from,operation_id,created_by) VALUES(p_days,at_time,op,p_actor);
 UPDATE org.sync_state SET config_revision=config_revision+1 WHERE singleton;
 INSERT INTO org.audit_log(operation_id,actor_type,actor_id,actor_label,object_type,object_id,action,effective_at,old_value,new_value)
 SELECT op,actor_type,actor_id,actor_label,'sla','global','save',at_time,COALESCE(previous,'{}'),p_days FROM org.change_operation WHERE id=op;
END $$;
CREATE OR REPLACE FUNCTION org.capture_global_sla() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.key='finding_sla_days' THEN PERFORM org.record_global_sla(NEW.value::jsonb,'ARTEX 管理员'); END IF;
 RETURN NEW;
END $$;
DO $$ BEGIN
 IF NOT EXISTS(SELECT 1 FROM org.sla_policy_history WHERE business_line_id IS NULL AND voided_at IS NULL) THEN
   PERFORM org.record_global_sla(value::jsonb,'系统') FROM public.settings WHERE key='finding_sla_days';
 END IF;
END $$;

CREATE OR REPLACE FUNCTION org.snapshot_finding_event() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE primary_asset BIGINT; born TIMESTAMPTZ; resolved RECORD; severity_at TEXT; policy RECORD;
BEGIN
 -- Existing startup import is legacy data, not a newly observed lifecycle event.
 IF NEW.source IN ('backfill','baseline') THEN RETURN NEW; END IF;
 NEW.asset_id:=NULL; NEW.department_id:=NULL; NEW.business_line_id:=NULL; NEW.owner_id:=NULL;
 NEW.asset_ownership_id:=NULL; NEW.line_owner_history_id:=NULL; NEW.department_head_history_id:=NULL; NEW.fallback_owner_history_id:=NULL;
 NEW.owner_resolution:='unassigned'; NEW.attribution_quality:='unknown'; NEW.attribution_version:=1;
 NEW.sla_policy_id:=NULL; NEW.sla_days:=NULL;
 SELECT asset_id INTO primary_asset FROM org.finding_asset_history WHERE finding_id=NEW.finding_id AND voided_at IS NULL
 AND tstzrange(valid_from,valid_to,'[)') @> NEW.occurred_at;
 IF NEW.kind='discovered' THEN born:=NEW.occurred_at;
 ELSE SELECT min(occurred_at) INTO born FROM public.finding_history WHERE finding_id=NEW.finding_id AND kind='discovered'; END IF;
 IF primary_asset IS NOT NULL THEN
   PERFORM asset_id FROM org.asset_registry WHERE asset_id=primary_asset FOR UPDATE;
 END IF;
 IF primary_asset IS NOT NULL AND born IS NOT NULL THEN
   SELECT * INTO resolved FROM org.resolve_owner(primary_asset,NEW.occurred_at,born);
   NEW.asset_id:=resolved.asset_id; NEW.department_id:=resolved.department_id; NEW.business_line_id:=resolved.business_line_id; NEW.owner_id:=resolved.owner_id;
   NEW.asset_ownership_id:=resolved.asset_ownership_id; NEW.line_owner_history_id:=resolved.line_owner_history_id;
   NEW.department_head_history_id:=resolved.department_head_history_id; NEW.fallback_owner_history_id:=resolved.fallback_owner_history_id;
   NEW.owner_resolution:=resolved.owner_resolution; NEW.attribution_quality:=resolved.attribution_quality;
 END IF;
 severity_at:=NEW.metadata->>'severity';
 IF severity_at IS NULL THEN
   SELECT metadata->>'severity' INTO severity_at FROM public.finding_history WHERE finding_id=NEW.finding_id AND occurred_at<=NEW.occurred_at AND metadata ? 'severity'
   ORDER BY occurred_at DESC,id DESC LIMIT 1;
 END IF;
 SELECT id,days INTO policy FROM org.sla_policy_history WHERE voided_at IS NULL AND days IS NOT NULL
 AND (business_line_id IS NULL OR business_line_id=NEW.business_line_id)
 AND tstzrange(valid_from,valid_to,'[)') @> NEW.occurred_at ORDER BY business_line_id IS NOT NULL DESC LIMIT 1;
 IF FOUND AND policy.days ? severity_at THEN NEW.sla_policy_id:=policy.id; NEW.sla_days:=(policy.days->>severity_at)::integer; END IF;
 RETURN NEW;
END $$;

DO $$ BEGIN
 IF NOT EXISTS(SELECT 1 FROM pg_trigger WHERE tgrelid='public.findings'::regclass AND tgname='trg_00_org_finding_asset') THEN
   CREATE TRIGGER trg_00_org_finding_asset AFTER INSERT ON public.findings FOR EACH ROW EXECUTE FUNCTION org.bind_initial_finding_asset();
 END IF;
 IF NOT EXISTS(SELECT 1 FROM pg_trigger WHERE tgrelid='public.finding_history'::regclass AND tgname='trg_00_org_event_snapshot') THEN
   CREATE TRIGGER trg_00_org_event_snapshot BEFORE INSERT ON public.finding_history FOR EACH ROW EXECUTE FUNCTION org.snapshot_finding_event();
 END IF;
 IF NOT EXISTS(SELECT 1 FROM pg_trigger WHERE tgrelid='public.settings'::regclass AND tgname='trg_org_global_sla') THEN
   CREATE TRIGGER trg_org_global_sla AFTER INSERT OR UPDATE OF value ON public.settings FOR EACH ROW EXECUTE FUNCTION org.capture_global_sla();
 END IF;
END $$;

-- Phase-5 report quality counts are stored per dimension, not hidden in samples.
ALTER TABLE report.daily_vuln_stat ADD COLUMN IF NOT EXISTS unknown_event_count BIGINT NOT NULL DEFAULT 0;
ALTER TABLE report.daily_vuln_stat ADD COLUMN IF NOT EXISTS unknown_open_count BIGINT NOT NULL DEFAULT 0;
ALTER TABLE report.daily_vuln_stat ADD COLUMN IF NOT EXISTS missing_sla_closed_count BIGINT NOT NULL DEFAULT 0;
ALTER TABLE report.daily_vuln_stat ADD COLUMN IF NOT EXISTS missing_sla_open_count BIGINT NOT NULL DEFAULT 0;
ALTER TABLE report.daily_vuln_stat ADD COLUMN IF NOT EXISTS missing_discovery_count BIGINT NOT NULL DEFAULT 0;
ALTER TABLE report.daily_snapshot ADD COLUMN IF NOT EXISTS sla_days JSONB NOT NULL DEFAULT '{}';
ALTER TABLE report.daily_snapshot ADD COLUMN IF NOT EXISTS history_since TIMESTAMPTZ;
