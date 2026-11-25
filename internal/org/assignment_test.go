package org

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// All fixtures use the disposable database selected by ARTEX_ORG_TEST_DSN.
func testAssignmentAssets(t *testing.T, s *Store, ctx context.Context, count int) ([]int64, string) {
	t.Helper()
	prefix := "batch-" + uuid.NewString()
	rows, err := s.DB.QueryContext(ctx, `WITH a AS (
 INSERT INTO public.assets(type,domain,created_at) SELECT 'subdomain',$1||'-'||n||'.example.invalid',now()-interval '2 days'
 FROM generate_series(1,$2::int) n RETURNING id,type,domain,created_at,updated_at
) INSERT INTO org.asset_registry(asset_id,asset_type,identifier,first_seen_at,source_updated_at)
 SELECT id,type,domain,created_at,updated_at FROM a RETURNING asset_id`, prefix, count)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	ids := make([]int64, 0, count)
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(ids) != count {
		t.Fatalf("fixture inserted %d assets, want %d", len(ids), count)
	}
	return ids, prefix
}

func testAssignmentRequest(ids []int64, dep int64) AssignmentRequest {
	return AssignmentRequest{Selection: AssignmentSelection{Mode: "ids", IDs: ids, ExpectedCount: len(ids)}, DepartmentID: dep, Scope: "all_open"}
}

func testAssignmentPreview(t *testing.T, s *Store, ctx context.Context, r AssignmentRequest) AssignmentPreview {
	t.Helper()
	p, err := s.PreviewAssignment(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func testAssignmentConfirm(t *testing.T, s *Store, ctx context.Context, p AssignmentPreview) AssignmentResult {
	t.Helper()
	r, err := s.ConfirmAssignment(ctx, AssignmentConfirm{Token: p.Token, IdempotencyKey: uuid.NewString(), OverwriteConfirmed: true, BackdateConfirmed: true})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func testAssignmentCount(t *testing.T, s *Store, ctx context.Context, ids []int64, where string) int {
	t.Helper()
	var n int
	err := s.DB.QueryRowContext(ctx, `SELECT count(*) FROM org.asset_ownership WHERE asset_id=ANY($1::bigint[]) AND `+where, ids).Scan(&n)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func testAssignmentFinding(t *testing.T, s *Store, ctx context.Context, asset int64, severity string) int64 {
	t.Helper()
	var id int64
	err := s.DB.QueryRowContext(ctx, `INSERT INTO public.findings(asset_id,asset_ids,name,severity,status,assignment_source,created_at)
 VALUES($1,jsonb_build_array($1::bigint),'batch fixture',$2,'pending','manual',now()-interval '1 day') RETURNING id`, asset, severity).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func testAssignmentSnapshot(t *testing.T, s *Store, ctx context.Context) string {
	t.Helper()
	var out string
	err := s.DB.QueryRowContext(ctx, `SELECT md5(jsonb_build_array(
 (SELECT jsonb_agg(x ORDER BY asset_id) FROM org.asset_registry x),
 (SELECT jsonb_agg(x ORDER BY id) FROM org.asset_ownership x),
 (SELECT jsonb_agg(x ORDER BY id) FROM org.asset_assignment_job x),
 (SELECT jsonb_agg(x ORDER BY job_id,asset_id) FROM org.asset_assignment_item x),
 (SELECT jsonb_agg(x ORDER BY id) FROM org.change_operation x),
 (SELECT jsonb_agg(x ORDER BY id) FROM org.audit_log x),
 (SELECT jsonb_agg(x ORDER BY id) FROM org.attribution_rule x),
 (SELECT jsonb_agg(x ORDER BY id) FROM org.attribution_rule_revision x),
 (SELECT jsonb_agg(x) FROM org.assignment_signing_key x)
 )::text)`).Scan(&out)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestAssignment48PreviewReadOnlyActualAndIdempotent(t *testing.T) {
	s, ctx := testOrgStore(t)
	dep := testDepartment(t, s, ctx)
	ids, _ := testAssignmentAssets(t, s, ctx, 48)
	for i, id := range ids {
		testAssignmentFinding(t, s, ctx, id, []string{"critical", "high", "medium", "low"}[i%4])
	}
	before := testAssignmentSnapshot(t, s, ctx)
	p := testAssignmentPreview(t, s, ctx, testAssignmentRequest(ids, dep.EntityID))
	if p.AssetCount != 48 || p.OpenCount != 48 || p.AffectedOpen != 48 || p.AlreadyAssigned != 0 {
		t.Fatalf("wrong preview: %+v", p)
	}
	for _, sev := range []string{"critical", "high", "medium", "low"} {
		if p.Severity[sev] != 12 {
			t.Fatalf("severity %s=%d", sev, p.Severity[sev])
		}
	}
	if after := testAssignmentSnapshot(t, s, ctx); before != after {
		t.Fatal("preview wrote persistent data")
	}
	c := AssignmentConfirm{Token: p.Token, IdempotencyKey: uuid.NewString()}
	r, err := s.ConfirmAssignment(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	if r.State != "succeeded" || r.Assigned != 48 || r.Total != 48 || r.EffectiveAt == nil {
		t.Fatalf("actual result: %+v", r)
	}
	if n := testAssignmentCount(t, s, ctx, ids, "voided_at IS NULL AND valid_to IS NULL AND source='manual'"); n != 48 {
		t.Fatalf("assigned %d", n)
	}
	second, err := s.ConfirmAssignment(ctx, c)
	if err != nil || second.OperationID != r.OperationID {
		t.Fatalf("repeat: %+v %v", second, err)
	}
	if n := testAssignmentCount(t, s, ctx, ids, "true"); n != 48 {
		t.Fatalf("idempotent request wrote duplicate rows: %d", n)
	}
	newIDs, _ := testAssignmentAssets(t, s, ctx, 1)
	other := testAssignmentPreview(t, s, ctx, testAssignmentRequest(newIDs, dep.EntityID))
	c.Token = other.Token
	if _, err = s.ConfirmAssignment(ctx, c); err == nil {
		t.Fatal("same idempotency key accepted a different signed request")
	}
	var audit int
	if err = s.DB.QueryRowContext(ctx, `SELECT count(*) FROM org.audit_log WHERE operation_id=$1`, r.OperationID).Scan(&audit); err != nil || audit < 48 {
		t.Fatalf("missing per-asset audit %d: %v", audit, err)
	}
}

func TestAssignmentFilterCountAndSameCountMembershipChangeReject(t *testing.T) {
	s, ctx := testOrgStore(t)
	dep := testDepartment(t, s, ctx)
	ids, prefix := testAssignmentAssets(t, s, ctx, 48)
	r := AssignmentRequest{Selection: AssignmentSelection{Mode: "filter", Filter: AssetFilter{Status: "unassigned", Q: prefix}, ExpectedCount: 47}, DepartmentID: dep.EntityID, Scope: "all_open"}
	if _, err := s.PreviewAssignment(ctx, r); err == nil {
		t.Fatal("accepted mismatched filter count")
	}
	r.Selection.ExpectedCount = 48
	p := testAssignmentPreview(t, s, ctx, r)
	if _, err := s.DB.ExecContext(ctx, `UPDATE public.assets SET domain=$2 WHERE id=$1`, ids[0], uuid.NewString()+".example.invalid"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.ExecContext(ctx, `UPDATE org.asset_registry SET identifier=$2 WHERE asset_id=$1`, ids[0], uuid.NewString()+".example.invalid"); err != nil {
		t.Fatal(err)
	}
	more, _ := testAssignmentAssets(t, s, ctx, 1)
	if _, err := s.DB.ExecContext(ctx, `UPDATE public.assets SET domain=$2 WHERE id=$1`, more[0], prefix+"-replacement.example.invalid"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.ExecContext(ctx, `UPDATE org.asset_registry SET identifier=$2 WHERE asset_id=$1`, more[0], prefix+"-replacement.example.invalid"); err != nil {
		t.Fatal(err)
	}
	if p2 := testAssignmentPreview(t, s, ctx, r); p2.AssetCount != 48 {
		t.Fatalf("replacement count %+v", p2)
	}
	if _, err := s.ConfirmAssignment(ctx, AssignmentConfirm{Token: p.Token, IdempotencyKey: uuid.NewString()}); err == nil {
		t.Fatal("same-count changed membership accepted")
	}
	if n := testAssignmentCount(t, s, ctx, append(ids, more...), "true"); n != 0 {
		t.Fatalf("rejected request partly wrote %d", n)
	}
}

func TestAssignmentConcurrentOwnershipAndTamperingRejectWholeBatch(t *testing.T) {
	s, ctx := testOrgStore(t)
	a := testDepartment(t, s, ctx)
	b := testDepartment(t, s, ctx)
	ids, _ := testAssignmentAssets(t, s, ctx, 3)
	p := testAssignmentPreview(t, s, ctx, testAssignmentRequest(ids, a.EntityID))
	testAssignmentConfirm(t, s, ctx, testAssignmentPreview(t, s, ctx, testAssignmentRequest(ids[:1], b.EntityID)))
	if _, err := s.ConfirmAssignment(ctx, AssignmentConfirm{Token: p.Token, IdempotencyKey: uuid.NewString(), OverwriteConfirmed: true}); err == nil {
		t.Fatal("stale ownership preview accepted")
	}
	if n := testAssignmentCount(t, s, ctx, ids, "true"); n != 1 {
		t.Fatalf("whole batch rejection wrote %d rows", n)
	}
	if _, err := s.ConfirmAssignment(ctx, AssignmentConfirm{Token: p.Token + "tampered", IdempotencyKey: uuid.NewString(), OverwriteConfirmed: true}); err == nil {
		t.Fatal("tampered token accepted")
	}
}

func TestAssignmentOverwriteConfirmationAndFutureFindingCohorts(t *testing.T) {
	s, ctx := testOrgStore(t)
	a := testDepartment(t, s, ctx)
	b := testDepartment(t, s, ctx)
	ids, _ := testAssignmentAssets(t, s, ctx, 1)
	first := testAssignmentConfirm(t, s, ctx, testAssignmentPreview(t, s, ctx, testAssignmentRequest(ids, a.EntityID)))
	r := testAssignmentRequest(ids, b.EntityID)
	r.Scope = "future_findings"
	p := testAssignmentPreview(t, s, ctx, r)
	if p.AlreadyAssigned != 1 || len(p.Transfers) != 1 {
		t.Fatalf("overwrite impact missing %+v", p)
	}
	if _, err := s.ConfirmAssignment(ctx, AssignmentConfirm{Token: p.Token, IdempotencyKey: uuid.NewString()}); err == nil {
		t.Fatal("overwrite accepted without explicit confirmation")
	}
	second := testAssignmentConfirm(t, s, ctx, p)
	for _, tc := range []struct {
		at, born time.Time
		want     int64
	}{
		{second.EffectiveAt.Add(time.Second), first.EffectiveAt.Add(-time.Hour), a.EntityID},
		{second.EffectiveAt.Add(time.Second), *second.EffectiveAt, b.EntityID},
		{second.EffectiveAt.Add(-time.Microsecond), *first.EffectiveAt, a.EntityID},
	} {
		got, err := s.ResolveOwner(ctx, ids[0], tc.at, tc.born)
		if err != nil || got.DepartmentID == nil || *got.DepartmentID != tc.want {
			t.Fatalf("temporal cohort got %+v %v want %d", got, err, tc.want)
		}
	}
	if n := testAssignmentCount(t, s, ctx, ids, "true"); n < 3 {
		t.Fatalf("history overwritten: %d rows", n)
	}
}

func TestAssignmentFirstBackdateRequiresConsentAndNeverAssigned(t *testing.T) {
	s, ctx := testOrgStore(t)
	dep := testDepartment(t, s, ctx)
	ids, _ := testAssignmentAssets(t, s, ctx, 1)
	finding := testAssignmentFinding(t, s, ctx, ids[0], "high")
	// This fixture explicitly knew its primary asset at discovery; old unknown
	// bindings must not be guessed merely from the current asset_id projection.
	if _, err := s.DB.ExecContext(ctx, `INSERT INTO org.finding_asset_history(finding_id,asset_id,valid_from,operation_id,created_by)
 SELECT id,asset_id,created_at,$2,'test fixture' FROM public.findings WHERE id=$1 AND NOT EXISTS(SELECT 1 FROM org.finding_asset_history WHERE finding_id=$1)`, finding, dep.OperationID); err != nil {
		t.Fatal(err)
	}
	r := testAssignmentRequest(ids, dep.EntityID)
	r.BackdateFirstAssignment = true
	p := testAssignmentPreview(t, s, ctx, r)
	if !p.BackdateEligible {
		t.Fatal("first assignment not eligible")
	}
	if _, err := s.ConfirmAssignment(ctx, AssignmentConfirm{Token: p.Token, IdempotencyKey: uuid.NewString()}); err == nil {
		t.Fatal("backdate accepted without second consent")
	}
	r.Scope = "future_findings"
	if _, err := s.PreviewAssignment(ctx, r); err == nil {
		t.Fatal("accepted backdate plus future-only scope")
	}
	r.Scope = "all_open"
	result := testAssignmentConfirm(t, s, ctx, p)
	var from, first time.Time
	if err := s.DB.QueryRowContext(ctx, `SELECT o.valid_from,g.first_seen_at FROM org.asset_ownership o JOIN org.asset_registry g USING(asset_id) WHERE o.asset_id=$1 AND o.voided_at IS NULL`, ids[0]).Scan(&from, &first); err != nil {
		t.Fatal(err)
	}
	if !from.Equal(first) {
		t.Fatalf("backdate=%s want first=%s", from, first)
	}
	var correction int
	if err := s.DB.QueryRowContext(ctx, `SELECT count(*) FROM org.finding_event_attribution_correction WHERE finding_id=$1 AND operation_id=$2 AND reason='first_assignment_backdate'`, finding, result.OperationID).Scan(&correction); err != nil || correction == 0 {
		t.Fatalf("missing append-only backdate correction %d: %v", correction, err)
	}
	var audit bool
	if err := s.DB.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM org.audit_log WHERE operation_id=$1 AND (action LIKE '%backdate%' OR details::text LIKE '%backdate%'))`, result.OperationID).Scan(&audit); err != nil || !audit {
		t.Fatalf("missing explicit backdate audit %v", err)
	}
	if _, err := s.PreviewAssignment(ctx, r); err == nil {
		t.Fatal("backdate accepted for previously assigned asset")
	}
	u, err := s.UndoAssignment(ctx, result.OperationID, uuid.NewString())
	if err != nil || u.Restored != 1 {
		t.Fatalf("backdate undo %+v %v", u, err)
	}
	if err = s.DB.QueryRowContext(ctx, `SELECT count(*) FROM org.finding_event_attribution_correction WHERE finding_id=$1 AND reason='backdate_undo'`, finding).Scan(&correction); err != nil || correction == 0 {
		t.Fatalf("backdate undo must append correction: %d %v", correction, err)
	}
}

func TestAssignmentUndoVoidsWithoutDeleteAndRefusesNewOwnership(t *testing.T) {
	s, ctx := testOrgStore(t)
	a := testDepartment(t, s, ctx)
	b := testDepartment(t, s, ctx)
	ids, _ := testAssignmentAssets(t, s, ctx, 2)
	testAssignmentConfirm(t, s, ctx, testAssignmentPreview(t, s, ctx, testAssignmentRequest(ids, a.EntityID)))
	second := testAssignmentConfirm(t, s, ctx, testAssignmentPreview(t, s, ctx, testAssignmentRequest(ids, b.EntityID)))
	before := testAssignmentCount(t, s, ctx, ids, "true")
	key := uuid.NewString()
	u, err := s.UndoAssignment(ctx, second.OperationID, key)
	if err != nil || u.Restored != 2 || u.Reversed != 0 {
		t.Fatalf("undo %+v %v", u, err)
	}
	if after := testAssignmentCount(t, s, ctx, ids, "true"); after != before {
		t.Fatalf("undo physically removed rows before=%d after=%d", before, after)
	}
	if n := testAssignmentCount(t, s, ctx, ids, "voided_at IS NOT NULL"); n != 2 {
		t.Fatalf("expected 2 voided rows got %d", n)
	}
	if n := testAssignmentCount(t, s, ctx, ids, fmt.Sprintf("voided_at IS NULL AND valid_to IS NULL AND department_id=%d", a.EntityID)); n != 2 {
		t.Fatalf("restored previous intervals %d", n)
	}
	if again, err := s.UndoAssignment(ctx, second.OperationID, key); err != nil || again.OperationID != u.OperationID {
		t.Fatalf("undo retry %+v %v", again, err)
	}
	third := testAssignmentConfirm(t, s, ctx, testAssignmentPreview(t, s, ctx, testAssignmentRequest(ids, b.EntityID)))
	testAssignmentConfirm(t, s, ctx, testAssignmentPreview(t, s, ctx, testAssignmentRequest(ids[:1], a.EntityID)))
	if _, err := s.UndoAssignment(ctx, third.OperationID, uuid.NewString()); err == nil {
		t.Fatal("undo overwrote newer assignment")
	}
}

func TestAssignmentUndoLateRecordedBackdatedEventUsesReverseHistory(t *testing.T) {
	s, ctx := testOrgStore(t)
	a := testDepartment(t, s, ctx)
	b := testDepartment(t, s, ctx)
	ids, _ := testAssignmentAssets(t, s, ctx, 1)
	finding := testAssignmentFinding(t, s, ctx, ids[0], "critical")
	testAssignmentConfirm(t, s, ctx, testAssignmentPreview(t, s, ctx, testAssignmentRequest(ids, a.EntityID)))
	second := testAssignmentConfirm(t, s, ctx, testAssignmentPreview(t, s, ctx, testAssignmentRequest(ids, b.EntityID)))
	var previous int64
	if err := s.DB.QueryRowContext(ctx, `SELECT event_revision FROM org.asset_registry WHERE asset_id=$1`, ids[0]).Scan(&previous); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.ExecContext(ctx, `INSERT INTO public.finding_history(finding_id,event_key,kind,actor,occurred_at) VALUES($1,$2,'developer_fixed','late test',$3)`, finding, uuid.NewString(), second.EffectiveAt.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	var current int64
	if err := s.DB.QueryRowContext(ctx, `SELECT event_revision FROM org.asset_registry WHERE asset_id=$1`, ids[0]).Scan(&current); err != nil || current <= previous {
		t.Fatalf("actual history insertion did not advance event counter %d -> %d: %v", previous, current, err)
	}
	u, err := s.UndoAssignment(ctx, second.OperationID, uuid.NewString())
	if err != nil || u.Reversed != 1 || u.Restored != 0 {
		t.Fatalf("late event must reverse, not erase %+v %v", u, err)
	}
	if n := testAssignmentCount(t, s, ctx, ids, "voided_at IS NOT NULL"); n != 0 {
		t.Fatalf("event-bearing history was voided: %d", n)
	}
	got, err := s.ResolveOwner(ctx, ids[0], *second.EffectiveAt, second.EffectiveAt.Add(-time.Hour))
	if err != nil || got.DepartmentID == nil || *got.DepartmentID != b.EntityID {
		t.Fatalf("past ownership changed %+v %v", got, err)
	}
	got, err = s.ResolveOwner(ctx, ids[0], time.Now().Add(time.Second), time.Now())
	if err != nil || got.DepartmentID == nil || *got.DepartmentID != a.EntityID {
		t.Fatalf("reverse did not restore current department %+v %v", got, err)
	}
}

func TestAssignmentHistoryReplayDoesNotInventNewEventsForUndo(t *testing.T) {
	s, ctx := testOrgStore(t)
	dep := testDepartment(t, s, ctx)
	ids, _ := testAssignmentAssets(t, s, ctx, 1)
	finding := testAssignmentFinding(t, s, ctx, ids[0], "medium")
	r := testAssignmentConfirm(t, s, ctx, testAssignmentPreview(t, s, ctx, testAssignmentRequest(ids, dep.EntityID)))
	var before, after, eventsBefore, eventsAfter int64
	if err := s.DB.QueryRowContext(ctx, `SELECT event_revision,(SELECT count(*) FROM public.finding_history WHERE finding_id=$2) FROM org.asset_registry WHERE asset_id=$1`, ids[0], finding).Scan(&before, &eventsBefore); err != nil {
		t.Fatal(err)
	}
	// Replaying an idempotent journal insert is what repeated schema backfill does.
	if _, err := s.DB.ExecContext(ctx, `INSERT INTO public.finding_history(finding_id,event_key,kind,actor,occurred_at)
 SELECT finding_id,event_key,kind,actor,occurred_at FROM public.finding_history WHERE finding_id=$1 ON CONFLICT(finding_id,event_key) DO NOTHING`, finding); err != nil {
		t.Fatal(err)
	}
	if err := s.DB.QueryRowContext(ctx, `SELECT event_revision,(SELECT count(*) FROM public.finding_history WHERE finding_id=$2) FROM org.asset_registry WHERE asset_id=$1`, ids[0], finding).Scan(&after, &eventsAfter); err != nil {
		t.Fatal(err)
	}
	if eventsBefore != eventsAfter || before != after {
		t.Fatalf("no-op history replay changed event state: rows %d -> %d, version %d -> %d", eventsBefore, eventsAfter, before, after)
	}
	u, err := s.UndoAssignment(ctx, r.OperationID, uuid.NewString())
	if err != nil || u.Restored != 1 || u.Reversed != 0 {
		t.Fatalf("duplicate event forced reverse undo %+v %v", u, err)
	}
}

func TestAssignmentUndoRefusesDisabledPreviousDepartment(t *testing.T) {
	s, ctx := testOrgStore(t)
	a := testDepartment(t, s, ctx)
	b := testDepartment(t, s, ctx)
	ids, _ := testAssignmentAssets(t, s, ctx, 1)
	testAssignmentConfirm(t, s, ctx, testAssignmentPreview(t, s, ctx, testAssignmentRequest(ids, a.EntityID)))
	second := testAssignmentConfirm(t, s, ctx, testAssignmentPreview(t, s, ctx, testAssignmentRequest(ids, b.EntityID)))
	_, err := s.Change(ctx, Command{Kind: "department", Action: "disable", ID: a.EntityID, Revision: 1, ConfirmImpact: true, Scope: "all_open", IdempotencyKey: uuid.NewString()})
	if err != nil {
		t.Fatal(err)
	}
	before := testAssignmentCount(t, s, ctx, ids, "true")
	if _, err = s.UndoAssignment(ctx, second.OperationID, uuid.NewString()); err == nil {
		t.Fatal("undo restored an inactive department")
	}
	if n := testAssignmentCount(t, s, ctx, ids, "true"); n != before {
		t.Fatalf("rejected undo changed history %d -> %d", before, n)
	}
	if n := testAssignmentCount(t, s, ctx, ids, fmt.Sprintf("voided_at IS NULL AND valid_to IS NULL AND department_id=%d", b.EntityID)); n != 1 {
		t.Fatal("rejected undo changed current department")
	}
}

func TestAssignmentGeneratedRuleAndManualPriority(t *testing.T) {
	s, ctx := testOrgStore(t)
	dep := testDepartment(t, s, ctx)
	other := testDepartment(t, s, ctx)
	ids, prefix := testAssignmentAssets(t, s, ctx, 1)
	r := testAssignmentRequest(ids, dep.EntityID)
	r.GeneratedRules = []Rule{{MatchType: "domain", MatchValue: "*." + prefix + ".example.invalid", DepartmentID: dep.EntityID, Priority: 5, Active: true}}
	result := testAssignmentConfirm(t, s, ctx, testAssignmentPreview(t, s, ctx, r))
	var count int
	if err := s.DB.QueryRowContext(ctx, `SELECT count(*) FROM org.attribution_rule_revision WHERE operation_id=$1`, result.OperationID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("generated rule not atomic with assignment: %d %v", count, err)
	}
	asset := testRuleAsset(t, s, ctx, "new."+prefix+".example.invalid", "")
	testSyncRuleAsset(t, s, ctx, asset)
	got, source, _ := testCurrentRuleOwner(t, s, ctx, asset)
	if got != dep.EntityID || source != "rule" {
		t.Fatalf("generated rule not usable %d %s", got, source)
	}
	var domain string
	if err := s.DB.QueryRowContext(ctx, `SELECT domain FROM public.assets WHERE id=$1`, ids[0]).Scan(&domain); err != nil {
		t.Fatal(err)
	}
	testSavedRule(t, s, ctx, Rule{MatchType: "domain", MatchValue: domain, DepartmentID: other.EntityID, Priority: 99, Active: true})
	testApplyRules(t, s, ctx, true)
	got, source, _ = testCurrentRuleOwner(t, s, ctx, ids[0])
	if got != dep.EntityID || source != "manual" {
		t.Fatalf("manual assignment overwritten %d %s", got, source)
	}
	u, err := s.UndoAssignment(ctx, result.OperationID, uuid.NewString())
	if err != nil || u.Restored != 1 {
		t.Fatalf("undo batch plus generated rule %+v %v", u, err)
	}
	if err = s.DB.QueryRowContext(ctx, `SELECT count(*) FROM org.attribution_rule_revision current WHERE current.valid_to IS NULL AND current.voided_at IS NULL AND current.active
 AND EXISTS(SELECT 1 FROM org.attribution_rule_revision old WHERE old.rule_id=current.rule_id AND old.operation_id=$1)`, result.OperationID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("generated rule remained enabled after undo: %d %v", count, err)
	}
	got, source, _ = testCurrentRuleOwner(t, s, ctx, asset)
	if got != dep.EntityID || source != "rule" {
		t.Fatalf("undo rewrote another asset's already-recorded rule attribution: %d %s", got, source)
	}
}

func TestAssignment501RunsAsAtomicPersistentJob(t *testing.T) {
	s, ctx := testOrgStore(t)
	dep := testDepartment(t, s, ctx)
	ids, _ := testAssignmentAssets(t, s, ctx, 501)
	p := testAssignmentPreview(t, s, ctx, testAssignmentRequest(ids, dep.EntityID))
	r := testAssignmentConfirm(t, s, ctx, p)
	if r.State != "queued" || r.JobID == "" || r.Total != 501 {
		t.Fatalf("not queued %+v", r)
	}
	if n := testAssignmentCount(t, s, ctx, ids, "true"); n != 0 {
		t.Fatalf("queued batch already wrote %d", n)
	}
	claimed, err := s.ProcessNextAssignment(ctx)
	if err != nil || !claimed {
		t.Fatalf("worker %+v %v", claimed, err)
	}
	job, err := s.AssignmentJob(ctx, r.JobID)
	if err != nil || job.State != "succeeded" || job.Assigned != 501 || job.Processed != 501 {
		t.Fatalf("job result %+v %v", job, err)
	}
	if n := testAssignmentCount(t, s, ctx, ids, "voided_at IS NULL AND valid_to IS NULL"); n != 501 {
		t.Fatalf("committed %d", n)
	}
	var items int
	if err = s.DB.QueryRowContext(ctx, `SELECT count(*) FROM org.asset_assignment_item WHERE job_id=$1`, r.JobID).Scan(&items); err != nil || items != 501 {
		t.Fatalf("undo records %d %v", items, err)
	}
	jobs, err := s.AssignmentJobs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, v := range jobs {
		if v.ID == r.JobID {
			found = true
		}
	}
	if !found {
		t.Fatal("completed job disappears on returning to page")
	}
}

func TestAssignment1001FailureRollsBackWholeBatch(t *testing.T) {
	s, ctx := testOrgStore(t)
	dep := testDepartment(t, s, ctx)
	ids, _ := testAssignmentAssets(t, s, ctx, 1001)
	req := testAssignmentRequest(ids, dep.EntityID)
	req.GeneratedRules = []Rule{{MatchType: "domain", MatchValue: uuid.NewString() + ".example.invalid", DepartmentID: dep.EntityID, Active: true}}
	r := testAssignmentConfirm(t, s, ctx, testAssignmentPreview(t, s, ctx, req))
	// This trigger exists only in the isolated test database, and fails the last insert.
	function := "test_assignment_fault_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	ddl := fmt.Sprintf(`CREATE FUNCTION org.%s() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN IF NEW.asset_id=%d THEN RAISE EXCEPTION 'injected batch failure'; END IF; RETURN NEW; END$$;
 CREATE TRIGGER %s BEFORE INSERT ON org.asset_ownership FOR EACH ROW EXECUTE FUNCTION org.%s()`, function, ids[len(ids)-1], function, function)
	if _, err := s.DB.ExecContext(ctx, ddl); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := s.DB.ExecContext(cleanup, fmt.Sprintf(`DROP TRIGGER IF EXISTS %s ON org.asset_ownership;DROP FUNCTION IF EXISTS org.%s()`, function, function)); err != nil {
			t.Errorf("remove isolated fault trigger: %v", err)
		}
	})
	claimed, _ := s.ProcessNextAssignment(ctx)
	if !claimed {
		t.Fatal("worker did not claim fault batch")
	}
	job, err := s.AssignmentJob(ctx, r.JobID)
	if err != nil || job.State != "failed" {
		t.Fatalf("failure not persisted %+v %v", job, err)
	}
	if n := testAssignmentCount(t, s, ctx, ids, "true"); n != 0 {
		t.Fatalf("failed batch left partial ownership: %d", n)
	}
	var items int
	if err = s.DB.QueryRowContext(ctx, `SELECT count(*) FROM org.asset_assignment_item WHERE job_id=$1`, r.JobID).Scan(&items); err != nil || items != 0 {
		t.Fatalf("failed batch left undo items %d %v", items, err)
	}
	var rules int
	if err = s.DB.QueryRowContext(ctx, `SELECT count(*) FROM org.attribution_rule_revision WHERE operation_id=$1`, r.OperationID).Scan(&rules); err != nil || rules != 0 {
		t.Fatalf("failed batch left generated rules: %d %v", rules, err)
	}
}

func TestAssignmentPreviewUnregisteredAssetDoesNotRegisterUntilConfirm(t *testing.T) {
	s, ctx := testOrgStore(t)
	dep := testDepartment(t, s, ctx)
	id := testRuleAsset(t, s, ctx, uuid.NewString()+".example.invalid", "")
	before := testAssignmentSnapshot(t, s, ctx)
	p := testAssignmentPreview(t, s, ctx, testAssignmentRequest([]int64{id}, dep.EntityID))
	if after := testAssignmentSnapshot(t, s, ctx); after != before {
		t.Fatal("preview registered or otherwise mutated an unseen asset")
	}
	var registered bool
	if err := s.DB.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM org.asset_registry WHERE asset_id=$1)`, id).Scan(&registered); err != nil || registered {
		t.Fatalf("unexpected registry write %v %v", registered, err)
	}
	r := testAssignmentConfirm(t, s, ctx, p)
	if r.Assigned != 1 || testAssignmentCount(t, s, ctx, []int64{id}, "voided_at IS NULL AND valid_to IS NULL") != 1 {
		t.Fatalf("confirm did not register and assign %+v", r)
	}
}

func TestAssignmentExpiredLeaseUsesFrozenSelection(t *testing.T) {
	s, ctx := testOrgStore(t)
	dep := testDepartment(t, s, ctx)
	ids, prefix := testAssignmentAssets(t, s, ctx, 501)
	req := AssignmentRequest{Selection: AssignmentSelection{Mode: "filter", Filter: AssetFilter{Status: "unassigned", Q: prefix}, ExpectedCount: 501}, DepartmentID: dep.EntityID, Scope: "all_open"}
	r := testAssignmentConfirm(t, s, ctx, testAssignmentPreview(t, s, ctx, req))
	if _, err := s.DB.ExecContext(ctx, `UPDATE org.asset_assignment_job SET state='running',lease_until=now()-interval '1 minute',claimed_by='crashed-test-worker',started_at=now()-interval '2 minutes' WHERE id=$1`, r.JobID); err != nil {
		t.Fatal(err)
	}
	newIDs, _ := testAssignmentAssets(t, s, ctx, 1)
	if _, err := s.DB.ExecContext(ctx, `UPDATE public.assets SET domain=$2 WHERE id=$1`, newIDs[0], prefix+"-later.example.invalid"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.ExecContext(ctx, `UPDATE org.asset_registry SET identifier=$2 WHERE asset_id=$1`, newIDs[0], prefix+"-later.example.invalid"); err != nil {
		t.Fatal(err)
	}
	claimed, err := s.ProcessNextAssignment(ctx)
	if err != nil || !claimed {
		t.Fatalf("lease recovery failed %v %v", claimed, err)
	}
	job, err := s.AssignmentJob(ctx, r.JobID)
	if err != nil || job.State != "succeeded" || job.Assigned != 501 {
		t.Fatalf("recovered job %+v %v", job, err)
	}
	if n := testAssignmentCount(t, s, ctx, ids, "voided_at IS NULL AND valid_to IS NULL"); n != 501 {
		t.Fatalf("frozen set incomplete %d", n)
	}
	if n := testAssignmentCount(t, s, ctx, newIDs, "true"); n != 0 {
		t.Fatalf("worker reran filter and changed frozen membership: %d", n)
	}
	var selection json.RawMessage
	if err = s.DB.QueryRowContext(ctx, `SELECT selection FROM org.asset_assignment_job WHERE id=$1`, r.JobID).Scan(&selection); err != nil {
		t.Fatal(err)
	}
	var frozen struct {
		ResolvedAssets []json.RawMessage `json:"resolved_assets"`
	}
	if err = json.Unmarshal(selection, &frozen); err != nil || len(frozen.ResolvedAssets) != 501 {
		t.Fatalf("persistent frozen set absent %d %v", len(frozen.ResolvedAssets), err)
	}
}

func TestAssignmentWorkerCancellationRollsBackThenLeaseRecovers(t *testing.T) {
	s, ctx := testOrgStore(t)
	dep := testDepartment(t, s, ctx)
	ids, _ := testAssignmentAssets(t, s, ctx, 501)
	r := testAssignmentConfirm(t, s, ctx, testAssignmentPreview(t, s, ctx, testAssignmentRequest(ids, dep.EntityID)))
	// A scoped advisory lock proves cancellation happens during the write transaction.
	lockKey := int64(800000000) + ids[0]
	locker, err := s.DB.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_, e := locker.ExecContext(cleanup, `SELECT pg_advisory_unlock($1)`, lockKey)
		if e != nil {
			t.Errorf("unlock isolated fixture: %v", e)
		}
		locker.Close()
	})
	if _, err = locker.ExecContext(ctx, `SELECT pg_advisory_lock($1)`, lockKey); err != nil {
		t.Fatal(err)
	}
	function := "test_assignment_pause_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	ddl := fmt.Sprintf(`CREATE FUNCTION org.%s() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN IF NEW.asset_id=%d THEN PERFORM pg_advisory_xact_lock(%d); END IF; RETURN NEW; END$$;
 CREATE TRIGGER %s BEFORE INSERT ON org.asset_ownership FOR EACH ROW EXECUTE FUNCTION org.%s()`, function, ids[250], lockKey, function, function)
	if _, err = s.DB.ExecContext(ctx, ddl); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, e := s.DB.ExecContext(cleanup, fmt.Sprintf(`DROP TRIGGER IF EXISTS %s ON org.asset_ownership;DROP FUNCTION IF EXISTS org.%s()`, function, function)); e != nil {
			t.Errorf("remove isolated pause trigger: %v", e)
		}
	})
	workerCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	done := make(chan error, 1)
	go func() { _, e := s.ProcessNextAssignment(workerCtx); done <- e }()
	finished := false
	defer func() {
		cancel()
		if !finished {
			select {
			case <-done:
			case <-time.After(6 * time.Second):
				t.Error("cancelled worker did not exit")
			}
		}
	}()
	blocked := false
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if err = s.DB.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_locks WHERE locktype='advisory' AND objid=$1::oid AND NOT granted)`, lockKey).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !blocked {
		t.Fatal("worker never entered assignment write transaction")
	}
	if n := testAssignmentCount(t, s, ctx, ids, "true"); n != 0 {
		t.Fatalf("uncommitted batch became visible: %d", n)
	}
	progress, err := s.AssignmentJob(ctx, r.JobID)
	if err != nil || progress.State != "running" || progress.Processed != 250 || progress.Assigned != 0 {
		t.Fatalf("progress must be visible separately from commit: %+v %v", progress, err)
	}
	cancel()
	select {
	case <-done:
		finished = true
	case <-time.After(6 * time.Second):
		t.Fatal("worker failed to exit after cancellation")
	}
	if _, err = locker.ExecContext(ctx, `SELECT pg_advisory_unlock($1)`, lockKey); err != nil {
		t.Fatal(err)
	}
	if n := testAssignmentCount(t, s, ctx, ids, "true"); n != 0 {
		t.Fatalf("cancelled batch committed %d rows", n)
	}
	job, err := s.AssignmentJob(ctx, r.JobID)
	if err != nil {
		t.Fatal(err)
	}
	if job.State != "running" && job.State != "queued" {
		t.Fatalf("cancelled job is not reclaimable: %+v", job)
	}
	if _, err = s.DB.ExecContext(ctx, `UPDATE org.asset_assignment_job SET lease_until=now()-interval '1 minute' WHERE id=$1`, r.JobID); err != nil {
		t.Fatal(err)
	}
	claimed, err := s.ProcessNextAssignment(ctx)
	if err != nil || !claimed {
		t.Fatalf("reclaim cancelled worker %v %v", claimed, err)
	}
	job, err = s.AssignmentJob(ctx, r.JobID)
	if err != nil || job.State != "succeeded" || job.Assigned != 501 {
		t.Fatalf("recovered cancelled job %+v %v", job, err)
	}
}

func TestAssignmentIndependentQueuedBatchesShareConfigurationRevision(t *testing.T) {
	s, ctx := testOrgStore(t)
	dep := testDepartment(t, s, ctx)
	firstIDs, _ := testAssignmentAssets(t, s, ctx, 501)
	secondIDs, _ := testAssignmentAssets(t, s, ctx, 501)
	directory, err := s.Directory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	firstPreview := testAssignmentPreview(t, s, ctx, testAssignmentRequest(firstIDs, dep.EntityID))
	secondPreview := testAssignmentPreview(t, s, ctx, testAssignmentRequest(secondIDs, dep.EntityID))
	first := testAssignmentConfirm(t, s, ctx, firstPreview)
	second := testAssignmentConfirm(t, s, ctx, secondPreview)
	for _, expected := range []AssignmentResult{first, second} {
		claimed, e := s.ProcessNextAssignment(ctx)
		if e != nil || !claimed {
			t.Fatalf("independent queued batch %s failed claim=%v err=%v", expected.JobID, claimed, e)
		}
		job, e := s.AssignmentJob(ctx, expected.JobID)
		if e != nil || job.State != "succeeded" || job.Assigned != 501 {
			t.Fatalf("independent queued batch %+v %v", job, e)
		}
	}
	after, err := s.Directory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if after.ConfigRevision != directory.ConfigRevision {
		t.Fatalf("asset-only batch changed organization config revision %d -> %d", directory.ConfigRevision, after.ConfigRevision)
	}
	if n := testAssignmentCount(t, s, ctx, append(firstIDs, secondIDs...), "voided_at IS NULL AND valid_to IS NULL"); n != 1002 {
		t.Fatalf("only %d of 1002 assets committed", n)
	}
}
