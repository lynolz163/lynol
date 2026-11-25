package server

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"
)

func TestFindingMilestoneTimeAPI(t *testing.T) {
	s, finding, request := trafficEvidenceServer(t)
	base := time.Now().Add(-72 * time.Hour).Truncate(time.Second)
	if _, err := s.m.pg.Exec(`UPDATE findings SET created_at=$2 WHERE id=$1`, finding.FindingID, base); err != nil {
		t.Fatal(err)
	}
	if _, err := s.m.pg.Exec(`UPDATE finding_history SET occurred_at=$2 WHERE finding_id=$1 AND kind='discovered'`, finding.FindingID, base); err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/api/exploration/findings/%d/milestones", finding.FindingID)
	for _, raw := range []string{`"not-a-time"`, `"2026-01-01T00:00:00"`, fmt.Sprintf("%q", time.Now().Add(time.Hour).Format(time.RFC3339))} {
		response := request("POST", path, fmt.Sprintf(`{"milestone":"dispatched","recorded_at":%s}`, raw))
		if response.Code != 400 {
			t.Fatalf("invalid timestamp accepted: %s %d", raw, response.Code)
		}
	}
	chosen := base.Add(24 * time.Hour)
	response := request("POST", path, fmt.Sprintf(`{"milestone":"dispatched","recorded_at":%q}`, chosen.In(time.FixedZone("Beijing", 8*3600)).Format(time.RFC3339)))
	var result struct {
		RecordedAt time.Time `json:"recorded_at"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || response.Code != 200 || !result.RecordedAt.Equal(chosen) {
		t.Fatalf("chosen time not preserved: %d %s %v", response.Code, response.Body.String(), err)
	}
	response = request("POST", "/api/exploration/findings/milestones", fmt.Sprintf(`{"finding_ids":["%d"],"milestone":"developer_fixed","recorded_at":%q}`, finding.FindingID, base.Add(12*time.Hour).Format(time.RFC3339)))
	if response.Code != 400 {
		t.Fatalf("invalid batch order accepted: %d %s", response.Code, response.Body.String())
	}
	var started, finished time.Time
	if err := s.m.pg.QueryRow(`SELECT now()`).Scan(&started); err != nil {
		t.Fatal(err)
	}
	response = request("POST", path, `{"milestone":"developer_fixed"}`)
	if err := s.m.pg.QueryRow(`SELECT now()`).Scan(&finished); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || response.Code != 200 || result.RecordedAt.Before(started) || result.RecordedAt.After(finished) {
		t.Fatalf("omitted timestamp no longer records current time: %d %s %v", response.Code, response.Body.String(), err)
	}
}
