package org

import (
	"context"
	"time"

	"github.com/google/uuid"
)

type HistoryBackfillAssets struct {
	Registered int       `json:"registered"`
	Bound      int       `json:"bound"`
	Done       bool      `json:"done"`
	Operation  string    `json:"operation_id,omitempty"`
	Effective  time.Time `json:"effective_at,omitempty"`
}

// PrepareHistoryBackfillAssets registers live assets and confirms only a live
// finding's single, verifiable association. New bindings start now, never at
// discovery; they therefore provide no evidence for earlier journal events.
func (s *Store) PrepareHistoryBackfillAssets(ctx context.Context, limit int) (HistoryBackfillAssets, error) {
	out := HistoryBackfillAssets{}
	if limit < 1 || limit > 500 {
		return out, bad("batch_size", "每批数量须为 1 至 500")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SELECT config_revision FROM org.sync_state WHERE singleton FOR UPDATE`); err != nil {
		return out, err
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO org.asset_registry(asset_id,asset_type,identifier,first_seen_at,source_updated_at)
 SELECT a.id,a.type,COALESCE(NULLIF(a.url,''),NULLIF(a.domain,''),NULLIF(a.ip,''),NULLIF(a.app_name,''),'资产 #'||a.id),a.created_at,a.updated_at
 FROM public.assets a WHERE NOT EXISTS(SELECT 1 FROM org.asset_registry r WHERE r.asset_id=a.id)
 ORDER BY a.id LIMIT $1 ON CONFLICT DO NOTHING`, limit)
	if err != nil {
		return out, err
	}
	registered, err := result.RowsAffected()
	if err != nil {
		return out, err
	}
	out.Registered = int(registered)
	rows, err := tx.QueryContext(ctx, `SELECT f.id,a.id FROM public.findings f JOIN public.assets a
 ON a.id=CASE WHEN jsonb_array_length(f.asset_ids)=1 AND (f.asset_ids->>0) ~ '^[0-9]{1,18}$' THEN (f.asset_ids->>0)::bigint END
 WHERE f.asset_id IS NULL AND NOT EXISTS(SELECT 1 FROM org.finding_asset_history h WHERE h.finding_id=f.id AND h.voided_at IS NULL)
 ORDER BY f.id LIMIT $1 FOR UPDATE OF f`, limit)
	if err != nil {
		return out, err
	}
	type binding struct{ finding, asset int64 }
	bindings := []binding{}
	for rows.Next() {
		var b binding
		if err = rows.Scan(&b.finding, &b.asset); err != nil {
			rows.Close()
			return out, err
		}
		bindings = append(bindings, b)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	if out.Registered == 0 && len(bindings) == 0 {
		out.Done = true
		return out, tx.Commit()
	}
	out.Operation = uuid.NewString()
	if err = tx.QueryRowContext(ctx, `SELECT clock_timestamp()`).Scan(&out.Effective); err != nil {
		return out, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO org.change_operation(id,idempotency_key,request_hash,kind,actor_type,actor_id,actor_label,state,effective_at)
 VALUES($1,$1,repeat('0',64),'history_backfill_prepare','system','org-backfill','历史归属回填','running',$2)`, out.Operation, out.Effective); err != nil {
		return out, err
	}
	for _, b := range bindings {
		// A candidate can be beyond this batch's asset-registration limit.
		result, err = tx.ExecContext(ctx, `INSERT INTO org.asset_registry(asset_id,asset_type,identifier,first_seen_at,source_updated_at)
 SELECT id,type,COALESCE(NULLIF(url,''),NULLIF(domain,''),NULLIF(ip,''),NULLIF(app_name,''),'资产 #'||id),created_at,updated_at
 FROM public.assets WHERE id=$1 ON CONFLICT DO NOTHING`, b.asset)
		if err != nil {
			return out, err
		}
		registered, err = result.RowsAffected()
		if err != nil {
			return out, err
		}
		out.Registered += int(registered)
		var changed bool
		if err = tx.QueryRowContext(ctx, `SELECT org.write_finding_asset($1,$2,$3,$4,'历史归属回填')`, b.finding, b.asset, out.Effective, out.Operation).Scan(&changed); err != nil {
			return out, err
		}
		if changed {
			out.Bound++
		}
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO org.audit_log(operation_id,actor_type,actor_id,actor_label,object_type,object_id,action,effective_at,new_value,details)
 VALUES($1,'system','org-backfill','历史归属回填','history_backfill',$1::uuid::text,'prepare_current_assets',$2,$3,'{"historical_binding_inferred":false}')`, out.Operation, out.Effective, jsonValue(out)); err != nil {
		return out, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE org.change_operation SET state='succeeded',finished_at=clock_timestamp(),result=$2 WHERE id=$1`, out.Operation, jsonValue(out)); err != nil {
		return out, err
	}
	return out, tx.Commit()
}
