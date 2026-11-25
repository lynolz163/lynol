package org

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

func (s *Store) destructive(ctx context.Context, tx *sql.Tx, c Command, d Directory, op string, at time.Time) error {
	receiver := c.ReceiverID
	if receiver != nil && *receiver == c.ID && c.Action != "migrate" {
		return bad("receiver_id", "接收方不能是当前对象")
	}
	switch c.Kind {
	case "person":
		raw, _, _ := entity(d, c.Kind, c.ID)
		p := raw.(Person)
		if len(p.Roles) > 0 && receiver == nil {
			return bad("receiver_id", "该人员仍担任负责人，请选择交接人")
		}
		if err := activeRef(ctx, tx, "app_user", "receiver_id", receiver); err != nil {
			return err
		}
		if receiver != nil {
			for _, b := range d.BusinessLines {
				var affected bool
				if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM org.business_line_owner_history WHERE business_line_id=$1 AND valid_to IS NULL AND voided_at IS NULL AND (owner_id=$2 OR backup_id=$2))", b.ID, c.ID).Scan(&affected); err != nil {
					return err
				}
				if !affected {
					continue
				}
				_, err := tx.ExecContext(ctx, `WITH old AS (UPDATE org.business_line_owner_history SET valid_to=$3 WHERE business_line_id=$1 AND valid_to IS NULL AND voided_at IS NULL AND (owner_id=$2 OR backup_id=$2) RETURNING *)
 INSERT INTO org.business_line_owner_history(business_line_id,owner_id,backup_id,line_active,valid_from,finding_from,finding_to,operation_id,created_by)
 SELECT business_line_id,CASE WHEN owner_id=$2 THEN $4 ELSE owner_id END,
 CASE WHEN owner_id=$2 AND backup_id=$4 THEN NULL WHEN backup_id=$2 AND owner_id=$4 THEN NULL WHEN backup_id=$2 THEN $4 ELSE backup_id END,
 line_active,$3::timestamptz,finding_from,finding_to,$5::uuid,'ARTEX 管理员' FROM old`, b.ID, c.ID, at, receiver, op)
				if err != nil {
					return err
				}
				if _, err = tx.ExecContext(ctx, "UPDATE org.business_line SET revision=revision+1 WHERE id=$1", b.ID); err != nil {
					return err
				}
				updated, e := directory(ctx, tx)
				if e != nil {
					return e
				}
				next, _, _ := entity(updated, "business_line", b.ID)
				if err = audit(ctx, tx, op, at, "business_line", b.ID, "handover", b, next); err != nil {
					return err
				}
			}
			for _, dep := range d.Departments {
				res, err := tx.ExecContext(ctx, `WITH old AS (UPDATE org.department_head_history SET valid_to=$3 WHERE department_id=$1 AND head_id=$2 AND valid_to IS NULL AND voided_at IS NULL RETURNING *)
 INSERT INTO org.department_head_history(department_id,head_id,department_active,valid_from,finding_from,finding_to,operation_id,created_by)
 SELECT department_id,$4::bigint,department_active,$3::timestamptz,finding_from,finding_to,$5::uuid,'ARTEX 管理员' FROM old`, dep.ID, c.ID, at, receiver, op)
				if err != nil {
					return err
				}
				n, _ := res.RowsAffected()
				if n > 0 {
					if _, err = tx.ExecContext(ctx, "UPDATE org.department SET revision=revision+1 WHERE id=$1", dep.ID); err != nil {
						return err
					}
					if err = audit(ctx, tx, op, at, "department", dep.ID, "handover", dep, map[string]any{"head_id": receiver}); err != nil {
						return err
					}
				}
			}
			res, err := tx.ExecContext(ctx, `WITH old AS (UPDATE org.fallback_owner_history SET valid_to=$2 WHERE owner_id=$1 AND valid_to IS NULL AND voided_at IS NULL RETURNING *)
 INSERT INTO org.fallback_owner_history(singleton,owner_id,valid_from,finding_from,finding_to,operation_id,created_by)
 SELECT singleton,$3::bigint,$2::timestamptz,finding_from,finding_to,$4::uuid,'ARTEX 管理员' FROM old`, c.ID, at, receiver, op)
			if err != nil {
				return err
			}
			n, _ := res.RowsAffected()
			if n > 0 {
				if err = audit(ctx, tx, op, at, "fallback", 1, "handover", map[string]any{"owner_id": c.ID}, map[string]any{"owner_id": receiver}); err != nil {
					return err
				}
			}
		}
		if _, err := tx.ExecContext(ctx, "UPDATE org.app_user SET active=false,revision=revision+1 WHERE id=$1", c.ID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "UPDATE org.app_user_state_history SET valid_to=$2 WHERE user_id=$1 AND valid_to IS NULL AND voided_at IS NULL", c.ID, at); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, "INSERT INTO org.app_user_state_history(user_id,department_id,dev_group_id,active,valid_from,operation_id,created_by) VALUES($1,$2,$3,false,$4,$5,'ARTEX 管理员')", c.ID, p.DepartmentID, p.DevGroupID, at, op)
		return err
	case "dev_group":
		var refs int
		if err := tx.QueryRowContext(ctx, "SELECT (SELECT count(*) FROM org.app_user WHERE dev_group_id=$1)+(SELECT count(*) FROM org.dev_group_business_line WHERE dev_group_id=$1 AND valid_to IS NULL AND voided_at IS NULL)", c.ID).Scan(&refs); err != nil {
			return err
		}
		if refs > 0 {
			return bad("receiver_id", "研发组仍有成员或关联业务线，请先移走成员并解除业务线关联")
		}
		_, err := tx.ExecContext(ctx, "UPDATE org.dev_group SET active=false,revision=revision+1 WHERE id=$1", c.ID)
		return err
	case "business_line":
		raw, _, _ := entity(d, c.Kind, c.ID)
		b := raw.(BusinessLine)
		if c.Action == "migrate" {
			if receiver == nil {
				return bad("receiver_id", "请选择目标部门")
			}
			if *receiver == b.DepartmentID {
				return bad("receiver_id", "目标部门与当前部门相同")
			}
			if err := activeRef(ctx, tx, "department", "receiver_id", receiver); err != nil {
				return err
			}
			return migrateLine(ctx, tx, b, *receiver, op, at)
		}
		var count int
		if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM org.asset_ownership WHERE business_line_id=$1 AND valid_to IS NULL AND voided_at IS NULL", c.ID).Scan(&count); err != nil {
			return err
		}
		if (count > 0 || b.Open > 0) && receiver == nil {
			return bad("receiver_id", "该业务线仍有资产或待处置漏洞，请选择接收业务线")
		}
		if receiver != nil {
			if err := activeRef(ctx, tx, "business_line", "receiver_id", receiver); err != nil {
				return err
			}
			var dep int64
			if err := tx.QueryRowContext(ctx, "SELECT department_id FROM org.business_line WHERE id=$1", receiver).Scan(&dep); err != nil {
				return err
			}
			if err := transferAssets(ctx, tx, "business_line_id", c.ID, dep, receiver, op, at); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, "UPDATE org.business_line SET active=false,revision=revision+1 WHERE id=$1", c.ID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "UPDATE org.dev_group_business_line SET valid_to=$2 WHERE business_line_id=$1 AND valid_to IS NULL AND voided_at IS NULL", c.ID, at); err != nil {
			return err
		}
		return rotateOwners(ctx, tx, "business_line_owner_history", "business_line_id", "owner_id,backup_id,line_active", c.ID, at, false, op, b.PrimaryID, b.BackupID, false)
	case "department":
		raw, _, _ := entity(d, c.Kind, c.ID)
		dep := raw.(Department)
		impact := impactFromDirectory(d, c)
		var assets int
		if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM org.asset_ownership WHERE department_id=$1 AND valid_to IS NULL AND voided_at IS NULL", c.ID).Scan(&assets); err != nil {
			return err
		}
		if (len(impact.BusinessLines) > 0 || dep.Open > 0 || assets > 0) && receiver == nil {
			return bad("receiver_id", "部门仍有业务线、资产或待处置漏洞，请指定接收部门")
		}
		if receiver != nil {
			if err := activeRef(ctx, tx, "department", "receiver_id", receiver); err != nil {
				return err
			}
			for _, b := range d.BusinessLines {
				if b.DepartmentID == c.ID && b.Active {
					if err := migrateLine(ctx, tx, b, *receiver, op, at); err != nil {
						return err
					}
					if err := audit(ctx, tx, op, at, "business_line", b.ID, "migrate", b, map[string]any{"department_id": receiver}); err != nil {
						return err
					}
				}
			}
			if err := transferAssets(ctx, tx, "department_id", c.ID, *receiver, nil, op, at); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, "UPDATE org.department SET active=false,revision=revision+1 WHERE id=$1", c.ID); err != nil {
			return err
		}
		return rotateOwners(ctx, tx, "department_head_history", "department_id", "head_id,department_active", c.ID, at, false, op, dep.HeadID, false)
	}
	return bad("kind", "不支持此操作")
}

func migrateLine(ctx context.Context, tx *sql.Tx, b BusinessLine, dep int64, op string, at time.Time) error {
	if _, err := tx.ExecContext(ctx, "UPDATE org.business_line_department_history SET valid_to=$2 WHERE business_line_id=$1 AND valid_to IS NULL AND voided_at IS NULL", b.ID, at); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO org.business_line_department_history(business_line_id,department_id,valid_from,operation_id,created_by) VALUES($1,$2,$3,$4,'ARTEX 管理员')", b.ID, dep, at, op); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "UPDATE org.business_line SET department_id=$2,revision=revision+1 WHERE id=$1", b.ID, dep); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "UPDATE org.dev_group_business_line SET valid_to=$2 WHERE business_line_id=$1 AND valid_to IS NULL AND voided_at IS NULL", b.ID, at); err != nil {
		return err
	}
	return transferAssets(ctx, tx, "business_line_id", b.ID, dep, &b.ID, op, at)
}
func transferAssets(ctx context.Context, tx *sql.Tx, field string, source, dep int64, line *int64, op string, at time.Time) error {
	rows, err := tx.QueryContext(ctx, "SELECT DISTINCT asset_id FROM org.asset_ownership WHERE "+field+"=$1 AND valid_to IS NULL AND voided_at IS NULL ORDER BY asset_id", source)
	if err != nil {
		return err
	}
	ids := []int64{}
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, id := range ids {
		if _, err = tx.ExecContext(ctx, "SELECT asset_id FROM org.asset_registry WHERE asset_id=$1 FOR UPDATE", id); err != nil {
			return err
		}
		var old []byte
		if err = tx.QueryRowContext(ctx, "SELECT COALESCE(jsonb_agg(to_jsonb(o)),'[]') FROM org.asset_ownership o WHERE asset_id=$1 AND "+field+"=$2 AND valid_to IS NULL AND voided_at IS NULL", id, source).Scan(&old); err != nil {
			return err
		}
		q := fmt.Sprintf(`WITH old AS (UPDATE org.asset_ownership SET valid_to=$3 WHERE asset_id=$1 AND %s=$2 AND valid_to IS NULL AND voided_at IS NULL RETURNING *)
 INSERT INTO org.asset_ownership(asset_id,department_id,business_line_id,source,valid_from,finding_from,finding_to,operation_id,created_by)
 SELECT asset_id,$4::bigint,$5::bigint,'manual',$3::timestamptz,finding_from,finding_to,$6::uuid,'ARTEX 管理员' FROM old`, field)
		if _, err = tx.ExecContext(ctx, q, id, source, at, dep, line, op); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "UPDATE org.asset_registry SET ownership_revision=ownership_revision+1 WHERE asset_id=$1", id); err != nil {
			return err
		}
		if err = audit(ctx, tx, op, at, "asset", id, "transfer", jsonRaw(old), map[string]any{"department_id": dep, "business_line_id": line}); err != nil {
			return err
		}
	}
	return nil
}

type jsonRaw = json.RawMessage
