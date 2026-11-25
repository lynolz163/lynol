package report

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/artex/internal/org"
	"github.com/Autumn-27/artex/notify"
	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
)

func TestEndingEventAttributionAndMergedP90(t *testing.T) {
	a, b, x, y := int64(1), int64(2), int64(3), int64(4)
	at := time.Date(2026, 11, 1, 8, 0, 0, 0, db.FindingMetricsLocation)
	states := map[int64]*state{}
	d := &daily{Buckets: map[string]*bucket{}, QualityKnown: true}
	sla := 7
	events := []event{{FindingID: 1, Kind: "discovered", At: at, NewStatus: "pending", DepartmentID: &a, OwnerID: &x, Version: 1, Quality: "recorded", Metadata: json.RawMessage(`{"severity":"high"}`)},
		{FindingID: 1, Kind: "dispatched", At: at.Add(time.Hour), DepartmentID: &a, OwnerID: &x, Version: 1, Quality: "recorded", Metadata: json.RawMessage(`{}`)},
		{FindingID: 1, Kind: "developer_fixed", At: at.Add(49 * time.Hour), DepartmentID: &b, OwnerID: &y, Version: 1, Quality: "recorded", Metadata: json.RawMessage(`{}`)},
		{FindingID: 1, Kind: "status_changed", NewStatus: "fixed", At: at.Add(72 * time.Hour), DepartmentID: &b, OwnerID: &y, Version: 1, Quality: "recorded", SLADays: &sla, Metadata: json.RawMessage(`{}`)}}
	for _, e := range events {
		if err := replay(states, e, d); err != nil {
			t.Fatal(err)
		}
	}
	ba := d.get(dimension{DepartmentID: &a, OwnerID: &x, Severity: "high"})
	bb := d.get(dimension{DepartmentID: &b, OwnerID: &y, Severity: "high"})
	if ba.New != 1 || ba.Closed != 0 || bb.Closed != 1 || bb.New != 0 || bb.SLAMet != 1 || len(bb.Samples["development"]) != 1 || bb.Samples["development"][0] != 48 {
		t.Fatalf("wrong ending attribution A=%+v B=%+v", ba, bb)
	}
	d1 := &daily{Day: "2026-11-01", ID: 1, StockKnown: true, QualityKnown: true, Buckets: map[string]*bucket{}}
	d2 := &daily{Day: "2026-11-02", ID: 2, StockKnown: true, QualityKnown: true, Buckets: map[string]*bucket{}}
	b1 := d1.get(dimension{DepartmentID: &a, Severity: "high"})
	b1.Open = 9
	b1.Samples["closure"] = []float64{1, 2, 3, 4, 5, 6, 7, 8, 9}
	b2 := d2.get(dimension{DepartmentID: &a, Severity: "high"})
	b2.Open = 2
	b2.Samples["closure"] = []float64{100}
	r := summarize(d1.Day, d2.Day, map[string]*daily{d1.Day: d1, d2.Day: d2}, db.FindingMetricsFilter{}, labels{"department": {a: {Name: "A", Active: true}}, "business_line": {}, "assignee": {}})
	if r.Summary.Open != 2 {
		t.Fatalf("daily stock incorrectly summed: %d", r.Summary.Open)
	}
	for _, v := range r.Durations {
		if v.Key == "closure" && (v.Count != 10 || v.P90 == nil || *v.P90 != 9) {
			t.Fatalf("P90 averaged instead of merged: %+v", v)
		}
	}
	unknown := summarize("2026-11-01", "2026-11-03", map[string]*daily{d1.Day: d1, d2.Day: d2}, db.FindingMetricsFilter{}, labels{"department": {a: {Name: "A", Active: true}}, "business_line": {}, "assignee": {}})
	if unknown.StockKnown || unknown.Trend[2].Open != nil || unknown.DataQuality.MissingSnapshotDays != 1 {
		t.Fatalf("missing historical day treated as zero: %+v", unknown)
	}
}

var testSchema struct {
	sync.Once
	err error
}

func testStore(t *testing.T) (*Store, context.Context) {
	t.Helper()
	dsn := os.Getenv("ARTEX_ORG_TEST_DSN")
	if dsn == "" {
		t.Skip("ARTEX_ORG_TEST_DSN required; production configuration is never read")
	}
	conn, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	conn.SetMaxOpenConns(4)
	t.Cleanup(func() { conn.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	t.Cleanup(cancel)
	testSchema.Do(func() {
		text := ""
		for _, name := range []string{"schema.sql", "finding_history.sql", "finding_reports.sql", "responsibility.sql"} {
			raw, e := os.ReadFile(filepath.Join("..", "..", "db", name))
			if e != nil {
				testSchema.err = e
				return
			}
			text += string(raw) + "\n"
		}
		_, testSchema.err = conn.ExecContext(ctx, text+org.SchemaSQL)
	})
	if testSchema.err != nil {
		t.Fatal(testSchema.err)
	}
	// Each test controls its own snapshot timeline. Earlier tests deliberately seal
	// historical days, so their heads must not suppress later synthetic fixtures.
	if _, err = conn.ExecContext(ctx, `DELETE FROM report.daily_snapshot_head; DELETE FROM report.daily_vuln_stat; DELETE FROM report.daily_snapshot; DELETE FROM settings WHERE key IN ('org_report_started_at','org_report_correction_watermark')`); err != nil {
		t.Fatal(err)
	}
	return New(conn), ctx
}
func testDepartment(t *testing.T, s *Store, ctx context.Context) int64 {
	t.Helper()
	raw, _ := json.Marshal(org.Department{Name: "报表-" + uuid.NewString(), Code: uuid.NewString(), Active: true})
	v, err := org.New(s.DB).Change(ctx, org.Command{Kind: "department", Action: "save", Data: raw, Scope: "all_open", IdempotencyKey: uuid.NewString()})
	if err != nil {
		t.Fatal(err)
	}
	return v.EntityID
}
func testEvent(t *testing.T, s *Store, ctx context.Context, finding int64, kind, status string, at time.Time, dep *int64, version int, meta string) int64 {
	t.Helper()
	var id int64
	quality := "recorded"
	if version == 0 {
		quality = "unknown"
	}
	err := s.DB.QueryRowContext(ctx, `INSERT INTO finding_history(finding_id,kind,new_status,actor,occurred_at,metadata,source,department_id,attribution_version,attribution_quality,sla_days) VALUES($1,$2,$3,'isolated fixture',$4,$5,'backfill',$6,$7,$8,7) RETURNING id`, finding, kind, status, at, meta, dep, version, quality).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestSealedSnapshotsRemainFrozenAndOldNamesUseStableIDs(t *testing.T) {
	s, ctx := testStore(t)
	dep := testDepartment(t, s, ctx)
	now := time.Now()
	old := date(now).AddDate(0, 0, -10).Add(time.Hour)
	finding := int64(900000) + dep
	testEvent(t, s, ctx, finding, "discovered", "pending", old, &dep, 1, `{"severity":"high","vulnclass":"test"}`)
	testEvent(t, s, ctx, finding, "status_changed", "fixed", old.Add(time.Hour), &dep, 1, `{}`)
	if err := s.Refresh(ctx, now); err != nil {
		t.Fatal(err)
	}
	day := old.Format("2006-01-02")
	first, err := s.Metrics(ctx, day, day, db.FindingMetricsFilter{DepartmentID: &dep})
	if err != nil {
		t.Fatal(err)
	}
	if first.Summary.New != 1 || first.Summary.Closed != 1 || first.StockKnown || len(first.SnapshotIDs) != 1 {
		t.Fatalf("historical summary %+v", first)
	}
	testEvent(t, s, ctx, finding+100000, "discovered", "pending", old.Add(2*time.Hour), &dep, 1, `{"severity":"high"}`)
	if _, err = s.DB.ExecContext(ctx, `UPDATE org.department SET name='重命名部门',active=false WHERE id=$1`, dep); err != nil {
		t.Fatal(err)
	}
	if err = s.Refresh(ctx, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	after, err := s.Metrics(ctx, day, day, db.FindingMetricsFilter{DepartmentID: &dep})
	if err != nil {
		t.Fatal(err)
	}
	if after.Summary.New != 1 || after.SnapshotIDs[0] != first.SnapshotIDs[0] {
		t.Fatalf("ordinary refresh replaced sealed head %+v", after)
	}
	for _, g := range after.Groups {
		if g.Dimension == "department" && (g.ID == nil || *g.ID != dep || g.Name != "重命名部门" || g.Active) {
			t.Fatalf("disabled renamed department not queryable %+v", g)
		}
	}
}

func TestTodayUsesTemporalOwnershipWhileEventCountsStayAtDiscovery(t *testing.T) {
	s, ctx := testStore(t)
	a := testDepartment(t, s, ctx)
	b := testDepartment(t, s, ctx)
	var asset int64
	if err := s.DB.QueryRowContext(ctx, `INSERT INTO assets(type,domain) VALUES('subdomain',$1) RETURNING id`, uuid.NewString()+".example.invalid").Scan(&asset); err != nil {
		t.Fatal(err)
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = org.SyncAssetTx(ctx, tx, asset); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	assign := func(dep int64) {
		t.Helper()
		p, e := org.New(s.DB).PreviewAssignment(ctx, org.AssignmentRequest{Selection: org.AssignmentSelection{Mode: "ids", IDs: []int64{asset}, ExpectedCount: 1}, DepartmentID: dep, Scope: "all_open"})
		if e != nil {
			t.Fatal(e)
		}
		if _, e = org.New(s.DB).ConfirmAssignment(ctx, org.AssignmentConfirm{Token: p.Token, IdempotencyKey: uuid.NewString(), OverwriteConfirmed: true}); e != nil {
			t.Fatal(e)
		}
	}
	assign(a)
	var finding int64
	if err = s.DB.QueryRowContext(ctx, `INSERT INTO findings(asset_ids,severity,status,name,assignment_source) VALUES(jsonb_build_array($1::bigint),'high','pending','live ownership test','manual') RETURNING id`, asset).Scan(&finding); err != nil {
		t.Fatal(err)
	}
	if err = s.Refresh(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	day := date(time.Now()).Format("2006-01-02")
	before, err := s.Metrics(ctx, day, day, db.FindingMetricsFilter{DepartmentID: &a})
	if err != nil {
		t.Fatal(err)
	}
	if before.Summary.New != 1 || before.Summary.Open != 1 {
		t.Fatalf("initial %+v", before)
	}
	assign(b)
	if err = s.Refresh(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	old, err := s.Metrics(ctx, day, day, db.FindingMetricsFilter{DepartmentID: &a})
	if err != nil {
		t.Fatal(err)
	}
	current, err := s.Metrics(ctx, day, day, db.FindingMetricsFilter{DepartmentID: &b})
	if err != nil {
		t.Fatal(err)
	}
	if old.Summary.New != 1 || old.Summary.Open != 0 || current.Summary.New != 0 || current.Summary.Open != 1 {
		t.Fatalf("current attribution rewrote discovery old=%+v current=%+v", old.Summary, current.Summary)
	}
}

func TestDepartmentReportsKeepDistinctFrozenPayloadsAndLegacyPayload(t *testing.T) {
	s, ctx := testStore(t)
	a := testDepartment(t, s, ctx)
	b := testDepartment(t, s, ctx)
	now := time.Now()
	from, _, _, _ := db.FindingReportPeriod("weekly", now)
	testEvent(t, s, ctx, 1100000+a, "discovered", "pending", from.Add(time.Hour), &a, 1, `{"severity":"high"}`)
	testEvent(t, s, ctx, 1100000+b, "discovered", "pending", from.Add(time.Hour), &b, 1, `{"severity":"critical"}`)
	if err := s.Refresh(ctx, now); err != nil {
		t.Fatal(err)
	}
	first, err := s.StoreReport(ctx, "weekly", now, db.FindingMetricsFilter{DepartmentID: &a})
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.StoreReport(ctx, "weekly", now, db.FindingMetricsFilter{DepartmentID: &b})
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("department IDs absent from report unique filter")
	}
	var digest string
	if err = s.DB.QueryRowContext(ctx, `SELECT md5(current_payload::text||previous_payload::text) FROM finding_reports WHERE id=$1`, first).Scan(&digest); err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.ExecContext(ctx, `UPDATE org.department SET name='报告后更名' WHERE id=$1`, a); err != nil {
		t.Fatal(err)
	}
	again, err := s.StoreReport(ctx, "weekly", now, db.FindingMetricsFilter{DepartmentID: &a})
	if err != nil || again != first {
		t.Fatalf("reopened report changed identity %d %v", again, err)
	}
	var after string
	if err = s.DB.QueryRowContext(ctx, `SELECT md5(current_payload::text||previous_payload::text) FROM finding_reports WHERE id=$1`, first).Scan(&after); err != nil || digest != after {
		t.Fatalf("saved payload changed %v", err)
	}
	loaded, err := (&db.DB{DB: s.DB}).GetFindingReport(ctx, first)
	if err != nil || loaded.AttributionVersion != 1 || loaded.DepartmentID == nil || *loaded.DepartmentID != a || loaded.Current.Summary.New != 1 {
		t.Fatalf("saved report read %+v %v", loaded, err)
	}
	var legacy int64
	err = s.DB.QueryRowContext(ctx, `INSERT INTO finding_reports(kind,period_start,period_end,filter,current_payload,previous_payload) VALUES('weekly','2000-01-01','2000-01-07',$1,'{"summary":{"new":77}}','{"summary":{"new":66}}') RETURNING id`, jsonValue(map[string]string{"business_line": uuid.NewString()})).Scan(&legacy)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Refresh(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	legacyReport, err := (&db.DB{DB: s.DB}).GetFindingReport(ctx, legacy)
	if err != nil || legacyReport.AttributionVersion != 0 || legacyReport.Current.Summary.New != 77 || legacyReport.Previous.Summary.New != 66 {
		t.Fatalf("legacy payload altered %+v %v", legacyReport, err)
	}
}

func TestDepartmentQueueUsesOnlyItsHeadChannelsAndDoesNotSend(t *testing.T) {
	s, ctx := testStore(t)
	dep := testDepartment(t, s, ctx)
	other := testDepartment(t, s, ctx)
	create := func(kind string, data any) org.ChangeResult {
		t.Helper()
		raw, _ := json.Marshal(data)
		v, e := org.New(s.DB).Change(ctx, org.Command{Kind: kind, Action: "save", Data: raw, Scope: "all_open", IdempotencyKey: uuid.NewString()})
		if e != nil {
			t.Fatal(e)
		}
		return v
	}
	person := create("person", org.Person{Name: "部门测试负责人", Email: uuid.NewString() + "@example.invalid", DepartmentID: &dep, Active: true})
	var ownChannel, otherChannel int64
	for i, p := range []*int64{&ownChannel, &otherChannel} {
		if err := s.DB.QueryRowContext(ctx, `INSERT INTO notification_channels(name,kind,enabled,config,mode) VALUES($1,'email',true,'{"host":"smtp.example.invalid","port":465,"from":"fixture@example.invalid","to":["unused@example.invalid"],"tls":true}','realtime') RETURNING id`, uuid.NewString()).Scan(p); err != nil {
			t.Fatal(err)
		}
		_ = i
	}
	for _, pair := range []struct {
		dep, channel int64
		head         *int64
	}{{dep, ownChannel, &person.EntityID}, {other, otherChannel, nil}} {
		directory, e := org.New(s.DB).Directory(ctx)
		if e != nil {
			t.Fatal(e)
		}
		for _, d := range directory.Departments {
			if d.ID == pair.dep {
				d.ChannelIDs = []int64{pair.channel}
				d.HeadID = pair.head
				raw, _ := json.Marshal(d)
				if _, e = org.New(s.DB).Change(ctx, org.Command{Kind: "department", Action: "save", ID: d.ID, Revision: d.Revision, Data: raw, Scope: "all_open", ConfirmImpact: true, IdempotencyKey: uuid.NewString()}); e != nil {
					t.Fatal(e)
				}
			}
		}
	}
	now := time.Now()
	if err := s.Refresh(ctx, now); err != nil {
		t.Fatal(err)
	}
	id, err := s.StoreReport(ctx, "weekly", now, db.FindingMetricsFilter{DepartmentID: &dep})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.QueueReport(ctx, id, notify.ReportContent{ID: id, Title: "fixture report"}); err != nil {
		t.Fatal(err)
	}
	if err = s.QueueReport(ctx, id, notify.ReportContent{ID: id, Title: "fixture report"}); err != nil {
		t.Fatal(err)
	}
	var count int
	var channel, department int64
	var state string
	if err = s.DB.QueryRowContext(ctx, `SELECT count(*),min(d.channel_id),min((e.snapshot->'report'->>'department_id')::bigint),min(d.state) FROM finding_reports r JOIN notification_events e ON e.id=r.event_id JOIN notification_deliveries d ON d.event_id=e.id WHERE r.id=$1`, id).Scan(&count, &channel, &department, &state); err != nil || count != 1 || channel != ownChannel || department != dep || state != "pending" {
		t.Fatalf("routing wrong count=%d channel=%d dept=%d state=%s err=%v", count, channel, department, state, err)
	}
	otherID, err := s.StoreReport(ctx, "weekly", now, db.FindingMetricsFilter{DepartmentID: &other})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.QueueReport(ctx, otherID, notify.ReportContent{ID: otherID}); err != nil {
		t.Fatal(err)
	}
	var contextRaw []byte
	var eventID *int64
	if err = s.DB.QueryRowContext(ctx, `SELECT event_id,delivery_context FROM finding_reports WHERE id=$1`, otherID).Scan(&eventID, &contextRaw); err != nil {
		t.Fatal(err)
	}
	var route DeliveryContext
	if err = json.Unmarshal(contextRaw, &route); err != nil || eventID != nil || route.State != "unavailable" || route.HeadID != nil {
		t.Fatalf("missing head fell back to another department %+v event=%v err=%v", route, eventID, err)
	}
}

func TestApprovedBackdateCreatesNewGenerationWithoutChangingSavedReport(t *testing.T) {
	s, ctx := testStore(t)
	dep := testDepartment(t, s, ctx)
	now := time.Now()
	from, _, _, _ := db.FindingReportPeriod("weekly", now)
	assetTime := from.Add(time.Hour)
	findingTime := assetTime.Add(time.Hour)
	var asset, finding int64
	if err := s.DB.QueryRowContext(ctx, `INSERT INTO assets(type,domain,created_at) VALUES('subdomain',$1,$2) RETURNING id`, uuid.NewString()+".example.invalid", assetTime).Scan(&asset); err != nil {
		t.Fatal(err)
	}
	if err := s.DB.QueryRowContext(ctx, `INSERT INTO findings(asset_ids,severity,status,name,assignment_source,created_at) VALUES(jsonb_build_array($1::bigint),'critical','pending','backdate fixture','manual',$2) RETURNING id`, asset, findingTime).Scan(&finding); err != nil {
		t.Fatal(err)
	}
	if err := s.Refresh(ctx, now); err != nil {
		t.Fatal(err)
	}
	day := findingTime.In(db.FindingMetricsLocation).Format("2006-01-02")
	original, err := s.Metrics(ctx, day, day, db.FindingMetricsFilter{DepartmentID: &dep})
	if err != nil {
		t.Fatal(err)
	}
	saved, err := s.StoreReport(ctx, "weekly", now, db.FindingMetricsFilter{DepartmentID: &dep})
	if err != nil {
		t.Fatal(err)
	}
	var beforePayload string
	if err = s.DB.QueryRowContext(ctx, `SELECT md5(current_payload::text||previous_payload::text) FROM finding_reports WHERE id=$1`, saved).Scan(&beforePayload); err != nil {
		t.Fatal(err)
	}
	p, err := org.New(s.DB).PreviewAssignment(ctx, org.AssignmentRequest{Selection: org.AssignmentSelection{Mode: "ids", IDs: []int64{asset}, ExpectedCount: 1}, DepartmentID: dep, Scope: "all_open", BackdateFirstAssignment: true})
	if err != nil {
		t.Fatal(err)
	}
	assigned, err := org.New(s.DB).ConfirmAssignment(ctx, org.AssignmentConfirm{Token: p.Token, IdempotencyKey: uuid.NewString(), BackdateConfirmed: true})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Refresh(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	corrected, err := s.Metrics(ctx, day, day, db.FindingMetricsFilter{DepartmentID: &dep})
	if err != nil {
		t.Fatal(err)
	}
	if original.Summary.New != 0 || corrected.Summary.New != 1 || corrected.SnapshotIDs[0] == original.SnapshotIDs[0] || corrected.StockKnown {
		t.Fatalf("backdate generation before=%+v after=%+v", original, corrected)
	}
	if _, err = org.New(s.DB).UndoAssignment(ctx, assigned.OperationID, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	if err = s.Refresh(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	undone, err := s.Metrics(ctx, day, day, db.FindingMetricsFilter{DepartmentID: &dep})
	if err != nil || undone.Summary.New != 0 || undone.SnapshotIDs[0] == corrected.SnapshotIDs[0] {
		t.Fatalf("undo did not append restored generation %+v %v", undone, err)
	}
	if err = s.Refresh(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	var lastHead int64
	var generations int
	if err = s.DB.QueryRowContext(ctx, `SELECT h.snapshot_id,(SELECT count(*) FROM report.daily_snapshot WHERE day=h.day) FROM report.daily_snapshot_head h WHERE h.day=$1::date`, day).Scan(&lastHead, &generations); err != nil || lastHead != undone.SnapshotIDs[0] || generations != 3 {
		t.Fatalf("processed corrections kept regenerating: head=%d generations=%d err=%v", lastHead, generations, err)
	}
	var afterPayload string
	if err = s.DB.QueryRowContext(ctx, `SELECT md5(current_payload::text||previous_payload::text) FROM finding_reports WHERE id=$1`, saved).Scan(&afterPayload); err != nil || afterPayload != beforePayload {
		t.Fatalf("approved correction changed saved report: %v", err)
	}
}

func TestLegacyUnknownAttributionNeverUsesMatchingLegacyNames(t *testing.T) {
	s, ctx := testStore(t)
	dep := testDepartment(t, s, ctx)
	now := time.Now()
	at := date(now).Add(time.Hour)
	if at.After(now) {
		at = date(now)
	}
	testEvent(t, s, ctx, 1400000+dep, "discovered", "pending", at, &dep, 0, `{"severity":"high","business_line":"报表业务线","assignee":"同名负责人","business_line_id":1,"assignee_id":1}`)
	if err := s.Refresh(ctx, now); err != nil {
		t.Fatal(err)
	}
	day := at.Format("2006-01-02")
	filtered, err := s.Metrics(ctx, day, day, db.FindingMetricsFilter{DepartmentID: &dep})
	if err != nil || filtered.Summary.New != 0 {
		t.Fatalf("legacy IDs promoted into org attribution: %+v %v", filtered, err)
	}
	zero := int64(0)
	unknown, err := s.Metrics(ctx, day, day, db.FindingMetricsFilter{DepartmentID: &zero})
	if err != nil || unknown.DataQuality.UnknownEvents == 0 || unknown.Summary.New == 0 {
		t.Fatalf("unknown fact disappeared: %+v %v", unknown, err)
	}
}

func TestKnownRetestFactsSurviveUnknownAttributionAndMissingStockDays(t *testing.T) {
	at := time.Date(2026, 11, 25, 8, 0, 0, 0, db.FindingMetricsLocation)
	dep := int64(1)
	d := &daily{Day: "2026-11-25", ID: 1, Buckets: map[string]*bucket{}, QualityKnown: true, StockKnown: true}
	states := map[int64]*state{}
	for _, finding := range []int64{1, 2} {
		quality := "recorded"
		var department *int64 = &dep
		if finding == 2 {
			quality = "unknown"
			department = nil
		}
		for _, e := range []event{{FindingID: finding, Kind: "discovered", NewStatus: "pending", At: at, DepartmentID: department, Version: 1, Quality: quality, Metadata: json.RawMessage(`{"severity":"high"}`)},
			{FindingID: finding, Kind: "retest_completed", At: at.Add(time.Hour), DepartmentID: department, Version: 1, Quality: quality, Metadata: json.RawMessage(`{"status":"completed","verdict":"fixed"}`)}} {
			if err := replay(states, e, d); err != nil {
				t.Fatal(err)
			}
		}
	}
	r := summarize("2026-11-24", "2026-11-25", map[string]*daily{d.Day: d}, db.FindingMetricsFilter{}, labels{"department": {1: {Name: "部门", Active: true}}, "business_line": {}, "assignee": {}})
	if !r.QualityKnown || r.Summary.FirstRetestTotal != 2 || r.Summary.FirstRetestPassed != 2 || r.Summary.VerifiedFixes != 2 || r.DataQuality.UnknownEvents != 2 || r.DataQuality.MissingSnapshotDays != 1 {
		t.Fatalf("unknown attribution or stock hid known retest facts %+v", r)
	}
	partial := event{FindingID: 3, Kind: "retest_completed", At: at, Version: 1, Quality: "unknown", Metadata: json.RawMessage(`{"status":"completed","verdict":"fixed"}`)}
	if err := replay(states, partial, d); err != nil {
		t.Fatal(err)
	}
	r = summarize(d.Day, d.Day, map[string]*daily{d.Day: d}, db.FindingMetricsFilter{}, labels{"department": {1: {Name: "部门", Active: true}}, "business_line": {}, "assignee": {}})
	if r.QualityKnown || r.Summary.FirstRetestTotal != 2 || r.Summary.VerifiedFixes != 2 || r.Summary.RetestPassed != 3 {
		t.Fatalf("incomplete chain polluted first-fix denominator %+v", r)
	}
}

func TestStockAgeUsesSevenAndThirtyDayBoundaries(t *testing.T) {
	s, ctx := testStore(t)
	at := time.Now()
	states := map[int64]*state{}
	for i, age := range []time.Duration{7*24*time.Hour - time.Second, 7 * 24 * time.Hour, 30*24*time.Hour - time.Second, 30 * 24 * time.Hour} {
		id := int64(99000000 + i)
		states[id] = &state{ID: id, Severity: "high", Status: "pending", Discovered: at.Add(-age)}
	}
	d := &daily{Buckets: map[string]*bucket{}, QualityKnown: true, StockKnown: true}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err = fillStock(ctx, tx, states, d, at); err != nil {
		t.Fatal(err)
	}
	counts := [3]int{}
	for _, b := range d.Buckets {
		for i, n := range b.Ages {
			counts[i] += n
		}
	}
	if counts != [3]int{1, 2, 1} {
		t.Fatalf("7/30 day buckets %v want [1 2 1]", counts)
	}
}
