package org

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/google/uuid"
)

type assignmentUndoItem struct {
	Asset         int64
	AfterRevision int64
	EventRevision int64
	BeforeJSON    json.RawMessage
	Before        []ownershipRow
	AfterIDs      []int64
}

func (s *Store) UndoAssignment(ctx context.Context, original, key string) (AssignmentUndoResult, error) {
	if _, err := uuid.Parse(original); err != nil {
		return AssignmentUndoResult{}, bad("id", "批次编号不正确")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return AssignmentUndoResult{}, err
	}
	defer tx.Rollback()
	op, at, cached, err := beginRuleOperation(ctx, tx, "assignment_undo", key, original)
	if err != nil {
		return AssignmentUndoResult{}, err
	}
	if cached != nil {
		var r AssignmentUndoResult
		err = json.Unmarshal(cached, &r)
		return r, err
	}
	var job, state string
	var backdate bool
	err = tx.QueryRowContext(ctx, "SELECT j.id,o.state,j.backdate_first_assignment FROM org.asset_assignment_job j JOIN org.change_operation o ON o.id=j.operation_id WHERE o.id=$1 AND j.state='succeeded'", original).Scan(&job, &state, &backdate)
	if err == sql.ErrNoRows {
		return AssignmentUndoResult{}, bad("id", "批次尚未完成或不存在")
	}
	if err != nil {
		return AssignmentUndoResult{}, err
	}
	if state != "succeeded" {
		return AssignmentUndoResult{}, conflict("该批次已撤销")
	}
	if _, err = tx.ExecContext(ctx, "UPDATE org.change_operation SET undo_of=$2 WHERE id=$1", op, original); err != nil {
		return AssignmentUndoResult{}, err
	}
	rows, err := tx.QueryContext(ctx, "SELECT asset_id,after_revision,event_revision_after,before_rows,to_jsonb(after_row_ids) FROM org.asset_assignment_item WHERE job_id=$1 ORDER BY asset_id", job)
	if err != nil {
		return AssignmentUndoResult{}, err
	}
	items := []assignmentUndoItem{}
	for rows.Next() {
		var v assignmentUndoItem
		var ids []byte
		if err = rows.Scan(&v.Asset, &v.AfterRevision, &v.EventRevision, &v.BeforeJSON, &ids); err != nil {
			rows.Close()
			return AssignmentUndoResult{}, err
		}
		if err = json.Unmarshal(v.BeforeJSON, &v.Before); err != nil {
			rows.Close()
			return AssignmentUndoResult{}, err
		}
		if err = json.Unmarshal(ids, &v.AfterIDs); err != nil {
			rows.Close()
			return AssignmentUndoResult{}, err
		}
		items = append(items, v)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return AssignmentUndoResult{}, err
	}
	generated, err := loadRules(ctx, tx)
	if err != nil {
		return AssignmentUndoResult{}, err
	}
	rulesToDisable := []Rule{}
	for _, r := range generated {
		var createdByBatch bool
		if err = tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM org.attribution_rule_revision WHERE rule_id=$1 AND operation_id=$2)", r.ID, original).Scan(&createdByBatch); err != nil {
			return AssignmentUndoResult{}, err
		}
		if !createdByBatch {
			continue
		}
		var currentOp string
		if err = tx.QueryRowContext(ctx, "SELECT operation_id::text FROM org.attribution_rule_revision WHERE id=$1", r.RevisionID).Scan(&currentOp); err != nil {
			return AssignmentUndoResult{}, err
		}
		if currentOp != original {
			return AssignmentUndoResult{}, conflict("该批生成的规则之后已被修改，请先处理规则变更后再撤销")
		}
		rulesToDisable = append(rulesToDisable, r)
	}
	events := map[int64]int64{}
	for _, item := range items {
		var revision, event int64
		if err = tx.QueryRowContext(ctx, "SELECT ownership_revision,event_revision FROM org.asset_registry WHERE asset_id=$1 FOR UPDATE", item.Asset).Scan(&revision, &event); err != nil {
			return AssignmentUndoResult{}, err
		}
		if revision != item.AfterRevision {
			return AssignmentUndoResult{}, conflict("部分资产之后已有新的归属，请重新预览，不能覆盖他人修改")
		}
		events[item.Asset] = event
		for _, old := range item.Before {
			if err = activeRef(ctx, tx, "department", "receiver_id", old.DepartmentID); err != nil {
				return AssignmentUndoResult{}, conflict("原接收部门已停用，请重新选择可用接收方")
			}
			if err = activeRef(ctx, tx, "business_line", "receiver_id", old.BusinessLineID); err != nil {
				return AssignmentUndoResult{}, conflict("原接收业务线已停用，请重新选择可用接收方")
			}
		}
	}
	// Effective time is taken after all asset locks, not before waiting for events.
	if err = tx.QueryRowContext(ctx, "SELECT clock_timestamp()").Scan(&at); err != nil {
		return AssignmentUndoResult{}, err
	}
	out := AssignmentUndoResult{OperationID: op}
	for _, item := range items {
		before, _, e := ownershipRows(ctx, tx, item.Asset)
		if e != nil {
			return out, e
		}
		mode := "restore"
		if events[item.Asset] == item.EventRevision {
			if _, err = tx.ExecContext(ctx, "UPDATE org.asset_ownership SET voided_at=$2,voided_by=$3 WHERE id=ANY($1) AND voided_at IS NULL", item.AfterIDs, at, op); err != nil {
				return out, err
			}
			for _, old := range item.Before {
				if _, err = tx.ExecContext(ctx, "UPDATE org.asset_ownership SET valid_to=$2 WHERE id=$1", old.ID, old.ValidTo); err != nil {
					return out, err
				}
			}
			out.Restored++
		} else {
			mode = "reverse"
			if _, err = tx.ExecContext(ctx, "UPDATE org.asset_ownership SET valid_to=$2 WHERE asset_id=$1 AND valid_to IS NULL AND voided_at IS NULL", item.Asset, at); err != nil {
				return out, err
			}
			for _, old := range item.Before {
				if _, err = tx.ExecContext(ctx, `INSERT INTO org.asset_ownership(asset_id,department_id,business_line_id,source,rule_id,rule_revision_id,valid_from,finding_from,finding_to,operation_id,created_by) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,'ARTEX 管理员')`, item.Asset, old.DepartmentID, old.BusinessLineID, old.Source, old.RuleID, old.RuleRevisionID, at, old.FindingFrom, old.FindingTo, op); err != nil {
					return out, err
				}
			}
			if len(item.Before) == 0 {
				if _, err = tx.ExecContext(ctx, "INSERT INTO org.asset_ownership(asset_id,source,valid_from,operation_id,created_by) VALUES($1,'manual',$2,$3,'ARTEX 管理员')", item.Asset, at, op); err != nil {
					return out, err
				}
			}
			out.Reversed++
		}
		if _, err = tx.ExecContext(ctx, "UPDATE org.asset_registry SET ownership_revision=ownership_revision+1 WHERE asset_id=$1", item.Asset); err != nil {
			return out, err
		}
		after, _, e := ownershipRows(ctx, tx, item.Asset)
		if e != nil {
			return out, e
		}
		if err = audit(ctx, tx, op, at, "asset", item.Asset, "assignment_undo_"+mode, before, after); err != nil {
			return out, err
		}
	}
	for _, r := range rulesToDisable {
		if _, err = tx.ExecContext(ctx, "UPDATE org.attribution_rule_revision SET valid_to=$2 WHERE id=$1", r.RevisionID, at); err != nil {
			return out, err
		}
		var domain, cidr any
		if r.MatchType == "domain" {
			domain = r.MatchValue
		} else {
			cidr = r.MatchValue
		}
		old := r
		r.Revision++
		r.Active = false
		if err = tx.QueryRowContext(ctx, `INSERT INTO org.attribution_rule_revision(rule_id,match_type,domain_pattern,ip_cidr,department_id,business_line_id,priority,active,valid_from,operation_id,created_by) VALUES($1,$2,$3,$4,$5,$6,$7,false,$8,$9,'ARTEX 管理员') RETURNING id`, r.ID, r.MatchType, domain, cidr, r.DepartmentID, r.BusinessLineID, r.Priority, at, op).Scan(&r.RevisionID); err != nil {
			return out, err
		}
		if _, err = tx.ExecContext(ctx, "UPDATE org.attribution_rule SET revision=$2 WHERE id=$1", r.ID, r.Revision); err != nil {
			return out, err
		}
		if err = audit(ctx, tx, op, at, "rule", r.ID, "undo_generated_rule", old, r); err != nil {
			return out, err
		}
	}
	if backdate {
		// Restore the snapshot that preceded the original approved correction. Events
		// actually recorded after that batch keep their original attribution unchanged.
		_, err = tx.ExecContext(ctx, `INSERT INTO org.finding_event_attribution_correction(history_id,finding_id,asset_id,department_id,business_line_id,owner_id,asset_ownership_id,owner_resolution,operation_id,reason,details)
 SELECT c.history_id,c.finding_id,c.asset_id,CASE WHEN p.id IS NOT NULL THEN p.department_id ELSE h.department_id END,CASE WHEN p.id IS NOT NULL THEN p.business_line_id ELSE h.business_line_id END,CASE WHEN p.id IS NOT NULL THEN p.owner_id ELSE h.owner_id END,CASE WHEN p.id IS NOT NULL THEN p.asset_ownership_id ELSE h.asset_ownership_id END,COALESCE(p.owner_resolution,h.owner_resolution,'unassigned'),$2::uuid,'backdate_undo',jsonb_build_object('undo_of',$1::text)
 FROM org.finding_event_attribution_correction c JOIN public.finding_history h ON h.id=c.history_id LEFT JOIN LATERAL(SELECT * FROM org.finding_event_attribution_correction older WHERE older.history_id=c.history_id AND older.id<c.id ORDER BY older.id DESC LIMIT 1)p ON true WHERE c.operation_id=$1::uuid ON CONFLICT(history_id,operation_id) DO NOTHING`, original, op)
		if err != nil {
			return out, err
		}
	}
	out.Mode = "restore"
	if out.Reversed > 0 {
		out.Mode = "reverse"
	}
	if out.Reversed > 0 && out.Restored > 0 {
		out.Mode = "mixed"
	}
	if _, err = tx.ExecContext(ctx, "UPDATE org.change_operation SET state='undone' WHERE id=$1", original); err != nil {
		return out, err
	}
	if err = finishRuleOperation(ctx, tx, op, out, len(rulesToDisable) > 0); err != nil {
		return out, err
	}
	return out, tx.Commit()
}
