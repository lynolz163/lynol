package db

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"testing"
	"time"

	"github.com/Autumn-27/artex/notify"
)

func TestResponsibilityDirectoryFlow(t *testing.T) {
	d, fid := retestDB(t)
	ctx := t.Context()
	prefix := t.Name()
	primary := ResponsibilityParty{Kind: "person", Name: prefix + "主", Active: true, Email: "primary@example.com"}
	backup := ResponsibilityParty{Kind: "person", Name: prefix + "备", Active: true}
	fallback := ResponsibilityParty{Kind: "person", Name: prefix + "兜底", Active: true}
	for _, p := range []*ResponsibilityParty{&primary, &backup, &fallback} {
		if _, err := d.SaveResponsibilityParty(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	var assetID, automatic, conflicting int64
	line := BusinessLine{Name: prefix, Code: "responsibility-flow", Active: true, PrimaryID: &primary.ID, BackupID: &backup.ID,
		SLA: FindingSLA{"critical": 1, "high": 2, "medium": 10, "low": 30}, Rules: []ResponsibilityRule{{Kind: "domain", Value: "*.responsibility.example.com"}}}
	second := BusinessLine{Name: prefix + "冲突", Code: "responsibility-conflict", Active: true}
	t.Cleanup(func() {
		d.Exec(`UPDATE settings SET value='0' WHERE key='responsibility_fallback_id'`)
		d.Exec(`DELETE FROM findings WHERE id=ANY($1::bigint[])`, []int64{automatic, conflicting})
		d.Exec(`DELETE FROM business_lines WHERE id=ANY($1::bigint[])`, []int64{line.ID, second.ID})
		d.Exec(`DELETE FROM responsibility_parties WHERE id=ANY($1::bigint[])`, []int64{primary.ID, backup.ID, fallback.ID})
		d.Exec(`DELETE FROM assets WHERE id=$1`, assetID)
	})
	ch := newTestChannel(t, d, notify.KindWeCom, NotifyModeRealtime, `{}`)
	other := newTestChannel(t, d, notify.KindEmail, NotifyModeRealtime, `{}`)
	line.ChannelIDs = []int64{ch.ID}
	if err := d.SaveBusinessLine(ctx, &line); err != nil {
		t.Fatal(err)
	}
	if err := d.SetResponsibilityFallback(ctx, fallback.ID); err != nil {
		t.Fatal(err)
	}
	if err := d.QueryRow(`INSERT INTO assets(type,domain) VALUES('subdomain','api.responsibility.example.com') RETURNING id`).Scan(&assetID); err != nil {
		t.Fatal(err)
	}
	var err error
	automatic, err = d.AddFinding(0, 0, "XSS", "自动归属", "high", "", "", "", []int64{assetID})
	if err != nil {
		t.Fatal(err)
	}
	f, err := d.GetFinding(automatic)
	if err != nil || f.BusinessLineID == nil || *f.BusinessLineID != line.ID || f.Assignee != primary.Name {
		t.Fatalf("auto assignment: %+v %v", f, err)
	}
	if _, err = d.Exec(`UPDATE findings SET asset_ids=jsonb_build_array($2::bigint) WHERE id=$1`, fid, assetID); err != nil {
		t.Fatal(err)
	}
	linked, err := d.GetFinding(fid)
	if err != nil || linked.Assignee != primary.Name || linked.BusinessLine != line.Name {
		t.Fatal("late-linked asset did not replace fallback", linked, err)
	}
	if _, err = d.SetFindingResponsibility(ctx, []int64{fid}, line.Name, primary.Name); err != nil {
		t.Fatal(err)
	}
	if _, err = d.SetFindingResponsibility(ctx, []int64{fid}, "不存在", primary.Name); !errors.Is(err, ErrResponsibilityInput) {
		t.Fatal("unknown directory accepted", err)
	}
	beforeName := line.Name
	line.Name += "新名称"
	if err = d.SaveBusinessLine(ctx, &line); err != nil {
		t.Fatal(err)
	}
	f, err = d.GetFinding(fid)
	if err != nil || f.BusinessLine != line.Name || *f.BusinessLineID != line.ID {
		t.Fatal("rename lost identity", f, err)
	}
	history, err := d.FindingHistory(ctx, fid, 0)
	if err != nil {
		t.Fatal(err)
	}
	preserved := false
	for _, e := range history {
		var m map[string]any
		json.Unmarshal(e.Metadata, &m)
		if m["business_line"] == beforeName {
			preserved = true
		}
	}
	if !preserved {
		t.Fatal("rename rewrote original journal label")
	}
	if err = d.RefreshFindingMetrics(ctx); err != nil {
		t.Fatal(err)
	}
	today := time.Now().In(FindingMetricsLocation).Format("2006-01-02")
	metrics, err := d.FindingMetrics(ctx, today, today, FindingMetricsFilter{BusinessLine: beforeName})
	if err != nil || metrics.Summary.Open != 2 {
		t.Fatalf("renamed metrics: %+v %v", metrics.Summary, err)
	}
	rows, total, err := d.ListFindingsPage(FindingFilter{BusinessLine: line.Name, ActiveOnly: true}, 1, 20)
	if err != nil || total != 2 || len(rows) != 2 {
		t.Fatal("filtered list mismatch", total, err)
	}
	primary.Active = false
	moved, err := d.SaveResponsibilityParty(ctx, &primary)
	if err != nil || moved != 2 {
		t.Fatal("backup transfer", moved, err)
	}
	f, _ = d.GetFinding(fid)
	if f.Assignee != backup.Name {
		t.Fatal("backup not selected", f.Assignee)
	}
	backup.Active = false
	moved, err = d.SaveResponsibilityParty(ctx, &backup)
	if err != nil || moved != 2 {
		t.Fatal("fallback transfer", moved, err)
	}
	f, _ = d.GetFinding(fid)
	if f.Assignee != fallback.Name {
		t.Fatal("fallback not selected", f.Assignee)
	}
	fallback.Active = false
	if _, err = d.SaveResponsibilityParty(ctx, &fallback); !errors.Is(err, ErrResponsibilityInput) {
		t.Fatal("fallback may not be disabled without replacement", err)
	}
	event := addTestEvent(t, d, notify.EventFindingCreated, automatic, notify.Snapshot{Severity: "high"})
	if _, _, err = d.FanOutPendingEvents(ctx, 200); err != nil {
		t.Fatal(err)
	}
	var selected, unselected int
	d.QueryRow(`SELECT count(*) FILTER(WHERE channel_id=$2),count(*) FILTER(WHERE channel_id=$3) FROM notification_deliveries WHERE event_id=$1`, event, ch.ID, other.ID).Scan(&selected, &unselected)
	if selected != 1 || unselected != 0 {
		t.Fatal("line routing did not restrict channel", selected, unselected)
	}
	second.Rules = line.Rules
	if err = d.SaveBusinessLine(ctx, &second); err != nil {
		t.Fatal(err)
	}
	conflicting, err = d.AddFinding(0, 0, "XSS", "规则冲突", "high", "", "", "", []int64{assetID})
	if err != nil {
		t.Fatal(err)
	}
	f, _ = d.GetFinding(conflicting)
	if f.BusinessLine != "" || f.Assignee != fallback.Name {
		t.Fatal("conflict guessed a line", f)
	}
	result, err := d.ApplyResponsibilityRules(ctx)
	if err != nil || result.Conflicts < 1 {
		t.Fatal("conflict not reported", result, err)
	}
	second.Active = false
	if err = d.SaveBusinessLine(ctx, &second); err != nil {
		t.Fatal(err)
	}
	result, err = d.ApplyResponsibilityRules(ctx)
	if err != nil || result.Assigned < 1 {
		t.Fatal("existing rule assignment", result, err)
	}
}

func TestResponsibilityAssetRuleBoundaries(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	line := BusinessLine{Name: t.Name(), Code: "rule-boundaries", Active: true, Rules: []ResponsibilityRule{{Kind: "domain", Value: "*.scope.example.com"}, {Kind: "cidr", Value: "10.18.0.128/24"}, {Kind: "cidr", Value: "2001:db8::/32"}}}
	if err = d.SaveBusinessLine(t.Context(), &line); err != nil {
		t.Fatal(err)
	}
	defer d.Exec(`DELETE FROM business_lines WHERE id=$1`, line.ID)
	for _, c := range []struct {
		domain, ip, url string
		matches         bool
	}{
		{"api.scope.example.com", "", "", true}, {"scope.example.com", "", "", false}, {"notscope.example.com", "", "", false},
		{"", "10.18.0.8", "", true}, {"", "10.18.1.8", "", false}, {"", "invalid", "", false},
		{"", "2001:db8::5", "", true}, {"", "", "https://api.scope.example.com/login", true},
	} {
		var id int64
		if err = d.QueryRow(`INSERT INTO assets(type,domain,ip,url) VALUES('app',$1,$2,$3) RETURNING id`, c.domain, c.ip, c.url).Scan(&id); err != nil {
			t.Fatal(err)
		}
		var matched bool
		err = d.QueryRow(`SELECT $1=ANY(responsibility_matching_lines(jsonb_build_array($2::bigint)))`, line.ID, id).Scan(&matched)
		d.Exec(`DELETE FROM assets WHERE id=$1`, id)
		if err != nil || matched != c.matches {
			t.Fatalf("rule %+v: %v %v", c, matched, err)
		}
	}
	line.Rules = []ResponsibilityRule{{Kind: "cidr", Value: "invalid"}}
	if err = d.SaveBusinessLine(t.Context(), &line); !errors.Is(err, ErrResponsibilityInput) {
		t.Fatal("invalid rule accepted", err)
	}
}

func TestResponsibilitySLAHistory(t *testing.T) {
	start := time.Date(2026, 11, 1, 0, 0, 0, 0, FindingMetricsLocation)
	event := func(id int64, kind string, hours int, status, raw string) FindingHistoryEvent {
		return FindingHistoryEvent{ID: id, FindingID: 1, Kind: kind, NewStatus: status, OccurredAt: start.Add(time.Duration(hours) * time.Hour), Metadata: json.RawMessage(raw)}
	}
	events := []FindingHistoryEvent{
		event(1, "discovered", 0, "pending", `{"severity":"high","business_line":"支付","sla":{"high":2}}`),
		event(2, "status_changed", 72, "fixed", `{}`),
		event(3, "sla_changed", 96, "", `{"sla":{"high":10}}`),
	}
	snapshots, err := BuildFindingDailySnapshots(events, map[int64]string{1: "test"}, start.Add(5*24*time.Hour), start, FindingSLA{"high": 7}, nil)
	if err != nil {
		t.Fatal(err)
	}
	result := SummarizeFindingMetrics(snapshots, FindingMetricsFilter{})
	if result.Summary.SLAClosed != 1 || result.Summary.SLAMet != 0 {
		t.Fatal("custom SLA did not apply, or later change rewrote closure", result.Summary)
	}
}

// Exercise the upgrade on a new, owned database populated before directory migration.
func TestResponsibilityLegacyMigration(t *testing.T) {
	parsed, err := url.Parse(testDSN(t))
	if err != nil {
		t.Fatal(err)
	}
	admin, err := sql.Open("pgx", parsed.String())
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	name := fmt.Sprintf("artex_resp_migration_%d", time.Now().UnixNano())
	if _, err = admin.ExecContext(t.Context(), `CREATE DATABASE `+name); err != nil {
		t.Fatal(err)
	}
	defer admin.ExecContext(t.Context(), `DROP DATABASE `+name+` WITH (FORCE)`)
	parsed.Path = "/" + name
	legacy, err := sql.Open("pgx", parsed.String())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = legacy.ExecContext(t.Context(), schemaSQL+"\n"+findingHistorySQL); err != nil {
		legacy.Close()
		t.Fatal(err)
	}
	var id int64
	if err = legacy.QueryRow(`INSERT INTO findings(name,severity,business_line,assignee) VALUES('历史漏洞','high','历史业务','历史团队') RETURNING id`).Scan(&id); err != nil {
		legacy.Close()
		t.Fatal(err)
	}
	legacy.Close()
	d, err := Open(parsed.String())
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	f, err := d.GetFinding(id)
	if err != nil || f.BusinessLineID == nil || f.AssigneeID == nil || f.BusinessLine != "历史业务" || f.Assignee != "历史团队" {
		t.Fatalf("legacy association: %+v %v", f, err)
	}
	directory, err := d.ResponsibilityDirectory(t.Context())
	if err != nil || len(directory.BusinessLines) != 1 || len(directory.Parties) != 1 || directory.Parties[0].Kind != "team" {
		t.Fatalf("legacy directory: %+v %v", directory, err)
	}
	if _, err = d.ExecContext(t.Context(), responsibilitySQL); err != nil {
		t.Fatal("repeated migration", err)
	}
	var count int
	d.QueryRow(`SELECT count(*) FROM responsibility_parties`).Scan(&count)
	if count != 1 {
		t.Fatal("migration duplicated party", count)
	}
}
