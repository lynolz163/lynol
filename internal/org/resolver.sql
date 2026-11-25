-- Resolver uses event-time history, never current profile state.
CREATE OR REPLACE FUNCTION org.resolve_owner(p_asset BIGINT,p_at TIMESTAMPTZ,p_discovered TIMESTAMPTZ DEFAULT NULL)
RETURNS TABLE(asset_id BIGINT,department_id BIGINT,business_line_id BIGINT,owner_id BIGINT,
 asset_ownership_id BIGINT,line_owner_history_id BIGINT,department_head_history_id BIGINT,fallback_owner_history_id BIGINT,
 owner_resolution TEXT,attribution_quality TEXT)
LANGUAGE sql STABLE AS $$
 WITH point AS (SELECT p_at at_time,COALESCE(p_discovered,p_at) born),
 a AS (SELECT o.* FROM org.asset_ownership o,point p WHERE o.asset_id=p_asset AND o.voided_at IS NULL
 AND tstzrange(o.valid_from,o.valid_to,'[)') @> p.at_time AND tstzrange(o.finding_from,o.finding_to,'[)') @> p.born),
 l AS (SELECT h.* FROM org.business_line_owner_history h JOIN a ON a.business_line_id=h.business_line_id,point p
 WHERE h.voided_at IS NULL AND tstzrange(h.valid_from,h.valid_to,'[)') @> p.at_time AND tstzrange(h.finding_from,h.finding_to,'[)') @> p.born),
 d AS (SELECT h.* FROM org.department_head_history h JOIN a ON a.department_id=h.department_id,point p
 WHERE h.voided_at IS NULL AND tstzrange(h.valid_from,h.valid_to,'[)') @> p.at_time AND tstzrange(h.finding_from,h.finding_to,'[)') @> p.born),
 f AS (SELECT h.* FROM org.fallback_owner_history h,point p WHERE h.voided_at IS NULL
 AND tstzrange(h.valid_from,h.valid_to,'[)') @> p.at_time AND tstzrange(h.finding_from,h.finding_to,'[)') @> p.born),
 candidates AS (
 SELECT owner_id id,1 priority,'primary'::text reason FROM l WHERE line_active
 UNION ALL SELECT backup_id,2,'backup' FROM l WHERE line_active
 UNION ALL SELECT head_id,3,'department_head' FROM d WHERE department_active
 UNION ALL SELECT owner_id,4,'fallback' FROM f),
 chosen AS (SELECT c.id,c.reason FROM candidates c,point p WHERE c.id IS NOT NULL AND EXISTS(
 SELECT 1 FROM org.app_user_state_history s WHERE s.user_id=c.id AND s.active AND s.voided_at IS NULL
 AND tstzrange(s.valid_from,s.valid_to,'[)') @> p.at_time) ORDER BY priority LIMIT 1)
 SELECT p_asset,(SELECT a.department_id FROM a),(SELECT a.business_line_id FROM a),(SELECT id FROM chosen),
 (SELECT a.id FROM a),(SELECT l.id FROM l),(SELECT d.id FROM d),(SELECT f.id FROM f),
 COALESCE((SELECT reason FROM chosen),'unassigned'),CASE WHEN (SELECT a.department_id FROM a) IS NOT NULL THEN 'recorded' ELSE 'unassigned' END
$$;
CREATE OR REPLACE FUNCTION org.resolve_finding_owner(p_finding BIGINT,p_at TIMESTAMPTZ)
RETURNS TABLE(asset_id BIGINT,department_id BIGINT,business_line_id BIGINT,owner_id BIGINT,
 asset_ownership_id BIGINT,line_owner_history_id BIGINT,department_head_history_id BIGINT,fallback_owner_history_id BIGINT,
 owner_resolution TEXT,attribution_quality TEXT)
LANGUAGE sql STABLE AS $$
 WITH fact AS (
 SELECT (SELECT h.asset_id FROM org.finding_asset_history h WHERE h.finding_id=p_finding AND h.voided_at IS NULL AND tstzrange(h.valid_from,h.valid_to,'[)') @> p_at) asset,
 (SELECT min(occurred_at) FROM public.finding_history WHERE finding_id=p_finding AND kind='discovered') discovered
 ) SELECT r.asset_id,
 CASE WHEN fact.asset IS NOT NULL AND fact.discovered IS NOT NULL THEN r.department_id END,
 CASE WHEN fact.asset IS NOT NULL AND fact.discovered IS NOT NULL THEN r.business_line_id END,
 CASE WHEN fact.asset IS NOT NULL AND fact.discovered IS NOT NULL THEN r.owner_id END,
 CASE WHEN fact.asset IS NOT NULL AND fact.discovered IS NOT NULL THEN r.asset_ownership_id END,
 CASE WHEN fact.asset IS NOT NULL AND fact.discovered IS NOT NULL THEN r.line_owner_history_id END,
 CASE WHEN fact.asset IS NOT NULL AND fact.discovered IS NOT NULL THEN r.department_head_history_id END,
 CASE WHEN fact.asset IS NOT NULL AND fact.discovered IS NOT NULL THEN r.fallback_owner_history_id END,
 CASE WHEN fact.asset IS NOT NULL AND fact.discovered IS NOT NULL THEN r.owner_resolution ELSE 'unassigned' END,
 CASE WHEN fact.asset IS NOT NULL AND fact.discovered IS NOT NULL THEN r.attribution_quality ELSE 'unknown' END
 FROM fact CROSS JOIN LATERAL org.resolve_owner(fact.asset,p_at,fact.discovered) r
$$;
