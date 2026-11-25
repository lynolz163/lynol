package org

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
)

func (s *Store) ConfirmAssignment(ctx context.Context, c AssignmentConfirm) (AssignmentResult, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return AssignmentResult{}, err
	}
	defer tx.Rollback()
	secret, err := previewSecret(ctx, tx)
	if err != nil {
		return AssignmentResult{}, err
	}
	p, err := readAssignmentToken(secret, c.Token)
	if err != nil {
		return AssignmentResult{}, err
	}
	op, _, cached, err := beginRuleOperation(ctx, tx, "asset_assignment", c.IdempotencyKey, c)
	if err != nil {
		return AssignmentResult{}, err
	}
	if cached != nil {
		var r AssignmentResult
		err = json.Unmarshal(cached, &r)
		return r, err
	}
	if time.Now().Unix() > p.Expires {
		return AssignmentResult{}, conflict("预览已过期，请重新预览")
	}
	if p.AlreadyAssigned > 0 && !c.OverwriteConfirmed {
		return AssignmentResult{}, bad("overwrite_confirmed", "请确认将已有归属资产转交给所选部门")
	}
	if p.Request.BackdateFirstAssignment && !c.BackdateConfirmed {
		return AssignmentResult{}, bad("backdate_confirmed", "回溯会改变这些资产的历史归属，请再次确认")
	}
	if err = assignmentTarget(ctx, tx, p.Request); err != nil {
		return AssignmentResult{}, err
	}
	assets, err := selectAssignmentAssets(ctx, tx, p.Request.Selection)
	if err != nil {
		return AssignmentResult{}, err
	}
	for _, a := range assets {
		if a.Registered {
			if _, err = tx.ExecContext(ctx, "SELECT asset_id FROM org.asset_registry WHERE asset_id=$1 FOR UPDATE", a.AssetID); err != nil {
				return AssignmentResult{}, err
			}
		}
	}
	members, hash, err := assignmentMembers(ctx, tx, assets)
	if err != nil {
		return AssignmentResult{}, err
	}
	var rev int64
	if err = tx.QueryRowContext(ctx, "SELECT config_revision FROM org.sync_state WHERE singleton").Scan(&rev); err != nil {
		return AssignmentResult{}, err
	}
	if hash != p.Fingerprint || rev != p.ConfigRevision {
		return AssignmentResult{}, conflict("数据已变化，请重新预览")
	}
	plan := assignmentPlan{Token: p, ResolvedAssets: members}
	var job string
	err = tx.QueryRowContext(ctx, `INSERT INTO org.asset_assignment_job(operation_id,selection,expected_count,expected_fingerprint,target_department_id,target_business_line_id,apply_scope,backdate_first_assignment,overwrite_confirmed,generated_rules) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) RETURNING id`, op, jsonValue(plan), len(assets), hash, p.Request.DepartmentID, p.Request.BusinessLineID, p.Request.Scope, p.Request.BackdateFirstAssignment, c.OverwriteConfirmed, jsonValue(p.Request.GeneratedRules)).Scan(&job)
	if err != nil {
		return AssignmentResult{}, err
	}
	out := AssignmentResult{OperationID: op, JobID: job, State: "queued", Total: len(assets)}
	if len(assets) > 500 {
		if _, err = tx.ExecContext(ctx, "UPDATE org.change_operation SET state='queued',result=$2 WHERE id=$1", op, jsonValue(out)); err != nil {
			return out, err
		}
		return out, tx.Commit()
	}
	out, err = s.executeAssignment(ctx, tx, job, op, plan, assets, nil)
	if err != nil {
		return out, err
	}
	return out, tx.Commit()
}

type ownershipRow struct {
	ID             int64      `json:"id"`
	AssetID        int64      `json:"asset_id"`
	DepartmentID   *int64     `json:"department_id"`
	BusinessLineID *int64     `json:"business_line_id"`
	Source         string     `json:"source"`
	RuleID         *int64     `json:"rule_id"`
	RuleRevisionID *int64     `json:"rule_revision_id"`
	ValidFrom      time.Time  `json:"valid_from"`
	ValidTo        *time.Time `json:"valid_to"`
	FindingFrom    *time.Time `json:"finding_from"`
	FindingTo      *time.Time `json:"finding_to"`
}

func ownershipRows(ctx context.Context, q reader, id int64) (json.RawMessage, []ownershipRow, error) {
	var raw json.RawMessage
	err := q.QueryRowContext(ctx, "SELECT COALESCE(jsonb_agg(to_jsonb(h) ORDER BY id),'[]') FROM org.asset_ownership h WHERE asset_id=$1 AND valid_to IS NULL AND voided_at IS NULL", id).Scan(&raw)
	if err != nil {
		return nil, nil, err
	}
	var rows []ownershipRow
	err = json.Unmarshal(raw, &rows)
	return raw, rows, err
}

func (s *Store) executeAssignment(ctx context.Context, tx *sql.Tx, job, op string, plan assignmentPlan, assets []RuleAssetView, progress func(int) error) (AssignmentResult, error) {
	out := AssignmentResult{OperationID: op, JobID: job, Total: len(assets), State: "running"}
	var at time.Time
	if err := tx.QueryRowContext(ctx, "SELECT clock_timestamp()").Scan(&at); err != nil {
		return out, err
	}
	out.EffectiveAt = &at
	r := plan.Token.Request
	if _, err := tx.ExecContext(ctx, "UPDATE org.change_operation SET effective_at=$2,state='running' WHERE id=$1", op, at); err != nil {
		return out, err
	}
	for _, g := range r.GeneratedRules {
		var id int64
		if err := tx.QueryRowContext(ctx, "INSERT INTO org.attribution_rule(created_by) VALUES('ARTEX 管理员') RETURNING id").Scan(&id); err != nil {
			return out, err
		}
		g.ID = id
		g.Revision = 1
		var domain, cidr any
		if g.MatchType == "domain" {
			domain = g.MatchValue
		} else {
			cidr = g.MatchValue
		}
		if err := tx.QueryRowContext(ctx, `INSERT INTO org.attribution_rule_revision(rule_id,match_type,domain_pattern,ip_cidr,department_id,business_line_id,priority,active,valid_from,operation_id,created_by) VALUES($1,$2,$3,$4,$5,$6,$7,true,$8,$9,'ARTEX 管理员') RETURNING id`, id, g.MatchType, domain, cidr, r.DepartmentID, r.BusinessLineID, g.Priority, at, op).Scan(&g.RevisionID); err != nil {
			return out, err
		}
		if err := audit(ctx, tx, op, at, "rule", id, "generated_with_assignment", nil, g); err != nil {
			return out, err
		}
	}
	for i, a := range assets {
		if _, err := registerAsset(ctx, tx, a); err != nil {
			return out, err
		}
		var currentRevision, eventRevision int64
		if err := tx.QueryRowContext(ctx, "SELECT ownership_revision,event_revision FROM org.asset_registry WHERE asset_id=$1 FOR UPDATE", a.AssetID).Scan(&currentRevision, &eventRevision); err != nil {
			return out, err
		}
		before, _, err := ownershipRows(ctx, tx, a.AssetID)
		if err != nil {
			return out, err
		}
		effective := at
		if r.BackdateFirstAssignment {
			var ever bool
			if err = tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM org.asset_ownership WHERE asset_id=$1)", a.AssetID).Scan(&ever); err != nil {
				return out, err
			}
			if ever {
				return out, conflict("存在历史归属记录，不能回溯，请重新预览")
			}
			effective = a.FirstSeenAt
		}
		if err = writeManualOwnership(ctx, tx, a.AssetID, r.DepartmentID, r.BusinessLineID, r.Scope, op, effective); err != nil {
			return out, err
		}
		var afterIDs []int64
		rows, err := tx.QueryContext(ctx, "SELECT id FROM org.asset_ownership WHERE operation_id=$1 AND asset_id=$2 ORDER BY id", op, a.AssetID)
		if err != nil {
			return out, err
		}
		for rows.Next() {
			var id int64
			if err = rows.Scan(&id); err != nil {
				rows.Close()
				return out, err
			}
			afterIDs = append(afterIDs, id)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return out, err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO org.asset_assignment_item(job_id,asset_id,before_revision,after_revision,event_revision_after,before_rows,after_row_ids) VALUES($1,$2,$3,$4,$5,$6,$7)`, job, a.AssetID, currentRevision, currentRevision+1, eventRevision, string(before), afterIDs); err != nil {
			return out, err
		}
		action := "manual_assignment"
		if r.BackdateFirstAssignment {
			action = "first_assignment_backdate"
			if err = recordAttributionCorrections(ctx, tx, a.AssetID, op, "first_assignment_backdate", at); err != nil {
				return out, err
			}
		}
		after, _, e := ownershipRows(ctx, tx, a.AssetID)
		if e != nil {
			return out, e
		}
		if err = audit(ctx, tx, op, effective, "asset", a.AssetID, action, before, after); err != nil {
			return out, err
		}
		if progress != nil && (i%25 == 24 || i+1 == len(assets)) {
			if err = progress(i + 1); err != nil {
				return out, err
			}
		}
	}
	out.State = "succeeded"
	out.Assigned = len(assets)
	out.Processed = len(assets)
	res, err := tx.ExecContext(ctx, "UPDATE org.asset_assignment_job SET state='succeeded',processed_count=expected_count,started_at=COALESCE(started_at,$2),finished_at=clock_timestamp(),effective_at=$2,lease_until=NULL WHERE id=$1 AND ($3='' OR claimed_by=$3)", job, at, plan.Claim)
	if err != nil {
		return out, err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return out, conflict("任务已由其他执行器接管")
	}
	if err := finishRuleOperation(ctx, tx, op, out, len(r.GeneratedRules) > 0); err != nil {
		return out, err
	}
	return out, nil
}
func writeManualOwnership(ctx context.Context, tx *sql.Tx, id, dep int64, line *int64, scope, op string, at time.Time) error {
	future := scope == "future_findings"
	_, err := tx.ExecContext(ctx, `WITH closed AS (UPDATE org.asset_ownership SET valid_to=$2 WHERE asset_id=$1 AND valid_to IS NULL AND voided_at IS NULL AND (NOT $3::boolean OR finding_to IS NULL OR finding_to>$2) RETURNING *)
 INSERT INTO org.asset_ownership(asset_id,department_id,business_line_id,source,rule_id,rule_revision_id,valid_from,finding_from,finding_to,operation_id,created_by)
 SELECT asset_id,department_id,business_line_id,source,rule_id,rule_revision_id,$2::timestamptz,finding_from,LEAST(finding_to,$2::timestamptz),$4::uuid,'ARTEX 管理员' FROM closed WHERE $3::boolean AND (finding_from IS NULL OR finding_from<$2)`, id, at, future, op)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO org.asset_ownership(asset_id,department_id,business_line_id,source,valid_from,finding_from,operation_id,created_by) VALUES($1,$2,$3,'manual',$4,CASE WHEN $5::boolean THEN $4::timestamptz ELSE NULL END,$6,'ARTEX 管理员')`, id, dep, line, at, future, op); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "UPDATE org.asset_registry SET ownership_revision=ownership_revision+1 WHERE asset_id=$1", id)
	return err
}

func recordAttributionCorrections(ctx context.Context, tx *sql.Tx, asset int64, op, reason string, cutoff time.Time) error {
	// Only an event with a known primary asset binding is corrected. Unknown old
	// associations stay unknown; phase 6 must not guess them from asset_ids order.
	_, err := tx.ExecContext(ctx, `INSERT INTO org.finding_event_attribution_correction(history_id,finding_id,asset_id,department_id,business_line_id,owner_id,asset_ownership_id,owner_resolution,operation_id,reason,details)
 SELECT h.id,h.finding_id,$1,r.department_id,r.business_line_id,r.owner_id,r.asset_ownership_id,r.owner_resolution,$2,$3,jsonb_build_object('original_attribution_version',h.attribution_version)
 FROM public.finding_history h CROSS JOIN LATERAL org.resolve_owner($1,h.occurred_at,COALESCE((SELECT min(d.occurred_at) FROM public.finding_history d WHERE d.finding_id=h.finding_id AND d.kind='discovered'),h.occurred_at)) r
 WHERE h.recorded_at<=$4 AND (h.asset_id=$1 OR EXISTS(SELECT 1 FROM org.finding_asset_history b WHERE b.finding_id=h.finding_id AND b.asset_id=$1 AND b.voided_at IS NULL AND tstzrange(b.valid_from,b.valid_to,'[)') @> h.occurred_at)) ON CONFLICT(history_id,operation_id) DO NOTHING`, asset, op, reason, cutoff)
	return err
}
