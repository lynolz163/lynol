package report

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/Autumn-27/artex/db"
)

type Store struct{ DB *sql.DB }

func New(database *sql.DB) *Store { return &Store{DB: database} }
func jsonValue(v any) string      { b, _ := json.Marshal(v); return string(b) }
func date(t time.Time) time.Time {
	x := t.In(db.FindingMetricsLocation)
	return time.Date(x.Year(), x.Month(), x.Day(), 0, 0, 0, 0, db.FindingMetricsLocation)
}

// Refresh never replaces a sealed head during an ordinary refresh. Missing old
// days get event totals with explicitly unknown stock, not fabricated snapshots.
func (s *Store) Refresh(ctx context.Context, now time.Time) error {
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(7337741005)`); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO settings(key,value) VALUES('org_report_started_at',$1) ON CONFLICT DO NOTHING`, now.Format(time.RFC3339Nano)); err != nil {
		return err
	}
	var sinceText string
	if err = tx.QueryRowContext(ctx, `SELECT value FROM settings WHERE key='org_report_started_at'`).Scan(&sinceText); err != nil {
		return err
	}
	since, err := time.Parse(time.RFC3339Nano, sinceText)
	if err != nil {
		return err
	}
	var revision, watermark int64
	if err = tx.QueryRowContext(ctx, `SELECT config_revision,(SELECT COALESCE(max(id),0) FROM finding_history) FROM org.sync_state WHERE singleton`).Scan(&revision, &watermark); err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, `SELECT h.id,h.finding_id,h.kind,h.old_status,h.new_status,h.occurred_at,h.recorded_at,h.metadata,
 CASE WHEN c.id IS NOT NULL THEN c.asset_id ELSE h.asset_id END,
 CASE WHEN c.id IS NOT NULL THEN c.department_id ELSE h.department_id END,
 CASE WHEN c.id IS NOT NULL THEN c.business_line_id ELSE h.business_line_id END,
 CASE WHEN c.id IS NOT NULL THEN c.owner_id ELSE h.owner_id END,
 CASE WHEN c.id IS NOT NULL THEN 1 ELSE h.attribution_version END,
 CASE WHEN c.id IS NOT NULL THEN CASE WHEN c.asset_id IS NULL THEN 'unknown' WHEN c.department_id IS NULL THEN 'unassigned' ELSE 'recorded' END ELSE h.attribution_quality END,h.sla_days
 FROM finding_history h LEFT JOIN LATERAL(SELECT * FROM org.finding_event_attribution_correction c WHERE c.history_id=h.id ORDER BY id DESC LIMIT 1)c ON true
 WHERE h.occurred_at<=$1 ORDER BY h.occurred_at,h.id`, now)
	if err != nil {
		return err
	}
	events := []event{}
	for rows.Next() {
		var e event
		if err = rows.Scan(&e.ID, &e.FindingID, &e.Kind, &e.OldStatus, &e.NewStatus, &e.At, &e.Recorded, &e.Metadata, &e.AssetID, &e.DepartmentID, &e.LineID, &e.OwnerID, &e.Version, &e.Quality, &e.SLADays); err != nil {
			rows.Close()
			return err
		}
		events = append(events, e)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	type head struct {
		ID     int64
		Sealed bool
		Known  bool
	}
	heads := map[string]head{}
	rows, err = tx.QueryContext(ctx, `SELECT h.day::text,h.snapshot_id,h.sealed_at IS NOT NULL,s.stock_known FROM report.daily_snapshot_head h JOIN report.daily_snapshot s ON s.id=h.snapshot_id`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var day string
		var h head
		if err = rows.Scan(&day, &h.ID, &h.Sealed, &h.Known); err != nil {
			rows.Close()
			return err
		}
		heads[day] = h
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	// Approved first-assignment corrections are the only history replacement path.
	corrections := map[string]string{}
	var correctionCursor, correctionMax int64
	if err = tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT value::bigint FROM settings WHERE key='org_report_correction_watermark'),0),(SELECT COALESCE(max(id),0) FROM org.finding_event_attribution_correction)`).Scan(&correctionCursor, &correctionMax); err != nil {
		return err
	}
	rows, err = tx.QueryContext(ctx, `SELECT c.operation_id::text,min((h.occurred_at AT TIME ZONE 'Asia/Shanghai')::date)::text
 FROM org.finding_event_attribution_correction c JOIN finding_history h ON h.id=c.history_id
 WHERE c.id>$1 GROUP BY c.operation_id ORDER BY min(c.id)`, correctionCursor)
	if err != nil {
		return err
	}
	today := date(now)
	start := today.AddDate(0, 0, -365)
	for rows.Next() {
		var op, from string
		if err = rows.Scan(&op, &from); err != nil {
			rows.Close()
			return err
		}
		day, e := time.ParseInLocation("2006-01-02", from, db.FindingMetricsLocation)
		if e != nil {
			rows.Close()
			return e
		}
		if day.Before(start) {
			day = start
		}
		for ; !day.After(today); day = day.AddDate(0, 0, 1) {
			corrections[day.Format("2006-01-02")] = op
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	states := map[int64]*state{}
	cursor := 0
	scratch := &daily{Buckets: map[string]*bucket{}, QualityKnown: true}
	for cursor < len(events) && events[cursor].At.Before(start) {
		if err = replay(states, events[cursor], scratch); err != nil {
			return err
		}
		cursor++
	}
	for day := start; !day.After(today); day = day.AddDate(0, 0, 1) {
		key := day.Format("2006-01-02")
		end := day.AddDate(0, 0, 1)
		cutoff := end.Add(-time.Microsecond)
		if day.Equal(today) {
			cutoff = now
		}
		d := daily{Day: key, At: now, HistorySince: since, Buckets: map[string]*bucket{}, QualityKnown: true, StockKnown: day.Equal(today) || !day.Before(date(since).AddDate(0, 0, 1))}
		for cursor < len(events) && !events[cursor].At.After(cutoff) {
			if err = replay(states, events[cursor], &d); err != nil {
				return err
			}
			cursor++
		}
		h, exists := heads[key]
		if exists {
			d.StockKnown = h.Known
		}
		correction := corrections[key]
		if exists && h.Sealed && correction == "" {
			continue
		}
		// A day missed while the server was stopped has no trusted stock snapshot.
		if !exists && !day.Equal(today) && !day.Equal(today.AddDate(0, 0, -1)) {
			d.StockKnown = false
		}
		var slaRaw []byte
		if err = tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT days FROM org.sla_policy_history WHERE business_line_id IS NULL AND voided_at IS NULL AND tstzrange(valid_from,valid_to,'[)') @> $1::timestamptz),'{}'::jsonb)`, cutoff).Scan(&slaRaw); err != nil {
			return err
		}
		if err = json.Unmarshal(slaRaw, &d.SLA); err != nil {
			return err
		}
		if d.StockKnown {
			if err = fillStock(ctx, tx, states, &d, cutoff); err != nil {
				return err
			}
		}
		var generation int
		if err = tx.QueryRowContext(ctx, `SELECT COALESCE(max(generation),0)+1 FROM report.daily_snapshot WHERE day=$1::date`, key).Scan(&generation); err != nil {
			return err
		}
		var correctionID any
		if correction != "" {
			correctionID = correction
		}
		if err = tx.QueryRowContext(ctx, `INSERT INTO report.daily_snapshot(day,generation,generated_at,source_history_watermark,source_config_revision,stock_known,quality_known,correction_operation_id,sla_days,history_since)
 VALUES($1::date,$2,$3,$4,$5,$6,$7,$8::uuid,$9,$10) RETURNING id`, key, generation, now, watermark, revision, d.StockKnown, d.QualityKnown, correctionID, jsonValue(d.SLA), since).Scan(&d.ID); err != nil {
			return err
		}
		keys := make([]string, 0, len(d.Buckets))
		for k := range d.Buckets {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			b := d.Buckets[k]
			var open, overdue, a0, a1, a2 any
			if d.StockKnown {
				open = b.Open
				overdue = b.Overdue
				a0 = b.Ages[0]
				a1 = b.Ages[1]
				a2 = b.Ages[2]
			}
			_, err = tx.ExecContext(ctx, `INSERT INTO report.daily_vuln_stat(snapshot_id,department_id,business_line_id,owner_id,severity,vulnclass,new_count,closed_count,reopened_count,open_count,overdue_count,sla_closed_count,sla_met_count,retest_passed_count,retest_failed_count,retest_other_count,first_fix_passed_count,first_fix_total_count,verified_fix_count,rework_round_count,age_0_7_count,age_7_30_count,age_30_plus_count,duration_samples,overdue_items,unknown_event_count,unknown_open_count,missing_sla_closed_count,missing_sla_open_count,missing_discovery_count)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,$27,$28,$29,$30)`, d.ID, b.dimension.DepartmentID, b.LineID, b.dimension.OwnerID, b.dimension.Severity, b.Class, b.New, b.Closed, b.Reopened, open, overdue, b.SLAClosed, b.SLAMet, b.RetestPassed, b.RetestFailed, b.RetestOther, b.FirstRetestPassed, b.FirstRetestTotal, b.VerifiedFixes, b.ReworkRounds, a0, a1, a2, jsonValue(b.Samples), jsonValue(b.OverdueItems), b.UnknownEvents, b.UnknownOpen, b.MissingSLAClosed, b.MissingSLAOpen, b.MissingDiscovery)
			if err != nil {
				return err
			}
		}
		var sealed any
		if !day.Equal(today) {
			sealed = now
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO report.daily_snapshot_head(day,snapshot_id,sealed_at) VALUES($1::date,$2,$3) ON CONFLICT(day) DO UPDATE SET snapshot_id=EXCLUDED.snapshot_id,sealed_at=EXCLUDED.sealed_at WHERE report.daily_snapshot_head.sealed_at IS NULL OR $4::uuid IS NOT NULL`, key, d.ID, sealed, correctionID); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO settings(key,value) VALUES('org_report_correction_watermark',$1) ON CONFLICT(key) DO UPDATE SET value=EXCLUDED.value`, fmt.Sprint(correctionMax)); err != nil {
		return err
	}
	return tx.Commit()
}

func fillStock(ctx context.Context, tx *sql.Tx, states map[int64]*state, d *daily, at time.Time) error {
	ids := make([]int64, 0, len(states))
	for id, st := range states {
		if active(st.Status) {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		st := states[id]
		var asset, dep, line, owner *int64
		var quality, identifier, name string
		var available bool
		var sla *int
		err := tx.QueryRowContext(ctx, `SELECT r.asset_id,r.department_id,r.business_line_id,r.owner_id,r.attribution_quality,COALESCE(g.identifier,''),COALESCE(NULLIF(f.name,''),NULLIF(f.vulnclass,''),'漏洞 #'||($1::bigint)::text),f.id IS NOT NULL,
 (SELECT (h.days->>$3)::int FROM org.sla_policy_history h WHERE h.voided_at IS NULL AND h.days IS NOT NULL AND (h.business_line_id=r.business_line_id OR h.business_line_id IS NULL) AND tstzrange(h.valid_from,h.valid_to,'[)') @> $2::timestamptz ORDER BY h.business_line_id NULLS LAST LIMIT 1)
 FROM org.resolve_finding_owner($1,$2) r LEFT JOIN org.asset_registry g ON g.asset_id=r.asset_id LEFT JOIN public.findings f ON f.id=$1`, id, at, st.Severity).Scan(&asset, &dep, &line, &owner, &quality, &identifier, &name, &available, &sla)
		if err != nil {
			return err
		}
		b := d.get(dimension{dep, line, owner, st.Severity, st.Class})
		b.Open++
		if quality == "unknown" {
			b.UnknownOpen++
		}
		if st.Discovered.IsZero() {
			b.MissingDiscovery++
			d.QualityKnown = false
			continue
		}
		age := at.Sub(st.Discovered).Hours() / 24
		if age < 7 {
			b.Ages[0]++
		} else if age < 30 {
			b.Ages[1]++
		} else {
			b.Ages[2]++
		}
		if sla == nil || *sla <= 0 {
			b.MissingSLAOpen++
			continue
		}
		deadline := st.Discovered.Add(time.Duration(*sla) * 24 * time.Hour)
		if at.After(deadline) {
			b.Overdue++
			b.OverdueItems = append(b.OverdueItems, db.FindingOverdue{DepartmentID: dep, BusinessLineID: line, OwnerID: owner, AssetID: asset, Asset: identifier, ID: id, Name: name, Severity: st.Severity, AgeDays: age, OverdueDays: at.Sub(deadline).Hours() / 24, Available: available, DiscoveredAt: &st.Discovered, DeadlineAt: &deadline, AsOf: &at})
		}
	}
	return nil
}

func filterID(want, actual *int64) bool {
	return want == nil || *want == 0 && actual == nil || actual != nil && *want == *actual
}
func filterBucket(f db.FindingMetricsFilter, b *bucket) bool {
	return (f.Severity == "" || f.Severity == b.dimension.Severity) && filterID(f.DepartmentID, b.dimension.DepartmentID) && filterID(f.BusinessLineID, b.LineID) && filterID(f.OwnerID, b.dimension.OwnerID)
}
func ValidateFilter(f db.FindingMetricsFilter) error {
	if f.Severity != "" && !db.ValidSeverity(f.Severity) {
		return fmt.Errorf("无效的严重等级")
	}
	if f.BusinessLine != "" || f.Assignee != "" {
		return fmt.Errorf("请使用部门、业务线和负责人的稳定编号筛选")
	}
	for _, id := range []*int64{f.DepartmentID, f.BusinessLineID, f.OwnerID} {
		if id != nil && *id < 0 {
			return fmt.Errorf("筛选编号不能为负数")
		}
	}
	return nil
}
