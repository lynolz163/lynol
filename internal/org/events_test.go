package org

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
)

type eventFixture struct {
	dep, person, line, asset, finding int64
	born                              time.Time
}

func newEventFixture(t *testing.T, s *Store, ctx context.Context) eventFixture {
	t.Helper()
	dep := testDepartment(t, s, ctx)
	p := testPerson(t, s, ctx, dep.EntityID, "快照负责人", uuid.NewString()+"@example.invalid")
	line := testChange(t, s, ctx, "business_line", 0, 0, BusinessLine{Name: "事件-" + uuid.NewString(), Code: uuid.NewString(), DepartmentID: dep.EntityID, PrimaryID: &p.EntityID, Active: true, SLA: map[string]int{"critical": 2, "high": 4, "medium": 20, "low": 60}})
	a := testAsset(t, s, ctx, dep.EntityID, line.EntityID, line)
	v := eventFixture{dep: dep.EntityID, person: p.EntityID, line: line.EntityID, asset: a}
	if err := s.DB.QueryRowContext(ctx, `INSERT INTO public.findings(name,severity,asset_ids) VALUES('事件快照','high',jsonb_build_array($1::bigint)) RETURNING id,created_at`, a).Scan(&v.finding, &v.born); err != nil {
		t.Fatal(err)
	}
	return v
}
func readEventIDs(t *testing.T, s *Store, ctx context.Context, finding int64, kind string) (asset, dep, owner sql.NullInt64, quality string, sla sql.NullInt64) {
	t.Helper()
	if err := s.DB.QueryRowContext(ctx, `SELECT asset_id,department_id,owner_id,attribution_quality,sla_days FROM public.finding_history WHERE finding_id=$1 AND kind=$2 ORDER BY id DESC LIMIT 1`, finding, kind).Scan(&asset, &dep, &owner, &quality, &sla); err != nil {
		t.Fatal(err)
	}
	return
}
func TestEventFirstDiscoveredSeesInitialBindingAndSLA(t *testing.T) {
	s, ctx := testOrgStore(t)
	v := newEventFixture(t, s, ctx)
	a, d, o, q, sla := readEventIDs(t, s, ctx, v.finding, "discovered")
	if !a.Valid || a.Int64 != v.asset || d.Int64 != v.dep || o.Int64 != v.person || q != "recorded" || !sla.Valid || sla.Int64 != 4 {
		t.Fatalf("snapshot asset=%v dep=%v owner=%v quality=%s sla=%v", a, d, o, q, sla)
	}
	var primary, revision int64
	if err := s.DB.QueryRowContext(ctx, `SELECT f.asset_id,g.event_revision FROM public.findings f JOIN org.asset_registry g ON g.asset_id=f.asset_id WHERE f.id=$1`, v.finding).Scan(&primary, &revision); err != nil || primary != v.asset || revision != 1 {
		t.Fatalf("projection/counter %d/%d: %v", primary, revision, err)
	}
	var version int
	if err := s.DB.QueryRowContext(ctx, `SELECT attribution_version FROM public.finding_history WHERE finding_id=$1 AND kind='discovered'`, v.finding).Scan(&version); err != nil || version != 1 {
		t.Fatalf("version %d %v", version, err)
	}
}
func TestEventMultipleAssetsRequireExplicitProspectiveSelection(t *testing.T) {
	s, ctx := testOrgStore(t)
	v := newEventFixture(t, s, ctx)
	ids, _ := testAssignmentAssets(t, s, ctx, 1)
	var id int64
	if err := s.DB.QueryRowContext(ctx, `INSERT INTO public.findings(name,severity,asset_ids) VALUES('多资产','high',jsonb_build_array($1::bigint,$2::bigint)) RETURNING id`, v.asset, ids[0]).Scan(&id); err != nil {
		t.Fatal(err)
	}
	a, d, _, q, _ := readEventIDs(t, s, ctx, id, "discovered")
	if a.Valid || d.Valid || q != "unknown" {
		t.Fatalf("guessed multi asset: %v %v %s", a, d, q)
	}
	view, e := s.FindingAsset(ctx, id)
	if e != nil || !view.NeedsConfirmation || len(view.Candidates) != 2 {
		t.Fatalf("view %+v %v", view, e)
	}
	key := uuid.NewString()
	changed, e := s.SetFindingAsset(ctx, id, v.asset, key)
	if e != nil {
		t.Fatal(e)
	}
	replay, e := s.SetFindingAsset(ctx, id, v.asset, key)
	if e != nil || replay.OperationID != changed.OperationID {
		t.Fatalf("idempotency %+v %v", replay, e)
	}
	a, d, _, q, _ = readEventIDs(t, s, ctx, id, "discovered")
	if a.Valid || d.Valid || q != "unknown" {
		t.Fatal("selection rewrote discovery")
	}
	a, d, o, q, _ := readEventIDs(t, s, ctx, id, "primary_asset_changed")
	if a.Int64 != v.asset || d.Int64 != v.dep || o.Int64 != v.person || q != "recorded" {
		t.Fatalf("selected snapshot %v/%v/%v/%s", a, d, o, q)
	}
	unrelated, _ := testAssignmentAssets(t, s, ctx, 1)
	if _, e = s.SetFindingAsset(ctx, id, unrelated[0], uuid.NewString()); e == nil {
		t.Fatal("accepted unrelated primary asset")
	}
}
func TestEventDelayedMilestoneUsesHistoricAttributionAndOwner(t *testing.T) {
	s, ctx := testOrgStore(t)
	v := newEventFixture(t, s, ctx)
	p := testPerson(t, s, ctx, v.dep, "新负责人", uuid.NewString()+"@example.invalid")
	line := testLine(t, s, ctx, v.line)
	line.PrimaryID = &p.EntityID
	changed := testChange(t, s, ctx, "business_line", line.ID, line.Revision, line)
	if _, e := s.DB.ExecContext(ctx, `UPDATE public.findings SET dispatched_at=$2,developer_fixed_at=$3 WHERE id=$1`, v.finding, v.born, changed.EffectiveAt.Add(time.Microsecond)); e != nil {
		t.Fatal(e)
	}
	_, _, old, _, _ := readEventIDs(t, s, ctx, v.finding, "dispatched")
	_, _, now, _, _ := readEventIDs(t, s, ctx, v.finding, "developer_fixed")
	if old.Int64 != v.person || now.Int64 != p.EntityID {
		t.Fatalf("late event resolved wrong owners %v %v", old, now)
	}
	other := testDepartment(t, s, ctx)
	request := testAssignmentRequest([]int64{v.asset}, other.EntityID)
	testAssignmentConfirm(t, s, ctx, testAssignmentPreview(t, s, ctx, request))
	if _, e := s.DB.ExecContext(ctx, `INSERT INTO public.finding_history(finding_id,kind,actor,occurred_at) VALUES($1,'delayed_probe','test',$2)`, v.finding, v.born); e != nil {
		t.Fatal(e)
	}
	_, dep, owner, _, _ := readEventIDs(t, s, ctx, v.finding, "delayed_probe")
	if dep.Int64 != v.dep || owner.Int64 != v.person {
		t.Fatalf("late event guessed current org %v %v", dep, owner)
	}
}
func TestEventLifecycleRetestDeleteAndArchiveKeepSnapshots(t *testing.T) {
	s, ctx := testOrgStore(t)
	v := newEventFixture(t, s, ctx)
	if _, e := s.DB.ExecContext(ctx, `UPDATE public.findings SET status='fixed' WHERE id=$1`, v.finding); e != nil {
		t.Fatal(e)
	}
	if _, e := s.DB.ExecContext(ctx, `UPDATE public.findings SET status='confirmed' WHERE id=$1`, v.finding); e != nil {
		t.Fatal(e)
	}
	var retest int64
	if e := s.DB.QueryRowContext(ctx, `INSERT INTO public.finding_retests(finding_id,snapshot) VALUES($1,'{}') RETURNING id`, v.finding).Scan(&retest); e != nil {
		t.Fatal(e)
	}
	if _, e := s.DB.ExecContext(ctx, `UPDATE public.finding_retests SET started_at=clock_timestamp(),finished_at=clock_timestamp(),status='completed',verdict='fixed' WHERE id=$1`, retest); e != nil {
		t.Fatal(e)
	}
	for _, kind := range []string{"status_changed", "retest_requested", "retest_started", "retest_completed"} {
		a, d, o, q, _ := readEventIDs(t, s, ctx, v.finding, kind)
		if a.Int64 != v.asset || d.Int64 != v.dep || o.Int64 != v.person || q != "recorded" {
			t.Fatalf("%s: %v/%v/%v/%s", kind, a, d, o, q)
		}
	}
	if _, e := s.DB.ExecContext(ctx, `DELETE FROM public.findings WHERE id=$1`, v.finding); e != nil {
		t.Fatal(e)
	}
	a, d, o, q, _ := readEventIDs(t, s, ctx, v.finding, "removed")
	if a.Int64 != v.asset || d.Int64 != v.dep || o.Int64 != v.person || q != "recorded" {
		t.Fatalf("removed lost binding %v %v %v %s", a, d, o, q)
	}
	archived := newEventFixture(t, s, ctx)
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = tx.ExecContext(ctx, `SELECT set_config('artex.finding_archiving','true',true)`); e == nil {
		_, e = tx.ExecContext(ctx, `DELETE FROM public.findings WHERE id=$1`, archived.finding)
	}
	if e != nil {
		tx.Rollback()
		t.Fatal(e)
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	var removed int
	if e = s.DB.QueryRowContext(ctx, `SELECT count(*) FROM public.finding_history WHERE finding_id=$1 AND kind='removed'`, archived.finding).Scan(&removed); e != nil || removed != 0 {
		t.Fatalf("archive fabricated deletion %d %v", removed, e)
	}
	if _, e = s.DB.ExecContext(ctx, `INSERT INTO public.findings(id,name,severity,asset_ids,created_at) VALUES($1,'恢复','high',jsonb_build_array($2::bigint),$3)`, archived.finding, archived.asset, archived.born); e != nil {
		t.Fatal(e)
	}
	var count int
	if e = s.DB.QueryRowContext(ctx, `SELECT count(*) FROM org.finding_asset_history WHERE finding_id=$1`, archived.finding).Scan(&count); e != nil || count != 1 {
		t.Fatalf("restore duplicated binding %d %v", count, e)
	}
}
func TestEventGlobalSLAIsTemporalAndUnassignedStillCounts(t *testing.T) {
	s, ctx := testOrgStore(t)
	ids, _ := testAssignmentAssets(t, s, ctx, 1)
	var id int64
	var born time.Time
	if e := s.DB.QueryRowContext(ctx, `INSERT INTO public.findings(name,severity,asset_ids) VALUES('未归属计时','critical',jsonb_build_array($1::bigint)) RETURNING id,created_at`, ids[0]).Scan(&id, &born); e != nil {
		t.Fatal(e)
	}
	_, d, _, q, sla := readEventIDs(t, s, ctx, id, "discovered")
	if d.Valid || q != "unassigned" || !sla.Valid {
		t.Fatalf("unassigned sla %v %s %v", d, q, sla)
	}
	var old string
	if e := s.DB.QueryRowContext(ctx, `SELECT value FROM public.settings WHERE key='finding_sla_days'`).Scan(&old); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.DB.Exec(`UPDATE public.settings SET value=$1 WHERE key='finding_sla_days'`, old) })
	if _, e := s.DB.ExecContext(ctx, `UPDATE public.settings SET value='{"critical":5,"high":8,"medium":31,"low":91}' WHERE key='finding_sla_days'`); e != nil {
		t.Fatal(e)
	}
	if _, e := s.DB.ExecContext(ctx, `INSERT INTO public.finding_history(finding_id,kind,actor,occurred_at) VALUES($1,'late_sla','test',$2),($1,'current_sla','test',clock_timestamp())`, id, born); e != nil {
		t.Fatal(e)
	}
	_, _, _, _, late := readEventIDs(t, s, ctx, id, "late_sla")
	_, _, _, _, current := readEventIDs(t, s, ctx, id, "current_sla")
	if late.Int64 != sla.Int64 || current.Int64 != 5 {
		t.Fatalf("SLA overwritten %v/%v orig%v", late, current, sla)
	}
}
func TestEventSchemaRestartLeavesLegacyJournalUntouched(t *testing.T) {
	s, ctx := testOrgStore(t)
	var history int64
	if e := s.DB.QueryRowContext(ctx, `INSERT INTO public.finding_history(finding_id,kind,actor,occurred_at,source,metadata) VALUES(-987654,'discovered','legacy',now()-interval '1 year','backfill','{"severity":"high"}') RETURNING id`).Scan(&history); e != nil {
		t.Fatal(e)
	}
	var before, after json.RawMessage
	if e := s.DB.QueryRowContext(ctx, `SELECT to_jsonb(h) FROM public.finding_history h WHERE id=$1`, history).Scan(&before); e != nil {
		t.Fatal(e)
	}
	if _, e := s.DB.ExecContext(ctx, SchemaSQL); e != nil {
		t.Fatal(e)
	}
	if e := s.DB.QueryRowContext(ctx, `SELECT to_jsonb(h) FROM public.finding_history h WHERE id=$1`, history).Scan(&after); e != nil {
		t.Fatal(e)
	}
	if string(before) != string(after) {
		t.Fatal("schema restart rewrote legacy event")
	}
}
