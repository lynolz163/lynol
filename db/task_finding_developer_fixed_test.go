package db

import (
	"errors"
	"testing"
	"time"
)

func TestTaskFindingDeveloperFixedFullScopePreservesExistingAndSkipsOtherTasks(t *testing.T) {
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
	if _, err := d.RecordFindingMilestone(ctx, already, "developer_fixed", &oldTime); err != nil {
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
	preview, err := d.PreviewTaskFindingDeveloperFixed(ctx, task.ID, &chosen)
	if err != nil || preview.Total != 1212 || preview.Eligible != 1205 || preview.AlreadyRecorded != 1 || preview.SkippedStatus != 6 || preview.InvalidTime != 0 || !preview.CanRecord || preview.SeverityCounts["high"] != 1205 {
		t.Fatalf("full task preview: %+v, %v", preview, err)
	}
	if after := taskDispatchFacts(t, d); after != before {
		t.Fatal("preview changed persisted facts or notification queues")
	}
	notifications := taskDispatchNotificationCounts(t, d)
	var unchangedBefore string
	if err := d.QueryRow(`SELECT concat_ws(':',
	 (SELECT md5(string_agg(id::text||':'||status||':'||COALESCE(dispatched_at::text,''),',' ORDER BY id)) FROM findings WHERE task_id=$1),
	 (SELECT count(*) FROM finding_retests))`, task.ID).Scan(&unchangedBefore); err != nil {
		t.Fatal(err)
	}
	result, err := d.RecordTaskFindingDeveloperFixed(ctx, task.ID, &chosen)
	if err != nil || result.Recorded != 1205 || result.Skipped != 7 || result.AlreadyRecorded != 1 || result.SkippedStatus != 6 || result.RecordedAt == nil || !result.RecordedAt.Equal(chosen) {
		t.Fatalf("dispatch: %+v, %v", result, err)
	}
	var updated, journal int
	if err := d.QueryRow(`SELECT count(*) FROM findings WHERE task_id=$1 AND developer_fixed_at=$2`, task.ID, chosen).Scan(&updated); err != nil {
		t.Fatal(err)
	}
	if err := d.QueryRow(`SELECT count(*) FROM finding_history h JOIN findings f ON f.id=h.finding_id WHERE f.task_id=$1 AND h.kind='developer_fixed' AND h.occurred_at=$2`, task.ID, chosen).Scan(&journal); err != nil || updated != 1205 || journal != 1205 {
		t.Fatalf("actual rows=%d journal=%d: %v", updated, journal, err)
	}
	for _, id := range untouched {
		f, err := d.GetFinding(id)
		if err != nil || f.DeveloperFixedAt != nil {
			t.Fatalf("source/other/unassigned finding %d was modified: %+v %v", id, f, err)
		}
	}
	f, err := d.GetFinding(already)
	if err != nil || f.DeveloperFixedAt == nil || !f.DeveloperFixedAt.Equal(oldTime) {
		t.Fatalf("existing dispatch replaced: %+v %v", f, err)
	}
	var unchangedAfter string
	if err := d.QueryRow(`SELECT concat_ws(':',
	 (SELECT md5(string_agg(id::text||':'||status||':'||COALESCE(dispatched_at::text,''),',' ORDER BY id)) FROM findings WHERE task_id=$1),
	 (SELECT count(*) FROM finding_retests))`, task.ID).Scan(&unchangedAfter); err != nil || unchangedAfter != unchangedBefore {
		t.Fatalf("feedback changed finding status, dispatch, closure or retest count: %v", err)
	}
	if got := taskDispatchNotificationCounts(t, d); got != notifications {
		t.Fatal("recording dispatch unexpectedly queued a notification")
	}
	before = taskDispatchFacts(t, d)
	retry, err := d.RecordTaskFindingDeveloperFixed(ctx, task.ID, nil)
	if err != nil || retry.Recorded != 0 || retry.RecordedAt != nil || retry.AlreadyRecorded != 1206 || retry.SkippedStatus != 6 {
		t.Fatalf("duplicate dispatch: %+v %v", retry, err)
	}
	if taskDispatchFacts(t, d) != before {
		t.Fatal("duplicate dispatch changed time or appended history")
	}
}

func TestTaskFindingDeveloperFixedRecordsThreeRoundsAndReopen(t *testing.T) {
	d := taskDispatchDB(t)
	ctx := t.Context()
	task := taskDispatchTask(t, d)
	if err := d.SetStatus(task.ID, "done"); err != nil {
		t.Fatal(err)
	}
	base := time.Now().Add(-72 * time.Hour).Truncate(time.Second)
	id := taskDispatchFinding(t, d, task.ID, FindingConfirmed, base)
	firstFeedback := base.Add(12 * time.Hour)
	var oldHistoryID int64
	var oldHistoryDigest string
	for round, verdict := range []string{"reproduced", "reproduced", "fixed"} {
		var chosen *time.Time
		if round == 0 {
			chosen = &firstFeedback
		} else {
			before := taskDispatchFacts(t, d)
			invalid, err := d.PreviewTaskFindingDeveloperFixed(ctx, task.ID, &firstFeedback)
			if err != nil || invalid.Eligible != 1 || invalid.InvalidTime != 1 || invalid.CanRecord {
				t.Fatalf("round %d invalid prior-cycle time: %+v %v", round+1, invalid, err)
			}
			if _, err := d.RecordTaskFindingDeveloperFixed(ctx, task.ID, &firstFeedback); !errors.Is(err, ErrInvalidFindingMilestoneTime) || taskDispatchFacts(t, d) != before {
				t.Fatalf("round %d accepted old time or changed facts: %v", round+1, err)
			}
		}
		preview, err := d.PreviewTaskFindingDeveloperFixed(ctx, task.ID, chosen)
		if err != nil || preview.Eligible != 1 || !preview.CanRecord {
			t.Fatalf("round %d feedback preview: %+v %v", round+1, preview, err)
		}
		notifications := taskDispatchNotificationCounts(t, d)
		var start, finish time.Time
		if err := d.QueryRow(`SELECT now()`).Scan(&start); err != nil {
			t.Fatal(err)
		}
		result, err := d.RecordTaskFindingDeveloperFixed(ctx, task.ID, chosen)
		if err != nil || result.Recorded != 1 || result.RecordedAt == nil {
			t.Fatalf("round %d feedback: %+v %v", round+1, result, err)
		}
		if err := d.QueryRow(`SELECT now()`).Scan(&finish); err != nil {
			t.Fatal(err)
		}
		if chosen == nil && (result.RecordedAt.Before(start) || result.RecordedAt.After(finish)) {
			t.Fatalf("round %d current timestamp outside transaction: %v", round+1, result.RecordedAt)
		}
		f, err := d.GetFinding(id)
		if err != nil || f.Status != FindingConfirmed || f.DispatchedAt != nil || f.VerifiedFixedAt != nil || f.DeveloperFixedAt == nil || !f.DeveloperFixedAt.Equal(*result.RecordedAt) {
			t.Fatalf("round %d feedback changed closure/status or required dispatch: %+v %v", round+1, f, err)
		}
		var retestCount int
		if err := d.QueryRow(`SELECT count(*) FROM finding_retests WHERE finding_id=$1`, id).Scan(&retestCount); err != nil || retestCount != round || taskDispatchNotificationCounts(t, d) != notifications {
			t.Fatalf("feedback started a retest or queued a notification: retests=%d %v", retestCount, err)
		}
		if oldHistoryID > 0 {
			var digest string
			if err := d.QueryRow(`SELECT md5(COALESCE(string_agg(to_jsonb(h)::text,',' ORDER BY id),'')) FROM finding_history h WHERE finding_id=$1 AND id<=$2`, id, oldHistoryID).Scan(&digest); err != nil || digest != oldHistoryDigest {
				t.Fatalf("round %d rewrote previous journal rows: %v", round+1, err)
			}
		}
		before := taskDispatchFacts(t, d)
		again, err := d.RecordTaskFindingDeveloperFixed(ctx, task.ID, nil)
		if err != nil || again.Recorded != 0 || again.AlreadyRecorded != 1 || taskDispatchFacts(t, d) != before {
			t.Fatalf("round %d retry changed current feedback/history: %+v %v", round+1, again, err)
		}
		r, conversation, _, err := d.CreateFindingRetest(ctx, id, "local multi-round fixture; no worker")
		if err != nil {
			t.Fatal(err)
		}
		if started, err := d.StartFindingRetest(ctx, r.ID); err != nil || !started {
			t.Fatalf("round %d retest start: %t %v", round+1, started, err)
		}
		if err := d.RecordFindingRetestResult(ctx, conversation.ID, verdict, "local verification", "fixture evidence"); err != nil {
			t.Fatal(err)
		}
		f, err = d.GetFinding(id)
		if err != nil || f.Status != FindingConfirmed || f.VerifiedFixedAt != nil {
			t.Fatalf("round %d staged verdict closed finding before completion: %+v %v", round+1, f, err)
		}
		if err := d.FinishFindingRetest(r.ID, "completed", ""); err != nil {
			t.Fatal(err)
		}
		if err := d.QueryRow(`SELECT max(id),md5(COALESCE(string_agg(to_jsonb(h)::text,',' ORDER BY id),'')) FROM finding_history h WHERE finding_id=$1`, id).Scan(&oldHistoryID, &oldHistoryDigest); err != nil {
			t.Fatal(err)
		}
	}
	f, err := d.GetFinding(id)
	if err != nil || f.Status != FindingFixed || f.VerifiedFixedAt == nil {
		t.Fatalf("third retest did not complete closure: %+v %v", f, err)
	}
	var feedback, requested, started, completed int
	if err := d.QueryRow(`SELECT count(*) FILTER(WHERE kind='developer_fixed'),count(*) FILTER(WHERE kind='retest_requested'),
	 count(*) FILTER(WHERE kind='retest_started'),count(*) FILTER(WHERE kind='retest_completed') FROM finding_history WHERE finding_id=$1`, id).Scan(&feedback, &requested, &started, &completed); err != nil || feedback != 3 || requested != 3 || started != 3 || completed != 3 {
		t.Fatalf("three rounds not preserved: feedback=%d requested=%d started=%d completed=%d %v", feedback, requested, started, completed, err)
	}
	if _, _, _, err := d.SetFindingStatusWithNotify(ctx, id, FindingConfirmed); err != nil {
		t.Fatal(err)
	}
	p, err := d.PreviewTaskFindingDeveloperFixed(ctx, task.ID, nil)
	if err != nil || p.Eligible != 1 || !p.CanRecord {
		t.Fatalf("reopened finding not eligible: %+v %v", p, err)
	}
	notifications := taskDispatchNotificationCounts(t, d)
	r, err := d.RecordTaskFindingDeveloperFixed(ctx, task.ID, nil)
	if err != nil || r.Recorded != 1 || taskDispatchNotificationCounts(t, d) != notifications {
		t.Fatalf("reopened finding feedback: %+v %v", r, err)
	}
	var digest string
	if err := d.QueryRow(`SELECT md5(COALESCE(string_agg(to_jsonb(h)::text,',' ORDER BY id),'')) FROM finding_history h WHERE finding_id=$1 AND id<=$2`, id, oldHistoryID).Scan(&digest); err != nil || digest != oldHistoryDigest {
		t.Fatalf("reopen feedback rewrote three prior rounds: %v", err)
	}
}

func TestTaskFindingDeveloperFixedRequiresCompletedTask(t *testing.T) {
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
			p, err := d.PreviewTaskFindingDeveloperFixed(t.Context(), task.ID, nil)
			if err != nil || p.CanRecord || p.Reason == "" {
				t.Fatalf("unfinished preview: %+v %v", p, err)
			}
			if _, err := d.RecordTaskFindingDeveloperFixed(t.Context(), task.ID, nil); !errors.Is(err, ErrTaskDeveloperFixedNotCompleted) {
				t.Fatalf("unfinished dispatch accepted: %v", err)
			}
			if taskDispatchFacts(t, d) != before {
				t.Fatal("rejected task dispatch changed findings")
			}
		})
	}
	missing := task.ID + 1000000
	if _, err := d.PreviewTaskFindingDeveloperFixed(t.Context(), missing, nil); !errors.Is(err, ErrTaskNotFound) {
		t.Fatalf("missing preview: %v", err)
	}
	if _, err := d.RecordTaskFindingDeveloperFixed(t.Context(), missing, nil); !errors.Is(err, ErrTaskNotFound) {
		t.Fatalf("missing dispatch: %v", err)
	}
}

func TestTaskFindingDeveloperFixedManualTimeRejectsWholeBatch(t *testing.T) {
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
	if _, err := d.RecordFindingMilestone(t.Context(), third, "dispatched", &fixed); err != nil {
		t.Fatal(err)
	}
	for name, chosen := range map[string]time.Time{
		"before one discovery": base.Add(12 * time.Hour),
		"before one dispatch":  base.Add(30 * time.Hour),
		"future":               time.Now().Add(time.Hour),
	} {
		t.Run(name, func(t *testing.T) {
			before := taskDispatchFacts(t, d)
			preview, err := d.PreviewTaskFindingDeveloperFixed(t.Context(), task.ID, &chosen)
			if err != nil || preview.Eligible != 3 || preview.InvalidTime < 1 || preview.CanRecord {
				t.Fatalf("invalid preview: %+v %v", preview, err)
			}
			if _, err := d.RecordTaskFindingDeveloperFixed(t.Context(), task.ID, &chosen); !errors.Is(err, ErrInvalidFindingMilestoneTime) {
				t.Fatalf("invalid time accepted: %v", err)
			}
			if taskDispatchFacts(t, d) != before {
				t.Fatal("invalid batch partially changed facts or queued notifications")
			}
		})
	}
	valid := base.Add(48 * time.Hour)
	r, err := d.RecordTaskFindingDeveloperFixed(t.Context(), task.ID, &valid)
	if err != nil || r.Recorded != 3 || r.RecordedAt == nil || !r.RecordedAt.Equal(valid) {
		t.Fatalf("valid shared time: %+v %v", r, err)
	}
}
