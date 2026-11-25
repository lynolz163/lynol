package db

import (
	"encoding/json"
	"testing"

	"github.com/Autumn-27/artex/internal/org"
	"github.com/google/uuid"
)

func TestOrganizationFindingHistoryReturnsFrozenSnapshot(t *testing.T) {
	d, assets, _ := testSetup(t)
	defer d.Close()
	dep, domain, _ := organizationAssetRules(t, d)
	a, e := assets.UpsertRootDomain(UpsertRootDomainReq{Domain: domain})
	if e != nil {
		t.Fatal(e)
	}
	id, e := d.AddFinding(0, 0, "access_control", "快照字段", "high", "", "", "test-agent", []int64{a})
	if e != nil {
		t.Fatal(e)
	}
	history, e := d.FindingHistory(t.Context(), id, 0)
	if e != nil || len(history) != 1 {
		t.Fatalf("history %d %v", len(history), e)
	}
	h := history[0]
	if h.AssetID == nil || *h.AssetID != a || h.DepartmentID == nil || *h.DepartmentID != dep || h.AttributionVersion != 1 || h.AttributionQuality != "recorded" || h.SLADays == nil || *h.SLADays != 7 || h.RecordedAt.IsZero() {
		t.Fatalf("missing snapshot %+v", h)
	}
	raw, e := json.Marshal(h)
	if e != nil {
		t.Fatal(e)
	}
	var payload map[string]any
	if e = json.Unmarshal(raw, &payload); e != nil {
		t.Fatal(e)
	}
	for _, field := range []string{"asset_id", "department_id", "business_line_id", "owner_id", "sla_days", "recorded_at", "attribution_quality"} {
		if _, ok := payload[field]; !ok {
			t.Fatalf("missing JSON field %s", field)
		}
	}
}
func TestOrganizationArchiveRemapsPrimaryAssetAlongsideAssociations(t *testing.T) {
	tables := map[string]json.RawMessage{"findings": json.RawMessage(`[{"id":9,"asset_id":11,"asset_ids":[11,12]}]`), "exploration_anchors": json.RawMessage(`[]`), "task_asset_links": json.RawMessage(`[]`), "exploration_nodes": json.RawMessage(`[]`)}
	remapped, e := remapArchiveAssetReferences(tables, map[int64]int64{11: 21, 12: 22})
	if e != nil {
		t.Fatal(e)
	}
	var rows []struct {
		AssetID  int64   `json:"asset_id"`
		AssetIDs []int64 `json:"asset_ids"`
	}
	if e = json.Unmarshal(remapped["findings"], &rows); e != nil {
		t.Fatal(e)
	}
	if len(rows) != 1 || rows[0].AssetID != 21 || len(rows[0].AssetIDs) != 2 || rows[0].AssetIDs[0] != 21 || rows[0].AssetIDs[1] != 22 {
		t.Fatalf("remapped primary/links %+v", rows)
	}
}

func TestOrganizationFindingViewsAndFiltersShareCurrentAttribution(t *testing.T) {
	d, assets, _ := testSetup(t)
	defer d.Close()
	dep, domain, _ := organizationAssetRules(t, d)
	a, e := assets.UpsertRootDomain(UpsertRootDomainReq{Domain: domain})
	if e != nil {
		t.Fatal(e)
	}
	id, e := d.AddFinding(0, 0, "org-views-"+domain, "当前归属", "high", "", "", "test", []int64{a})
	if e != nil {
		t.Fatal(e)
	}
	// Legacy labels remain independent; the organization display does not overwrite them.
	if _, e = d.Exec(`UPDATE findings SET business_line='旧业务标签',assignee='旧处理人' WHERE id=$1`, id); e != nil {
		t.Fatal(e)
	}
	filter := FindingFilter{DepartmentID: &dep, Query: "org-views-" + domain}
	listed, total, e := d.ListFindingsPage(filter, 1, 20)
	if e != nil || total != 1 || len(listed) != 1 {
		t.Fatalf("list %d/%d %v", len(listed), total, e)
	}
	detail, e := d.GetFinding(id)
	if e != nil {
		t.Fatal(e)
	}
	exported, e := d.ListFindingsForExport(filter, nil)
	if e != nil || len(exported) != 1 {
		t.Fatalf("export %d %v", len(exported), e)
	}
	explicit, e := d.ListFindingsForExport(FindingFilter{}, []int64{id})
	if e != nil || len(explicit) != 1 {
		t.Fatalf("explicit export %d %v", len(explicit), e)
	}
	for _, v := range []*DBFinding{listed[0], detail, exported[0], explicit[0]} {
		var attribution struct {
			AssetID      int64  `json:"asset_id"`
			DepartmentID int64  `json:"department_id"`
			Quality      string `json:"attribution_quality"`
		}
		if e = json.Unmarshal(v.OrgAttribution, &attribution); e != nil || attribution.AssetID != a || attribution.DepartmentID != dep || attribution.Quality != "recorded" {
			t.Fatalf("view attribution %s: %v", v.OrgAttribution, e)
		}
		if v.BusinessLine != "旧业务标签" || v.Assignee != "旧处理人" {
			t.Fatalf("legacy fields changed %+v", v)
		}
	}
	zero := int64(0)
	filter.DepartmentID = &zero
	if _, total, e = d.ListFindingsPage(filter, 1, 20); e != nil || total != 0 {
		t.Fatalf("unassigned department filter %d %v", total, e)
	}
	filter.DepartmentID = &dep
	filter.OrgBusinessLineID = &zero
	filter.OwnerID = nil
	if _, total, e = d.ListFindingsPage(filter, 1, 20); e != nil || total != 1 {
		t.Fatalf("department-only filter %d %v", total, e)
	}
	wrong := int64(-987654)
	filter.OwnerID = &wrong
	if _, total, e = d.ListFindingsPage(filter, 1, 20); e != nil || total != 0 {
		t.Fatalf("mixed filters %d %v", total, e)
	}
}

func TestOrganizationDirectoryOpenCountsMatchFindingLinksWithoutPrimaryAsset(t *testing.T) {
	d, assets, _ := testSetup(t)
	defer d.Close()
	ctx := t.Context()
	store := org.New(d.DB)
	dep, domain, _ := organizationAssetRules(t, d)
	save := func(kind string, id, revision int64, data any) org.ChangeResult {
		t.Helper()
		raw, _ := json.Marshal(data)
		result, err := store.Change(ctx, org.Command{Kind: kind, Action: "save", ID: id, Revision: revision, Data: raw, Scope: "all_open", ConfirmImpact: true, IdempotencyKey: uuid.NewString()})
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	person := save("person", 0, 0, org.Person{Name: "列表计数兜底", Email: uuid.NewString() + "@example.invalid", DepartmentID: &dep, Active: true})
	directory, err := store.Directory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	save("fallback", 1, directory.ConfigRevision, map[string]int64{"owner_id": person.EntityID})
	line := save("business_line", 0, 0, org.BusinessLine{Name: "链接计数-" + uuid.NewString(), Code: uuid.NewString(), DepartmentID: dep, Active: true})
	asset, err := assets.UpsertRootDomain(UpsertRootDomainReq{Domain: domain})
	if err != nil {
		t.Fatal(err)
	}
	other, err := assets.UpsertRootDomain(UpsertRootDomainReq{Domain: uuid.NewString() + ".example.invalid"})
	if err != nil {
		t.Fatal(err)
	}
	preview, err := store.PreviewAssignment(ctx, org.AssignmentRequest{Selection: org.AssignmentSelection{Mode: "ids", IDs: []int64{asset}, ExpectedCount: 1}, DepartmentID: dep, BusinessLineID: &line.EntityID, Scope: "all_open"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.ConfirmAssignment(ctx, org.AssignmentConfirm{Token: preview.Token, IdempotencyKey: uuid.NewString(), OverwriteConfirmed: true}); err != nil {
		t.Fatal(err)
	}
	known, err := d.AddFinding(0, 0, "count-known", "已确认主资产", "high", "", "", "test", []int64{asset})
	if err != nil {
		t.Fatal(err)
	}
	unknown, err := d.AddFinding(0, 0, "count-unknown", "待确认主资产", "high", "", "", "test", []int64{asset, other})
	if err != nil {
		t.Fatal(err)
	}
	closed, err := d.AddFinding(0, 0, "count-closed", "已关闭", "high", "", "", "test", []int64{asset})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = d.SetFindingStatus(closed, FindingFixed); err != nil {
		t.Fatal(err)
	}
	zero := int64(0)
	assertCounts := func(wantDepartment int64, unknownIncluded bool) {
		t.Helper()
		dir, e := store.Directory(ctx)
		if e != nil {
			t.Fatal(e)
		}
		var depOpen, lineOpen, personOpen int64
		for _, v := range dir.Departments {
			if v.ID == dep {
				depOpen = v.Open
			}
		}
		for _, v := range dir.BusinessLines {
			if v.ID == line.EntityID {
				lineOpen = v.Open
			}
		}
		for _, v := range dir.People {
			if v.ID == person.EntityID {
				personOpen = v.Open
			}
		}
		if depOpen != wantDepartment || lineOpen != wantDepartment {
			t.Fatalf("directory counts department=%d line=%d want %d", depOpen, lineOpen, wantDepartment)
		}
		for _, check := range []struct {
			name   string
			filter FindingFilter
			count  int64
		}{
			{"department", FindingFilter{DepartmentID: &dep, ActiveOnly: true}, depOpen},
			{"business_line", FindingFilter{OrgBusinessLineID: &line.EntityID, ActiveOnly: true}, lineOpen},
			{"owner", FindingFilter{OwnerID: &person.EntityID, ActiveOnly: true}, personOpen},
			{"unassigned", FindingFilter{DepartmentID: &zero, ActiveOnly: true}, dir.UnassignedOpen},
		} {
			rows, total, e := d.ListFindingsPage(check.filter, 1, 10000)
			if e != nil {
				t.Fatal(e)
			}
			if int64(total) != check.count {
				t.Fatalf("%s directory=%d listing=%d", check.name, check.count, total)
			}
			if check.name == "owner" {
				foundKnown, foundUnknown, foundClosed := false, false, false
				for _, v := range rows {
					foundKnown = foundKnown || v.ID == known
					foundUnknown = foundUnknown || v.ID == unknown
					foundClosed = foundClosed || v.ID == closed
				}
				if !foundKnown || foundUnknown != unknownIncluded || foundClosed {
					t.Fatalf("owner rows known=%v unknown=%v closed=%v", foundKnown, foundUnknown, foundClosed)
				}
			}
		}
	}
	assertCounts(1, false)
	view, err := store.FindingAsset(ctx, unknown)
	if err != nil || !view.NeedsConfirmation {
		t.Fatalf("multi-asset binding unexpectedly chosen %+v %v", view, err)
	}
	if _, err = store.SetFindingAsset(ctx, unknown, asset, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	assertCounts(2, true)
}
