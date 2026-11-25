package org

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"
)

func (s *Store) Change(ctx context.Context, c Command) (ChangeResult, error) {
	if err := ValidateCommand(&c); err != nil {
		return ChangeResult{}, err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return ChangeResult{}, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "SELECT config_revision FROM org.sync_state WHERE singleton FOR UPDATE"); err != nil {
		return ChangeResult{}, err
	}
	hash := sha256.Sum256([]byte(jsonValue(c)))
	digest := hex.EncodeToString(hash[:])
	var previousHash string
	var resultRaw []byte
	err = tx.QueryRowContext(ctx, "SELECT request_hash,result FROM org.change_operation WHERE idempotency_key=$1", c.IdempotencyKey).Scan(&previousHash, &resultRaw)
	if err == nil {
		if previousHash != digest {
			return ChangeResult{}, conflict("同一提交编号不能用于不同修改")
		}
		var r ChangeResult
		err = json.Unmarshal(resultRaw, &r)
		return r, err
	}
	if err != sql.ErrNoRows {
		return ChangeResult{}, err
	}
	var op string
	var at time.Time
	if err = tx.QueryRowContext(ctx, "SELECT clock_timestamp()").Scan(&at); err != nil {
		return ChangeResult{}, err
	}
	if err = tx.QueryRowContext(ctx, `INSERT INTO org.change_operation(idempotency_key,request_hash,kind,actor_type,actor_id,actor_label,state,effective_at) VALUES($1,$2,$3,'admin','ARTEX','ARTEX 管理员','running',$4) RETURNING id`, c.IdempotencyKey, digest, c.Kind+"_"+c.Action, at).Scan(&op); err != nil {
		return ChangeResult{}, err
	}
	r, err := s.changeTx(ctx, tx, c, op, at)
	if err != nil {
		return r, err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE org.change_operation SET state='succeeded',finished_at=clock_timestamp(),result=$2 WHERE id=$1", op, jsonValue(r)); err != nil {
		return r, err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE org.sync_state SET config_revision=config_revision+1 WHERE singleton"); err != nil {
		return r, err
	}
	err = tx.Commit()
	return r, err
}

// rotateOwners closes overlapping effective rows, preserving birth cohorts outside
// this edit. Identifiers are fixed by callers, never supplied by HTTP parameters.
func rotateOwners(ctx context.Context, tx *sql.Tx, table, key, columns string, id int64, at time.Time, future bool, op string, values ...any) error {
	q := fmt.Sprintf(`WITH closed AS (UPDATE org.%s SET valid_to=$2::timestamptz WHERE %s=$1 AND valid_to IS NULL AND voided_at IS NULL AND (NOT $3::boolean OR finding_to IS NULL OR finding_to>$2::timestamptz) RETURNING *)
 INSERT INTO org.%s(%s,%s,valid_from,finding_from,finding_to,operation_id,created_by)
 SELECT %s,%s,$2::timestamptz,finding_from,LEAST(finding_to,$2::timestamptz),$4::uuid,'ARTEX 管理员' FROM closed WHERE $3::boolean AND (finding_from IS NULL OR finding_from<$2::timestamptz)`, table, key, table, key, columns, key, columns)
	if _, err := tx.ExecContext(ctx, q, id, at, future, op); err != nil {
		return err
	}
	args := []any{id, at, future, op}
	marks := ""
	for i, v := range values {
		if i > 0 {
			marks += ","
		}
		marks += fmt.Sprintf("$%d", i+5)
		args = append(args, v)
	}
	q = fmt.Sprintf(`INSERT INTO org.%s(%s,%s,valid_from,finding_from,operation_id,created_by) VALUES($1,%s,$2::timestamptz,CASE WHEN $3::boolean THEN $2::timestamptz ELSE NULL END,$4::uuid,'ARTEX 管理员')`, table, key, columns, marks)
	_, err := tx.ExecContext(ctx, q, args...)
	return err
}
func audit(ctx context.Context, tx *sql.Tx, op string, at time.Time, kind string, id int64, action string, old, newValue any) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO org.audit_log(operation_id,actor_type,actor_id,actor_label,object_type,object_id,action,effective_at,old_value,new_value) SELECT id,actor_type,actor_id,actor_label,$2,$3,$4,$5::timestamptz,$6::jsonb,$7::jsonb FROM org.change_operation WHERE id=$1`, op, kind, fmt.Sprint(id), action, at, jsonValue(old), jsonValue(newValue))
	return err
}

func (s *Store) changeTx(ctx context.Context, tx *sql.Tx, c Command, op string, at time.Time) (ChangeResult, error) {
	r := ChangeResult{OperationID: op, EntityID: c.ID, EffectiveAt: at}
	before, err := directory(ctx, tx)
	if err != nil {
		return r, err
	}
	old, revision, exists := entity(before, c.Kind, c.ID)
	if c.ID != 0 && !exists {
		return r, bad("id", "对象不存在，请刷新列表")
	}
	if c.ID != 0 {
		if err = checkRevision(revision, c.Revision); err != nil {
			return r, err
		}
	}
	impact := impactFromDirectory(before, c)
	if (c.Action == "disable" || c.Action == "migrate") && !c.ConfirmImpact {
		return r, bad("confirm_impact", "请先确认影响范围后再执行")
	}
	if c.Action != "save" {
		err = s.destructive(ctx, tx, c, before, op, at)
		if err != nil {
			return r, err
		}
		r.AffectedOpen = impact.Open
	} else {
		future := c.Scope == "future_findings" && c.ID != 0
		if len(c.ChannelDrafts) > 0 {
			if err = s.saveNotificationDrafts(ctx, tx, &c, op, at); err != nil {
				return r, err
			}
		}
		if c.Kind == "department" || c.Kind == "business_line" {
			var channels struct {
				IDs []int64 `json:"channel_ids"`
			}
			json.Unmarshal(c.Data, &channels)
			for _, id := range channels.IDs {
				var exists bool
				if err = tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM public.notification_channels WHERE id=$1)", id).Scan(&exists); err != nil {
					return r, err
				}
				if !exists {
					return r, bad("channel_ids", "通知渠道不存在，请刷新后重新选择")
				}
			}
		}
		switch c.Kind {
		case "department":
			var v Department
			json.Unmarshal(c.Data, &v)
			if err = activeRef(ctx, tx, "app_user", "head_id", v.HeadID); err != nil {
				return r, err
			}
			changed := true
			if c.ID == 0 {
				v.Active = true
				err = tx.QueryRowContext(ctx, "INSERT INTO org.department(name,code,channel_ids,created_by) VALUES($1,$2,$3,'ARTEX 管理员') RETURNING id", v.Name, v.Code, nonNil(v.ChannelIDs)).Scan(&c.ID)
			} else {
				o := old.(Department)
				if o.Active && !v.Active {
					return r, bad("active", "请使用停用操作并选择接收方")
				}
				changed = !sameID(o.HeadID, v.HeadID) || o.Active != v.Active
				if changed && impact.Open > 0 && !c.ConfirmImpact {
					return r, bad("confirm_impact", fmt.Sprintf("将影响 %d 个待处置漏洞，请确认生效范围", impact.Open))
				}
				_, err = tx.ExecContext(ctx, "UPDATE org.department SET name=$2,code=$3,channel_ids=$4,active=$5,revision=revision+1 WHERE id=$1", c.ID, v.Name, v.Code, nonNil(v.ChannelIDs), v.Active)
			}
			if err == nil && changed {
				err = rotateOwners(ctx, tx, "department_head_history", "department_id", "head_id,department_active", c.ID, at, future, op, v.HeadID, v.Active)
			}
		case "dev_group":
			var v Group
			json.Unmarshal(c.Data, &v)
			if err = activeRef(ctx, tx, "department", "department_id", &v.DepartmentID); err != nil {
				return r, err
			}
			if c.ID == 0 {
				err = tx.QueryRowContext(ctx, "INSERT INTO org.dev_group(name,department_id,created_by) VALUES($1,$2,'ARTEX 管理员') RETURNING id", v.Name, v.DepartmentID).Scan(&c.ID)
			} else {
				o := old.(Group)
				if o.DepartmentID != v.DepartmentID {
					return r, bad("department_id", "研发组不能直接跨部门，请先转移成员并新建研发组")
				}
				if o.Active && !v.Active {
					return r, bad("active", "请使用停用操作")
				}
				_, err = tx.ExecContext(ctx, "UPDATE org.dev_group SET name=$2,active=$3,revision=revision+1 WHERE id=$1", c.ID, v.Name, v.Active)
			}
		case "person":
			var v Person
			json.Unmarshal(c.Data, &v)
			if err = activeRef(ctx, tx, "department", "department_id", v.DepartmentID); err != nil {
				return r, err
			}
			if err = activeRef(ctx, tx, "dev_group", "dev_group_id", v.DevGroupID); err != nil {
				return r, err
			}
			if v.DevGroupID != nil {
				var dept int64
				if err = tx.QueryRowContext(ctx, "SELECT department_id FROM org.dev_group WHERE id=$1", v.DevGroupID).Scan(&dept); err != nil {
					return r, err
				}
				if v.DepartmentID == nil || dept != *v.DepartmentID {
					return r, bad("dev_group_id", "请选择所属部门内的研发组")
				}
			}
			changed := true
			if c.ID == 0 {
				v.Active = true
				err = tx.QueryRowContext(ctx, "INSERT INTO org.app_user(name,email,department_id,dev_group_id,created_by) VALUES($1,$2,$3,$4,'ARTEX 管理员') RETURNING id", v.Name, v.Email, v.DepartmentID, v.DevGroupID).Scan(&c.ID)
			} else {
				o := old.(Person)
				if o.Active && !v.Active {
					return r, bad("active", "请使用停用并交接操作")
				}
				changed = !sameID(o.DepartmentID, v.DepartmentID) || !sameID(o.DevGroupID, v.DevGroupID) || o.Active != v.Active
				_, err = tx.ExecContext(ctx, "UPDATE org.app_user SET name=$2,email=$3,department_id=$4,dev_group_id=$5,active=$6,revision=revision+1 WHERE id=$1", c.ID, v.Name, v.Email, v.DepartmentID, v.DevGroupID, v.Active)
			}
			if err == nil && changed {
				_, err = tx.ExecContext(ctx, "UPDATE org.app_user_state_history SET valid_to=$2 WHERE user_id=$1 AND valid_to IS NULL AND voided_at IS NULL", c.ID, at)
				if err == nil {
					_, err = tx.ExecContext(ctx, "INSERT INTO org.app_user_state_history(user_id,department_id,dev_group_id,active,valid_from,operation_id,created_by) VALUES($1,$2,$3,$4,$5,$6,'ARTEX 管理员')", c.ID, v.DepartmentID, v.DevGroupID, v.Active, at, op)
				}
			}
		case "business_line":
			var v BusinessLine
			json.Unmarshal(c.Data, &v)
			if err = activeRef(ctx, tx, "department", "department_id", &v.DepartmentID); err != nil {
				return r, err
			}
			if err = activeRef(ctx, tx, "app_user", "primary_id", v.PrimaryID); err != nil {
				return r, err
			}
			if err = activeRef(ctx, tx, "app_user", "backup_id", v.BackupID); err != nil {
				return r, err
			}
			seen := map[int64]bool{}
			for _, g := range v.GroupIDs {
				if seen[g] {
					return r, bad("group_ids", "研发组不能重复")
				}
				seen[g] = true
				var ok bool
				if err = tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM org.dev_group WHERE id=$1 AND department_id=$2 AND active)", g, v.DepartmentID).Scan(&ok); err != nil {
					return r, err
				}
				if !ok {
					return r, bad("group_ids", "请选择同部门已启用的研发组")
				}
			}
			changed := true
			if c.ID == 0 {
				v.Active = true
				err = tx.QueryRowContext(ctx, "INSERT INTO org.business_line(name,code,department_id,channel_ids,created_by) VALUES($1,$2,$3,$4,'ARTEX 管理员') RETURNING id", v.Name, v.Code, v.DepartmentID, nonNil(v.ChannelIDs)).Scan(&c.ID)
				if err == nil {
					_, err = tx.ExecContext(ctx, "INSERT INTO org.business_line_department_history(business_line_id,department_id,valid_from,operation_id,created_by) VALUES($1,$2,$3,$4,'ARTEX 管理员')", c.ID, v.DepartmentID, at, op)
				}
			} else {
				o := old.(BusinessLine)
				if o.DepartmentID != v.DepartmentID {
					return r, bad("department_id", "请使用“迁移业务线”操作变更所属部门")
				}
				if o.Active && !v.Active {
					return r, bad("active", "请使用停用并转交操作")
				}
				changed = !sameID(o.PrimaryID, v.PrimaryID) || !sameID(o.BackupID, v.BackupID) || o.Active != v.Active
				if changed && impact.Open > 0 && !c.ConfirmImpact {
					return r, bad("confirm_impact", fmt.Sprintf("将影响 %d 个待处置漏洞的处理人，请确认生效范围", impact.Open))
				}
				_, err = tx.ExecContext(ctx, "UPDATE org.business_line SET name=$2,code=$3,channel_ids=$4,active=$5,revision=revision+1 WHERE id=$1", c.ID, v.Name, v.Code, nonNil(v.ChannelIDs), v.Active)
			}
			if err != nil {
				return r, err
			}
			if changed {
				if err = rotateOwners(ctx, tx, "business_line_owner_history", "business_line_id", "owner_id,backup_id,line_active", c.ID, at, future, op, v.PrimaryID, v.BackupID, v.Active); err != nil {
					return r, err
				}
			}
			if _, err = tx.ExecContext(ctx, "UPDATE org.dev_group_business_line SET valid_to=$2 WHERE business_line_id=$1 AND valid_to IS NULL AND voided_at IS NULL", c.ID, at); err != nil {
				return r, err
			}
			for _, g := range v.GroupIDs {
				if _, err = tx.ExecContext(ctx, "INSERT INTO org.dev_group_business_line(dev_group_id,business_line_id,department_id,valid_from,operation_id,created_by) VALUES($1,$2,$3,$4,$5,'ARTEX 管理员')", g, c.ID, v.DepartmentID, at, op); err != nil {
					return r, err
				}
			}
			slaChanged := c.ID != r.EntityID
			if o, ok := old.(BusinessLine); ok {
				slaChanged = jsonValue(o.SLA) != jsonValue(v.SLA)
			}
			if slaChanged {
				if _, err = tx.ExecContext(ctx, "UPDATE org.sla_policy_history SET valid_to=$2 WHERE business_line_id=$1 AND valid_to IS NULL AND voided_at IS NULL", c.ID, at); err != nil {
					return r, err
				}
				var days any
				if v.SLA != nil {
					days = jsonValue(v.SLA)
				}
				_, err = tx.ExecContext(ctx, "INSERT INTO org.sla_policy_history(business_line_id,days,valid_from,operation_id,created_by) VALUES($1,$2,$3,$4,'ARTEX 管理员')", c.ID, days, at, op)
			}
		case "fallback":
			var v struct {
				OwnerID int64 `json:"owner_id"`
			}
			json.Unmarshal(c.Data, &v)
			if err = activeRef(ctx, tx, "app_user", "owner_id", &v.OwnerID); err != nil {
				return r, err
			}
			if impact.Open > 0 && !c.ConfirmImpact {
				return r, bad("confirm_impact", "修改兜底人员会影响待处置漏洞，请确认生效范围")
			}
			err = rotateOwners(ctx, tx, "fallback_owner_history", "singleton", "owner_id", 1, at, future, op, v.OwnerID)
		}
		if err != nil {
			return r, err
		}
		if !future {
			r.AffectedOpen = impact.Open
		}
	}
	r.EntityID = c.ID
	after, err := directory(ctx, tx)
	if err != nil {
		return r, err
	}
	next, _, _ := entity(after, c.Kind, c.ID)
	if err = audit(ctx, tx, op, at, c.Kind, c.ID, c.Action, old, next); err != nil {
		return r, err
	}
	return r, nil
}
func nonNil(v []int64) []int64 {
	if v == nil {
		return []int64{}
	}
	return v
}
