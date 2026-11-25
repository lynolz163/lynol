package db

import (
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestFindingMilestoneManualBatchAndAttempts(t *testing.T) {
	d, first := retestDB(t)
	ctx := t.Context()
	stamp := func(at time.Time) *time.Time { return &at }
	second, err := d.AddFinding(0, 0, "manual-time", "second", "high", "", "", "test", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.DeleteFinding(second) })
	base := time.Now().Add(-72 * time.Hour).Truncate(time.Second)
	for id, created := range map[int64]time.Time{first: base, second: base.Add(24 * time.Hour)} {
		if _, err := d.Exec(`UPDATE findings SET created_at=$2 WHERE id=$1`, id, created); err != nil {
			t.Fatal(err)
		}
		if _, err := d.Exec(`UPDATE finding_history SET occurred_at=$2 WHERE finding_id=$1 AND kind='discovered'`, id, created); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := d.RecordFindingMilestoneBatch(ctx, []int64{first, second}, "dispatched", stamp(base.Add(12*time.Hour))); !errors.Is(err, ErrInvalidFindingMilestoneTime) {
		t.Fatalf("batch accepted time before second finding's discovery: %v", err)
	}
	finding, err := d.GetFinding(first)
	if err != nil || finding.DispatchedAt != nil {
		t.Fatalf("invalid batch partially saved: %+v %v", finding, err)
	}
	dispatched := base.Add(36 * time.Hour)
	recorded, err := d.RecordFindingMilestoneBatch(ctx, []int64{first, second}, "dispatched", &dispatched)
	if err != nil || len(recorded) != 2 {
		t.Fatalf("batch: %+v %v", recorded, err)
	}
	for _, at := range recorded {
		if !at.Equal(dispatched) {
			t.Fatal("batch replaced chosen time with now")
		}
	}
	if _, err := d.RecordFindingMilestone(ctx, first, "developer_fixed", stamp(dispatched.Add(-time.Hour))); !errors.Is(err, ErrInvalidFindingMilestoneTime) {
		t.Fatalf("feedback before dispatch accepted: %v", err)
	}
	fixed := base.Add(48 * time.Hour)
	if _, err := d.RecordFindingMilestone(ctx, first, "developer_fixed", &fixed); err != nil {
		t.Fatal(err)
	}
	again, err := d.RecordFindingMilestone(ctx, first, "developer_fixed", stamp(fixed.Add(time.Hour)))
	if err != nil || !again.Equal(fixed) {
		t.Fatal("repeat submission changed existing time", err)
	}
	if _, _, _, err := d.SetFindingStatusWithNotify(ctx, first, FindingFixed); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := d.SetFindingStatusWithNotify(ctx, first, FindingConfirmed); err != nil {
		t.Fatal(err)
	}
	if _, err := d.RecordFindingMilestone(ctx, first, "developer_fixed", stamp(fixed.Add(time.Hour))); !errors.Is(err, ErrInvalidFindingMilestoneTime) {
		t.Fatalf("new attempt accepted time before reopen: %v", err)
	}
	if _, err := d.RecordFindingMilestone(ctx, first, "developer_fixed", nil); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := d.QueryRow(`SELECT count(*) FROM finding_history WHERE finding_id=$1 AND kind='developer_fixed'`, first).Scan(&count); err != nil || count != 2 {
		t.Fatalf("attempt history lost or duplicated: %d %v", count, err)
	}
}

func TestFindingMilestoneBackfillRecomputesDurationsWithOriginalSLA(t *testing.T) {
	d, fid := retestDB(t)
	ctx := t.Context()
	stamp := func(at time.Time) *time.Time { return &at }
	base := time.Now().Add(-10 * 24 * time.Hour).Truncate(time.Second)
	line := fmt.Sprintf("manual-time-stats-%d", fid)
	if _, err := d.Exec(`UPDATE findings SET created_at=$2,business_line=$3 WHERE id=$1`, fid, base, line); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`UPDATE finding_history SET occurred_at=$2,metadata=metadata||jsonb_build_object('business_line',$3::text)
		WHERE finding_id=$1 AND kind='discovered'`, fid, base, line); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`INSERT INTO finding_history(finding_id,kind,old_status,new_status,actor,occurred_at) VALUES
		($1,'retest_requested','','','test',$2),($1,'status_changed','pending','fixed','test',$3)`, fid, base.Add(72*time.Hour), base.Add(8*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	var oldRules string
	if err := d.QueryRow(`SELECT value FROM settings WHERE key='finding_sla_days'`).Scan(&oldRules); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Exec(`UPDATE settings SET value=$1 WHERE key='finding_sla_days'`, oldRules) })
	if err := d.RefreshFindingMetrics(ctx); err != nil {
		t.Fatal(err)
	}
	from, to := base.In(FindingMetricsLocation).Format("2006-01-02"), base.Add(9*24*time.Hour).In(FindingMetricsLocation).Format("2006-01-02")
	before, err := d.FindingMetrics(ctx, from, to, FindingMetricsFilter{BusinessLine: line})
	if err != nil || before.Durations[0].Count != 0 || before.Summary.SLAClosed != 1 || before.Summary.SLAMet != 0 {
		t.Fatalf("initial historical summary: %+v %v", before, err)
	}
	if _, err := d.RecordFindingMilestone(ctx, fid, "dispatched", stamp(base.Add(24*time.Hour))); err != nil {
		t.Fatal(err)
	}
	if _, err := d.RecordFindingMilestone(ctx, fid, "developer_fixed", stamp(base.Add(48*time.Hour))); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`UPDATE settings SET value='{"critical":3,"high":14,"medium":30,"low":90}' WHERE key='finding_sla_days'`); err != nil {
		t.Fatal(err)
	}
	if err := d.RefreshFindingMetrics(ctx); err != nil {
		t.Fatal(err)
	}
	after, err := d.FindingMetrics(ctx, from, to, FindingMetricsFilter{BusinessLine: line})
	if err != nil {
		t.Fatal(err)
	}
	for _, stage := range after.Durations[:3] {
		if stage.Count != 1 || stage.Average == nil || *stage.Average != 24 {
			t.Fatalf("late milestone missing from %s: %+v", stage.Key, stage)
		}
	}
	if after.SLA["high"] != 7 || after.Summary.SLAMet != 0 || after.Summary.SLAClosed != 1 {
		t.Fatalf("historical SLA was rewritten: %+v", after)
	}
}
