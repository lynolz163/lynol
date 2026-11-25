CREATE TABLE IF NOT EXISTS responsibility_parties (
    id BIGSERIAL PRIMARY KEY,
    kind TEXT NOT NULL CHECK (kind IN ('person','team')),
    name TEXT NOT NULL UNIQUE,
    aliases TEXT[] NOT NULL DEFAULT '{}',
    email TEXT NOT NULL DEFAULT '',
    team_id BIGINT REFERENCES responsibility_parties(id),
    lead_id BIGINT REFERENCES responsibility_parties(id),
    active BOOLEAN NOT NULL DEFAULT true
);
CREATE TABLE IF NOT EXISTS business_lines (
    id BIGSERIAL PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    code TEXT NOT NULL UNIQUE,
    aliases TEXT[] NOT NULL DEFAULT '{}',
    primary_id BIGINT REFERENCES responsibility_parties(id),
    backup_id BIGINT REFERENCES responsibility_parties(id),
    team_ids JSONB NOT NULL DEFAULT '[]',
    channel_ids JSONB NOT NULL DEFAULT '[]',
    sla JSONB,
    rules JSONB NOT NULL DEFAULT '[]',
    active BOOLEAN NOT NULL DEFAULT true
);
INSERT INTO settings(key,value) VALUES ('responsibility_fallback_id','0') ON CONFLICT DO NOTHING;

-- Preserve legacy names, including archived history. This runs once; later names
-- are selected from this directory and remain attached to stable IDs.
DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM settings WHERE key='responsibility_directory_started_at') THEN
    INSERT INTO business_lines(name,code,aliases)
    SELECT name,'legacy-'||substr(md5(name),1,12),ARRAY[name] FROM (
      SELECT business_line name FROM findings WHERE business_line<>''
      UNION SELECT metadata->>'business_line' FROM finding_history WHERE metadata->>'business_line'<>''
    ) names ON CONFLICT DO NOTHING;
    INSERT INTO responsibility_parties(kind,name,aliases)
    SELECT CASE WHEN name ~ '(组|团队)$' THEN 'team' ELSE 'person' END,name,ARRAY[name] FROM (
      SELECT assignee name FROM findings WHERE assignee<>''
      UNION SELECT metadata->>'assignee' FROM finding_history WHERE metadata->>'assignee'<>''
    ) names ON CONFLICT DO NOTHING;
    UPDATE findings f SET business_line_id=b.id FROM business_lines b WHERE f.business_line=b.name;
    UPDATE findings f SET assignee_id=p.id FROM responsibility_parties p WHERE f.assignee=p.name;
    UPDATE findings SET assignment_source='manual' WHERE business_line<>'' OR assignee<>'';
    INSERT INTO settings(key,value) VALUES ('responsibility_directory_started_at',now()::text);
  END IF;
END $$;

CREATE OR REPLACE FUNCTION responsibility_default_owner(line_id BIGINT) RETURNS BIGINT LANGUAGE sql STABLE AS $$
  SELECT COALESCE(
    (SELECT p.id FROM business_lines b JOIN responsibility_parties p ON p.id=b.primary_id AND p.active WHERE b.id=line_id),
    (SELECT p.id FROM business_lines b JOIN responsibility_parties p ON p.id=b.backup_id AND p.active WHERE b.id=line_id),
    (SELECT p.id FROM settings s JOIN responsibility_parties p ON p.id=s.value::bigint AND p.active WHERE s.key='responsibility_fallback_id'))
$$;

CREATE OR REPLACE FUNCTION responsibility_matching_lines(asset_ids JSONB) RETURNS BIGINT[] LANGUAGE sql STABLE AS $$
  SELECT COALESCE(array_agg(DISTINCT b.id),'{}'::bigint[])
  FROM business_lines b CROSS JOIN LATERAL jsonb_array_elements(b.rules) rule
  JOIN assets a ON asset_ids @> jsonb_build_array(a.id)
  WHERE b.active AND (
    (rule->>'kind'='domain' AND EXISTS (
      SELECT 1 FROM unnest(ARRAY[lower(a.domain),lower(a.root_domain),lower(substring(a.url from '^https?://([^/:?#]+)'))]) host
      WHERE (left(rule->>'value',2)<>'*.' AND host=rule->>'value')
         OR (left(rule->>'value',2)='*.' AND host LIKE '%.'||substr(rule->>'value',3))
    )) OR
    (rule->>'kind'='cidr' AND CASE WHEN pg_input_is_valid(a.ip,'inet') THEN a.ip::inet <<= (rule->>'value')::cidr ELSE false END)
  )
$$;

CREATE OR REPLACE FUNCTION assign_finding_responsibility() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE matches BIGINT[];
BEGIN
  IF NEW.assignment_source='auto' AND NEW.business_line_id IS NULL AND NEW.business_line='' THEN
    matches := responsibility_matching_lines(NEW.asset_ids);
    NEW.assignment_conflict := cardinality(matches)>1;
    IF cardinality(matches)=1 THEN
      NEW.business_line_id := matches[1];
      SELECT name,sla INTO NEW.business_line,NEW.responsibility_sla FROM business_lines WHERE id=matches[1];
    END IF;
    IF cardinality(matches)=1 OR (NEW.assignee_id IS NULL AND NEW.assignee='') THEN
      NEW.assignee_id := responsibility_default_owner(NEW.business_line_id);
      NEW.assignee := COALESCE((SELECT name FROM responsibility_parties WHERE id=NEW.assignee_id),'');
    END IF;
  END IF;
  RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS trg_finding_responsibility ON findings;
CREATE TRIGGER trg_finding_responsibility BEFORE INSERT OR UPDATE OF asset_ids ON findings
FOR EACH ROW EXECUTE FUNCTION assign_finding_responsibility();
