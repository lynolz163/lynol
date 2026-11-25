package org

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
)

type RuleAssetView struct {
	AssetID           int64     `json:"asset_id"`
	Type              string    `json:"type"`
	Identifier        string    `json:"identifier"`
	FirstSeenAt       time.Time `json:"first_seen_at"`
	DepartmentID      *int64    `json:"department_id"`
	BusinessLineID    *int64    `json:"business_line_id"`
	OwnerID           *int64    `json:"owner_id"`
	Source            string    `json:"source"`
	RuleID            *int64    `json:"rule_id"`
	OwnershipRevision int64     `json:"ownership_revision"`
	Match             RuleMatch `json:"match"`
	Open              int64     `json:"open"`
	Registered        bool      `json:"registered"`
	ManualProtected   bool      `json:"manual_protected"`
	RuleRevisionID    *int64    `json:"-"`
	UpdatedAt         time.Time `json:"-"`
	Candidate         RuleAsset `json:"-"`
}
type AssetList struct {
	Items         []RuleAssetView `json:"items"`
	Total         int             `json:"total"`
	LastSyncedAt  *time.Time      `json:"last_synced_at"`
	UnsyncedCount int             `json:"unsynced_count"`
}
type SyncResult struct {
	Synced       int       `json:"synced"`
	Assigned     int       `json:"assigned"`
	Conflicts    int       `json:"conflicts"`
	OperationID  string    `json:"operation_id"`
	LastSyncedAt time.Time `json:"last_synced_at"`
}

func loadRuleAssets(ctx context.Context, q reader, assetID int64) ([]RuleAssetView, error) {
	rows, err := q.QueryContext(ctx, `SELECT a.id,a.type,COALESCE(NULLIF(a.domain,''),NULLIF(a.url,''),NULLIF(a.ip,''),NULLIF(a.bundle_id,''),NULLIF(a.app_name,''),a.type||':'||a.id),COALESCE(g.first_seen_at,a.created_at),a.updated_at,
 COALESCE(a.domain,''),COALESCE(a.root_domain,''),COALESCE(a.url,''),COALESCE(a.ip,''),g.asset_id IS NOT NULL,
 r.department_id,r.business_line_id,r.owner_id,COALESCE(o.source,''),o.rule_id,o.rule_revision_id,COALESCE(g.ownership_revision,0),
 EXISTS(SELECT 1 FROM org.asset_ownership h WHERE h.asset_id=a.id AND h.valid_to IS NULL AND h.voided_at IS NULL AND h.source IN ('manual','import')),
 (SELECT count(*) FROM public.findings f WHERE f.asset_id=a.id AND f.status IN ('pending','in_progress','confirmed'))
 FROM public.assets a LEFT JOIN org.asset_registry g ON g.asset_id=a.id
 CROSS JOIN LATERAL org.resolve_owner(a.id,statement_timestamp(),statement_timestamp()) r
 LEFT JOIN org.asset_ownership o ON o.id=r.asset_ownership_id
 WHERE ($1::bigint=0 OR a.id=$1) ORDER BY a.id`, assetID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RuleAssetView{}
	for rows.Next() {
		var a RuleAssetView
		var domain, root, rawurl, ip string
		if err = rows.Scan(&a.AssetID, &a.Type, &a.Identifier, &a.FirstSeenAt, &a.UpdatedAt, &domain, &root, &rawurl, &ip, &a.Registered, &a.DepartmentID, &a.BusinessLineID, &a.OwnerID, &a.Source, &a.RuleID, &a.RuleRevisionID, &a.OwnershipRevision, &a.ManualProtected, &a.Open); err != nil {
			return nil, err
		}
		a.Candidate = RuleAsset{AssetID: a.AssetID, Domains: []string{}, IP: ip}
		if domain != "" {
			a.Candidate.Domains = append(a.Candidate.Domains, domain)
		}
		if parsed, e := url.Parse(rawurl); e == nil && parsed.Hostname() != "" {
			a.Candidate.Domains = append(a.Candidate.Domains, parsed.Hostname())
		}
		// URL-based services/endpoints may store an IP literal as hostname
		// without a separate ip column. Treat that literal as an IP candidate.
		if a.Candidate.IP == "" {
			for _, host := range a.Candidate.Domains {
				if addr, e := netip.ParseAddr(host); e == nil {
					a.Candidate.IP = addr.Unmap().String()
					break
				}
			}
		}
		if len(a.Candidate.Domains) == 0 && root != "" {
			a.Candidate.Domains = append(a.Candidate.Domains, root)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func beginRuleOperation(ctx context.Context, tx *sql.Tx, kind, key string, request any) (string, time.Time, []byte, error) {
	if _, e := uuid.Parse(key); e != nil {
		return "", time.Time{}, nil, bad("idempotency_key", "请刷新后重新提交")
	}
	if _, err := tx.ExecContext(ctx, "SELECT config_revision FROM org.sync_state WHERE singleton FOR UPDATE"); err != nil {
		return "", time.Time{}, nil, err
	}
	bytes := sha256.Sum256([]byte(kind + ":" + jsonValue(request)))
	hash := hex.EncodeToString(bytes[:])
	var prior string
	var cached []byte
	err := tx.QueryRowContext(ctx, "SELECT request_hash,result FROM org.change_operation WHERE idempotency_key=$1", key).Scan(&prior, &cached)
	if err == nil {
		if prior != hash {
			return "", time.Time{}, nil, conflict("同一提交编号不能用于不同操作")
		}
		return "", time.Time{}, cached, nil
	}
	if err != sql.ErrNoRows {
		return "", time.Time{}, nil, err
	}
	var op string
	var at time.Time
	if err = tx.QueryRowContext(ctx, "SELECT clock_timestamp()").Scan(&at); err != nil {
		return "", at, nil, err
	}
	err = tx.QueryRowContext(ctx, `INSERT INTO org.change_operation(idempotency_key,request_hash,kind,actor_type,actor_id,actor_label,state,effective_at) VALUES($1,$2,$3,'admin','ARTEX','ARTEX 管理员','running',$4) RETURNING id`, key, hash, kind, at).Scan(&op)
	return op, at, nil, err
}
func finishRuleOperation(ctx context.Context, tx *sql.Tx, op string, result any, configChanged bool) error {
	if _, err := tx.ExecContext(ctx, "UPDATE org.change_operation SET state='succeeded',finished_at=clock_timestamp(),result=$2 WHERE id=$1", op, jsonValue(result)); err != nil {
		return err
	}
	if configChanged {
		_, err := tx.ExecContext(ctx, "UPDATE org.sync_state SET config_revision=config_revision+1 WHERE singleton")
		return err
	}
	return nil
}

func registerAsset(ctx context.Context, tx *sql.Tx, a RuleAssetView) (bool, error) {
	var id int64
	err := tx.QueryRowContext(ctx, `INSERT INTO org.asset_registry(asset_id,asset_type,identifier,first_seen_at,source_updated_at) VALUES($1,$2,$3,$4,$5) ON CONFLICT(asset_id) DO NOTHING RETURNING asset_id`, a.AssetID, a.Type, a.Identifier, a.FirstSeenAt, a.UpdatedAt).Scan(&id)
	if err == nil {
		return true, nil
	}
	if err != sql.ErrNoRows {
		return false, err
	}
	_, err = tx.ExecContext(ctx, `UPDATE org.asset_registry SET asset_type=$2,identifier=$3,source_updated_at=$4,source_deleted_at=NULL,synced_at=clock_timestamp() WHERE asset_id=$1`, a.AssetID, a.Type, a.Identifier, a.UpdatedAt)
	return false, err
}

// assignRuleTx is the single rule ownership writer used by ingestion, sync and
// explicit application. The caller owns the configuration and registry row locks.
func assignRuleTx(ctx context.Context, tx *sql.Tx, a RuleAssetView, rule Rule, scope, op string, at time.Time) error {
	future := scope == "future_findings"
	var old json.RawMessage
	if err := tx.QueryRowContext(ctx, "SELECT COALESCE(jsonb_agg(to_jsonb(h)),'[]') FROM org.asset_ownership h WHERE asset_id=$1 AND valid_to IS NULL AND voided_at IS NULL", a.AssetID).Scan(&old); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `WITH closed AS (UPDATE org.asset_ownership SET valid_to=$2 WHERE asset_id=$1 AND valid_to IS NULL AND voided_at IS NULL AND (NOT $3::boolean OR finding_to IS NULL OR finding_to>$2) RETURNING *)
 INSERT INTO org.asset_ownership(asset_id,department_id,business_line_id,source,rule_id,rule_revision_id,valid_from,finding_from,finding_to,operation_id,created_by)
 SELECT asset_id,department_id,business_line_id,source,rule_id,rule_revision_id,$2::timestamptz,finding_from,LEAST(finding_to,$2::timestamptz),$4::uuid,(SELECT actor_label FROM org.change_operation WHERE id=$4::uuid) FROM closed WHERE $3::boolean AND (finding_from IS NULL OR finding_from<$2)`, a.AssetID, at, future, op)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO org.asset_ownership(asset_id,department_id,business_line_id,source,rule_id,rule_revision_id,valid_from,finding_from,operation_id,created_by) VALUES($1,$2,$3,'rule',$4,$5,$6,CASE WHEN $7::boolean THEN $6::timestamptz ELSE NULL END,$8,(SELECT actor_label FROM org.change_operation WHERE id=$8::uuid))`, a.AssetID, rule.DepartmentID, rule.BusinessLineID, rule.ID, rule.RevisionID, at, future, op)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE org.asset_registry SET ownership_revision=ownership_revision+1 WHERE asset_id=$1", a.AssetID); err != nil {
		return err
	}
	return audit(ctx, tx, op, at, "asset", a.AssetID, "apply_rule", old, map[string]any{"department_id": rule.DepartmentID, "business_line_id": rule.BusinessLineID, "rule_id": rule.ID, "rule_revision_id": rule.RevisionID, "scope": scope})
}

// SyncAssetTx is called from the existing asset upsert transaction. It performs
// no networking and only newly registered assets are eligible for automatic rules.
func SyncAssetTx(ctx context.Context, tx *sql.Tx, assetID int64) error {
	if _, err := tx.ExecContext(ctx, "SELECT config_revision FROM org.sync_state WHERE singleton FOR UPDATE"); err != nil {
		return err
	}
	assets, err := loadRuleAssets(ctx, tx, assetID)
	if err != nil {
		return err
	}
	if len(assets) == 0 {
		return bad("asset_id", "资产不存在")
	}
	a := assets[0]
	fresh, err := registerAsset(ctx, tx, a)
	if err != nil {
		return err
	}
	if !fresh {
		return nil
	}
	rules, err := loadRules(ctx, tx)
	if err != nil {
		return err
	}
	matched := MatchRules(a.Candidate, rules)
	var at time.Time
	if err = tx.QueryRowContext(ctx, "SELECT clock_timestamp()").Scan(&at); err != nil {
		return err
	}
	op := uuid.NewString()
	requestHash := sha256.Sum256([]byte("register_asset:" + jsonValue(a.AssetID)))
	if _, err = tx.ExecContext(ctx, `INSERT INTO org.change_operation(id,idempotency_key,request_hash,kind,actor_type,actor_id,actor_label,state,effective_at) VALUES($1,$1,$2,'asset_ingest','system','asset-store','资产入库','running',$3)`, op, hex.EncodeToString(requestHash[:]), at); err != nil {
		return err
	}
	if matched.Status == "matched" && !a.ManualProtected {
		for _, rule := range rules {
			if matched.RuleID != nil && rule.ID == *matched.RuleID {
				if err = assignRuleTx(ctx, tx, a, rule, "all_open", op, at); err != nil {
					return err
				}
				break
			}
		}
	}
	if err = audit(ctx, tx, op, at, "asset", assetID, "register", nil, map[string]any{"identifier": a.Identifier, "match": matched}); err != nil {
		return err
	}
	return finishRuleOperation(ctx, tx, op, map[string]any{"asset_id": assetID, "match": matched}, false)
}

func (s *Store) SyncAssets(ctx context.Context, key string) (SyncResult, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return SyncResult{}, err
	}
	defer tx.Rollback()
	op, at, cached, err := beginRuleOperation(ctx, tx, "asset_sync", key, struct{}{})
	if err != nil {
		return SyncResult{}, err
	}
	if cached != nil {
		var r SyncResult
		err = json.Unmarshal(cached, &r)
		return r, err
	}
	assets, err := loadRuleAssets(ctx, tx, 0)
	if err != nil {
		return SyncResult{}, err
	}
	rules, err := loadRules(ctx, tx)
	if err != nil {
		return SyncResult{}, err
	}
	out := SyncResult{OperationID: op, LastSyncedAt: at}
	for _, a := range assets {
		fresh, e := registerAsset(ctx, tx, a)
		if e != nil {
			return out, e
		}
		out.Synced++
		if !fresh {
			continue
		}
		match := MatchRules(a.Candidate, rules)
		if match.Status == "conflict" {
			out.Conflicts++
		}
		if match.Status == "matched" && !a.ManualProtected {
			for _, rule := range rules {
				if match.RuleID != nil && rule.ID == *match.RuleID {
					if err = assignRuleTx(ctx, tx, a, rule, "all_open", op, at); err != nil {
						return out, err
					}
					out.Assigned++
					break
				}
			}
		}
		if err = audit(ctx, tx, op, at, "asset", a.AssetID, "sync", nil, map[string]any{"identifier": a.Identifier, "match": match}); err != nil {
			return out, err
		}
	}
	if _, err = tx.ExecContext(ctx, "UPDATE org.sync_state SET assets_last_synced_at=$1 WHERE singleton", at); err != nil {
		return out, err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE org.asset_registry r SET source_deleted_at=clock_timestamp() WHERE source_deleted_at IS NULL AND NOT EXISTS(SELECT 1 FROM public.assets a WHERE a.id=r.asset_id)"); err != nil {
		return out, err
	}
	if err = finishRuleOperation(ctx, tx, op, out, false); err != nil {
		return out, err
	}
	return out, tx.Commit()
}

func normalizedScope(scope string) (string, error) {
	if scope == "" {
		return "future_findings", nil
	}
	if scope != "future_findings" && scope != "all_open" {
		return "", bad("scope", "请选择仅新漏洞或同时处理存量")
	}
	return scope, nil
}
func stringsContainsFold(s, q string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(q))
}
