-- Organization governance additive schema; applied after legacy ARTEX schemas.
-- Target PostgreSQL 16. Run AFTER existing ARTEX / finding_history / reports schemas.
-- Namespaced, additive, idempotent. No data backfill, DROP, DELETE or legacy DML.
CREATE EXTENSION IF NOT EXISTS btree_gist;
CREATE SCHEMA IF NOT EXISTS org;
CREATE SCHEMA IF NOT EXISTS report;

CREATE TABLE IF NOT EXISTS org.department (
 id BIGSERIAL PRIMARY KEY,
 name TEXT NOT NULL CHECK (btrim(name) <> ''),
 name_key TEXT GENERATED ALWAYS AS (lower(btrim(name))) STORED UNIQUE,
 code TEXT NOT NULL UNIQUE,
 active BOOLEAN NOT NULL DEFAULT true,
 channel_ids BIGINT[] NOT NULL DEFAULT '{}',
 revision BIGINT NOT NULL DEFAULT 1,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(), created_by TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS org.dev_group (
 id BIGSERIAL PRIMARY KEY,
 department_id BIGINT NOT NULL REFERENCES org.department(id),
 name TEXT NOT NULL CHECK (btrim(name) <> ''),
 name_key TEXT GENERATED ALWAYS AS (lower(btrim(name))) STORED,
 active BOOLEAN NOT NULL DEFAULT true,
 revision BIGINT NOT NULL DEFAULT 1,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(), created_by TEXT NOT NULL,
 UNIQUE(department_id,name_key), UNIQUE(id,department_id)
);
CREATE TABLE IF NOT EXISTS org.app_user (
 id BIGSERIAL PRIMARY KEY,
 name TEXT NOT NULL CHECK (btrim(name) <> ''),
 email TEXT NOT NULL CHECK (btrim(email) <> ''),
 email_key TEXT GENERATED ALWAYS AS (lower(btrim(email))) STORED UNIQUE,
 department_id BIGINT REFERENCES org.department(id),
 dev_group_id BIGINT,
 active BOOLEAN NOT NULL DEFAULT true,
 revision BIGINT NOT NULL DEFAULT 1,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(), created_by TEXT NOT NULL,
 CHECK (dev_group_id IS NULL OR department_id IS NOT NULL),
 FOREIGN KEY(dev_group_id,department_id) REFERENCES org.dev_group(id,department_id)
);
CREATE TABLE IF NOT EXISTS org.business_line (
 id BIGSERIAL PRIMARY KEY,
 department_id BIGINT NOT NULL REFERENCES org.department(id),
 name TEXT NOT NULL CHECK (btrim(name) <> ''),
 name_key TEXT GENERATED ALWAYS AS (lower(btrim(name))) STORED,
 code TEXT NOT NULL UNIQUE,
 active BOOLEAN NOT NULL DEFAULT true,
 channel_ids BIGINT[] NOT NULL DEFAULT '{}',
 revision BIGINT NOT NULL DEFAULT 1,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(), created_by TEXT NOT NULL,
 UNIQUE(department_id,name_key)
);
-- Profiles above are current UI projections. Historical owner resolution uses
-- interval rows below, NEVER the current active/department/group profile fields.
-- Departments/users/lines/groups are disabled, not physically deleted.

CREATE TABLE IF NOT EXISTS org.change_operation (
 id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
 idempotency_key UUID NOT NULL UNIQUE,
 request_hash TEXT NOT NULL CHECK (length(request_hash)=64),
 kind TEXT NOT NULL,
 actor_type TEXT NOT NULL CHECK (actor_type IN ('admin','system','worker')),
 actor_id TEXT NOT NULL, actor_label TEXT NOT NULL,
 state TEXT NOT NULL DEFAULT 'queued' CHECK (state IN ('queued','running','succeeded','failed','undone')),
 requested_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 effective_at TIMESTAMPTZ, finished_at TIMESTAMPTZ,
 undo_of UUID REFERENCES org.change_operation(id),
 result JSONB NOT NULL DEFAULT '{}', error_code TEXT, error_detail JSONB
);
CREATE UNIQUE INDEX IF NOT EXISTS uq_org_active_undo ON org.change_operation(undo_of)
 WHERE undo_of IS NOT NULL AND state IN ('queued','running','succeeded');
CREATE TABLE IF NOT EXISTS org.audit_log (
 id BIGSERIAL PRIMARY KEY,
 operation_id UUID NOT NULL REFERENCES org.change_operation(id),
 actor_type TEXT NOT NULL, actor_id TEXT NOT NULL, actor_label TEXT NOT NULL,
 object_type TEXT NOT NULL, object_id TEXT NOT NULL, action TEXT NOT NULL,
 recorded_at TIMESTAMPTZ NOT NULL DEFAULT now(), effective_at TIMESTAMPTZ NOT NULL,
 old_value JSONB NOT NULL DEFAULT '{}', new_value JSONB NOT NULL DEFAULT '{}',
 details JSONB NOT NULL DEFAULT '{}'
);
CREATE INDEX IF NOT EXISTS idx_org_audit_object ON org.audit_log(object_type,object_id,recorded_at DESC,id DESC);

-- Identity bridge: the upstream assets table can physically remove/archive rows.
-- No FK back to public.assets: deletion must not erase or block historical facts.
-- Contains no credentials or original asset payloads.
CREATE TABLE IF NOT EXISTS org.asset_registry (
 asset_id BIGINT PRIMARY KEY,
 asset_type TEXT NOT NULL, identifier TEXT NOT NULL,
 first_seen_at TIMESTAMPTZ NOT NULL,
 source_updated_at TIMESTAMPTZ NOT NULL,
 source_deleted_at TIMESTAMPTZ,
 ownership_revision BIGINT NOT NULL DEFAULT 0,
 event_revision BIGINT NOT NULL DEFAULT 0,
 synced_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_org_asset_registry_first_seen ON org.asset_registry(first_seen_at,asset_id);
CREATE TABLE IF NOT EXISTS org.sync_state (
 singleton BOOLEAN PRIMARY KEY DEFAULT true CHECK (singleton),
 config_revision BIGINT NOT NULL DEFAULT 0,
 assets_last_synced_at TIMESTAMPTZ,
 history_backfill_cursor BIGINT NOT NULL DEFAULT 0,
 backfill_version INTEGER NOT NULL DEFAULT 0,
 backfill_started_at TIMESTAMPTZ, backfill_finished_at TIMESTAMPTZ,
 backfill_summary JSONB NOT NULL DEFAULT '{}'
);
INSERT INTO org.sync_state(singleton) VALUES (true) ON CONFLICT DO NOTHING;

CREATE TABLE IF NOT EXISTS org.app_user_state_history (
 id BIGSERIAL PRIMARY KEY,
 user_id BIGINT NOT NULL REFERENCES org.app_user(id),
 department_id BIGINT REFERENCES org.department(id), dev_group_id BIGINT REFERENCES org.dev_group(id),
 active BOOLEAN NOT NULL,
 valid_from TIMESTAMPTZ NOT NULL, valid_to TIMESTAMPTZ,
 CHECK (valid_to IS NULL OR valid_from < valid_to),
 
 operation_id UUID NOT NULL REFERENCES org.change_operation(id),
 created_by TEXT NOT NULL, recorded_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 voided_at TIMESTAMPTZ, voided_by UUID REFERENCES org.change_operation(id),
 CHECK ((voided_at IS NULL) = (voided_by IS NULL)),
 
 EXCLUDE USING gist (user_id WITH =, tstzrange(valid_from,valid_to,'[)') WITH &&)
 WHERE (voided_at IS NULL)
);
CREATE INDEX IF NOT EXISTS idx_org_app_user_state_history_operation ON org.app_user_state_history(operation_id);

CREATE TABLE IF NOT EXISTS org.business_line_department_history (
 id BIGSERIAL PRIMARY KEY,
 business_line_id BIGINT NOT NULL REFERENCES org.business_line(id),
 department_id BIGINT NOT NULL REFERENCES org.department(id),
 valid_from TIMESTAMPTZ NOT NULL, valid_to TIMESTAMPTZ,
 CHECK (valid_to IS NULL OR valid_from < valid_to),
 
 operation_id UUID NOT NULL REFERENCES org.change_operation(id),
 created_by TEXT NOT NULL, recorded_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 voided_at TIMESTAMPTZ, voided_by UUID REFERENCES org.change_operation(id),
 CHECK ((voided_at IS NULL) = (voided_by IS NULL)),
 
 EXCLUDE USING gist (business_line_id WITH =, tstzrange(valid_from,valid_to,'[)') WITH &&)
 WHERE (voided_at IS NULL)
);
CREATE INDEX IF NOT EXISTS idx_org_business_line_department_history_operation ON org.business_line_department_history(operation_id);

CREATE TABLE IF NOT EXISTS org.department_head_history (
 id BIGSERIAL PRIMARY KEY,
 department_id BIGINT NOT NULL REFERENCES org.department(id),
 head_id BIGINT REFERENCES org.app_user(id), department_active BOOLEAN NOT NULL DEFAULT true,
 valid_from TIMESTAMPTZ NOT NULL, valid_to TIMESTAMPTZ,
 CHECK (valid_to IS NULL OR valid_from < valid_to),
  finding_from TIMESTAMPTZ, finding_to TIMESTAMPTZ,
 CHECK (finding_from IS NULL OR finding_to IS NULL OR finding_from < finding_to),
 operation_id UUID NOT NULL REFERENCES org.change_operation(id),
 created_by TEXT NOT NULL, recorded_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 voided_at TIMESTAMPTZ, voided_by UUID REFERENCES org.change_operation(id),
 CHECK ((voided_at IS NULL) = (voided_by IS NULL)),
 
 EXCLUDE USING gist (department_id WITH =, tstzrange(valid_from,valid_to,'[)') WITH &&, tstzrange(finding_from,finding_to,'[)') WITH &&)
 WHERE (voided_at IS NULL)
);
CREATE INDEX IF NOT EXISTS idx_org_department_head_history_operation ON org.department_head_history(operation_id);

CREATE TABLE IF NOT EXISTS org.business_line_owner_history (
 id BIGSERIAL PRIMARY KEY,
 business_line_id BIGINT NOT NULL REFERENCES org.business_line(id),
 owner_id BIGINT REFERENCES org.app_user(id), backup_id BIGINT REFERENCES org.app_user(id),
 line_active BOOLEAN NOT NULL DEFAULT true,
 CHECK (owner_id IS NULL OR backup_id IS NULL OR owner_id <> backup_id),
 valid_from TIMESTAMPTZ NOT NULL, valid_to TIMESTAMPTZ,
 CHECK (valid_to IS NULL OR valid_from < valid_to),
  finding_from TIMESTAMPTZ, finding_to TIMESTAMPTZ,
 CHECK (finding_from IS NULL OR finding_to IS NULL OR finding_from < finding_to),
 operation_id UUID NOT NULL REFERENCES org.change_operation(id),
 created_by TEXT NOT NULL, recorded_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 voided_at TIMESTAMPTZ, voided_by UUID REFERENCES org.change_operation(id),
 CHECK ((voided_at IS NULL) = (voided_by IS NULL)),
 
 EXCLUDE USING gist (business_line_id WITH =, tstzrange(valid_from,valid_to,'[)') WITH &&, tstzrange(finding_from,finding_to,'[)') WITH &&)
 WHERE (voided_at IS NULL)
);
CREATE INDEX IF NOT EXISTS idx_org_business_line_owner_history_operation ON org.business_line_owner_history(operation_id);

CREATE TABLE IF NOT EXISTS org.fallback_owner_history (
 id BIGSERIAL PRIMARY KEY,
 singleton INTEGER NOT NULL DEFAULT 1 CHECK (singleton=1),
 owner_id BIGINT REFERENCES org.app_user(id),
 valid_from TIMESTAMPTZ NOT NULL, valid_to TIMESTAMPTZ,
 CHECK (valid_to IS NULL OR valid_from < valid_to),
  finding_from TIMESTAMPTZ, finding_to TIMESTAMPTZ,
 CHECK (finding_from IS NULL OR finding_to IS NULL OR finding_from < finding_to),
 operation_id UUID NOT NULL REFERENCES org.change_operation(id),
 created_by TEXT NOT NULL, recorded_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 voided_at TIMESTAMPTZ, voided_by UUID REFERENCES org.change_operation(id),
 CHECK ((voided_at IS NULL) = (voided_by IS NULL)),
 
 EXCLUDE USING gist (singleton WITH =, tstzrange(valid_from,valid_to,'[)') WITH &&, tstzrange(finding_from,finding_to,'[)') WITH &&)
 WHERE (voided_at IS NULL)
);
CREATE INDEX IF NOT EXISTS idx_org_fallback_owner_history_operation ON org.fallback_owner_history(operation_id);

CREATE TABLE IF NOT EXISTS org.dev_group_business_line (
 id BIGSERIAL PRIMARY KEY,
 dev_group_id BIGINT NOT NULL REFERENCES org.dev_group(id),
 business_line_id BIGINT NOT NULL REFERENCES org.business_line(id),
 department_id BIGINT NOT NULL REFERENCES org.department(id),
 valid_from TIMESTAMPTZ NOT NULL, valid_to TIMESTAMPTZ,
 CHECK (valid_to IS NULL OR valid_from < valid_to),
 
 operation_id UUID NOT NULL REFERENCES org.change_operation(id),
 created_by TEXT NOT NULL, recorded_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 voided_at TIMESTAMPTZ, voided_by UUID REFERENCES org.change_operation(id),
 CHECK ((voided_at IS NULL) = (voided_by IS NULL)),
 
 EXCLUDE USING gist (dev_group_id WITH =, business_line_id WITH =, tstzrange(valid_from,valid_to,'[)') WITH &&)
 WHERE (voided_at IS NULL)
);
CREATE INDEX IF NOT EXISTS idx_org_dev_group_business_line_operation ON org.dev_group_business_line(operation_id);

CREATE TABLE IF NOT EXISTS org.attribution_rule (
 id BIGSERIAL PRIMARY KEY,
 revision BIGINT NOT NULL DEFAULT 1,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(), created_by TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS org.attribution_rule_revision (
 id BIGSERIAL PRIMARY KEY,
 rule_id BIGINT NOT NULL REFERENCES org.attribution_rule(id),
 match_type TEXT NOT NULL CHECK (match_type IN ('domain','ip_cidr')),
 domain_pattern TEXT, ip_cidr CIDR,
 department_id BIGINT NOT NULL REFERENCES org.department(id),
 business_line_id BIGINT REFERENCES org.business_line(id),
 priority INTEGER NOT NULL DEFAULT 0, active BOOLEAN NOT NULL DEFAULT true,
 CHECK ((match_type='domain' AND domain_pattern IS NOT NULL AND ip_cidr IS NULL)
     OR (match_type='ip_cidr' AND ip_cidr IS NOT NULL AND domain_pattern IS NULL)),
 valid_from TIMESTAMPTZ NOT NULL, valid_to TIMESTAMPTZ,
 CHECK (valid_to IS NULL OR valid_from < valid_to),
 
 operation_id UUID NOT NULL REFERENCES org.change_operation(id),
 created_by TEXT NOT NULL, recorded_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 voided_at TIMESTAMPTZ, voided_by UUID REFERENCES org.change_operation(id),
 CHECK ((voided_at IS NULL) = (voided_by IS NULL)),
 UNIQUE(id,rule_id),
 EXCLUDE USING gist (rule_id WITH =, tstzrange(valid_from,valid_to,'[)') WITH &&)
 WHERE (voided_at IS NULL)
);
CREATE INDEX IF NOT EXISTS idx_org_attribution_rule_revision_operation ON org.attribution_rule_revision(operation_id);

CREATE TABLE IF NOT EXISTS org.asset_ownership (
 id BIGSERIAL PRIMARY KEY,
 asset_id BIGINT NOT NULL REFERENCES org.asset_registry(asset_id),
 department_id BIGINT REFERENCES org.department(id),
 business_line_id BIGINT REFERENCES org.business_line(id),
 source TEXT NOT NULL CHECK (source IN ('manual','rule','import')),
 rule_id BIGINT REFERENCES org.attribution_rule(id), rule_revision_id BIGINT,
 CHECK (business_line_id IS NULL OR department_id IS NOT NULL),
 CHECK ((source='rule' AND rule_id IS NOT NULL AND rule_revision_id IS NOT NULL)
     OR (source IN ('manual','import') AND rule_id IS NULL AND rule_revision_id IS NULL)),
 FOREIGN KEY(rule_revision_id,rule_id) REFERENCES org.attribution_rule_revision(id,rule_id),
 valid_from TIMESTAMPTZ NOT NULL, valid_to TIMESTAMPTZ,
 CHECK (valid_to IS NULL OR valid_from < valid_to),
  finding_from TIMESTAMPTZ, finding_to TIMESTAMPTZ,
 CHECK (finding_from IS NULL OR finding_to IS NULL OR finding_from < finding_to),
 operation_id UUID NOT NULL REFERENCES org.change_operation(id),
 created_by TEXT NOT NULL, recorded_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 voided_at TIMESTAMPTZ, voided_by UUID REFERENCES org.change_operation(id),
 CHECK ((voided_at IS NULL) = (voided_by IS NULL)),
 
 EXCLUDE USING gist (asset_id WITH =, tstzrange(valid_from,valid_to,'[)') WITH &&, tstzrange(finding_from,finding_to,'[)') WITH &&)
 WHERE (voided_at IS NULL)
);
CREATE INDEX IF NOT EXISTS idx_org_asset_ownership_operation ON org.asset_ownership(operation_id);

CREATE INDEX IF NOT EXISTS idx_org_asset_ownership_live_department
 ON org.asset_ownership(department_id,business_line_id,asset_id)
 WHERE valid_to IS NULL AND voided_at IS NULL;

CREATE TABLE IF NOT EXISTS org.finding_asset_history (
 id BIGSERIAL PRIMARY KEY,
 finding_id BIGINT NOT NULL,
 asset_id BIGINT REFERENCES org.asset_registry(asset_id),
 valid_from TIMESTAMPTZ NOT NULL, valid_to TIMESTAMPTZ,
 CHECK (valid_to IS NULL OR valid_from < valid_to),
 
 operation_id UUID NOT NULL REFERENCES org.change_operation(id),
 created_by TEXT NOT NULL, recorded_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 voided_at TIMESTAMPTZ, voided_by UUID REFERENCES org.change_operation(id),
 CHECK ((voided_at IS NULL) = (voided_by IS NULL)),
 
 EXCLUDE USING gist (finding_id WITH =, tstzrange(valid_from,valid_to,'[)') WITH &&)
 WHERE (voided_at IS NULL)
);
CREATE INDEX IF NOT EXISTS idx_org_finding_asset_history_operation ON org.finding_asset_history(operation_id);

CREATE TABLE IF NOT EXISTS org.sla_policy_history (
 id BIGSERIAL PRIMARY KEY,
 business_line_id BIGINT REFERENCES org.business_line(id),
 policy_key BIGINT GENERATED ALWAYS AS (COALESCE(business_line_id,0)) STORED,
 -- null on a business line means inherit the global policy at the same time.
 days JSONB,
 CHECK (days IS NOT NULL OR business_line_id IS NOT NULL),
 CHECK (days IS NULL OR ((jsonb_typeof(days)='object'
  AND days ?& ARRAY['critical','high','medium','low']
  AND (days->>'critical')::integer>=1
  AND (days->>'critical')::integer<=(days->>'high')::integer
  AND (days->>'high')::integer<=(days->>'medium')::integer
  AND (days->>'medium')::integer<=(days->>'low')::integer
  AND (days->>'low')::integer<=365) IS TRUE)),
 valid_from TIMESTAMPTZ NOT NULL, valid_to TIMESTAMPTZ,
 CHECK (valid_to IS NULL OR valid_from < valid_to),
 
 operation_id UUID NOT NULL REFERENCES org.change_operation(id),
 created_by TEXT NOT NULL, recorded_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 voided_at TIMESTAMPTZ, voided_by UUID REFERENCES org.change_operation(id),
 CHECK ((voided_at IS NULL) = (voided_by IS NULL)),
 
 EXCLUDE USING gist (policy_key WITH =, tstzrange(valid_from,valid_to,'[)') WITH &&)
 WHERE (voided_at IS NULL)
);
CREATE INDEX IF NOT EXISTS idx_org_sla_policy_history_operation ON org.sla_policy_history(operation_id);

-- Only created on CONFIRM, never on preview. A selection is frozen by a
-- signed filter/count/fingerprint; >500 assets executes via the same atomic writer.
CREATE TABLE IF NOT EXISTS org.asset_assignment_job (
 id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
 operation_id UUID NOT NULL UNIQUE REFERENCES org.change_operation(id),
 selection JSONB NOT NULL, expected_count BIGINT NOT NULL CHECK (expected_count>=0),
 expected_fingerprint TEXT NOT NULL CHECK (length(expected_fingerprint)=64),
 target_department_id BIGINT REFERENCES org.department(id),
 target_business_line_id BIGINT REFERENCES org.business_line(id),
 apply_scope TEXT NOT NULL CHECK (apply_scope IN ('all_open','future_findings')),
 backdate_first_assignment BOOLEAN NOT NULL DEFAULT false,
 overwrite_confirmed BOOLEAN NOT NULL DEFAULT false,
 generated_rules JSONB NOT NULL DEFAULT '[]',
 state TEXT NOT NULL DEFAULT 'queued' CHECK (state IN ('queued','running','succeeded','failed')),
 processed_count BIGINT NOT NULL DEFAULT 0 CHECK (processed_count>=0),
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(), started_at TIMESTAMPTZ, finished_at TIMESTAMPTZ,
 effective_at TIMESTAMPTZ, lease_until TIMESTAMPTZ, claimed_by TEXT,
 error_code TEXT, error_detail JSONB, completed_notice_at TIMESTAMPTZ,
 CHECK (target_business_line_id IS NULL OR target_department_id IS NOT NULL)
);
CREATE INDEX IF NOT EXISTS idx_org_assignment_jobs_pending ON org.asset_assignment_job(state,created_at);
CREATE TABLE IF NOT EXISTS org.asset_assignment_item (
 job_id UUID NOT NULL REFERENCES org.asset_assignment_job(id),
 asset_id BIGINT NOT NULL REFERENCES org.asset_registry(asset_id),
 before_revision BIGINT NOT NULL, after_revision BIGINT NOT NULL,
 event_revision_after BIGINT NOT NULL,
 before_rows JSONB NOT NULL, after_row_ids BIGINT[] NOT NULL,
 PRIMARY KEY(job_id,asset_id)
);

-- An approved first-assignment backdate adds corrections; it never overwrites
-- the original event. Phase-6 one-time unknown-field backfill is separately audited.
CREATE TABLE IF NOT EXISTS org.finding_event_attribution_correction (
 id BIGSERIAL PRIMARY KEY,
 history_id BIGINT NOT NULL, finding_id BIGINT NOT NULL,
 asset_id BIGINT REFERENCES org.asset_registry(asset_id),
 department_id BIGINT REFERENCES org.department(id),
 business_line_id BIGINT REFERENCES org.business_line(id),
 owner_id BIGINT REFERENCES org.app_user(id),
 asset_ownership_id BIGINT REFERENCES org.asset_ownership(id),
 owner_resolution TEXT NOT NULL,
 operation_id UUID NOT NULL REFERENCES org.change_operation(id),
 recorded_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 reason TEXT NOT NULL CHECK (reason IN ('first_assignment_backdate','backdate_undo')),
 details JSONB NOT NULL DEFAULT '{}',
 UNIQUE(history_id,operation_id),
 CHECK (business_line_id IS NULL OR department_id IS NOT NULL)
);
CREATE INDEX IF NOT EXISTS idx_org_event_correction_history ON org.finding_event_attribution_correction(history_id,id DESC);

-- Keep upstream multi-asset associations and legacy text ownership fields intact.
-- The new module writes asset_id only, not old business_line/assignee columns.
ALTER TABLE public.findings ADD COLUMN IF NOT EXISTS asset_id BIGINT REFERENCES org.asset_registry(asset_id);
CREATE INDEX IF NOT EXISTS idx_findings_org_asset ON public.findings(asset_id);
ALTER TABLE public.finding_history ADD COLUMN IF NOT EXISTS asset_id BIGINT REFERENCES org.asset_registry(asset_id);
ALTER TABLE public.finding_history ADD COLUMN IF NOT EXISTS department_id BIGINT REFERENCES org.department(id);
ALTER TABLE public.finding_history ADD COLUMN IF NOT EXISTS business_line_id BIGINT REFERENCES org.business_line(id);
ALTER TABLE public.finding_history ADD COLUMN IF NOT EXISTS owner_id BIGINT REFERENCES org.app_user(id);
ALTER TABLE public.finding_history ADD COLUMN IF NOT EXISTS asset_ownership_id BIGINT REFERENCES org.asset_ownership(id);
ALTER TABLE public.finding_history ADD COLUMN IF NOT EXISTS line_owner_history_id BIGINT REFERENCES org.business_line_owner_history(id);
ALTER TABLE public.finding_history ADD COLUMN IF NOT EXISTS department_head_history_id BIGINT REFERENCES org.department_head_history(id);
ALTER TABLE public.finding_history ADD COLUMN IF NOT EXISTS fallback_owner_history_id BIGINT REFERENCES org.fallback_owner_history(id);
ALTER TABLE public.finding_history ADD COLUMN IF NOT EXISTS owner_resolution TEXT;
ALTER TABLE public.finding_history ADD COLUMN IF NOT EXISTS attribution_version SMALLINT NOT NULL DEFAULT 0;
ALTER TABLE public.finding_history ADD COLUMN IF NOT EXISTS attribution_quality TEXT NOT NULL DEFAULT 'unknown';
ALTER TABLE public.finding_history ADD COLUMN IF NOT EXISTS sla_policy_id BIGINT REFERENCES org.sla_policy_history(id);
ALTER TABLE public.finding_history ADD COLUMN IF NOT EXISTS sla_days INTEGER;
CREATE INDEX IF NOT EXISTS idx_finding_history_org_dimensions
 ON public.finding_history(department_id,business_line_id,owner_id,occurred_at,id);

-- The old daily table has one row/day. Dimensions require child rows rather
-- than a department column incorrectly attached to a whole-day aggregate.
CREATE TABLE IF NOT EXISTS report.daily_snapshot (
 id BIGSERIAL PRIMARY KEY,
 day DATE NOT NULL, generation INTEGER NOT NULL CHECK (generation>0),
 generated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 attribution_version SMALLINT NOT NULL DEFAULT 1,
 source_history_watermark BIGINT NOT NULL,
 source_config_revision BIGINT NOT NULL,
 stock_known BOOLEAN NOT NULL, quality_known BOOLEAN NOT NULL,
 correction_operation_id UUID REFERENCES org.change_operation(id),
 UNIQUE(day,generation), UNIQUE(id,day)
);
CREATE TABLE IF NOT EXISTS report.daily_vuln_stat (
 id BIGSERIAL PRIMARY KEY,
 snapshot_id BIGINT NOT NULL REFERENCES report.daily_snapshot(id),
 department_id BIGINT REFERENCES org.department(id),
 business_line_id BIGINT REFERENCES org.business_line(id),
 owner_id BIGINT REFERENCES org.app_user(id),
 severity TEXT NOT NULL, vulnclass TEXT NOT NULL DEFAULT '',
 new_count BIGINT NOT NULL DEFAULT 0, closed_count BIGINT NOT NULL DEFAULT 0,
 reopened_count BIGINT NOT NULL DEFAULT 0,
 open_count BIGINT, overdue_count BIGINT,
 sla_closed_count BIGINT NOT NULL DEFAULT 0, sla_met_count BIGINT NOT NULL DEFAULT 0,
 retest_passed_count BIGINT NOT NULL DEFAULT 0, retest_failed_count BIGINT NOT NULL DEFAULT 0,
 retest_other_count BIGINT NOT NULL DEFAULT 0,
 first_fix_passed_count BIGINT NOT NULL DEFAULT 0, first_fix_total_count BIGINT NOT NULL DEFAULT 0,
 verified_fix_count BIGINT NOT NULL DEFAULT 0, rework_round_count BIGINT NOT NULL DEFAULT 0,
 age_0_7_count BIGINT, age_7_30_count BIGINT, age_30_plus_count BIGINT,
 duration_samples JSONB NOT NULL DEFAULT '{}',
 overdue_items JSONB NOT NULL DEFAULT '[]',
 UNIQUE NULLS NOT DISTINCT(snapshot_id,department_id,business_line_id,owner_id,severity,vulnclass)
);
CREATE TABLE IF NOT EXISTS report.daily_snapshot_head (
 day DATE PRIMARY KEY,
 snapshot_id BIGINT NOT NULL,
 sealed_at TIMESTAMPTZ,
 FOREIGN KEY(snapshot_id,day) REFERENCES report.daily_snapshot(id,day)
);
ALTER TABLE public.daily_vuln_stats ADD COLUMN IF NOT EXISTS attribution_version SMALLINT NOT NULL DEFAULT 0;
ALTER TABLE public.daily_vuln_stats ADD COLUMN IF NOT EXISTS org_snapshot_id BIGINT REFERENCES report.daily_snapshot(id);
ALTER TABLE public.finding_reports ADD COLUMN IF NOT EXISTS attribution_version SMALLINT NOT NULL DEFAULT 0;
ALTER TABLE public.finding_reports ADD COLUMN IF NOT EXISTS department_id BIGINT REFERENCES org.department(id);
ALTER TABLE public.finding_reports ADD COLUMN IF NOT EXISTS business_line_id BIGINT REFERENCES org.business_line(id);
ALTER TABLE public.finding_reports ADD COLUMN IF NOT EXISTS owner_id BIGINT REFERENCES org.app_user(id);
ALTER TABLE public.finding_reports ADD COLUMN IF NOT EXISTS attribution_basis TEXT NOT NULL DEFAULT 'legacy';
ALTER TABLE public.finding_reports ADD COLUMN IF NOT EXISTS source_snapshot_ids BIGINT[] NOT NULL DEFAULT '{}';
ALTER TABLE public.finding_reports ADD COLUMN IF NOT EXISTS delivery_context JSONB NOT NULL DEFAULT '{}';
ALTER TABLE public.finding_report_schedules ADD COLUMN IF NOT EXISTS org_department_ids BIGINT[] NOT NULL DEFAULT '{}';
ALTER TABLE public.finding_report_schedules ADD COLUMN IF NOT EXISTS org_business_line_id BIGINT REFERENCES org.business_line(id);
ALTER TABLE public.finding_report_schedules ADD COLUMN IF NOT EXISTS org_owner_id BIGINT REFERENCES org.app_user(id);
ALTER TABLE public.finding_report_schedules ADD COLUMN IF NOT EXISTS org_scope_mode TEXT NOT NULL DEFAULT 'legacy';
