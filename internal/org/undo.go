package org

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"time"
)

func (s *Store) Undo(ctx context.Context, original, key string) (ChangeResult, error) {
	if _, err := uuid.Parse(original); err != nil {
		return ChangeResult{}, bad("id", "变更编号不正确")
	}
	if _, err := uuid.Parse(key); err != nil {
		return ChangeResult{}, bad("idempotency_key", "请刷新页面重新操作")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return ChangeResult{}, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "SELECT config_revision FROM org.sync_state WHERE singleton FOR UPDATE"); err != nil {
		return ChangeResult{}, err
	}
	digestBytes := sha256.Sum256([]byte("undo:" + original))
	digest := hex.EncodeToString(digestBytes[:])
	var hash string
	var resultJSON []byte
	err = tx.QueryRowContext(ctx, "SELECT request_hash,result FROM org.change_operation WHERE idempotency_key=$1", key).Scan(&hash, &resultJSON)
	if err == nil {
		if hash != digest {
			return ChangeResult{}, conflict("提交编号已用于其他操作")
		}
		var result ChangeResult
		err = json.Unmarshal(resultJSON, &result)
		return result, err
	}
	if err != sql.ErrNoRows {
		return ChangeResult{}, err
	}
	var kind, object, action, state, operationKind string
	var oldJSON []byte
	var originalAt time.Time
	var auditID int64
	err = tx.QueryRowContext(ctx, `SELECT a.object_type,a.object_id,a.action,a.old_value,o.effective_at,o.state,a.id,o.kind FROM org.audit_log a JOIN org.change_operation o ON o.id=a.operation_id WHERE o.id=$1 ORDER BY a.id DESC LIMIT 1`, original).Scan(&kind, &object, &action, &oldJSON, &originalAt, &state, &auditID, &operationKind)
	if err == sql.ErrNoRows {
		return ChangeResult{}, bad("id", "找不到该次变更")
	}
	if err != nil {
		return ChangeResult{}, err
	}
	if state != "succeeded" {
		return ChangeResult{}, conflict("此操作已撤销或尚未完成")
	}
	if operationKind == "people_import" {
		return ChangeResult{}, conflict("CSV 导入包含批量变更，请逐项编辑恢复，不能只撤销最后一行")
	}
	if action != "save" {
		return ChangeResult{}, conflict("停用、迁移或批量操作已涉及交接，请通过编辑或反向迁移恢复")
	}
	var newer bool
	if err = tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM org.audit_log WHERE object_type=$1 AND object_id=$2 AND id>$3)", kind, object, auditID).Scan(&newer); err != nil {
		return ChangeResult{}, err
	}
	if newer {
		return ChangeResult{}, conflict("该对象之后已有修改，不能覆盖新内容，请重新编辑")
	}
	var id int64
	if _, err = fmt.Sscan(object, &id); err != nil {
		return ChangeResult{}, err
	}
	if kind == "rule" {
		result, e := undoRuleTx(ctx, tx, id, oldJSON, original, key, digest)
		if e != nil {
			return result, e
		}
		return result, tx.Commit()
	}
	before, err := directory(ctx, tx)
	if err != nil {
		return ChangeResult{}, err
	}
	current, revision, exists := entity(before, kind, id)
	if !exists {
		return ChangeResult{}, bad("id", "对象不存在")
	}
	var op string
	var at time.Time
	if err = tx.QueryRowContext(ctx, "SELECT clock_timestamp()").Scan(&at); err != nil {
		return ChangeResult{}, err
	}
	if err = tx.QueryRowContext(ctx, `INSERT INTO org.change_operation(idempotency_key,request_hash,kind,actor_type,actor_id,actor_label,state,effective_at,undo_of) VALUES($1,$2,'undo','admin','ARTEX','ARTEX 管理员','running',$3,$4) RETURNING id`, key, digest, at, original).Scan(&op); err != nil {
		return ChangeResult{}, err
	}
	result := ChangeResult{OperationID: op, EntityID: id, EffectiveAt: at}
	if string(oldJSON) == "null" || string(oldJSON) == "{}" {
		result, err = s.changeTx(ctx, tx, Command{Kind: kind, Action: "disable", ID: id, Revision: revision, ConfirmImpact: true, Scope: "all_open"}, op, at)
	} else {
		// A later disable of a former owner must not make undo reassign to an
		// unavailable person. This is a transactional state check, not input parsing.
		var unavailable bool
		switch kind {
		case "business_line":
			err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM org.business_line_owner_history h WHERE h.business_line_id=$1 AND h.valid_to=$2 AND h.voided_at IS NULL AND (EXISTS(SELECT 1 FROM org.app_user p WHERE p.id=h.owner_id AND NOT p.active) OR EXISTS(SELECT 1 FROM org.app_user p WHERE p.id=h.backup_id AND NOT p.active)))`, id, originalAt).Scan(&unavailable)
		case "department":
			err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM org.department_head_history h JOIN org.app_user p ON p.id=h.head_id WHERE h.department_id=$1 AND h.valid_to=$2 AND h.voided_at IS NULL AND NOT p.active)`, id, originalAt).Scan(&unavailable)
		case "fallback":
			err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM org.fallback_owner_history h JOIN org.app_user p ON p.id=h.owner_id WHERE h.valid_to=$1 AND h.voided_at IS NULL AND NOT p.active)`, originalAt).Scan(&unavailable)
		}
		if err != nil {
			return result, err
		}
		if unavailable {
			return result, conflict("原负责人已停用，请通过编辑选择可用人员")
		}
		switch kind {
		case "department":
			var v Department
			json.Unmarshal(oldJSON, &v)
			_, err = tx.ExecContext(ctx, "UPDATE org.department SET name=$2,code=$3,channel_ids=$4,active=$5,revision=revision+1 WHERE id=$1", id, v.Name, v.Code, nonNil(v.ChannelIDs), v.Active)
		case "business_line":
			var v BusinessLine
			json.Unmarshal(oldJSON, &v)
			_, err = tx.ExecContext(ctx, "UPDATE org.business_line SET name=$2,code=$3,channel_ids=$4,active=$5,revision=revision+1 WHERE id=$1", id, v.Name, v.Code, nonNil(v.ChannelIDs), v.Active)
		case "person":
			var v Person
			json.Unmarshal(oldJSON, &v)
			_, err = tx.ExecContext(ctx, "UPDATE org.app_user SET name=$2,email=$3,department_id=$4,dev_group_id=$5,active=$6,revision=revision+1 WHERE id=$1", id, v.Name, v.Email, v.DepartmentID, v.DevGroupID, v.Active)
		case "dev_group":
			var v Group
			json.Unmarshal(oldJSON, &v)
			_, err = tx.ExecContext(ctx, "UPDATE org.dev_group SET name=$2,active=$3,revision=revision+1 WHERE id=$1", id, v.Name, v.Active)
		}
		if err != nil {
			return result, err
		}
		type history struct {
			table, key, columns string
			cohorts             bool
		}
		hs := []history{}
		switch kind {
		case "department":
			hs = []history{{"department_head_history", "department_id", "head_id,department_active", true}}
		case "business_line":
			hs = []history{{"business_line_owner_history", "business_line_id", "owner_id,backup_id,line_active", true}, {"sla_policy_history", "business_line_id", "days", false}, {"dev_group_business_line", "business_line_id", "dev_group_id,department_id", false}}
		case "person":
			hs = []history{{"app_user_state_history", "user_id", "department_id,dev_group_id,active", false}}
		case "fallback":
			hs = []history{{"fallback_owner_history", "singleton", "owner_id", true}}
		}
		for _, h := range hs {
			if _, err = tx.ExecContext(ctx, "UPDATE org."+h.table+" SET valid_to=$2 WHERE operation_id=$1 AND valid_to IS NULL AND voided_at IS NULL", original, at); err != nil {
				return result, err
			}
			columns := h.columns
			if h.cohorts {
				columns += ",finding_from,finding_to"
			}
			q := fmt.Sprintf("INSERT INTO org.%s(%s,%s,valid_from,operation_id,created_by) SELECT %s,%s,$3::timestamptz,$4::uuid,'ARTEX 管理员' FROM org.%s WHERE %s=$1 AND valid_to=$2 AND voided_at IS NULL", h.table, h.key, columns, h.key, columns, h.table, h.key)
			if _, err = tx.ExecContext(ctx, q, id, originalAt, at, op); err != nil {
				return result, err
			}
		}
		after, e := directory(ctx, tx)
		if e != nil {
			return result, e
		}
		next, _, _ := entity(after, kind, id)
		err = audit(ctx, tx, op, at, kind, id, "undo", current, next)
	}
	if err != nil {
		return result, err
	}
	if err = undoNotificationDrafts(ctx, tx, original, op, at); err != nil {
		return result, err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE org.change_operation SET state='undone' WHERE id=$1", original); err != nil {
		return result, err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE org.change_operation SET state='succeeded',finished_at=clock_timestamp(),result=$2 WHERE id=$1", op, jsonValue(result)); err != nil {
		return result, err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE org.sync_state SET config_revision=config_revision+1 WHERE singleton"); err != nil {
		return result, err
	}
	err = tx.Commit()
	return result, err
}
