package db

import (
	"errors"
	"fmt"
	"os"
	"testing"
	"time"
)

func taskDispatchDB(t *testing.T) *DB {
	t.Helper()
	if os.Getenv("ARTEX_PG_DSN") == "" {
		t.Skip("task dispatch integration tests require explicit ARTEX_PG_DSN")
	}
	d, err := Open(testDSN(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func taskDispatchTask(t *testing.T, d *DB, sources ...int64) *Task {
	t.Helper()
	task, err := d.CreateTaskWithOptions("task dispatch integration", "local fixture only", TaskCreateOptions{SourceTaskIDs: sources})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		d.Exec(`DELETE FROM conversations WHERE id IN (SELECT r.conversation_id FROM finding_retests r JOIN findings f ON f.id=r.finding_id WHERE f.task_id=$1)`, task.ID)
		d.Exec(`DELETE FROM findings WHERE task_id=$1`, task.ID)
		d.DeleteTask(task.ID)
	})
	return task
}

func taskDispatchFinding(t *testing.T, d *DB, taskID int64, status string, created time.Time) int64 {
	t.Helper()
	var id int64
	if err := d.QueryRow(`INSERT INTO findings(task_id,name,severity,status,created_at) VALUES(NULLIF($1,0),'dispatch fixture','high',$2,$3) RETURNING id`, taskID, status, created).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if taskID == 0 {
		t.Cleanup(func() { d.DeleteFinding(id) })
	}
	return id
}

// The isolated DB has no concurrent producers; this catches accidental writes
// to findings, their append-only journal, and either notification queue.
func taskDispatchFacts(t *testing.T, d *DB) string {
	t.Helper()
	var digest string
	if err := d.QueryRow(`SELECT concat_ws(':',
	 (SELECT md5(COALESCE(string_agg(to_jsonb(f)::text,',' ORDER BY id),'')) FROM findings f),
	 (SELECT md5(COALESCE(string_agg(to_jsonb(h)::text,',' ORDER BY id),'')) FROM finding_history h),
	 (SELECT count(*) FROM notification_events),(SELECT count(*) FROM notification_deliveries))`).Scan(&digest); err != nil {
		t.Fatal(err)
	}
	return digest
}

func taskDispatchNotificationCounts(t *testing.T, d *DB) string {
	t.Helper()
	var counts string
	if err := d.QueryRow(`SELECT concat_ws(':',(SELECT count(*) FROM notification_events),(SELECT count(*) FROM notification_deliveries))`).Scan(&counts); err != nil {
		t.Fatal(err)
	}
	return counts
}

func TestTaskFindingDispatchFullScopePreservesExistingAndSkipsOtherTasks(t *testing.T) {
	d := taskDispatchDB(t)
	ctx := t.Context()
	source := taskDispatchTask(t, d)
	other := taskDispatchTask(t, d)
	task := taskDispatchTask(t, d, source.ID)
	if err := d.SetStatus(task.ID, "done"); err != nil {
		t.Fatal(err)
	}
	base := time.Now().Add(-72 * time.Hour).Truncate(time.Second)
	if _, err := d.Exec(`INSERT INTO findings(task_id,name,severity,status,created_at)
	 SELECT $1,'dispatch bulk '||n,'high',CASE n%3 WHEN 0 THEN 'pending' WHEN 1 THEN 'in_progress' ELSE 'confirmed' END,$2
	 FROM generate_series(1,1205) n`, task.ID, base); err != nil {
		t.Fatal(err)
	}
	already := taskDispatchFinding(t, d, task.ID, FindingConfirmed, base)
	oldTime := base.Add(12 * time.Hour)
	if _, err := d.RecordFindingMilestone(ctx, already, "dispatched", &oldTime); err != nil {
		t.Fatal(err)
	}
	terminal := []string{FindingFixed, FindingResolved, FindingFalsePositive, FindingIgnored, FindingDuplicate, FindingRiskAccepted}
	for _, status := range terminal {
		taskDispatchFinding(t, d, task.ID, status, base)
	}
	untouched := []int64{
		taskDispatchFinding(t, d, source.ID, FindingConfirmed, base),
		taskDispatchFinding(t, d, other.ID, FindingPending, base),
		taskDispatchFinding(t, d, 0, FindingPending, base),
	}
	chosen := base.Add(24 * time.Hour)
	before := taskDispatchFacts(t, d)
	preview, err := d.PreviewTaskFindingDispatch(ctx, task.ID, &chosen)
	if err != nil || preview.Total != 1212 || preview.Eligible != 1205 || preview.AlreadyDispatched != 1 || preview.SkippedStatus != 6 || preview.InvalidTime != 0 || !preview.CanDispatch || preview.SeverityCounts["high"] != 1205 {
		t.Fatalf("full task preview: %+v, %v", preview, err)
	}
	if after := taskDispatchFacts(t, d); after != before {
		t.Fatal("preview changed persisted facts or notification queues")
	}
	notifications := taskDispatchNotificationCounts(t, d)
	result, err := d.DispatchTaskFindings(ctx, task.ID, &chosen)
	if err != nil || result.Recorded != 1205 || result.Skipped != 7 || result.AlreadyDispatched != 1 || result.SkippedStatus != 6 || result.RecordedAt == nil || !result.RecordedAt.Equal(chosen) {
		t.Fatalf("dispatch: %+v, %v", result, err)
	}
	var updated, journal int
	if err := d.QueryRow(`SELECT count(*) FROM findings WHERE task_id=$1 AND dispatched_at=$2`, task.ID, chosen).Scan(&updated); err != nil {
		t.Fatal(err)
	}
	if err := d.QueryRow(`SELECT count(*) FROM finding_history h JOIN findings f ON f.id=h.finding_id WHERE f.task_id=$1 AND h.kind='dispatched' AND h.occurred_at=$2`, task.ID, chosen).Scan(&journal); err != nil || updated != 1205 || journal != 1205 {
		t.Fatalf("actual rows=%d journal=%d: %v", updated, journal, err)
	}
	for _, id := range untouched {
		f, err := d.GetFinding(id)
		if err != nil || f.DispatchedAt != nil {
			t.Fatalf("source/other/unassigned finding %d was modified: %+v %v", id, f, err)
		}
	}
	f, err := d.GetFinding(already)
	if err != nil || f.DispatchedAt == nil || !f.DispatchedAt.Equal(oldTime) {
		t.Fatalf("existing dispatch replaced: %+v %v", f, err)
	}
	if got := taskDispatchNotificationCounts(t, d); got != notifications {
		t.Fatal("recording dispatch unexpectedly queued a notification")
	}
	before = taskDispatchFacts(t, d)
	retry, err := d.DispatchTaskFindings(ctx, task.ID, nil)
	if err != nil || retry.Recorded != 0 || retry.RecordedAt != nil || retry.AlreadyDispatched != 1206 || retry.SkippedStatus != 6 {
		t.Fatalf("duplicate dispatch: %+v %v", retry, err)
	}
	if taskDispatchFacts(t, d) != before {
		t.Fatal("duplicate dispatch changed time or appended history")
	}
}

func TestTaskFindingDispatchRequiresCompletedTask(t *testing.T) {
	d := taskDispatchDB(t)
	task := taskDispatchTask(t, d)
	taskDispatchFinding(t, d, task.ID, FindingPending, time.Now().Add(-time.Hour))
	for _, state := range []string{"running", "paused", "failed", "timeout"} {
		t.Run(state, func(t *testing.T) {
			persisted := state
			if state == "paused" {
				persisted = "running"
			}
			if err := d.SetStatus(task.ID, persisted); err != nil {
				t.Fatal(err)
			}
			if err := d.SetPaused(task.ID, state == "paused"); err != nil {
				t.Fatal(err)
			}
			before := taskDispatchFacts(t, d)
			p, err := d.PreviewTaskFindingDispatch(t.Context(), task.ID, nil)
			if err != nil || p.CanDispatch || p.Reason == "" {
				t.Fatalf("unfinished preview: %+v %v", p, err)
			}
			if _, err := d.DispatchTaskFindings(t.Context(), task.ID, nil); !errors.Is(err, ErrTaskNotCompleted) {
				t.Fatalf("unfinished dispatch accepted: %v", err)
			}
			if taskDispatchFacts(t, d) != before {
				t.Fatal("rejected task dispatch changed findings")
			}
		})
	}
	missing := task.ID + 1000000
	if _, err := d.PreviewTaskFindingDispatch(t.Context(), missing, nil); !errors.Is(err, ErrTaskNotFound) {
		t.Fatalf("missing preview: %v", err)
	}
	if _, err := d.DispatchTaskFindings(t.Context(), missing, nil); !errors.Is(err, ErrTaskNotFound) {
		t.Fatalf("missing dispatch: %v", err)
	}
}

func TestTaskFindingDispatchManualTimeRejectsWholeBatch(t *testing.T) {
	d := taskDispatchDB(t)
	task := taskDispatchTask(t, d)
	if err := d.SetStatus(task.ID, "done"); err != nil {
		t.Fatal(err)
	}
	base := time.Now().Add(-72 * time.Hour).Truncate(time.Second)
	taskDispatchFinding(t, d, task.ID, FindingPending, base)
	taskDispatchFinding(t, d, task.ID, FindingPending, base.Add(24*time.Hour))
	third := taskDispatchFinding(t, d, task.ID, FindingPending, base)
	fixed := base.Add(36 * time.Hour)
	if _, err := d.RecordFindingMilestone(t.Context(), third, "developer_fixed", &fixed); err != nil {
		t.Fatal(err)
	}
	for name, chosen := range map[string]time.Time{
		"before one discovery":      base.Add(12 * time.Hour),
		"after one repair feedback": base.Add(48 * time.Hour),
		"future":                    time.Now().Add(time.Hour),
	} {
		t.Run(name, func(t *testing.T) {
			before := taskDispatchFacts(t, d)
			preview, err := d.PreviewTaskFindingDispatch(t.Context(), task.ID, &chosen)
			if err != nil || preview.Eligible != 3 || preview.InvalidTime < 1 || preview.CanDispatch {
				t.Fatalf("invalid preview: %+v %v", preview, err)
			}
			if _, err := d.DispatchTaskFindings(t.Context(), task.ID, &chosen); !errors.Is(err, ErrInvalidFindingMilestoneTime) {
				t.Fatalf("invalid time accepted: %v", err)
			}
			if taskDispatchFacts(t, d) != before {
				t.Fatal("invalid batch partially changed facts or queued notifications")
			}
		})
	}
	valid := base.Add(30 * time.Hour)
	r, err := d.DispatchTaskFindings(t.Context(), task.ID, &valid)
	if err != nil || r.Recorded != 3 || r.RecordedAt == nil || !r.RecordedAt.Equal(valid) {
		t.Fatalf("valid shared time: %+v %v", r, err)
	}
}

func TestTaskFindingDispatchRecordsNewAttemptAfterRetestOrReopen(t *testing.T) {
	d := taskDispatchDB(t)
	ctx := t.Context()
	task := taskDispatchTask(t, d)
	if err := d.SetStatus(task.ID, "done"); err != nil {
		t.Fatal(err)
	}
	base := time.Now().Add(-72 * time.Hour).Truncate(time.Second)
	failed := taskDispatchFinding(t, d, task.ID, FindingConfirmed, base)
	reopened := taskDispatchFinding(t, d, task.ID, FindingConfirmed, base)
	oldTime := base.Add(12 * time.Hour)
	if _, err := d.DispatchTaskFindings(ctx, task.ID, &oldTime); err != nil {
		t.Fatal(err)
	}
	fixed := base.Add(24 * time.Hour)
	if _, err := d.RecordFindingMilestoneBatch(ctx, []int64{failed, reopened}, "developer_fixed", &fixed); err != nil {
		t.Fatal(err)
	}
	r, conversation, _, err := d.CreateFindingRetest(ctx, failed, "local reproduced result; no worker started")
	if err != nil {
		t.Fatal(err)
	}
	if started, err := d.StartFindingRetest(ctx, r.ID); err != nil || !started {
		t.Fatalf("start retest: %t %v", started, err)
	}
	if err := d.RecordFindingRetestResult(ctx, conversation.ID, "reproduced", "local verification", "same issue still present"); err != nil {
		t.Fatal(err)
	}
	if err := d.FinishFindingRetest(r.ID, "completed", ""); err != nil {
		t.Fatal(err)
	}
	for _, status := range []string{FindingFixed, FindingConfirmed} {
		if _, _, _, err := d.SetFindingStatusWithNotify(ctx, reopened, status); err != nil {
			t.Fatal(err)
		}
	}
	before := taskDispatchFacts(t, d)
	if _, err := d.DispatchTaskFindings(ctx, task.ID, &fixed); !errors.Is(err, ErrInvalidFindingMilestoneTime) {
		t.Fatalf("new attempt accepted time before reopen/retest: %v", err)
	}
	if taskDispatchFacts(t, d) != before {
		t.Fatal("invalid new-attempt time changed old history")
	}
	var maxHistory int64
	var historyBefore string
	if err := d.QueryRow(`SELECT COALESCE(max(id),0),md5(COALESCE(string_agg(to_jsonb(h)::text,',' ORDER BY id),'')) FROM finding_history h`).Scan(&maxHistory, &historyBefore); err != nil {
		t.Fatal(err)
	}
	notifications := taskDispatchNotificationCounts(t, d)
	var startedAt, finishedAt time.Time
	d.QueryRow(`SELECT now()`).Scan(&startedAt)
	result, err := d.DispatchTaskFindings(ctx, task.ID, nil)
	d.QueryRow(`SELECT now()`).Scan(&finishedAt)
	if err != nil || result.Recorded != 2 || result.RecordedAt == nil || result.RecordedAt.Before(startedAt) || result.RecordedAt.After(finishedAt) {
		t.Fatalf("new remediation attempts: %+v %v", result, err)
	}
	var historyAfter string
	if err := d.QueryRow(`SELECT md5(COALESCE(string_agg(to_jsonb(h)::text,',' ORDER BY id),'')) FROM finding_history h WHERE id<=$1`, maxHistory).Scan(&historyAfter); err != nil || historyAfter != historyBefore {
		t.Fatalf("prior rounds were rewritten: %v", err)
	}
	for _, id := range []int64{failed, reopened} {
		var dispatched, feedback int
		if err := d.QueryRow(`SELECT count(*) FILTER(WHERE kind='dispatched'),count(*) FILTER(WHERE kind='developer_fixed') FROM finding_history WHERE finding_id=$1`, id).Scan(&dispatched, &feedback); err != nil || dispatched != 2 || feedback != 1 {
			t.Fatalf("finding %d lost attempt history: dispatch=%d feedback=%d %v", id, dispatched, feedback, err)
		}
	}
	if taskDispatchNotificationCounts(t, d) != notifications {
		t.Fatal("task dispatch added notifications")
	}
	before = taskDispatchFacts(t, d)
	again, err := d.DispatchTaskFindings(ctx, task.ID, nil)
	if err != nil || again.Recorded != 0 || taskDispatchFacts(t, d) != before {
		t.Fatal(fmt.Sprintf("new attempt repeat is not idempotent: %+v %v", again, err))
	}
}
