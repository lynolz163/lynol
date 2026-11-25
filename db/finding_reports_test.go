package db

import (
	"encoding/json"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/Autumn-27/artex/notify"
)

func TestFindingMetricsAssignmentAtEventTime(t *testing.T) {
	start := time.Date(2026, 11, 1, 0, 0, 0, 0, FindingMetricsLocation)
	var events []FindingHistoryEvent
	add := func(kind string, hour int, status string, metadata map[string]string) {
		raw, _ := json.Marshal(metadata)
		events = append(events, FindingHistoryEvent{ID: int64(len(events) + 1), FindingID: 1, Kind: kind, OccurredAt: start.Add(time.Duration(hour) * time.Hour), NewStatus: status, Metadata: raw})
	}
	add("discovered", 0, "pending", map[string]string{"severity": "high", "vulnclass": "XSS"})
	add("assignment_changed", 1, "", map[string]string{"business_line": "业务 A", "assignee": "团队甲"})
	add("status_changed", 25, "fixed", map[string]string{})
	add("assignment_changed", 26, "", map[string]string{"business_line": "业务 B", "assignee": "团队乙"})
	add("status_changed", 27, "confirmed", map[string]string{})
	snapshots, err := BuildFindingDailySnapshots(events, map[int64]string{1: "test"}, start.Add(9*24*time.Hour), start, FindingSLA{"high": 7}, nil)
	if err != nil {
		t.Fatal(err)
	}
	a := SummarizeFindingMetrics(snapshots, FindingMetricsFilter{BusinessLine: "业务 A"})
	b := SummarizeFindingMetrics(snapshots, FindingMetricsFilter{BusinessLine: "业务 B", Assignee: "团队乙"})
	if a.Summary.Closed != 1 || a.Summary.Open != 0 || *a.Durations[4].P90 != 25 {
		t.Fatalf("original closure attribution lost: %+v", a)
	}
	if b.Summary.New != 0 || b.Summary.Closed != 0 || b.Summary.Reopened != 1 || b.Summary.Open != 1 || b.Summary.Overdue != 1 || len(b.Overdue) != 1 {
		t.Fatalf("current attribution mismatch: %+v", b)
	}
	none := SummarizeFindingMetrics(snapshots, FindingMetricsFilter{BusinessLine: "__none__"})
	if none.Summary.New != 1 || none.Summary.Open != 0 {
		t.Fatalf("unassigned filter: %+v", none.Summary)
	}
	if len(a.Groups) != 2 || a.Groups[0].P90 == nil || *a.Groups[0].P90 != 25 {
		t.Fatalf("group duration missing: %+v", a.Groups)
	}
}

func TestFindingReportCalendarAndDueTime(t *testing.T) {
	now := time.Date(2024, 3, 1, 9, 0, 0, 0, FindingMetricsLocation)
	from, to, previousFrom, previousTo := FindingReportPeriod("monthly", now)
	if from.Format("2006-01-02") != "2024-02-01" || to.Format("2006-01-02") != "2024-02-29" || previousFrom.Format("2006-01-02") != "2024-01-01" || previousTo.Format("2006-01-02") != "2024-01-31" {
		t.Fatal("calendar month boundaries", from, to, previousFrom, previousTo)
	}
	monday := time.Date(2026, 11, 23, 9, 0, 0, 0, FindingMetricsLocation)
	from, to, previousFrom, previousTo = FindingReportPeriod("weekly", monday)
	if from.Day() != 16 || to.Day() != 22 || previousFrom.Day() != 9 || previousTo.Day() != 15 {
		t.Fatal("calendar week boundaries")
	}
	channel := int64(1)
	s := FindingReportSchedule{Kind: "weekly", Enabled: true, Weekday: 1, Hour: 9, Monthday: 1, ChannelID: &channel}
	if FindingReportScheduleDue(s, monday.Add(-time.Minute)) || !FindingReportScheduleDue(s, monday) || !FindingReportScheduleDue(s, monday.Add(24*time.Hour)) {
		t.Fatal("weekly schedule boundary or restart catchup")
	}
	s.Kind = "monthly"
	if !FindingReportScheduleDue(s, now) || FindingReportScheduleDue(s, now.Add(-time.Minute)) {
		t.Fatal("monthly schedule boundary")
	}
	s.Enabled = false
	if FindingReportScheduleDue(s, now) {
		t.Fatal("disabled schedule ran")
	}
}

func TestFindingResponsibilityAndReportQueue(t *testing.T) {
	d, fid := retestDB(t)
	ctx := t.Context()
	line := BusinessLine{Name: "业务线测试", Code: "report-test", Active: true}
	party := ResponsibilityParty{Name: "测试团队", Kind: "team", Active: true}
	if err := d.SaveBusinessLine(ctx, &line); err != nil {
		t.Fatal(err)
	}
	if _, err := d.SaveResponsibilityParty(ctx, &party); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		d.Exec(`DELETE FROM business_lines WHERE id=$1`, line.ID)
		d.Exec(`DELETE FROM responsibility_parties WHERE id=$1`, party.ID)
	})
	for range 2 {
		if _, err := d.SetFindingResponsibility(ctx, []int64{fid}, "业务线测试", "测试团队"); err != nil {
			t.Fatal(err)
		}
	}
	finding, err := d.GetFinding(fid)
	if err != nil || finding.BusinessLine != "业务线测试" || finding.Assignee != "测试团队" {
		t.Fatalf("detail: %+v %v", finding, err)
	}
	exported, err := d.ListFindingsForExport(FindingFilter{}, []int64{fid})
	if err != nil || len(exported) != 1 || exported[0].Assignee != "测试团队" {
		t.Fatalf("existing finding export broken: %v", err)
	}
	events, err := d.FindingHistory(ctx, fid, 0)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, e := range events {
		if e.Kind == "assignment_changed" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("assignment repeated or missing: %d", count)
	}
	if err := d.RefreshFindingMetrics(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := d.SetFindingResponsibility(ctx, []int64{fid}, "", ""); err != nil {
		t.Fatal(err)
	}
	options, err := d.FindingResponsibilityOptions(ctx)
	if err != nil || !slices.Contains(options.BusinessLines, "业务线测试") || !slices.Contains(options.Assignees, "测试团队") {
		t.Fatalf("historical assignments disappeared from filters: %+v %v", options, err)
	}
	filter := FindingMetricsFilter{BusinessLine: "业务线测试"}
	id, err := d.StoreFindingReport(ctx, "weekly", time.Now(), filter)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Exec(`DELETE FROM finding_reports WHERE id=$1`, id) })
	first, err := d.GetFindingReport(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	again, err := d.StoreFindingReport(ctx, "weekly", time.Now(), filter)
	if err != nil || again != id {
		t.Fatalf("duplicate period: %d %d %v", id, again, err)
	}
	second, err := d.GetFindingReport(ctx, id)
	if err != nil || !first.GeneratedAt.Equal(second.GeneratedAt) {
		t.Fatal("report regenerated its frozen timestamp", err)
	}
	channel, err := d.SaveNotificationChannel(ctx, &NotificationChannel{Name: "report-test", Kind: notify.KindWeCom, Mode: NotifyModeDigest, Config: json.RawMessage(`{"webhook":"http://127.0.0.1:1/never-contacted"}`)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Exec(`DELETE FROM notification_channels WHERE id=$1`, channel) })
	content := notify.ReportContent{ID: id, Title: "test report", Lines: []string{"line"}}
	for range 2 {
		if err := d.QueueFindingReport(ctx, id, channel, content); err != nil {
			t.Fatal(err)
		}
	}
	var eventID int64
	var n int
	if err := d.QueryRow(`SELECT event_id FROM finding_reports WHERE id=$1`, id).Scan(&eventID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Exec(`DELETE FROM notification_events WHERE id=$1`, eventID) })
	if err := d.QueryRow(`SELECT count(*) FROM notification_deliveries WHERE event_id=$1`, eventID).Scan(&n); err != nil || n != 1 {
		t.Fatalf("duplicate report delivery: %d %v", n, err)
	}
	digest, err := d.ClaimDigestBatch(ctx, channel, 20, time.Minute)
	if err != nil || len(digest) != 0 {
		t.Fatalf("report mixed into finding digest: %v %v", digest, err)
	}
	realtime, err := d.ClaimRealtimeDeliveries(ctx, channel, 1, time.Minute)
	if err != nil || len(realtime) != 1 || realtime[0].EventKind != notify.EventGovernanceReport {
		t.Fatalf("report not independently claimable: %v %v", realtime, err)
	}
	if err := d.MarkDeliveriesSent(ctx, []int64{realtime[0].ID}); err != nil {
		t.Fatal(err)
	}
	saved, err := d.GetFindingReport(ctx, id)
	if err != nil || saved.DeliveryState != "sent" {
		t.Fatal(fmt.Sprint(saved), err)
	}
}
