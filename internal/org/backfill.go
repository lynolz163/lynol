package org

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// Only durable evidence at the event's own timestamp can initialize old columns.
// Current findings.asset_ids, legacy organization IDs and labels are never used.
const historyBackfillCandidatesSQL = `WITH selected AS (
 SELECT h.* FROM public.finding_history h WHERE attribution_version=0
 AND ($1::bigint IS NULL OR h.id<=$1) AND ($2::bigint[] IS NULL OR h.id=ANY($2))
), facts AS (
 SELECT h.*,b.id binding_id,b.asset_id bound_asset,
 CASE WHEN h.kind='discovered' THEN h.occurred_at ELSE (SELECT min(d.occurred_at) FROM public.finding_history d WHERE d.finding_id=h.finding_id AND d.kind='discovered') END born,
 COALESCE(h.metadata->>'severity',(SELECT d.metadata->>'severity' FROM public.finding_history d WHERE d.finding_id=h.finding_id AND d.occurred_at<=h.occurred_at AND d.metadata ? 'severity' ORDER BY d.occurred_at DESC,d.id DESC LIMIT 1)) severity_at
 FROM selected h LEFT JOIN LATERAL(SELECT id,asset_id FROM org.finding_asset_history b WHERE b.finding_id=h.finding_id AND b.voided_at IS NULL AND tstzrange(b.valid_from,b.valid_to,'[)') @> h.occurred_at)b ON true
), choices AS (
 SELECT f.*,f.asset_id IS NOT NULL AND f.binding_id IS NOT NULL AND f.asset_id IS DISTINCT FROM f.bound_asset conflicting,
 CASE WHEN binding_id IS NOT NULL THEN bound_asset ELSE asset_id END chosen_asset FROM facts f
), resolved AS (
 SELECT c.*,r.department_id resolved_department,r.business_line_id resolved_line,r.owner_id resolved_owner,
 r.asset_ownership_id resolved_ownership,r.line_owner_history_id resolved_line_owner,r.department_head_history_id resolved_head,
 r.fallback_owner_history_id resolved_fallback,r.owner_resolution resolved_reason,r.attribution_quality resolved_quality,
 NOT c.conflicting AND c.chosen_asset IS NOT NULL AND c.born IS NOT NULL AND r.asset_ownership_id IS NOT NULL eligible
 FROM choices c CROSS JOIN LATERAL org.resolve_owner(c.chosen_asset,c.occurred_at,c.born) r
)
SELECT h.id,h.finding_id,h.kind,h.occurred_at,
 CASE WHEN NOT h.conflicting THEN h.chosen_asset END,
 CASE WHEN h.eligible THEN h.resolved_department END,CASE WHEN h.eligible THEN h.resolved_line END,CASE WHEN h.eligible THEN h.resolved_owner END,
 CASE WHEN h.eligible THEN h.resolved_ownership END,CASE WHEN h.eligible THEN h.resolved_line_owner END,
 CASE WHEN h.eligible THEN h.resolved_head END,CASE WHEN h.eligible THEN h.resolved_fallback END,
 CASE WHEN h.eligible THEN h.resolved_reason ELSE 'unassigned' END,
 CASE WHEN h.eligible THEN h.resolved_quality ELSE 'unknown' END,
 CASE WHEN policy.days ? h.severity_at THEN policy.id END,
 CASE WHEN policy.days ? h.severity_at THEN (policy.days->>h.severity_at)::integer END,
 CASE WHEN h.conflicting THEN 'conflicting_primary_asset'
 WHEN h.chosen_asset IS NULL THEN 'missing_primary_asset_evidence'
 WHEN h.born IS NULL THEN 'missing_discovery'
 WHEN h.resolved_ownership IS NULL THEN 'missing_ownership_history'
 WHEN h.resolved_department IS NULL THEN 'historically_unassigned' ELSE 'historical_attribution' END,
 jsonb_build_object('asset_id',h.asset_id,'department_id',h.department_id,'business_line_id',h.business_line_id,'owner_id',h.owner_id,
 'asset_ownership_id',h.asset_ownership_id,'line_owner_history_id',h.line_owner_history_id,'department_head_history_id',h.department_head_history_id,
 'fallback_owner_history_id',h.fallback_owner_history_id,'owner_resolution',h.owner_resolution,'attribution_quality',h.attribution_quality,
 'attribution_version',h.attribution_version,'sla_policy_id',h.sla_policy_id,'sla_days',h.sla_days)
 FROM resolved h LEFT JOIN LATERAL(SELECT id,days FROM org.sla_policy_history p WHERE p.voided_at IS NULL AND p.days IS NOT NULL
 AND (p.business_line_id IS NULL OR (h.eligible AND p.business_line_id=h.resolved_line))
 AND tstzrange(p.valid_from,p.valid_to,'[)') @> h.occurred_at ORDER BY p.business_line_id IS NOT NULL DESC LIMIT 1)policy ON true
 ORDER BY h.id`

type historyBackfillCandidate struct {
	HistoryID, FindingID int64
	Kind                 string
	At                   time.Time
	Attribution
	SLAPolicyID *int64          `json:"sla_policy_id"`
	SLADays     *int            `json:"sla_days"`
	Reason      string          `json:"reason"`
	Before      json.RawMessage `json:"-"`
}

func scanBackfillCandidate(row interface{ Scan(...any) error }) (historyBackfillCandidate, error) {
	var c historyBackfillCandidate
	err := row.Scan(&c.HistoryID, &c.FindingID, &c.Kind, &c.At, &c.AssetID, &c.DepartmentID, &c.BusinessLineID, &c.OwnerID,
		&c.AssetOwnershipID, &c.LineOwnerHistoryID, &c.DepartmentHeadHistoryID, &c.FallbackOwnerHistoryID, &c.OwnerResolution, &c.AttributionQuality,
		&c.SLAPolicyID, &c.SLADays, &c.Reason, &c.Before)
	return c, err
}
func (s *HistoryBackfillSummary) add(c historyBackfillCandidate) {
	if s.Reasons == nil {
		s.Reasons = map[string]int64{}
	}
	s.Processed++
	s.Reasons[c.Reason]++
	switch c.AttributionQuality {
	case "recorded":
		s.Recorded++
	case "unassigned":
		s.Unassigned++
	default:
		s.Unknown++
	}
	if c.SLADays == nil {
		s.MissingSLA++
	}
}

func (s *Store) PreviewHistoryBackfill(ctx context.Context) (HistoryBackfillPreview, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out := HistoryBackfillPreview{Samples: []HistoryBackfillSample{}, UnresolvedSamples: []HistoryBackfillSample{}, Summary: HistoryBackfillSummary{Reasons: map[string]int64{}}, Candidates: HistoryBackfillSummary{Reasons: map[string]int64{}}}
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	var raw []byte
	if err = tx.QueryRowContext(ctx, `SELECT COALESCE(backfill_run_id::text,''),history_backfill_upper_id,history_backfill_cursor,backfill_started_at,backfill_finished_at,backfill_summary FROM org.sync_state WHERE singleton`).Scan(&out.RunID, &out.UpperHistoryID, &out.Cursor, &out.StartedAt, &out.FinishedAt, &raw); err != nil {
		return out, err
	}
	if err = json.Unmarshal(raw, &out.Summary); err != nil {
		return out, err
	}
	out.Done = out.FinishedAt != nil
	if err = tx.QueryRowContext(ctx, `SELECT count(*),count(*) FILTER(WHERE $1::bigint=0 OR id<=$1) FROM public.finding_history WHERE attribution_version=0`, out.UpperHistoryID).Scan(&out.CurrentRemaining, &out.Remaining); err != nil {
		return out, err
	}
	if out.RunID == "" {
		if err = tx.QueryRowContext(ctx, `SELECT COALESCE(max(id),0) FROM public.finding_history`).Scan(&out.UpperHistoryID); err != nil {
			return out, err
		}
	}
	rows, err := tx.QueryContext(ctx, historyBackfillCandidatesSQL, nil, nil)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		c, e := scanBackfillCandidate(rows)
		if e != nil {
			rows.Close()
			return out, e
		}
		out.Candidates.add(c)
		if len(out.Samples) < 20 {
			out.Samples = append(out.Samples, HistoryBackfillSample{c.HistoryID, c.FindingID, c.Kind, c.At, c.AssetID, c.DepartmentID, c.BusinessLineID, c.OwnerID, c.AttributionQuality, c.Reason, c.SLADays})
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	// Keep the unresolved quality list inspectable after all v0 rows are initialized.
	if out.RunID != "" {
		rows, err = tx.QueryContext(ctx, `SELECT h.id,h.finding_id,h.kind,h.occurred_at,h.asset_id,h.department_id,h.business_line_id,h.owner_id,h.attribution_quality,a.details->>'reason',h.sla_days
 FROM org.audit_log a JOIN public.finding_history h ON a.object_id=h.id::text
 WHERE a.object_type='finding_history' AND a.action='initialize_attribution' AND a.details->>'run_id'=$1 AND h.attribution_quality='unknown'
 ORDER BY h.id LIMIT 20`, out.RunID)
		if err != nil {
			return out, err
		}
		for rows.Next() {
			var sample HistoryBackfillSample
			if err = rows.Scan(&sample.HistoryID, &sample.FindingID, &sample.Kind, &sample.OccurredAt, &sample.AssetID, &sample.DepartmentID, &sample.BusinessLineID, &sample.OwnerID, &sample.Quality, &sample.Reason, &sample.SLADays); err != nil {
				rows.Close()
				return out, err
			}
			out.UnresolvedSamples = append(out.UnresolvedSamples, sample)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return out, err
		}
	}
	err = tx.QueryRowContext(ctx, `SELECT
 (SELECT count(*) FROM public.findings f JOIN public.assets a ON a.id=CASE WHEN jsonb_array_length(f.asset_ids)=1 AND (f.asset_ids->>0) ~ '^[0-9]{1,18}$' THEN (f.asset_ids->>0)::bigint END WHERE f.asset_id IS NULL AND NOT EXISTS(SELECT 1 FROM org.finding_asset_history h WHERE h.finding_id=f.id AND h.voided_at IS NULL)),
 (SELECT count(*) FROM public.findings f WHERE f.asset_id IS NULL AND jsonb_array_length(f.asset_ids)>1 AND NOT EXISTS(SELECT 1 FROM org.finding_asset_history h WHERE h.finding_id=f.id AND h.voided_at IS NULL)),
 (SELECT count(*) FROM public.assets a WHERE NOT EXISTS(SELECT 1 FROM org.asset_registry g WHERE g.asset_id=a.id)),
 (SELECT count(*) FROM public.business_lines),(SELECT count(*) FROM public.responsibility_parties)`).Scan(&out.CurrentSingleAssetCandidates, &out.AmbiguousCurrentFindings, &out.UnregisteredAssets, &out.LegacyBusinessLines, &out.LegacyPeople)
	return out, err
}

// BackfillHistoryBatch performs one bounded, atomic maintenance batch. The first
// successful batch freezes a run's upper journal ID; completing a run never
// implicitly opens another one. A retry simply selects the still-uninitialized rows.
func (s *Store) BackfillHistoryBatch(ctx context.Context, batchSize int) (HistoryBackfillBatch, error) {
	out := HistoryBackfillBatch{Summary: HistoryBackfillSummary{Reasons: map[string]int64{}}}
	if batchSize == 0 {
		batchSize = 200
	}
	if batchSize < 1 || batchSize > 500 {
		return out, bad("batch_size", "每批数量须为 1 至 500")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	var finished *time.Time
	var raw []byte
	if err = tx.QueryRowContext(ctx, `SELECT COALESCE(backfill_run_id::text,''),history_backfill_upper_id,history_backfill_cursor,backfill_finished_at,backfill_summary FROM org.sync_state WHERE singleton FOR UPDATE`).Scan(&out.RunID, &out.UpperHistoryID, &out.Cursor, &finished, &raw); err != nil {
		return out, err
	}
	if err = json.Unmarshal(raw, &out.Summary); err != nil {
		return out, err
	}
	if out.RunID == "" {
		if err = tx.QueryRowContext(ctx, `SELECT COALESCE(max(id),0) FROM public.finding_history`).Scan(&out.UpperHistoryID); err != nil {
			return out, err
		}
		out.RunID = uuid.NewString()
		if _, err = tx.ExecContext(ctx, `INSERT INTO org.change_operation(id,idempotency_key,request_hash,kind,actor_type,actor_id,actor_label,state,effective_at) VALUES($1,$1,repeat('0',64),'history_backfill','system','org-backfill','历史归属回填','running',clock_timestamp())`, out.RunID); err != nil {
			return out, err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE org.sync_state SET backfill_run_id=$1,history_backfill_upper_id=$2,history_backfill_cursor=0,backfill_version=0,backfill_started_at=clock_timestamp(),backfill_finished_at=NULL,backfill_summary='{}' WHERE singleton`, out.RunID, out.UpperHistoryID); err != nil {
			return out, err
		}
		out.Cursor = 0
		out.Summary = HistoryBackfillSummary{Reasons: map[string]int64{}}
	}
	if finished != nil {
		out.Done = true
		err = tx.QueryRowContext(ctx, `SELECT count(*) FILTER(WHERE id<=$1),count(*) FROM public.finding_history WHERE attribution_version=0`, out.UpperHistoryID).Scan(&out.Remaining, &out.CurrentRemaining)
		return out, err
	}
	ids := []int64{}
	rows, err := tx.QueryContext(ctx, `SELECT id FROM public.finding_history WHERE attribution_version=0 AND id<=$1 ORDER BY id LIMIT $2 FOR UPDATE`, out.UpperHistoryID, batchSize)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return out, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	if len(ids) > 0 {
		out.BatchOperationID = uuid.NewString()
		if _, err = tx.ExecContext(ctx, `INSERT INTO org.change_operation(id,idempotency_key,request_hash,kind,actor_type,actor_id,actor_label,state,effective_at) VALUES($1,$1,repeat('0',64),'history_backfill_batch','system','org-backfill','历史归属回填','running',clock_timestamp())`, out.BatchOperationID); err != nil {
			return out, err
		}
		rows, err = tx.QueryContext(ctx, historyBackfillCandidatesSQL, out.UpperHistoryID, ids)
		if err != nil {
			return out, err
		}
		candidates := []historyBackfillCandidate{}
		for rows.Next() {
			c, e := scanBackfillCandidate(rows)
			if e != nil {
				rows.Close()
				return out, e
			}
			candidates = append(candidates, c)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return out, err
		}
		for _, c := range candidates {
			_, err = tx.ExecContext(ctx, `UPDATE public.finding_history SET asset_id=$2,department_id=$3,business_line_id=$4,owner_id=$5,
 asset_ownership_id=$6,line_owner_history_id=$7,department_head_history_id=$8,fallback_owner_history_id=$9,
 owner_resolution=$10,attribution_quality=$11,sla_policy_id=$12,sla_days=$13,attribution_version=1
 WHERE id=$1 AND attribution_version=0`, c.HistoryID, c.AssetID, c.DepartmentID, c.BusinessLineID, c.OwnerID, c.AssetOwnershipID, c.LineOwnerHistoryID, c.DepartmentHeadHistoryID, c.FallbackOwnerHistoryID, c.OwnerResolution, c.AttributionQuality, c.SLAPolicyID, c.SLADays)
			if err != nil {
				return out, err
			}
			after := struct {
				Attribution
				SLAPolicyID *int64 `json:"sla_policy_id"`
				SLADays     *int   `json:"sla_days"`
				Version     int    `json:"attribution_version"`
			}{c.Attribution, c.SLAPolicyID, c.SLADays, 1}
			if _, err = tx.ExecContext(ctx, `INSERT INTO org.audit_log(operation_id,actor_type,actor_id,actor_label,object_type,object_id,action,effective_at,old_value,new_value,details)
 SELECT id,actor_type,actor_id,actor_label,'finding_history',$2::bigint::text,'initialize_attribution',$3,$4::jsonb,$5::jsonb,$6::jsonb
 FROM org.change_operation WHERE id=$1`, out.BatchOperationID, c.HistoryID, c.At, jsonValue(c.Before), jsonValue(after), jsonValue(map[string]any{"run_id": out.RunID, "reason": c.Reason})); err != nil {
				return out, err
			}
			out.Summary.add(c)
			out.Processed++
			if c.HistoryID > out.Cursor {
				out.Cursor = c.HistoryID
			}
		}
	}
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FILTER(WHERE id<=$1),count(*) FROM public.finding_history WHERE attribution_version=0`, out.UpperHistoryID).Scan(&out.Remaining, &out.CurrentRemaining); err != nil {
		return out, err
	}
	out.Done = out.Remaining == 0
	if _, err = tx.ExecContext(ctx, `UPDATE org.sync_state SET history_backfill_cursor=$1,backfill_summary=$2,backfill_version=CASE WHEN $3 THEN 1 ELSE 0 END,backfill_finished_at=CASE WHEN $3 THEN clock_timestamp() ELSE NULL END WHERE singleton`, out.Cursor, jsonValue(out.Summary), out.Done); err != nil {
		return out, err
	}
	if out.BatchOperationID != "" {
		if _, err = tx.ExecContext(ctx, `UPDATE org.change_operation SET state='succeeded',finished_at=clock_timestamp(),result=$2 WHERE id=$1`, out.BatchOperationID, jsonValue(out)); err != nil {
			return out, err
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE org.change_operation SET state=CASE WHEN $2 THEN 'succeeded' ELSE 'running' END,finished_at=CASE WHEN $2 THEN clock_timestamp() ELSE NULL END,result=$3 WHERE id=$1`, out.RunID, out.Done, jsonValue(out)); err != nil {
		return out, err
	}
	return out, tx.Commit()
}
