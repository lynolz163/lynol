-- Stable, server-only preview signing material; repeated startup does not rotate it.
CREATE TABLE IF NOT EXISTS org.assignment_signing_key (
 singleton BOOLEAN PRIMARY KEY DEFAULT true CHECK(singleton),
 secret TEXT NOT NULL DEFAULT (gen_random_uuid()::text || gen_random_uuid()::text)
);
INSERT INTO org.assignment_signing_key(singleton) VALUES(true) ON CONFLICT DO NOTHING;

-- This counter tracks actual inserts, including events with a user-supplied old
-- occurred_at. Attribution snapshots themselves are added in phase 5.
CREATE OR REPLACE FUNCTION org.count_asset_event() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE target BIGINT; previous BIGINT; targets BIGINT[];
BEGIN
 IF TG_OP='UPDATE' AND NEW IS NOT DISTINCT FROM OLD THEN RETURN NEW; END IF;
 target := NEW.asset_id;
 IF NEW.attribution_version=0 AND target IS NULL THEN SELECT asset_id INTO target FROM public.findings WHERE id=NEW.finding_id; END IF;
 IF NEW.attribution_version=0 AND target IS NULL THEN
   SELECT asset_id INTO target FROM org.finding_asset_history WHERE finding_id=NEW.finding_id AND voided_at IS NULL
    AND tstzrange(valid_from,valid_to,'[)') @> NEW.occurred_at;
 END IF;
 IF TG_OP='UPDATE' THEN previous:=OLD.asset_id; END IF;
 targets:=ARRAY[target,previous];
 IF TG_WHEN='BEFORE' THEN
   PERFORM asset_id FROM org.asset_registry WHERE asset_id=ANY(targets) ORDER BY asset_id FOR UPDATE;
 ELSE
   UPDATE org.asset_registry SET event_revision=event_revision+1 WHERE asset_id=ANY(targets);
 END IF;
 RETURN NEW;
END $$;
DO $$ BEGIN
 IF NOT EXISTS(SELECT 1 FROM pg_trigger WHERE tgrelid='public.finding_history'::regclass AND tgname='trg_org_asset_event_counter') THEN
  CREATE TRIGGER trg_org_asset_event_counter BEFORE INSERT ON public.finding_history FOR EACH ROW EXECUTE FUNCTION org.count_asset_event();
 END IF;
 IF NOT EXISTS(SELECT 1 FROM pg_trigger WHERE tgrelid='public.finding_history'::regclass AND tgname='trg_org_asset_event_committed') THEN
  CREATE TRIGGER trg_org_asset_event_committed AFTER INSERT ON public.finding_history FOR EACH ROW EXECUTE FUNCTION org.count_asset_event();
 END IF;
 IF NOT EXISTS(SELECT 1 FROM pg_trigger WHERE tgrelid='public.finding_history'::regclass AND tgname='trg_org_asset_event_update_lock') THEN
  CREATE TRIGGER trg_org_asset_event_update_lock BEFORE UPDATE OF finding_id,asset_id,kind,old_status,new_status,occurred_at,metadata ON public.finding_history FOR EACH ROW EXECUTE FUNCTION org.count_asset_event();
 END IF;
 IF NOT EXISTS(SELECT 1 FROM pg_trigger WHERE tgrelid='public.finding_history'::regclass AND tgname='trg_org_asset_event_update_committed') THEN
  CREATE TRIGGER trg_org_asset_event_update_committed AFTER UPDATE OF finding_id,asset_id,kind,old_status,new_status,occurred_at,metadata ON public.finding_history FOR EACH ROW EXECUTE FUNCTION org.count_asset_event();
 END IF;
END $$;
