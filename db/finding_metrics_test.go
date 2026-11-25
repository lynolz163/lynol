package db

import (
	"encoding/json"
	"math"
	"sort"
	"testing"
	"time"
)

func TestFindingMetricsRepairQualityAcrossPeriods(t *testing.T) {
	start := time.Date(2026, 11, 1, 0, 0, 0, 0, FindingMetricsLocation)
	var events []FindingHistoryEvent
	add := func(id int64, kind string, hours int, status string, metadata map[string]any) {
		raw, _ := json.Marshal(metadata)
		events = append(events, FindingHistoryEvent{ID: int64(len(events) + 1), FindingID: id, Kind: kind,
			OccurredAt: start.Add(time.Duration(hours) * time.Hour), NewStatus: status, Metadata: raw})
	}
	verdict := func(id int64, hours int, result string) {
		add(id, "retest_completed", hours, "", map[string]any{"status": "completed", "verdict": result})
	}
	for id := int64(1); id <= 4; id++ {
		add(id, "discovered", 0, "pending", map[string]any{"severity": "high"})
	}
	verdict(1, 20, "reproduced") // Failure before the selected period must still count as rework.
	verdict(1, 21, "inconclusive")
	verdict(1, 26, "reproduced")
	verdict(1, 50, "fixed")
	add(1, "status_changed", 72, "confirmed", nil)
	verdict(1, 73, "reproduced")
	verdict(1, 80, "fixed")
	verdict(2, 8, "inconclusive") // No conclusion does not consume the first attempt.
	verdict(2, 30, "fixed")
	verdict(2, 31, "fixed")                    // An already closed finding does not create another completed repair cycle.
	add(3, "status_changed", 15, "fixed", nil) // Manual closure alone cannot prove repair quality.
	add(3, "status_changed", 40, "confirmed", nil)
	verdict(3, 41, "reproduced")
	verdict(3, 60, "fixed")
	verdict(4, 4, "reproduced")
	add(4, "baseline", 10, "fixed", map[string]any{"severity": "high"}) // Legacy manual closure has no original timestamp.
	add(4, "status_changed", 40, "confirmed", nil)
	verdict(4, 80, "fixed") // Reopening the baseline starts a fresh cycle without carrying the old failure.
	sort.SliceStable(events, func(i, j int) bool { return events[i].OccurredAt.Before(events[j].OccurredAt) })
	snapshots, err := BuildFindingDailySnapshots(events, nil, start.Add(95*time.Hour), start, FindingSLA{"high": 7}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var selected []FindingDailySnapshot
	for _, snapshot := range snapshots {
		if snapshot.Day >= "2026-11-02" {
			selected = append(selected, snapshot)
		}
	}
	report := SummarizeFindingMetrics(selected, FindingMetricsFilter{})
	if !report.QualityKnown || report.Summary.FirstRetestTotal != 2 || report.Summary.FirstRetestPassed != 1 ||
		report.Summary.VerifiedFixes != 5 || report.Summary.ReworkRounds != 4 {
		t.Fatalf("quality metrics lost cross-period history or counted repeats: %+v", report.Summary)
	}
	rework := report.Durations[5]
	if rework.Count != 3 || rework.Average == nil || math.Abs(*rework.Average-56.0/3) > 0.0001 {
		t.Fatalf("wrong rework durations: %+v", rework)
	}
	selected[0].QualityKnown = false
	if SummarizeFindingMetrics(selected, FindingMetricsFilter{}).QualityKnown {
		t.Fatal("old snapshots without quality statistics must not be shown as zero")
	}
}

func TestFindingMetricsUnassignedSLAAndAllOverdue(t *testing.T) {
	start := time.Date(2026, 11, 1, 0, 0, 0, 0, FindingMetricsLocation)
	var events []FindingHistoryEvent
	for id := int64(1); id <= 32; id++ {
		severity := "critical"
		if id == 32 {
			severity = "low"
		}
		raw, _ := json.Marshal(map[string]string{"severity": severity})
		events = append(events, FindingHistoryEvent{ID: id, FindingID: id, Kind: "discovered", NewStatus: "pending", OccurredAt: start, Metadata: raw})
	}
	events = append(events, FindingHistoryEvent{ID: 33, FindingID: 31, Kind: "status_changed", NewStatus: "fixed", OccurredAt: start.Add(24 * time.Hour), Metadata: []byte("{}")})
	snapshots, err := BuildFindingDailySnapshots(events, nil, start.Add(4*24*time.Hour), start, FindingSLA{"critical": 3, "low": 90}, nil)
	if err != nil {
		t.Fatal(err)
	}
	report := SummarizeFindingMetrics(snapshots, FindingMetricsFilter{BusinessLine: "__none__", Assignee: "__none__"})
	if report.Summary.Open != 31 || report.Summary.Overdue != 30 || report.Summary.SLAMet != 1 || report.Summary.SLAClosed != 1 || len(report.Overdue) != 30 {
		t.Fatalf("unassigned findings skipped SLA or overdue list was truncated: %+v, rows %d", report.Summary, len(report.Overdue))
	}
	for _, group := range report.Groups {
		if group.Overdue != 30 || group.SLAClosed+group.Overdue != 31 {
			t.Fatalf("wrong full SLA denominator: %+v", group)
		}
	}
}

func TestFindingMetricsReopenAndUnknownHistory(t *testing.T) {
	start := time.Date(2026, 11, 1, 0, 0, 0, 0, FindingMetricsLocation)
	var events []FindingHistoryEvent
	add := func(id int64, kind string, hours int, status string, metadata map[string]any) {
		raw, _ := json.Marshal(metadata)
		if metadata == nil {
			raw = []byte("{}")
		}
		events = append(events, FindingHistoryEvent{ID: int64(len(events) + 1), FindingID: id, Kind: kind,
			OccurredAt: start.Add(time.Duration(hours) * time.Hour), NewStatus: status, Metadata: raw})
	}
	add(1, "discovered", 0, "pending", map[string]any{"severity": "high", "vulnclass": "XSS"})
	add(2, "discovered", 0, "pending", map[string]any{"severity": "critical", "vulnclass": "SQLi"})
	add(3, "discovered", 0, "pending", map[string]any{"severity": "high", "vulnclass": "XSS"})
	add(1, "dispatched", 1, "", nil)
	add(1, "developer_fixed", 25, "", nil)
	add(1, "retest_requested", 26, "", nil)
	add(1, "retest_completed", 27, "", map[string]any{
		"status": "completed", "verdict": "fixed", "requested_at": start.Add(26 * time.Hour),
	})
	add(1, "status_changed", 27, "fixed", nil)
	add(3, "baseline", 28, "fixed", map[string]any{"severity": "high", "vulnclass": "XSS"})
	add(1, "retest_completed", 49, "", map[string]any{"status": "completed", "verdict": "reproduced"})
	add(1, "status_changed", 49, "confirmed", nil)
	add(1, "dispatched", 50, "", nil)
	add(1, "developer_fixed", 51, "", nil)
	add(1, "retest_requested", 52, "", nil)
	add(1, "retest_completed", 53, "", map[string]any{"status": "completed", "verdict": "inconclusive"})
	add(1, "retest_completed", 55, "", map[string]any{"status": "stopped", "verdict": "fixed"})
	snapshots, err := BuildFindingDailySnapshots(events, map[int64]string{1: "one", 2: "two"},
		start.Add(8*24*time.Hour), start.Add(28*time.Hour), FindingSLA{"high": 7, "critical": 3}, nil)
	if err != nil {
		t.Fatal(err)
	}
	report := SummarizeFindingMetrics(snapshots, FindingMetricsFilter{})
	if report.Summary.New != 3 || report.Summary.Closed != 1 || report.Summary.Reopened != 1 ||
		report.Summary.Open != 2 || report.Summary.Overdue != 2 {
		t.Fatalf("unexpected totals: %+v", report.Summary)
	}
	if report.Summary.RetestFailed != 1 || report.Summary.RetestPassed != 1 || report.Summary.RetestOther != 2 {
		t.Fatalf("retest denominator included unfinished/inconclusive results: %+v", report.Summary)
	}
	if report.Durations[0].Count != 2 || *report.Durations[0].P90 != 1 ||
		report.Durations[4].Count != 1 || *report.Durations[4].Average != 27 {
		t.Fatalf("unexpected phase samples: %+v", report.Durations)
	}
	for _, point := range report.Trend {
		if point.Day == "2026-11-01" && point.Open != nil {
			t.Fatal("stock before collection must remain unknown")
		}
	}
	high := SummarizeFindingMetrics(snapshots, FindingMetricsFilter{Severity: "high"})
	if high.Summary.New != 2 || high.Summary.Open != 1 || len(high.Overdue) != 1 {
		t.Fatalf("severity filtering mismatch: %+v", high.Summary)
	}
}

func TestFindingMetricsP90UsesCombinedSamples(t *testing.T) {
	snapshots := []FindingDailySnapshot{
		{Buckets: []*FindingMetricBucket{{Samples: map[string][]float64{"closure": {1, 2, 3, 4, 5}}}}},
		{Buckets: []*FindingMetricBucket{{Samples: map[string][]float64{"closure": {6, 7, 8, 9, 100}}}}},
	}
	report := SummarizeFindingMetrics(snapshots, FindingMetricsFilter{})
	duration := report.Durations[4]
	if duration.Count != 10 || *duration.Average != 14.5 || *duration.P90 != 9 {
		t.Fatalf("expected combined average 14.5 and nearest-rank P90 9, got %+v", duration)
	}
	if report.Durations[0].Average != nil || report.Durations[0].P90 != nil {
		t.Fatal("missing phase samples must not become zero durations")
	}
}

func TestFindingHistoryMilestonesAndMetrics(t *testing.T) {
	d, findingID := retestDB(t)
	ctx := t.Context()
	first, err := d.RecordFindingMilestone(ctx, findingID, "dispatched", nil)
	if err != nil {
		t.Fatal(err)
	}
	repeated, err := d.RecordFindingMilestone(ctx, findingID, "dispatched", nil)
	if err != nil || !first.Equal(repeated) {
		t.Fatalf("duplicate milestone changed time: %v", err)
	}
	if _, err := d.RecordFindingMilestoneBatch(ctx, []int64{findingID}, "developer_fixed", nil); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := d.SetFindingStatusWithNotify(ctx, findingID, FindingFixed); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := d.SetFindingStatusWithNotify(ctx, findingID, FindingConfirmed); err != nil {
		t.Fatal(err)
	}
	if _, err := d.RecordFindingMilestone(ctx, findingID, "developer_fixed", nil); err != nil {
		t.Fatal(err)
	}
	events, err := d.FindingHistory(ctx, findingID, 0)
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for _, event := range events {
		counts[event.Kind]++
		if event.Actor == "" {
			t.Fatal("missing actor")
		}
	}
	if counts["discovered"] != 1 || counts["dispatched"] != 1 || counts["developer_fixed"] != 2 || counts["status_changed"] != 2 {
		t.Fatalf("incomplete or duplicate history: %v", counts)
	}
	if err := d.RefreshFindingMetrics(ctx); err != nil {
		t.Fatal(err)
	}
	today := time.Now().In(FindingMetricsLocation).Format("2006-01-02")
	report, err := d.FindingMetrics(ctx, today, today, FindingMetricsFilter{Severity: "high"})
	if err != nil || report.GeneratedAt == nil || !report.StockKnown {
		t.Fatalf("materialized report unavailable: %v %+v", err, report)
	}
	before, err := d.FindingMetrics(ctx, today, today, FindingMetricsFilter{Severity: "critical"})
	if err != nil {
		t.Fatal(err)
	}
	// A single edit can change classification and close the finding together.
	if _, err := d.Exec(`UPDATE findings SET severity='critical',status='fixed' WHERE id=$1`, findingID); err != nil {
		t.Fatal(err)
	}
	events, err = d.FindingHistory(ctx, findingID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if events[0].Kind != "status_changed" || events[1].Kind != "classification_changed" {
		t.Fatal("closure must use the classification selected in the same edit")
	}
	if err := d.RefreshFindingMetrics(ctx); err != nil {
		t.Fatal(err)
	}
	report, err = d.FindingMetrics(ctx, today, today, FindingMetricsFilter{Severity: "critical"})
	if err != nil || report.Summary.Closed != before.Summary.Closed+1 || report.Summary.SLAClosed != before.Summary.SLAClosed+1 {
		t.Fatalf("closure classified incorrectly: %v %+v", err, report.Summary)
	}
}

func TestFindingHistoryBackfillIsIdempotent(t *testing.T) {
	d, findingID := retestDB(t)
	tx, err := d.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	_, err = tx.Exec(`UPDATE findings SET created_at='2026-10-01',dispatched_at='2026-10-02',
		developer_fixed_at='2026-10-04',status='fixed' WHERE id=$1;
	`, findingID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`INSERT INTO finding_retests(finding_id,status,verdict,snapshot,created_at,started_at,finished_at)
		VALUES ($1,'completed','fixed','{}','2026-10-05','2026-10-05','2026-10-06')`, findingID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`DELETE FROM finding_history WHERE finding_id=$1`, findingID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`DELETE FROM settings WHERE key='finding_history_started_at'`); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := tx.Exec(findingHistorySQL); err != nil {
			t.Fatal(err)
		}
	}
	var count, backfilled, baselines int
	if err := tx.QueryRow(`SELECT count(*),count(*) FILTER(WHERE source='backfill'),
		count(*) FILTER(WHERE source='baseline' AND kind='baseline')
		FROM finding_history WHERE finding_id=$1`, findingID).Scan(&count, &backfilled, &baselines); err != nil {
		t.Fatal(err)
	}
	if count != 7 || backfilled != 6 || baselines != 1 {
		t.Fatalf("backfill duplicated or invented records: count=%d imported=%d baselines=%d", count, backfilled, baselines)
	}
	var fixedAt time.Time
	if err := tx.QueryRow(`SELECT occurred_at FROM finding_history WHERE finding_id=$1 AND kind='developer_fixed'`, findingID).Scan(&fixedAt); err != nil {
		t.Fatal(err)
	}
	if fixedAt.Format("2006-01-02") != "2026-10-04" {
		t.Fatal("backfill replaced the original timestamp")
	}
}
