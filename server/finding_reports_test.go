package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Autumn-27/artex/db"
)

func TestFindingReportFullSLAAndQuality(t *testing.T) {
	report := &db.SavedFindingReport{Kind: "weekly", Current: &db.FindingMetricsReport{
		StockKnown: true, QualityKnown: true,
		Summary: db.FindingMetricBucket{SLAMet: 24, SLAClosed: 40, Open: 35, Overdue: 18,
			FirstRetestPassed: 28, FirstRetestTotal: 42, ReworkRounds: 12, VerifiedFixes: 40},
	}}
	content := strings.Join(findingReportContent(report).Lines, "\n")
	for _, want := range []string{"SLA 全量达标率 41.4%", "24 / 58", "已关闭口径 60.0%", "一次修复通过率 66.7%", "平均返工 0.30 轮"} {
		if !strings.Contains(content, want) {
			t.Fatalf("missing %q in %s", want, content)
		}
	}
	report.Current.StockKnown = false
	report.Current.QualityKnown = false
	content = strings.Join(findingReportContent(report).Lines, "\n")
	if strings.Contains(content, "全量达标率 41.4%") || strings.Contains(content, "平均返工 0.30") {
		t.Fatalf("unknown stock/quality rendered as measured: %s", content)
	}
}

func TestGovernanceReportEndToEnd(t *testing.T) {
	f := newNotifyFixture(t)
	var body string
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(io.LimitReader(r.Body, 8192))
		body = string(raw)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"errcode":0}`))
	}))
	t.Cleanup(receiver.Close)
	channelID := f.createChannel(t, map[string]any{"name": "治理报告测试", "kind": "wecom", "mode": "digest", "config": map[string]any{"webhook": receiver.URL}})
	bad := f.request("PUT", "/api/exploration/finding-report-schedules", `{"kind":"weekly","enabled":true,"weekday":1,"monthday":1,"hour":9,"minute":0,"channel_id":null,"filter":{}}`)
	if bad.Code != 400 {
		t.Fatalf("enabled without destination: %d", bad.Code)
	}
	schedule := fmt.Sprintf(`{"kind":"weekly","enabled":true,"weekday":1,"monthday":1,"hour":9,"minute":0,"channel_id":%d,"filter":{"severity":"","business_line":"test-scope","assignee":""}}`, channelID)
	saved := f.request("PUT", "/api/exploration/finding-report-schedules", schedule)
	if saved.Code != 200 {
		t.Fatalf("save schedule: %d %s", saved.Code, saved.Body.String())
	}
	t.Cleanup(func() {
		f.pg.Exec(`UPDATE finding_report_schedules SET enabled=false,channel_id=NULL WHERE kind='weekly'`)
	})
	monday := time.Date(2026, 11, 23, 9, 0, 0, 0, db.FindingMetricsLocation)
	for range 2 {
		if err := f.n.s.generateDueFindingReports(t.Context(), monday); err != nil {
			t.Fatal(err)
		}
	}
	var id, eventID int64
	if err := f.pg.QueryRow(`SELECT id,event_id FROM finding_reports WHERE kind='weekly' AND period_start='2026-11-16' AND filter->>'business_line'='test-scope'`).Scan(&id, &eventID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.pg.Exec(`DELETE FROM finding_reports WHERE id=$1`, id) })
	var deliveries int
	if err := f.pg.QueryRow(`SELECT count(*) FROM notification_deliveries WHERE event_id=$1`, eventID).Scan(&deliveries); err != nil || deliveries != 1 {
		t.Fatalf("scheduler duplicated report: %d %v", deliveries, err)
	}
	f.n.stepRealtime(t.Context(), f.channel(t, channelID), 1, "https://artex.example.test")
	var sent struct {
		Markdown struct {
			Content string `json:"content"`
		} `json:"markdown"`
	}
	if err := json.Unmarshal([]byte(body), &sent); err != nil || !strings.Contains(sent.Markdown.Content, "漏洞治理周报") || !strings.Contains(sent.Markdown.Content, fmt.Sprintf("/remediation-report?id=%d", id)) {
		t.Fatalf("report not delivered correctly: %s %v", body, err)
	}
	detail := f.request("GET", fmt.Sprintf("/api/exploration/finding-reports/%d", id), "")
	if detail.Code != 200 || !strings.Contains(detail.Body.String(), `"delivery_state":"sent"`) {
		t.Fatalf("delivery outcome absent: %d %s", detail.Code, detail.Body.String())
	}
}
