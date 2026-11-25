package report

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/artex/internal/org"
	"github.com/google/uuid"
)

type backfillFixture struct {
	Operation                                   string
	A, B, LineA, LineB, X, Y, Asset, OtherAsset int64
	Base, Mid                                   time.Time
}

func newBackfillFixture(t *testing.T, s *Store, ctx context.Context) backfillFixture {
	t.Helper()
	// Reset only this disposable fixture's migration checkpoint; production never
	// resets a completed run. Other organization facts remain available for regression.
	if _, err := s.DB.ExecContext(ctx, `UPDATE org.sync_state SET history_backfill_cursor=0,history_backfill_upper_id=0,backfill_run_id=NULL,backfill_version=0,backfill_started_at=NULL,backfill_finished_at=NULL,backfill_summary='{}' WHERE singleton`); err != nil {
		t.Fatal(err)
	}
	start, _, _, _ := db.FindingReportPeriod("weekly", time.Now())
	f := backfillFixture{Operation: uuid.NewString(), Base: start, Mid: start.Add(3 * 24 * time.Hour)}
	if _, err := s.DB.ExecContext(ctx, `INSERT INTO org.change_operation(id,idempotency_key,request_hash,kind,actor_type,actor_id,actor_label,state,effective_at,finished_at)
 VALUES($1,$1,repeat('0',64),'backfill_test_evidence','system','fixture','isolated history fixture','succeeded',$2,$2)`, f.Operation, f.Base); err != nil {
		t.Fatal(err)
	}
	for _, p := range []*int64{&f.A, &f.B} {
		if err := s.DB.QueryRowContext(ctx, `INSERT INTO org.department(name,code,created_by) VALUES($1,$2,'test fixture') RETURNING id`, "历史部门-"+uuid.NewString(), uuid.NewString()).Scan(p); err != nil {
			t.Fatal(err)
		}
	}
	for _, p := range []struct {
		ID  *int64
		Dep int64
	}{{&f.LineA, f.A}, {&f.LineB, f.B}} {
		if err := s.DB.QueryRowContext(ctx, `INSERT INTO org.business_line(department_id,name,code,created_by) VALUES($1,$2,$3,'test fixture') RETURNING id`, p.Dep, "历史业务线-"+uuid.NewString(), uuid.NewString()).Scan(p.ID); err != nil {
			t.Fatal(err)
		}
	}
	for _, p := range []struct {
		ID  *int64
		Dep int64
	}{{&f.X, f.A}, {&f.Y, f.B}} {
		if err := s.DB.QueryRowContext(ctx, `INSERT INTO org.app_user(name,email,department_id,created_by) VALUES($1,$2,$3,'test fixture') RETURNING id`, "历史负责人-"+uuid.NewString(), uuid.NewString()+"@example.invalid", p.Dep).Scan(p.ID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.DB.ExecContext(ctx, `INSERT INTO org.app_user_state_history(user_id,department_id,active,valid_from,valid_to,operation_id,created_by)
 VALUES($1,$3,true,$5,$6,$7,'test fixture'),($1,$3,false,$6,NULL,$7,'test fixture'),($2,$4,true,$5,NULL,$7,'test fixture')`, f.X, f.Y, f.A, f.B, f.Base, f.Mid, f.Operation); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.ExecContext(ctx, `UPDATE org.app_user SET active=false WHERE id=$1`, f.X); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.ExecContext(ctx, `INSERT INTO org.business_line_owner_history(business_line_id,owner_id,valid_from,operation_id,created_by)
 VALUES($1,$3,$5,$6,'test fixture'),($2,$4,$5,$6,'test fixture')`, f.LineA, f.LineB, f.X, f.Y, f.Base, f.Operation); err != nil {
		t.Fatal(err)
	}
	for _, p := range []*int64{&f.Asset, &f.OtherAsset} {
		if err := s.DB.QueryRowContext(ctx, `INSERT INTO assets(type,domain,created_at) VALUES('subdomain',$1,$2) RETURNING id`, uuid.NewString()+".example.invalid", f.Base).Scan(p); err != nil {
			t.Fatal(err)
		}
		if _, err := s.DB.ExecContext(ctx, `INSERT INTO org.asset_registry(asset_id,asset_type,identifier,first_seen_at,source_updated_at) VALUES($1,'subdomain',$2,$3,$3)`, *p, fmt.Sprint("historical-", *p), f.Base); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.DB.ExecContext(ctx, `INSERT INTO org.asset_ownership(asset_id,department_id,business_line_id,source,valid_from,valid_to,operation_id,created_by)
 VALUES($1,$3,$5,'manual',$7,$8,$9,'test fixture'),($1,$4,$6,'manual',$8,NULL,$9,'test fixture'),($2,$3,$5,'manual',$7,NULL,$9,'test fixture')`, f.Asset, f.OtherAsset, f.A, f.B, f.LineA, f.LineB, f.Base, f.Mid, f.Operation); err != nil {
		t.Fatal(err)
	}
	// These historical SLA ranges are explicit fixture evidence, not inferred from today's settings.
	if _, err := s.DB.ExecContext(ctx, `INSERT INTO org.sla_policy_history(business_line_id,days,valid_from,operation_id,created_by)
 VALUES($1,'{"critical":3,"high":7,"medium":30,"low":90}',$3,$4,'test fixture'),($2,'{"critical":5,"high":10,"medium":30,"low":90}',$3,$4,'test fixture')`, f.LineA, f.LineB, f.Base, f.Operation); err != nil {
		t.Fatal(err)
	}
	return f
}

func backfillFindingID(t *testing.T, s *Store, ctx context.Context) int64 {
	t.Helper()
	var id int64
	if err := s.DB.QueryRowContext(ctx, `SELECT nextval('findings_id_seq')`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}
func backfillBinding(t *testing.T, s *Store, ctx context.Context, f backfillFixture, finding, asset int64) {
	t.Helper()
	if _, err := s.DB.ExecContext(ctx, `INSERT INTO org.finding_asset_history(finding_id,asset_id,valid_from,operation_id,created_by) VALUES($1,$2,$3,$4,'retained historical evidence')`, finding, asset, f.Base, f.Operation); err != nil {
		t.Fatal(err)
	}
}
func backfillEvent(t *testing.T, s *Store, ctx context.Context, finding int64, kind, status string, at time.Time, asset *int64, metadata string) int64 {
	t.Helper()
	var id int64
	if err := s.DB.QueryRowContext(ctx, `INSERT INTO finding_history(finding_id,kind,new_status,actor,occurred_at,recorded_at,source,metadata,asset_id,attribution_version,attribution_quality)
 VALUES($1,$2,$3,'legacy fixture',$4,$4,'backfill',$5,$6,0,'unknown') RETURNING id`, finding, kind, status, at, metadata, asset).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}
func runBackfillFor(t *testing.T, s *Store, ctx context.Context, ids []int64, batch int) {
	t.Helper()
	for attempt := 0; attempt < 50; attempt++ {
		var pending int
		if err := s.DB.QueryRowContext(ctx, `SELECT count(*) FROM finding_history WHERE id=ANY($1::bigint[]) AND attribution_version=0`, ids).Scan(&pending); err != nil {
			t.Fatal(err)
		}
		if pending == 0 {
			return
		}
		if _, err := org.New(s.DB).BackfillHistoryBatch(ctx, batch); err != nil {
			t.Fatal(err)
		}
	}
	t.Fatal("bounded backfill did not process fixture records")
}

func backfillRead(t *testing.T, s *Store, ctx context.Context, id int64) event {
	t.Helper()
	var e event
	if err := s.DB.QueryRowContext(ctx, `SELECT asset_id,department_id,business_line_id,owner_id,attribution_version,attribution_quality,sla_days FROM finding_history WHERE id=$1`, id).Scan(&e.AssetID, &e.DepartmentID, &e.LineID, &e.OwnerID, &e.Version, &e.Quality, &e.SLADays); err != nil {
		t.Fatal(err)
	}
	return e
}

func TestHistoryBackfillUsesEvidenceAtEachLifecycleEventAndPreservesV1(t *testing.T) {
	s, ctx := testStore(t)
	f := newBackfillFixture(t, s, ctx)
	finding := backfillFindingID(t, s, ctx)
	backfillBinding(t, s, ctx, f, finding, f.Asset)
	ids := []int64{}
	times := []time.Time{f.Base.Add(time.Hour), f.Base.Add(2 * time.Hour), f.Mid.Add(time.Hour), f.Mid.Add(2 * time.Hour), f.Mid.Add(3 * time.Hour), f.Mid.Add(4 * time.Hour), f.Mid.Add(5 * time.Hour), f.Mid.Add(6 * time.Hour)}
	kinds := []string{"discovered", "dispatched", "developer_fixed", "retest_requested", "retest_completed", "status_changed", "status_changed", "status_changed"}
	statuses := []string{"pending", "", "", "", "", "fixed", "confirmed", "fixed"}
	for i, kind := range kinds {
		meta := `{"severity":"high","vulnclass":"history fixture"}`
		if kind == "retest_completed" {
			meta = `{"status":"completed","verdict":"reproduced","requested_at":"` + times[3].Format(time.RFC3339) + `"}`
		}
		ids = append(ids, backfillEvent(t, s, ctx, finding, kind, statuses[i], times[i], nil, meta))
	}
	var original string
	if err := s.DB.QueryRowContext(ctx, `SELECT md5(jsonb_agg(jsonb_build_object('id',id,'kind',kind,'old',old_status,'new',new_status,'at',occurred_at,'recorded',recorded_at,'source',source,'metadata',metadata) ORDER BY id)::text) FROM finding_history WHERE id=ANY($1::bigint[])`, ids).Scan(&original); err != nil {
		t.Fatal(err)
	}
	known := testEvent(t, s, ctx, finding+500000, "discovered", "pending", f.Base, &f.B, 1, `{"severity":"low","sentinel":"v1 must remain byte-identical"}`)
	var knownHash string
	if err := s.DB.QueryRowContext(ctx, `SELECT md5(to_jsonb(h)::text) FROM finding_history h WHERE id=$1`, known).Scan(&knownHash); err != nil {
		t.Fatal(err)
	}
	// Archiving the hot asset does not destroy retained historical identity.
	if _, err := s.DB.ExecContext(ctx, `DELETE FROM assets WHERE id=$1`, f.Asset); err != nil {
		t.Fatal(err)
	}
	runBackfillFor(t, s, ctx, ids, 2)
	for i, id := range ids {
		e := backfillRead(t, s, ctx, id)
		dep, line, owner, sla := f.A, f.LineA, f.X, 7
		if !times[i].Before(f.Mid) {
			dep, line, owner, sla = f.B, f.LineB, f.Y, 10
		}
		if e.Version != 1 || e.Quality == "unknown" || e.AssetID == nil || *e.AssetID != f.Asset || e.DepartmentID == nil || *e.DepartmentID != dep || e.LineID == nil || *e.LineID != line || e.OwnerID == nil || *e.OwnerID != owner || e.SLADays == nil || *e.SLADays != sla {
			t.Fatalf("event %s ignored event-time evidence: %+v want dep=%d line=%d owner=%d SLA=%d", kinds[i], e, dep, line, owner, sla)
		}
	}
	var after, knownAfter string
	if err := s.DB.QueryRowContext(ctx, `SELECT md5(jsonb_agg(jsonb_build_object('id',id,'kind',kind,'old',old_status,'new',new_status,'at',occurred_at,'recorded',recorded_at,'source',source,'metadata',metadata) ORDER BY id)::text) FROM finding_history WHERE id=ANY($1::bigint[])`, ids).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if err := s.DB.QueryRowContext(ctx, `SELECT md5(to_jsonb(h)::text) FROM finding_history h WHERE id=$1`, known).Scan(&knownAfter); err != nil {
		t.Fatal(err)
	}
	if original != after || knownHash != knownAfter {
		t.Fatal("backfill altered original evidence or an already-frozen v1 event")
	}
}

func TestHistoryBackfillNeverInfersOldOwnershipFromCurrentAssociationsOrNames(t *testing.T) {
	s, ctx := testStore(t)
	f := newBackfillFixture(t, s, ctx)
	// A retained global SLA is usable even without organization evidence. Its
	// historical value deliberately differs from both line overrides and today's
	// defaults; neither may substitute for the actual policy at this timestamp.
	if _, err := s.DB.ExecContext(ctx, `INSERT INTO org.sla_policy_history(days,valid_from,valid_to,operation_id,created_by)
 VALUES('{"critical":3,"high":9,"medium":30,"low":90}',$1,$2,$3,'retained global policy')`, f.Base, f.Mid, f.Operation); err != nil {
		t.Fatal(err)
	}
	ids := []int64{}
	// Simulate pre-upgrade findings: the only current asset association was saved
	// later, and there is no historical primary-asset binding for its discovery.
	for _, assets := range [][]int64{{f.Asset}, {f.Asset, f.OtherAsset}} {
		var finding int64
		if err := s.DB.QueryRowContext(ctx, `INSERT INTO findings(asset_ids,severity,name,created_at,assignment_source) VALUES('[]','high','legacy current projection',$1,'manual') RETURNING id`, f.Base.Add(time.Hour)).Scan(&finding); err != nil {
			t.Fatal(err)
		}
		var history int64
		if err := s.DB.QueryRowContext(ctx, `UPDATE finding_history SET attribution_version=0,attribution_quality='unknown',source='backfill' WHERE finding_id=$1 AND kind='discovered' RETURNING id`, finding).Scan(&history); err != nil {
			t.Fatal(err)
		}
		var primary any
		if len(assets) == 1 {
			primary = assets[0]
		}
		if _, err := s.DB.ExecContext(ctx, `UPDATE findings SET asset_ids=$2,asset_id=$3 WHERE id=$1`, finding, jsonValue(assets), primary); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, history)
	}
	nameOnly := backfillFindingID(t, s, ctx)
	metadata := fmt.Sprintf(`{"severity":"high","business_line_id":%d,"assignee_id":%d,"business_line":"same name","assignee":"same owner"}`, f.LineB, f.X)
	ids = append(ids, backfillEvent(t, s, ctx, nameOnly, "discovered", "pending", f.Base.Add(time.Hour), nil, metadata))
	conflict := backfillFindingID(t, s, ctx)
	backfillBinding(t, s, ctx, f, conflict, f.Asset)
	ids = append(ids, backfillEvent(t, s, ctx, conflict, "discovered", "pending", f.Base.Add(time.Hour), &f.OtherAsset, `{"severity":"high"}`))
	missingDiscovery := backfillFindingID(t, s, ctx)
	backfillBinding(t, s, ctx, f, missingDiscovery, f.Asset)
	ids = append(ids, backfillEvent(t, s, ctx, missingDiscovery, "developer_fixed", "", f.Base.Add(time.Hour), nil, `{"severity":"high"}`))
	missingOwnership := backfillFindingID(t, s, ctx)
	backfillBinding(t, s, ctx, f, missingOwnership, f.Asset)
	ids = append(ids, backfillEvent(t, s, ctx, missingOwnership, "discovered", "pending", f.Base.Add(-time.Hour), &f.Asset, `{"severity":"high"}`))
	runBackfillFor(t, s, ctx, ids, 2)
	for i, id := range ids {
		e := backfillRead(t, s, ctx, id)
		if e.Version != 1 || e.Quality != "unknown" || e.DepartmentID != nil || e.LineID != nil || e.OwnerID != nil {
			t.Fatalf("unproven event %d received invented ownership %+v", id, e)
		}
		if i < len(ids)-1 && (e.SLADays == nil || *e.SLADays != 9) {
			t.Fatalf("unknown event %d lost its known historical global SLA: %+v", id, e)
		}
		if i == len(ids)-1 && e.SLADays != nil {
			t.Fatalf("event predating every SLA policy received a current-policy guess: %+v", e)
		}
	}
}

func TestHistoryBackfillPreviewFrozenUpperBoundAndResumeAreIdempotent(t *testing.T) {
	s, ctx := testStore(t)
	f := newBackfillFixture(t, s, ctx)
	finding := backfillFindingID(t, s, ctx)
	backfillBinding(t, s, ctx, f, finding, f.Asset)
	ids := []int64{}
	for i := 0; i < 5; i++ {
		kind := "dispatched"
		if i == 0 {
			kind = "discovered"
		}
		ids = append(ids, backfillEvent(t, s, ctx, finding, kind, "pending", f.Base.Add(time.Duration(i+1)*time.Hour), nil, `{"severity":"high"}`))
	}
	snapshot := func() string {
		t.Helper()
		var hash string
		if err := s.DB.QueryRowContext(ctx, `SELECT md5(jsonb_build_array((SELECT jsonb_agg(h ORDER BY id) FROM finding_history h),(SELECT jsonb_agg(x) FROM org.sync_state x),(SELECT jsonb_agg(x ORDER BY id) FROM org.change_operation x),(SELECT jsonb_agg(x ORDER BY id) FROM org.audit_log x),(SELECT jsonb_agg(x ORDER BY asset_id) FROM org.asset_registry x))::text)`).Scan(&hash); err != nil {
			t.Fatal(err)
		}
		return hash
	}
	before := snapshot()
	if _, err := org.New(s.DB).PreviewHistoryBackfill(ctx); err != nil {
		t.Fatal(err)
	}
	if before != snapshot() {
		t.Fatal("read-only backfill preview wrote facts/checkpoints/audit")
	}
	if _, err := org.New(s.DB).BackfillHistoryBatch(ctx, 2); err != nil {
		t.Fatal(err)
	}
	var changed int
	if err := s.DB.QueryRowContext(ctx, `SELECT count(*) FROM finding_history WHERE id=ANY($1::bigint[]) AND attribution_version=1`, ids).Scan(&changed); err != nil || changed != 2 {
		t.Fatalf("first bounded batch wrote %d fixture rows: %v", changed, err)
	}
	late := backfillEvent(t, s, ctx, finding, "developer_fixed", "", f.Base.Add(8*time.Hour), nil, `{"severity":"high"}`)
	runBackfillFor(t, s, ctx, ids, 2)
	if _, err := org.New(s.DB).BackfillHistoryBatch(ctx, 2); err != nil {
		t.Fatal(err)
	}
	if e := backfillRead(t, s, ctx, late); e.Version != 0 {
		t.Fatal("new v0 row beyond frozen run upper bound was silently included")
	}
	done := snapshot()
	for i := 0; i < 3; i++ {
		if _, err := org.New(s.DB).BackfillHistoryBatch(ctx, 2); err != nil {
			t.Fatal(err)
		}
	}
	if done != snapshot() {
		t.Fatal("completed rerun created extra history, operations or audit")
	}
}

func TestHistoryBackfillDoesNotRewriteSealedDailyOrSavedReports(t *testing.T) {
	s, ctx := testStore(t)
	f := newBackfillFixture(t, s, ctx)
	finding := backfillFindingID(t, s, ctx)
	backfillBinding(t, s, ctx, f, finding, f.Asset)
	ids := []int64{backfillEvent(t, s, ctx, finding, "discovered", "pending", f.Base.Add(time.Hour), nil, `{"severity":"high"}`), backfillEvent(t, s, ctx, finding, "status_changed", "fixed", f.Mid.Add(time.Hour), nil, `{}`)}
	if err := s.Refresh(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	saved, err := s.StoreReport(ctx, "weekly", time.Now(), db.FindingMetricsFilter{DepartmentID: &f.A})
	if err != nil {
		t.Fatal(err)
	}
	var legacy int64
	if err = s.DB.QueryRowContext(ctx, `INSERT INTO finding_reports(kind,period_start,period_end,filter,current_payload,previous_payload) VALUES('monthly','2001-01-01','2001-01-31',$1,'{"summary":{"new":77}}','{"summary":{"new":66}}') RETURNING id`, jsonValue(map[string]string{"legacy": uuid.NewString()})).Scan(&legacy); err != nil {
		t.Fatal(err)
	}
	snapshot := func() string {
		t.Helper()
		var hash string
		if err := s.DB.QueryRowContext(ctx, `SELECT md5(jsonb_build_array(
 (SELECT jsonb_agg(h ORDER BY day) FROM report.daily_snapshot_head h WHERE sealed_at IS NOT NULL),
 (SELECT jsonb_agg(v ORDER BY id) FROM report.daily_vuln_stat v WHERE snapshot_id IN (SELECT snapshot_id FROM report.daily_snapshot_head WHERE sealed_at IS NOT NULL)),
 (SELECT jsonb_agg(jsonb_build_object('id',id,'filter',filter,'current',current_payload,'previous',previous_payload,'snapshots',source_snapshot_ids) ORDER BY id) FROM finding_reports WHERE id=ANY($1::bigint[])))::text)`, []int64{saved, legacy}).Scan(&hash); err != nil {
			t.Fatal(err)
		}
		return hash
	}
	before := snapshot()
	runBackfillFor(t, s, ctx, ids, 1)
	if before != snapshot() {
		t.Fatal("backfill itself rewrote frozen daily or report payload")
	}
	if err = s.Refresh(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	if before != snapshot() {
		t.Fatal("normal refresh after backfill rewrote sealed history/report")
	}
	for _, id := range ids {
		e := backfillRead(t, s, ctx, id)
		if e.Version != 1 || e.DepartmentID == nil {
			t.Fatalf("test did not actually backfill ownership: %+v", e)
		}
	}
	var corrections int
	if err = s.DB.QueryRowContext(ctx, `SELECT count(*) FROM org.finding_event_attribution_correction WHERE history_id=ANY($1::bigint[])`, ids).Scan(&corrections); err != nil || corrections != 0 {
		t.Fatalf("ordinary backfill invented approved historical corrections: %d %v", corrections, err)
	}
	old, err := (&db.DB{DB: s.DB}).GetFindingReport(ctx, legacy)
	if err != nil || old.Current.Summary.New != 77 || old.Previous.Summary.New != 66 {
		t.Fatalf("legacy report changed %+v %v", old, err)
	}
}

func TestHistoryBackfillPrepareBindsOnlyNowAndRepeatsWithoutChanges(t *testing.T) {
	s, ctx := testStore(t)
	f := newBackfillFixture(t, s, ctx)
	var finding, history int64
	if err := s.DB.QueryRowContext(ctx, `INSERT INTO findings(asset_ids,severity,created_at,assignment_source) VALUES('[]','high',$1,'manual') RETURNING id`, f.Base.Add(time.Hour)).Scan(&finding); err != nil {
		t.Fatal(err)
	}
	if err := s.DB.QueryRowContext(ctx, `UPDATE finding_history SET attribution_version=0,attribution_quality='unknown',source='backfill' WHERE finding_id=$1 AND kind='discovered' RETURNING id`, finding).Scan(&history); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.ExecContext(ctx, `UPDATE findings SET asset_ids=jsonb_build_array($2::bigint) WHERE id=$1`, finding, f.Asset); err != nil {
		t.Fatal(err)
	}
	prepared, err := org.New(s.DB).PrepareHistoryBackfillAssets(ctx, 200)
	if err != nil || prepared.Bound < 1 {
		t.Fatalf("prepare %+v %v", prepared, err)
	}
	var from time.Time
	var asset int64
	if err = s.DB.QueryRowContext(ctx, `SELECT asset_id,valid_from FROM org.finding_asset_history WHERE finding_id=$1 AND valid_to IS NULL AND voided_at IS NULL`, finding).Scan(&asset, &from); err != nil || asset != f.Asset || !from.Equal(prepared.Effective) || !from.After(f.Base.Add(time.Hour)) {
		t.Fatalf("current binding backdated: asset=%d from=%v result=%+v err=%v", asset, from, prepared, err)
	}
	var dep, owner *int64
	if err = s.DB.QueryRowContext(ctx, `SELECT department_id,owner_id FROM org.resolve_finding_owner($1,clock_timestamp())`, finding).Scan(&dep, &owner); err != nil || dep == nil || *dep != f.B || owner == nil || *owner != f.Y {
		t.Fatalf("current binding did not resolve current ownership dep=%v owner=%v err=%v", dep, owner, err)
	}
	runBackfillFor(t, s, ctx, []int64{history}, 200)
	old := backfillRead(t, s, ctx, history)
	if old.Quality != "unknown" || old.DepartmentID != nil || old.LineID != nil || old.OwnerID != nil {
		t.Fatalf("today's unique association was applied retroactively: %+v", old)
	}
	var beforeRows, beforeEvents int
	if err = s.DB.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM org.finding_asset_history WHERE finding_id=$1),(SELECT count(*) FROM finding_history WHERE finding_id=$1)`, finding).Scan(&beforeRows, &beforeEvents); err != nil {
		t.Fatal(err)
	}
	again, err := org.New(s.DB).PrepareHistoryBackfillAssets(ctx, 200)
	if err != nil || again.Bound != 0 || again.Registered != 0 {
		t.Fatalf("prepare repeat %+v %v", again, err)
	}
	var afterRows, afterEvents int
	if err = s.DB.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM org.finding_asset_history WHERE finding_id=$1),(SELECT count(*) FROM finding_history WHERE finding_id=$1)`, finding).Scan(&afterRows, &afterEvents); err != nil || beforeRows != afterRows || beforeEvents != afterEvents {
		t.Fatalf("prepare duplicated bindings/journal: rows %d -> %d events %d -> %d err=%v", beforeRows, afterRows, beforeEvents, afterEvents, err)
	}
}

func TestHistoryBackfillFailedBatchRollsBackRowsAndCursor(t *testing.T) {
	s, ctx := testStore(t)
	f := newBackfillFixture(t, s, ctx)
	finding := backfillFindingID(t, s, ctx)
	backfillBinding(t, s, ctx, f, finding, f.Asset)
	ids := []int64{backfillEvent(t, s, ctx, finding, "discovered", "pending", f.Base.Add(time.Hour), nil, `{"severity":"high"}`), backfillEvent(t, s, ctx, finding, "dispatched", "", f.Base.Add(2*time.Hour), nil, `{}`)}
	function := "test_backfill_fault_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := s.DB.ExecContext(ctx, fmt.Sprintf(`CREATE FUNCTION org.%s() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN IF NEW.id=%d AND OLD.attribution_version=0 AND NEW.attribution_version>0 THEN RAISE EXCEPTION 'isolated backfill failure'; END IF; RETURN NEW; END$$;
 CREATE TRIGGER %s BEFORE UPDATE ON finding_history FOR EACH ROW EXECUTE FUNCTION org.%s()`, function, ids[1], function, function)); err != nil {
		t.Fatal(err)
	}
	remove := func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := s.DB.ExecContext(cleanup, fmt.Sprintf(`DROP TRIGGER IF EXISTS %s ON finding_history;DROP FUNCTION IF EXISTS org.%s()`, function, function)); err != nil {
			t.Errorf("remove test-only fault trigger: %v", err)
		}
	}
	t.Cleanup(remove)
	if _, err := org.New(s.DB).BackfillHistoryBatch(ctx, 2); err == nil {
		t.Fatal("injected batch write failure was hidden")
	}
	for _, id := range ids {
		if e := backfillRead(t, s, ctx, id); e.Version != 0 {
			t.Fatalf("failed batch partially committed event %d", id)
		}
	}
	var cursor int64
	if err := s.DB.QueryRowContext(ctx, `SELECT history_backfill_cursor FROM org.sync_state WHERE singleton`).Scan(&cursor); err != nil || cursor != 0 {
		t.Fatalf("failed batch advanced checkpoint %d: %v", cursor, err)
	}
	remove()
	runBackfillFor(t, s, ctx, ids, 2)
}
