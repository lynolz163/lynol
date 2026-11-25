package server

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/Autumn-27/artex/db"
)

func TestResponsibilityAPIFlow(t *testing.T) {
	s, finding, request := trafficEvidenceServer(t)
	response := request("PUT", "/api/exploration/responsibility/parties", `{"kind":"person","name":"API责任人","email":"owner@example.com","active":true}`)
	if response.Code != 200 {
		t.Fatal(response.Code, response.Body.String())
	}
	var saved struct {
		Result struct {
			Party db.ResponsibilityParty `json:"party"`
		} `json:"result"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &saved); err != nil {
		t.Fatal(err)
	}
	party := saved.Result.Party
	response = request("PUT", "/api/exploration/responsibility/business-lines", fmt.Sprintf(`{"name":"API业务线","code":"api-test-line","primary_id":%d,"active":true}`, party.ID))
	if response.Code != 200 {
		t.Fatal(response.Code, response.Body.String())
	}
	var lineResult struct {
		Result db.BusinessLine `json:"result"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &lineResult); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		s.m.pg.Exec(`DELETE FROM business_lines WHERE id=$1`, lineResult.Result.ID)
		s.m.pg.Exec(`DELETE FROM responsibility_parties WHERE id=$1`, party.ID)
	})
	response = request("PUT", "/api/exploration/findings/responsibility", fmt.Sprintf(`{"finding_ids":["%d"],"business_line":"API业务线","assignee":"API责任人"}`, finding.FindingID))
	if response.Code != 200 {
		t.Fatal(response.Code, response.Body.String())
	}
	response = request("GET", "/api/exploration/responsibility", "")
	var directory db.ResponsibilityDirectory
	if err := json.Unmarshal(response.Body.Bytes(), &directory); err != nil || response.Code != 200 {
		t.Fatal(response.Code, response.Body.String(), err)
	}
	for _, line := range directory.BusinessLines {
		if line.ID == lineResult.Result.ID && line.Open != 1 {
			t.Fatal("directory count", line.Open)
		}
	}
	response = request("GET", "/api/exploration/findings?business_line=API业务线&active_only=true&page=1&limit=20", "")
	var list struct {
		Total int `json:"total"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &list); err != nil || response.Code != 200 || list.Total != 1 {
		t.Fatal("filtered list", response.Code, response.Body.String(), err)
	}
	response = request("PUT", "/api/exploration/responsibility/business-lines", `{"name":"bad","code":"bad","active":true,"rules":[{"kind":"cidr","value":"127.0.0.1/99"}]}`)
	if response.Code != 400 {
		t.Fatal("invalid rule accepted", response.Code, response.Body.String())
	}
}
