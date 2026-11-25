package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/Autumn-27/artex/notify"
)

type FindingReportSchedule struct {
	DepartmentIDs []int64              `json:"department_ids"`
	ScopeMode     string               `json:"scope_mode"`
	Kind          string               `json:"kind"`
	Enabled       bool                 `json:"enabled"`
	Weekday       int                  `json:"weekday"`
	Monthday      int                  `json:"monthday"`
	Hour          int                  `json:"hour"`
	Minute        int                  `json:"minute"`
	ChannelID     *int64               `json:"channel_id"`
	Filter        FindingMetricsFilter `json:"filter"`
}

type SavedFindingReport struct {
	AttributionVersion int                   `json:"attribution_version"`
	AttributionBasis   string                `json:"attribution_basis"`
	DepartmentID       *int64                `json:"department_id"`
	BusinessLineID     *int64                `json:"business_line_id"`
	OwnerID            *int64                `json:"owner_id"`
	SourceSnapshotIDs  []int64               `json:"source_snapshot_ids"`
	DeliveryContext    json.RawMessage       `json:"delivery_context"`
	ID                 int64                 `json:"id"`
	Kind               string                `json:"kind"`
	From               string                `json:"from"`
	To                 string                `json:"to"`
	GeneratedAt        time.Time             `json:"generated_at"`
	Filter             FindingMetricsFilter  `json:"filter"`
	Current            *FindingMetricsReport `json:"current,omitempty"`
	Previous           *FindingMetricsReport `json:"previous,omitempty"`
	DeliveryState      string                `json:"delivery_state"`
	DeliveryError      string                `json:"delivery_error"`
	DeliveryID         *int64                `json:"delivery_id"`
}

// FindingReportPeriod returns the last complete calendar period and its predecessor.
func FindingReportPeriod(kind string, now time.Time) (from, to, previousFrom, previousTo time.Time) {
	local := now.In(FindingMetricsLocation)
	boundary := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, FindingMetricsLocation)
	if kind == "monthly" {
		boundary = time.Date(local.Year(), local.Month(), 1, 0, 0, 0, 0, FindingMetricsLocation)
		from = boundary.AddDate(0, -1, 0)
		previousFrom = from.AddDate(0, -1, 0)
	} else {
		boundary = boundary.AddDate(0, 0, -(int(boundary.Weekday())+6)%7)
		from = boundary.AddDate(0, 0, -7)
		previousFrom = from.AddDate(0, 0, -7)
	}
	return from, boundary.AddDate(0, 0, -1), previousFrom, from.AddDate(0, 0, -1)
}

func FindingReportScheduleDue(s FindingReportSchedule, now time.Time) bool {
	if !s.Enabled || (s.ScopeMode != "departments" && s.ChannelID == nil) || (s.ScopeMode == "departments" && len(s.DepartmentIDs) == 0) {
		return false
	}
	local := now.In(FindingMetricsLocation)
	due := time.Date(local.Year(), local.Month(), local.Day(), s.Hour, s.Minute, 0, 0, FindingMetricsLocation)
	if s.Kind == "monthly" {
		due = time.Date(local.Year(), local.Month(), s.Monthday, s.Hour, s.Minute, 0, 0, FindingMetricsLocation)
	} else {
		due = due.AddDate(0, 0, -(int(due.Weekday())+6)%7+s.Weekday-1)
	}
	return !local.Before(due)
}

func (d *DB) FindingReportSchedules(ctx context.Context) ([]FindingReportSchedule, error) {
	rows, err := d.QueryContext(ctx, `SELECT kind,enabled,weekday,monthday,hour,minute,channel_id,filter,to_jsonb(org_department_ids),org_scope_mode FROM finding_report_schedules ORDER BY kind DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []FindingReportSchedule{}
	for rows.Next() {
		var s FindingReportSchedule
		var raw, departments []byte
		if err := rows.Scan(&s.Kind, &s.Enabled, &s.Weekday, &s.Monthday, &s.Hour, &s.Minute, &s.ChannelID, &raw, &departments, &s.ScopeMode); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(raw, &s.Filter); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(departments, &s.DepartmentIDs); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (d *DB) SaveFindingReportSchedule(ctx context.Context, s FindingReportSchedule) error {
	raw, _ := json.Marshal(s.Filter)
	_, err := d.ExecContext(ctx, `UPDATE finding_report_schedules SET enabled=$2,weekday=$3,monthday=$4,hour=$5,minute=$6,channel_id=$7,filter=$8,org_department_ids=$9,org_scope_mode=$10,org_business_line_id=$11,org_owner_id=$12 WHERE kind=$1`,
		s.Kind, s.Enabled, s.Weekday, s.Monthday, s.Hour, s.Minute, s.ChannelID, raw, s.DepartmentIDs, s.ScopeMode, s.Filter.BusinessLineID, s.Filter.OwnerID)
	return err
}

func (d *DB) StoreFindingReport(ctx context.Context, kind string, now time.Time, filter FindingMetricsFilter) (int64, error) {
	from, to, prevFrom, prevTo := FindingReportPeriod(kind, now)
	current, err := d.FindingMetrics(ctx, from.Format("2006-01-02"), to.Format("2006-01-02"), filter)
	if err != nil {
		return 0, err
	}
	previous, err := d.FindingMetrics(ctx, prevFrom.Format("2006-01-02"), prevTo.Format("2006-01-02"), filter)
	if err != nil {
		return 0, err
	}
	rawFilter, _ := json.Marshal(filter)
	rawCurrent, _ := json.Marshal(current)
	rawPrevious, _ := json.Marshal(previous)
	var id int64
	err = d.QueryRowContext(ctx, `INSERT INTO finding_reports(kind,period_start,period_end,filter,current_payload,previous_payload)
        VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(kind,period_start,filter) DO UPDATE SET id=finding_reports.id RETURNING id`,
		kind, from.Format("2006-01-02"), to.Format("2006-01-02"), rawFilter, rawCurrent, rawPrevious).Scan(&id)
	return id, err
}

const findingReportSelect = `SELECT r.id,r.kind,r.period_start::text,r.period_end::text,r.generated_at,r.filter,
    COALESCE(n.state,CASE WHEN r.delivery_context->>'state'='unavailable' THEN 'unavailable' ELSE 'generated' END),COALESCE(n.last_error,r.delivery_context->>'message',''),n.id,
    r.attribution_version,r.attribution_basis,r.department_id,r.business_line_id,r.owner_id,to_jsonb(r.source_snapshot_ids),r.delivery_context`
const findingReportJoin = ` FROM finding_reports r LEFT JOIN LATERAL(SELECT d.id,d.state,d.last_error FROM notification_deliveries d WHERE d.event_id=r.event_id ORDER BY CASE WHEN d.state='failed' THEN 0 WHEN d.state='sent' THEN 2 ELSE 1 END,d.id LIMIT 1)n ON true`

func (d *DB) ListFindingReports(ctx context.Context) ([]SavedFindingReport, error) {
	rows, err := d.QueryContext(ctx, findingReportSelect+findingReportJoin+` ORDER BY r.id DESC LIMIT 50`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SavedFindingReport{}
	for rows.Next() {
		var r SavedFindingReport
		var raw, snapshots []byte
		if err := rows.Scan(&r.ID, &r.Kind, &r.From, &r.To, &r.GeneratedAt, &raw, &r.DeliveryState, &r.DeliveryError, &r.DeliveryID, &r.AttributionVersion, &r.AttributionBasis, &r.DepartmentID, &r.BusinessLineID, &r.OwnerID, &snapshots, &r.DeliveryContext); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(raw, &r.Filter); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(snapshots, &r.SourceSnapshotIDs); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (d *DB) GetFindingReport(ctx context.Context, id int64) (*SavedFindingReport, error) {
	var r SavedFindingReport
	var rawFilter, rawCurrent, rawPrevious, snapshots []byte
	err := d.QueryRowContext(ctx, findingReportSelect+`,r.current_payload,r.previous_payload`+findingReportJoin+` WHERE r.id=$1`, id).
		Scan(&r.ID, &r.Kind, &r.From, &r.To, &r.GeneratedAt, &rawFilter, &r.DeliveryState, &r.DeliveryError, &r.DeliveryID, &r.AttributionVersion, &r.AttributionBasis, &r.DepartmentID, &r.BusinessLineID, &r.OwnerID, &snapshots, &r.DeliveryContext, &rawCurrent, &rawPrevious)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(rawFilter, &r.Filter); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(snapshots, &r.SourceSnapshotIDs); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(rawCurrent, &r.Current); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(rawPrevious, &r.Previous); err != nil {
		return nil, err
	}
	return &r, nil
}

// QueueFindingReport freezes its message and targets exactly the chosen channel.
// The existing delivery engine handles timeouts, retries and delivery history.
func (d *DB) QueueFindingReport(ctx context.Context, id, channelID int64, content notify.ReportContent) error {
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var eventID *int64
	if err := tx.QueryRowContext(ctx, `SELECT event_id FROM finding_reports WHERE id=$1 FOR UPDATE`, id).Scan(&eventID); err != nil {
		return err
	}
	if eventID != nil {
		return nil
	}
	snapshot, _ := json.Marshal(notify.Snapshot{Kind: notify.EventGovernanceReport, Name: content.Title, Report: &content})
	var eid int64
	if err := tx.QueryRowContext(ctx, `INSERT INTO notification_events(kind,finding_id,snapshot,fanned_out) VALUES($1,0,$2,true) RETURNING id`, notify.EventGovernanceReport, snapshot).Scan(&eid); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO notification_deliveries(event_id,channel_id) VALUES($1,$2)`, eid, channelID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE finding_reports SET event_id=$2 WHERE id=$1`, id, eid); err != nil {
		return err
	}
	return tx.Commit()
}
