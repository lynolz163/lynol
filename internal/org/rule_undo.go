package org

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
)

// Undoing a saved rule restores configuration from now onward. Existing asset
// ownership references its original rule version and is deliberately untouched.
func undoRuleTx(ctx context.Context, tx *sql.Tx, id int64, oldJSON []byte, original, key, digest string) (ChangeResult, error) {
	all, err := loadRules(ctx, tx)
	if err != nil {
		return ChangeResult{}, err
	}
	var current *Rule
	for i := range all {
		if all[i].ID == id {
			current = &all[i]
			break
		}
	}
	if current == nil {
		return ChangeResult{}, bad("id", "规则不存在")
	}
	restored := *current
	if string(oldJSON) == "null" || string(oldJSON) == "{}" {
		restored.Active = false
	} else {
		if err = json.Unmarshal(oldJSON, &restored); err != nil {
			return ChangeResult{}, err
		}
	}
	restored.ID = id
	restored.Revision = current.Revision + 1
	if err = validateRuleTarget(ctx, tx, restored); err != nil {
		return ChangeResult{}, err
	}
	var at time.Time
	var op string
	if err = tx.QueryRowContext(ctx, "SELECT clock_timestamp()").Scan(&at); err != nil {
		return ChangeResult{}, err
	}
	err = tx.QueryRowContext(ctx, `INSERT INTO org.change_operation(idempotency_key,request_hash,kind,actor_type,actor_id,actor_label,state,effective_at,undo_of) VALUES($1,$2,'rule_undo','admin','ARTEX','ARTEX 管理员','running',$3,$4) RETURNING id`, key, digest, at, original).Scan(&op)
	if err != nil {
		return ChangeResult{}, err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE org.attribution_rule_revision SET valid_to=$2 WHERE rule_id=$1 AND valid_to IS NULL AND voided_at IS NULL", id, at); err != nil {
		return ChangeResult{}, err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE org.attribution_rule SET revision=$2 WHERE id=$1", id, restored.Revision); err != nil {
		return ChangeResult{}, err
	}
	var domain, cidr any
	if restored.MatchType == "domain" {
		domain = restored.MatchValue
	} else {
		cidr = restored.MatchValue
	}
	err = tx.QueryRowContext(ctx, `INSERT INTO org.attribution_rule_revision(rule_id,match_type,domain_pattern,ip_cidr,department_id,business_line_id,priority,active,valid_from,operation_id,created_by) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,'ARTEX 管理员') RETURNING id`, id, restored.MatchType, domain, cidr, restored.DepartmentID, restored.BusinessLineID, restored.Priority, restored.Active, at, op).Scan(&restored.RevisionID)
	if err != nil {
		return ChangeResult{}, err
	}
	if err = audit(ctx, tx, op, at, "rule", id, "undo", current, restored); err != nil {
		return ChangeResult{}, err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE org.change_operation SET state='undone' WHERE id=$1", original); err != nil {
		return ChangeResult{}, err
	}
	result := ChangeResult{OperationID: op, EntityID: id, EffectiveAt: at}
	return result, finishRuleOperation(ctx, tx, op, result, true)
}
