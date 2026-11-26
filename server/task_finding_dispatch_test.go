package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Autumn-27/artex/db"
)

func TestTaskFindingDispatchAPIUsesTaskScopeAndChosenTime(t *testing.T) {
	if os.Getenv("ARTEX_PG_DSN") == "" {
		t.Skip("task dispatch integration test requires explicit ARTEX_PG_DSN")
	}
	s, finding, request := trafficEvidenceServer(t)
	f, err := s.m.pg.GetFinding(finding.FindingID)
	if err != nil || f.TaskID == nil {
		t.Fatalf("fixture task missing: %+v %v", f, err)
	}
	taskID := fmt.Sprint(*f.TaskID)
	path := "/api/tasks/" + taskID + "/findings/dispatch"
	base := time.Now().Add(-72 * time.Hour).Truncate(time.Second)
	if _, err := s.m.pg.Exec(`UPDATE findings SET created_at=$2 WHERE id=$1`, finding.FindingID, base); err != nil {
		t.Fatal(err)
	}
	if _, err := s.m.pg.Exec(`UPDATE finding_history SET occurred_at=$2 WHERE finding_id=$1 AND kind='discovered'`, finding.FindingID, base); err != nil {
		t.Fatal(err)
	}
	facts := func() string {
		t.Helper()
		var value string
		if err := s.m.pg.QueryRow(`SELECT concat_ws(':',
		 (SELECT md5(COALESCE(string_agg(to_jsonb(f)::text,',' ORDER BY id),'')) FROM findings f),
		 (SELECT md5(COALESCE(string_agg(to_jsonb(h)::text,',' ORDER BY id),'')) FROM finding_history h),
		 (SELECT count(*) FROM notification_events),(SELECT count(*) FROM notification_deliveries))`).Scan(&value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	for _, paused := range []bool{false, true} {
		if err := s.m.SetTaskPaused(taskID, paused); err != nil {
			t.Fatal(err)
		}
		before := facts()
		response := request("POST", path+"-preview", `{}`)
		var preview db.TaskFindingDispatchPreview
		if err := json.Unmarshal(response.Body.Bytes(), &preview); err != nil || response.Code != 200 || preview.CanDispatch || preview.Reason == "" {
			t.Fatalf("unfinished task preview paused=%t: %d %s %v", paused, response.Code, response.Body.String(), err)
		}
		response = request("POST", path, `{}`)
		if response.Code != http.StatusConflict || facts() != before {
			t.Fatalf("unfinished task changed: %d %s", response.Code, response.Body.String())
		}
	}
	if err := s.m.SetTaskPaused(taskID, false); err != nil {
		t.Fatal(err)
	}
	if err := s.m.SetTaskStatus(taskID, "done"); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{`"not-a-date"`, `"2026-01-01T00:00:00"`, fmt.Sprintf("%q", time.Now().Add(time.Hour).Format(time.RFC3339))} {
		before := facts()
		response := request("POST", path, fmt.Sprintf(`{"recorded_at":%s}`, raw))
		if response.Code != http.StatusBadRequest || facts() != before {
			t.Fatalf("invalid chosen time changed data: %d %s", response.Code, response.Body.String())
		}
	}
	for _, suffix := range []string{"", "-preview"} {
		response := request("POST", fmt.Sprintf("/api/tasks/%d/findings/dispatch%s", *f.TaskID+1000000, suffix), `{}`)
		if response.Code != http.StatusNotFound {
			t.Fatalf("unknown task: %d %s", response.Code, response.Body.String())
		}
	}
	unauthenticated := httptest.NewRecorder()
	s.Handler().ServeHTTP(unauthenticated, httptest.NewRequest("POST", path, strings.NewReader(`{}`)))
	if unauthenticated.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated task write: %d %s", unauthenticated.Code, unauthenticated.Body.String())
	}
	chosen := base.Add(24 * time.Hour)
	body := fmt.Sprintf(`{"recorded_at":%q}`, chosen.In(time.FixedZone("Beijing", 8*3600)).Format(time.RFC3339))
	before := facts()
	response := request("POST", path+"-preview", body)
	var preview db.TaskFindingDispatchPreview
	if err := json.Unmarshal(response.Body.Bytes(), &preview); err != nil || response.Code != 200 || preview.TaskID != taskID || preview.Total != 1 || preview.Eligible != 1 || !preview.CanDispatch {
		t.Fatalf("completed preview: %d %s %v", response.Code, response.Body.String(), err)
	}
	if facts() != before {
		t.Fatal("HTTP preview wrote persisted data")
	}
	var eventsBefore, deliveriesBefore int
	s.m.pg.QueryRow(`SELECT (SELECT count(*) FROM notification_events),(SELECT count(*) FROM notification_deliveries)`).Scan(&eventsBefore, &deliveriesBefore)
	response = request("POST", path, body)
	var result db.TaskFindingDispatchResult
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || response.Code != 200 || result.TaskID != taskID || result.Recorded != 1 || result.RecordedAt == nil || !result.RecordedAt.Equal(chosen) {
		t.Fatalf("chosen time: %d %s %v", response.Code, response.Body.String(), err)
	}
	before = facts()
	response = request("POST", path, `{}`)
	result = db.TaskFindingDispatchResult{}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || response.Code != 200 || result.Recorded != 0 || result.RecordedAt != nil || facts() != before {
		t.Fatalf("repeated task request: %d %s %v", response.Code, response.Body.String(), err)
	}
	newID, err := s.m.pg.AddFinding(*f.TaskID, 0, "TEST", "second local finding", "high", "", "", "test", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.m.pg.DeleteFinding(newID) })
	var start, finish time.Time
	s.m.pg.QueryRow(`SELECT now()`).Scan(&start)
	response = request("POST", path, `{}`)
	s.m.pg.QueryRow(`SELECT now()`).Scan(&finish)
	result = db.TaskFindingDispatchResult{}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || response.Code != 200 || result.Recorded != 1 || result.RecordedAt == nil || result.RecordedAt.Before(start) || result.RecordedAt.After(finish) {
		t.Fatalf("default current time: %d %s %v", response.Code, response.Body.String(), err)
	}
	var eventsAfter, deliveriesAfter int
	if err := s.m.pg.QueryRow(`SELECT (SELECT count(*) FROM notification_events),(SELECT count(*) FROM notification_deliveries)`).Scan(&eventsAfter, &deliveriesAfter); err != nil || eventsAfter != eventsBefore || deliveriesAfter != deliveriesBefore {
		t.Fatalf("dispatch queued notifications: events %d→%d deliveries %d→%d %v", eventsBefore, eventsAfter, deliveriesBefore, deliveriesAfter, err)
	}
}
