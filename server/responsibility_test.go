package server

import (
	"strings"
	"testing"
)

func TestLegacyResponsibilityWritesRedirectToOrganization(t *testing.T) {
	s, finding, request := trafficEvidenceServer(t)
	for _, kind := range []string{"parties", "business-lines", "fallback", "apply-rules"} {
		response := request("PUT", "/api/exploration/responsibility/"+kind, `{}`)
		if response.Code != 409 || !strings.Contains(response.Body.String(), "/settings/org") {
			t.Fatalf("%s: %d %s", kind, response.Code, response.Body.String())
		}
	}
	row, err := s.m.pg.GetFinding(finding.FindingID)
	if err != nil || row == nil || row.BusinessLine != "" || row.Assignee != "" {
		t.Fatalf("legacy assignment changed: %+v %v", row, err)
	}
	response := request("GET", "/api/exploration/responsibility", "")
	if response.Code != 200 {
		t.Fatal(response.Code, response.Body.String())
	}
}
