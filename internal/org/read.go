package org

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
)

type reader interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

const directorySQL = `WITH open_findings AS (
 SELECT f.id,r.* FROM public.findings f CROSS JOIN LATERAL org.resolve_finding_owner(f.id,statement_timestamp()) r
 WHERE f.status IN ('pending','in_progress','confirmed')
), lines AS (
 SELECT b.*,h.owner_id primary_id,h.backup_id,(SELECT days FROM org.sla_policy_history s WHERE s.business_line_id=b.id AND s.voided_at IS NULL AND s.valid_to IS NULL) sla,
 COALESCE((SELECT jsonb_agg(dev_group_id ORDER BY dev_group_id) FROM org.dev_group_business_line WHERE business_line_id=b.id AND voided_at IS NULL AND valid_to IS NULL),'[]') group_ids
 FROM org.business_line b LEFT JOIN LATERAL (SELECT * FROM org.business_line_owner_history h WHERE h.business_line_id=b.id AND h.voided_at IS NULL AND tstzrange(h.valid_from,h.valid_to,'[)') @> statement_timestamp() AND tstzrange(h.finding_from,h.finding_to,'[)') @> statement_timestamp()) h ON true
), roles AS (
 SELECT DISTINCT owner_id user_id,'primary' kind,business_line_id object_id,b.name,(SELECT count(*) FROM open_findings f WHERE f.business_line_id=b.id AND f.owner_id=h.owner_id) open FROM org.business_line_owner_history h JOIN org.business_line b ON b.id=h.business_line_id WHERE h.valid_to IS NULL AND h.voided_at IS NULL AND h.line_active AND h.owner_id IS NOT NULL
 UNION SELECT DISTINCT backup_id,'backup',business_line_id,b.name,(SELECT count(*) FROM open_findings f WHERE f.business_line_id=b.id AND f.owner_id=h.backup_id) FROM org.business_line_owner_history h JOIN org.business_line b ON b.id=h.business_line_id WHERE h.valid_to IS NULL AND h.voided_at IS NULL AND h.line_active AND h.backup_id IS NOT NULL
 UNION SELECT DISTINCT head_id,'department_head',department_id,d.name,(SELECT count(*) FROM open_findings f WHERE f.department_id=d.id AND f.owner_id=h.head_id) FROM org.department_head_history h JOIN org.department d ON d.id=h.department_id WHERE h.valid_to IS NULL AND h.voided_at IS NULL AND h.department_active AND head_id IS NOT NULL
 UNION SELECT DISTINCT h.owner_id,'fallback',1,'全局兜底',(SELECT count(*) FROM open_findings f WHERE f.owner_id=h.owner_id) FROM org.fallback_owner_history h WHERE h.valid_to IS NULL AND h.voided_at IS NULL AND h.owner_id IS NOT NULL
)
SELECT jsonb_build_object(
 'departments',COALESCE((SELECT jsonb_agg(to_jsonb(d)||jsonb_build_object('head_id',(SELECT head_id FROM org.department_head_history h WHERE h.department_id=d.id AND h.voided_at IS NULL AND tstzrange(h.valid_from,h.valid_to,'[)') @> statement_timestamp() AND tstzrange(h.finding_from,h.finding_to,'[)') @> statement_timestamp()),'open',(SELECT count(*) FROM open_findings f WHERE f.department_id=d.id)) ORDER BY d.id) FROM org.department d),'[]'),
 'business_lines',COALESCE((SELECT jsonb_agg(to_jsonb(b)||jsonb_build_object('open',(SELECT count(*) FROM open_findings f WHERE f.business_line_id=b.id)) ORDER BY b.id) FROM lines b),'[]'),
 'people',COALESCE((SELECT jsonb_agg(to_jsonb(p)||jsonb_build_object('open',(SELECT count(*) FROM open_findings f WHERE f.owner_id=p.id),'roles',COALESCE((SELECT jsonb_agg(to_jsonb(r)-'user_id') FROM roles r WHERE r.user_id=p.id),'[]')) ORDER BY p.id) FROM org.app_user p),'[]'),
 'groups',COALESCE((SELECT jsonb_agg(to_jsonb(g)||jsonb_build_object('members',(SELECT count(*) FROM org.app_user p WHERE p.dev_group_id=g.id)) ORDER BY g.id) FROM org.dev_group g),'[]'),
 'fallback_id',(SELECT owner_id FROM org.fallback_owner_history h WHERE h.voided_at IS NULL AND tstzrange(h.valid_from,h.valid_to,'[)') @> statement_timestamp() AND tstzrange(h.finding_from,h.finding_to,'[)') @> statement_timestamp()),
 'sla',COALESCE((SELECT days FROM org.sla_policy_history WHERE business_line_id IS NULL AND valid_to IS NULL AND voided_at IS NULL),(SELECT value::jsonb FROM public.settings WHERE key='finding_sla_days'),'{"critical":3,"high":7,"medium":30,"low":90}'),
 'can_edit',true,'unassigned_open',(SELECT count(*) FROM open_findings WHERE department_id IS NULL),'config_revision',(SELECT config_revision FROM org.sync_state WHERE singleton))`

func directory(ctx context.Context, q reader) (Directory, error) {
	var raw []byte
	var d Directory
	err := q.QueryRowContext(ctx, directorySQL).Scan(&raw)
	if err == nil {
		err = json.Unmarshal(raw, &d)
	}
	return d, err
}
func (s *Store) Directory(ctx context.Context) (Directory, error) { return directory(ctx, s.DB) }
func entity(d Directory, kind string, id int64) (any, int64, bool) {
	switch kind {
	case "department":
		for _, v := range d.Departments {
			if v.ID == id {
				return v, v.Revision, true
			}
		}
	case "business_line":
		for _, v := range d.BusinessLines {
			if v.ID == id {
				return v, v.Revision, true
			}
		}
	case "person":
		for _, v := range d.People {
			if v.ID == id {
				return v, v.Revision, true
			}
		}
	case "dev_group":
		for _, v := range d.Groups {
			if v.ID == id {
				return v, v.Revision, true
			}
		}
	case "fallback":
		return map[string]any{"owner_id": d.FallbackID}, d.ConfigRevision, true
	}
	return nil, 0, false
}
func impactFromDirectory(d Directory, c Command) Impact {
	v := Impact{BusinessLines: []Role{}, Roles: []Role{}}
	switch c.Kind {
	case "department":
		for _, x := range d.Departments {
			if x.ID == c.ID {
				v.Open = x.Open
			}
		}
		for _, x := range d.BusinessLines {
			if x.DepartmentID == c.ID && x.Active {
				v.BusinessLines = append(v.BusinessLines, Role{"business_line", x.ID, x.Name, x.Open})
			}
		}
	case "business_line":
		for _, x := range d.BusinessLines {
			if x.ID == c.ID {
				v.Open = x.Open
			}
		}
	case "person":
		for _, x := range d.People {
			if x.ID == c.ID {
				v.Open = x.Open
				v.Roles = x.Roles
			}
		}
	case "fallback":
		if d.FallbackID != nil {
			for _, x := range d.People {
				if x.ID == *d.FallbackID {
					v.Open = x.Open
				}
			}
		}
	}
	return v
}
func (s *Store) Impact(ctx context.Context, c Command) (Impact, error) {
	d, err := s.Directory(ctx)
	if err != nil {
		return Impact{}, err
	}
	v := impactFromDirectory(d, c)
	if c.Kind == "department" || c.Kind == "business_line" {
		field := "department_id"
		if c.Kind == "business_line" {
			field = "business_line_id"
		}
		err = s.DB.QueryRowContext(ctx, "SELECT count(DISTINCT asset_id) FROM org.asset_ownership WHERE "+field+"=$1 AND valid_to IS NULL AND voided_at IS NULL", c.ID).Scan(&v.Assets)
	}
	return v, err
}

type Attribution struct {
	AssetID                 *int64 `json:"asset_id"`
	DepartmentID            *int64 `json:"department_id"`
	BusinessLineID          *int64 `json:"business_line_id"`
	OwnerID                 *int64 `json:"owner_id"`
	AssetOwnershipID        *int64 `json:"asset_ownership_id"`
	LineOwnerHistoryID      *int64 `json:"line_owner_history_id"`
	DepartmentHeadHistoryID *int64 `json:"department_head_history_id"`
	FallbackOwnerHistoryID  *int64 `json:"fallback_owner_history_id"`
	OwnerResolution         string `json:"owner_resolution"`
	AttributionQuality      string `json:"attribution_quality"`
}

func (s *Store) ResolveOwner(ctx context.Context, assetID int64, at, discovered time.Time) (Attribution, error) {
	var a Attribution
	err := s.DB.QueryRowContext(ctx, "SELECT * FROM org.resolve_owner($1,$2,$3)", assetID, at, discovered).Scan(&a.AssetID, &a.DepartmentID, &a.BusinessLineID, &a.OwnerID, &a.AssetOwnershipID, &a.LineOwnerHistoryID, &a.DepartmentHeadHistoryID, &a.FallbackOwnerHistoryID, &a.OwnerResolution, &a.AttributionQuality)
	return a, err
}
