package org

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"
	"time"
)

type FindingAssetCandidate struct {
	AssetID    int64  `json:"asset_id"`
	Identifier string `json:"identifier"`
	Type       string `json:"type"`
}
type FindingAssetView struct {
	FindingID         int64                   `json:"finding_id"`
	AssetID           *int64                  `json:"asset_id"`
	Candidates        []FindingAssetCandidate `json:"candidates"`
	NeedsConfirmation bool                    `json:"needs_confirmation"`
}
type FindingAssetResult struct {
	OperationID string    `json:"operation_id"`
	FindingID   int64     `json:"finding_id"`
	AssetID     int64     `json:"asset_id"`
	EffectiveAt time.Time `json:"effective_at"`
}

func (s *Store) FindingAsset(ctx context.Context, id int64) (FindingAssetView, error) {
	out := FindingAssetView{FindingID: id, Candidates: []FindingAssetCandidate{}}
	var linked []byte
	err := s.DB.QueryRowContext(ctx, `SELECT asset_ids,(SELECT asset_id FROM org.finding_asset_history WHERE finding_id=f.id AND valid_to IS NULL AND voided_at IS NULL) FROM public.findings f WHERE id=$1`, id).Scan(&linked, &out.AssetID)
	if err == sql.ErrNoRows {
		return out, &APIError{Status: 404, Message: "漏洞不存在"}
	}
	if err != nil {
		return out, err
	}
	var ids []int64
	if err = json.Unmarshal(linked, &ids); err != nil {
		return out, err
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT id,type,COALESCE(NULLIF(domain,''),NULLIF(url,''),NULLIF(ip,''),NULLIF(bundle_id,''),NULLIF(app_name,''),type||':'||id) FROM public.assets WHERE id=ANY($1) ORDER BY id`, ids)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var a FindingAssetCandidate
		if err = rows.Scan(&a.AssetID, &a.Type, &a.Identifier); err != nil {
			return out, err
		}
		out.Candidates = append(out.Candidates, a)
	}
	out.NeedsConfirmation = out.AssetID == nil && len(out.Candidates) > 0
	return out, rows.Err()
}

// SetFindingAsset is prospective: selecting a primary asset never rewrites old events.
func (s *Store) SetFindingAsset(ctx context.Context, findingID, assetID int64, key string) (FindingAssetResult, error) {
	out := FindingAssetResult{FindingID: findingID, AssetID: assetID}
	if findingID <= 0 || assetID <= 0 {
		return out, bad("asset_id", "请选择关联资产作为归属主资产")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	op, _, cached, err := beginRuleOperation(ctx, tx, "finding_asset", key, map[string]int64{"finding_id": findingID, "asset_id": assetID})
	if err != nil {
		return out, err
	}
	if cached != nil {
		err = json.Unmarshal(cached, &out)
		return out, err
	}
	var associated bool
	err = tx.QueryRowContext(ctx, `SELECT asset_ids @> jsonb_build_array($2::bigint) FROM public.findings WHERE id=$1 FOR UPDATE`, findingID, assetID).Scan(&associated)
	if err == sql.ErrNoRows {
		return out, &APIError{Status: 404, Message: "漏洞不存在"}
	}
	if err != nil {
		return out, err
	}
	if !associated {
		return out, bad("asset_id", "主资产必须来自该漏洞的关联资产")
	}
	var exists bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM public.assets WHERE id=$1)`, assetID).Scan(&exists); err != nil {
		return out, err
	}
	if !exists {
		return out, bad("asset_id", "所选资产已不存在，请刷新关联资产")
	}
	if err = SyncAssetTx(ctx, tx, assetID); err != nil {
		return out, err
	}
	if _, err = tx.ExecContext(ctx, `SELECT asset_id FROM org.asset_registry WHERE asset_id IN ($1,(SELECT asset_id FROM org.finding_asset_history WHERE finding_id=$2 AND valid_to IS NULL AND voided_at IS NULL)) ORDER BY asset_id FOR UPDATE`, assetID, findingID); err != nil {
		return out, err
	}
	if err = tx.QueryRowContext(ctx, `SELECT clock_timestamp()`).Scan(&out.EffectiveAt); err != nil {
		return out, err
	}
	out.OperationID = op
	var changed bool
	if err = tx.QueryRowContext(ctx, `SELECT org.write_finding_asset($1,$2,$3,$4,'ARTEX 管理员')`, findingID, assetID, out.EffectiveAt, op).Scan(&changed); err != nil {
		return out, err
	}
	if changed {
		if _, err = tx.ExecContext(ctx, `INSERT INTO public.finding_history(finding_id,kind,actor,occurred_at,metadata) VALUES($1,'primary_asset_changed','ARTEX 管理员',$2,jsonb_build_object('asset_id',$3::bigint))`, findingID, out.EffectiveAt, assetID); err != nil {
			return out, err
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE org.change_operation SET effective_at=$2 WHERE id=$1`, op, out.EffectiveAt); err != nil {
		return out, err
	}
	if err = finishRuleOperation(ctx, tx, op, out, false); err != nil {
		return out, err
	}
	return out, tx.Commit()
}
func (s *Store) findingAssetHTTP(w http.ResponseWriter, r *http.Request) {
	id, e := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if e != nil || id <= 0 {
		writeError(w, bad("id", "漏洞编号不正确"))
		return
	}
	if r.Method == http.MethodGet {
		v, e := s.FindingAsset(r.Context(), id)
		if e != nil {
			writeError(w, e)
			return
		}
		writeJSON(w, 200, v)
		return
	}
	var b struct {
		AssetID int64  `json:"asset_id"`
		Key     string `json:"idempotency_key"`
	}
	if !decode(w, r, &b) {
		return
	}
	v, e := s.SetFindingAsset(r.Context(), id, b.AssetID, b.Key)
	if e != nil {
		writeError(w, e)
		return
	}
	writeJSON(w, 200, v)
}
