package report

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/artex/notify"
)

type DeliveryContext struct {
	State        string   `json:"state"`
	DepartmentID int64    `json:"department_id"`
	HeadID       *int64   `json:"head_id"`
	HeadName     string   `json:"head_name"`
	HeadEmail    string   `json:"head_email"`
	ChannelIDs   []int64  `json:"channel_ids"`
	CCEmails     []string `json:"cc_emails"`
	Message      string   `json:"message"`
}

// DeliveryRoute resolves the department's own active head and channels. It never
// falls back to another department, the asset fallback owner, or a legacy ID.
func (s *Store) DeliveryRoute(ctx context.Context, department int64, businessLineID *int64) (DeliveryContext, error) {
	r := DeliveryContext{State: "unavailable", DepartmentID: department, ChannelIDs: []int64{}, CCEmails: []string{}}
	var active bool
	err := s.DB.QueryRowContext(ctx, `SELECT d.active,u.id,COALESCE(u.name,''),COALESCE(u.email,'') FROM org.department d
 LEFT JOIN org.department_head_history h ON h.department_id=d.id AND h.voided_at IS NULL AND tstzrange(h.valid_from,h.valid_to,'[)') @> statement_timestamp() AND tstzrange(h.finding_from,h.finding_to,'[)') @> statement_timestamp()
 LEFT JOIN org.app_user u ON u.id=h.head_id AND u.active WHERE d.id=$1`, department).Scan(&active, &r.HeadID, &r.HeadName, &r.HeadEmail)
	if err == sql.ErrNoRows {
		r.Message = "部门不存在"
		return r, nil
	}
	if err != nil {
		return r, err
	}
	if !active {
		r.Message = "部门已停用"
		return r, nil
	}
	if r.HeadID == nil {
		r.Message = "部门尚未设置启用的负责人"
		return r, nil
	}
	r.CCEmails, err = s.DevelopmentGroupCC(ctx, department, businessLineID, r.HeadEmail)
	if err != nil {
		return r, err
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT c.id,c.kind,c.config FROM notification_channels c JOIN org.department d ON c.id=ANY(d.channel_ids) WHERE d.id=$1 AND c.enabled ORDER BY c.id`, department)
	if err != nil {
		return r, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var kind string
		var raw []byte
		if err = rows.Scan(&id, &kind, &raw); err != nil {
			return r, err
		}
		adapter, ok := notify.Get(kind)
		if !ok {
			continue
		}
		var cfg map[string]any
		if err = json.Unmarshal(raw, &cfg); err != nil {
			return r, err
		}
		if kind == notify.KindEmail {
			if r.HeadEmail == "" {
				continue
			}
			cfg["to"] = []any{r.HeadEmail}
			cfg["cc"] = r.CCEmails
		}
		if adapter.Validate(cfg) != nil {
			continue
		}
		r.ChannelIDs = append(r.ChannelIDs, id)
	}
	if err = rows.Err(); err != nil {
		return r, err
	}
	if len(r.ChannelIDs) == 0 {
		r.Message = "部门没有配置完整且启用的通知渠道"
		return r, nil
	}
	r.State = "ready"
	r.Message = "将发送至本部门当前负责人及已配置渠道"
	return r, nil
}

// DevelopmentGroupCC resolves current members of groups actually linked to the
// report's business-line scope. A nil line means all lines in this department;
// zero means findings without a business line and therefore no group recipients.
func (s *Store) DevelopmentGroupCC(ctx context.Context, department int64, businessLineID *int64, primaryEmail string) ([]string, error) {
	cc := []string{}
	rows, err := s.DB.QueryContext(ctx, `SELECT DISTINCT lower(btrim(u.email))
 FROM org.business_line l
 JOIN org.dev_group_business_line link ON link.business_line_id=l.id AND link.department_id=l.department_id
 AND link.voided_at IS NULL AND tstzrange(link.valid_from,link.valid_to,'[)') @> statement_timestamp()
 JOIN org.dev_group g ON g.id=link.dev_group_id AND g.department_id=l.department_id AND g.active
 JOIN org.app_user u ON u.dev_group_id=g.id AND u.department_id=g.department_id AND u.active
 WHERE l.department_id=$1 AND l.active AND ($2::bigint IS NULL OR l.id=$2)
 AND btrim(u.email)<>'' AND lower(btrim(u.email))<>lower(btrim($3)) ORDER BY 1`, department, businessLineID, primaryEmail)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var email string
		if err = rows.Scan(&email); err != nil {
			return nil, err
		}
		cc = append(cc, email)
	}
	return cc, rows.Err()
}

func (s *Store) StoreReport(ctx context.Context, kind string, now time.Time, f db.FindingMetricsFilter) (int64, error) {
	if err := ValidateFilter(f); err != nil {
		return 0, err
	}
	if f.DepartmentID == nil || *f.DepartmentID <= 0 {
		return 0, fmt.Errorf("请为报告选择一个部门")
	}
	f.AttributionVersion = 1
	from, to, pFrom, pTo := db.FindingReportPeriod(kind, now)
	current, err := s.Metrics(ctx, from.Format("2006-01-02"), to.Format("2006-01-02"), f)
	if err != nil {
		return 0, err
	}
	previous, err := s.Metrics(ctx, pFrom.Format("2006-01-02"), pTo.Format("2006-01-02"), f)
	if err != nil {
		return 0, err
	}
	route, err := s.DeliveryRoute(ctx, *f.DepartmentID, f.BusinessLineID)
	if err != nil {
		return 0, err
	}
	set := map[int64]bool{}
	for _, id := range append(current.SnapshotIDs, previous.SnapshotIDs...) {
		set[id] = true
	}
	ids := []int64{}
	for id := range set {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	var id int64
	err = s.DB.QueryRowContext(ctx, `INSERT INTO finding_reports(kind,period_start,period_end,filter,current_payload,previous_payload,attribution_version,department_id,business_line_id,owner_id,attribution_basis,source_snapshot_ids,delivery_context)
 VALUES($1,$2::date,$3::date,$4,$5,$6,1,$7,$8,$9,'event_snapshot_v1',$10,$11)
 ON CONFLICT(kind,period_start,filter) DO UPDATE SET id=finding_reports.id RETURNING id`, kind, from.Format("2006-01-02"), to.Format("2006-01-02"), jsonValue(f), jsonValue(current), jsonValue(previous), f.DepartmentID, f.BusinessLineID, f.OwnerID, ids, jsonValue(route)).Scan(&id)
	return id, err
}

func (s *Store) QueueReport(ctx context.Context, id int64, content notify.ReportContent) error {
	var department *int64
	var filter db.FindingMetricsFilter
	var rawFilter []byte
	if err := s.DB.QueryRowContext(ctx, `SELECT department_id,filter FROM finding_reports WHERE id=$1 AND attribution_version=1`, id).Scan(&department, &rawFilter); err != nil {
		return err
	}
	if department == nil {
		return fmt.Errorf("报告缺少部门范围")
	}
	if err := json.Unmarshal(rawFilter, &filter); err != nil {
		return err
	}
	route, err := s.DeliveryRoute(ctx, *department, filter.BusinessLineID)
	if err != nil {
		return err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var existing *int64
	if err = tx.QueryRowContext(ctx, `SELECT event_id FROM finding_reports WHERE id=$1 FOR UPDATE`, id).Scan(&existing); err != nil {
		return err
	}
	if existing != nil {
		return nil
	}
	if route.State != "ready" {
		_, err = tx.ExecContext(ctx, `UPDATE finding_reports SET delivery_context=$2 WHERE id=$1`, id, jsonValue(route))
		if err != nil {
			return err
		}
		return tx.Commit()
	}
	content.ID = id
	content.DepartmentID = *department
	content.BusinessLineID = filter.BusinessLineID
	var eventID int64
	if err = tx.QueryRowContext(ctx, `INSERT INTO notification_events(kind,finding_id,snapshot,fanned_out) VALUES($1,0,$2,true) RETURNING id`, notify.EventGovernanceReport, jsonValue(notify.Snapshot{Kind: notify.EventGovernanceReport, Name: content.Title, Report: &content})).Scan(&eventID); err != nil {
		return err
	}
	for _, channel := range route.ChannelIDs {
		if _, err = tx.ExecContext(ctx, `INSERT INTO notification_deliveries(event_id,channel_id) VALUES($1,$2)`, eventID, channel); err != nil {
			return err
		}
	}
	route.State = "queued"
	route.Message = "已进入本部门通知队列，发送前将再次核对负责人和渠道"
	if _, err = tx.ExecContext(ctx, `UPDATE finding_reports SET event_id=$2,delivery_context=$3 WHERE id=$1`, id, eventID, jsonValue(route)); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) ValidateSchedule(ctx context.Context, schedule db.FindingReportSchedule) error {
	if err := ValidateFilter(schedule.Filter); err != nil {
		return err
	}
	if schedule.ScopeMode != "departments" || len(schedule.DepartmentIDs) == 0 {
		return fmt.Errorf("请选择至少一个部门")
	}
	seen := map[int64]bool{}
	for _, id := range schedule.DepartmentIDs {
		if id <= 0 || seen[id] {
			return fmt.Errorf("部门编号不能重复或为空")
		}
		seen[id] = true
		var active bool
		if err := s.DB.QueryRowContext(ctx, `SELECT active FROM org.department WHERE id=$1`, id).Scan(&active); err == sql.ErrNoRows || err == nil && !active {
			return fmt.Errorf("请选择已启用的部门")
		} else if err != nil {
			return err
		}
	}
	if schedule.Filter.BusinessLineID != nil && *schedule.Filter.BusinessLineID > 0 {
		var dep int64
		if err := s.DB.QueryRowContext(ctx, `SELECT department_id FROM org.business_line WHERE id=$1`, *schedule.Filter.BusinessLineID).Scan(&dep); err != nil {
			return fmt.Errorf("业务线不存在")
		}
		if len(seen) != 1 || !seen[dep] {
			return fmt.Errorf("业务线范围需要只选择其所属部门")
		}
	}
	return nil
}
