package org

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestRuleMatchingPrioritySpecificityAndConflict(t *testing.T) {
	rule := func(id, dep int64, kind, value string, priority int) Rule {
		return Rule{ID: id, Revision: 1, MatchType: kind, MatchValue: value, DepartmentID: dep, Priority: priority, Active: true}
	}
	tests := []struct {
		name   string
		asset  RuleAsset
		rules  []Rule
		winner int64
		status string
	}{
		{"higher priority precedes exactness", RuleAsset{Domains: []string{"api.core.example.com"}}, []Rule{rule(1, 1, "domain", "api.core.example.com", 1), rule(2, 2, "domain", "*.example.com", 2)}, 2, "matched"},
		{"exact domain beats wildcard", RuleAsset{Domains: []string{"api.core.example.com"}}, []Rule{rule(1, 1, "domain", "api.core.example.com", 1), rule(2, 2, "domain", "*.example.com", 1)}, 1, "matched"},
		{"deeper wildcard suffix wins", RuleAsset{Domains: []string{"api.core.example.com"}}, []Rule{rule(1, 1, "domain", "*.example.com", 1), rule(2, 2, "domain", "*.core.example.com", 1)}, 2, "matched"},
		{"wildcard excludes apex", RuleAsset{Domains: []string{"example.com"}}, []Rule{rule(1, 1, "domain", "*.example.com", 1)}, 0, "unmatched"},
		{"suffix boundary is a domain label", RuleAsset{Domains: []string{"badexample.com"}}, []Rule{rule(1, 1, "domain", "*.example.com", 1)}, 0, "unmatched"},
		{"ipv4 smaller network wins", RuleAsset{IP: "192.0.2.9"}, []Rule{rule(1, 1, "ip_cidr", "192.0.0.0/16", 1), rule(2, 2, "ip_cidr", "192.0.2.0/24", 1)}, 2, "matched"},
		{"ipv6 smaller network wins", RuleAsset{IP: "2001:db8:1::9"}, []Rule{rule(1, 1, "ip_cidr", "2001:db8::/32", 1), rule(2, 2, "ip_cidr", "2001:db8:1::/48", 1)}, 2, "matched"},
		{"ipv4 outside network", RuleAsset{IP: "198.51.100.2"}, []Rule{rule(1, 1, "ip_cidr", "192.0.2.0/24", 1)}, 0, "unmatched"},
		{"ipv6 outside network", RuleAsset{IP: "2001:db9::1"}, []Rule{rule(1, 1, "ip_cidr", "2001:db8::/32", 1)}, 0, "unmatched"},
		{"equal domain and cidr are incomparable", RuleAsset{Domains: []string{"api.example.com"}, IP: "192.0.2.9"}, []Rule{rule(1, 1, "domain", "api.example.com", 1), rule(2, 2, "ip_cidr", "192.0.2.9/32", 1)}, 0, "conflict"},
		{"explicit priority resolves cross type", RuleAsset{Domains: []string{"api.example.com"}, IP: "192.0.2.9"}, []Rule{rule(1, 1, "domain", "api.example.com", 2), rule(2, 2, "ip_cidr", "192.0.2.9/32", 1)}, 1, "matched"},
		{"same exactness different targets conflict", RuleAsset{Domains: []string{"api.example.com"}}, []Rule{rule(1, 1, "domain", "api.example.com", 1), rule(2, 2, "domain", "api.example.com", 1)}, 0, "conflict"},
		{"different exact hosts have equal precision", RuleAsset{Domains: []string{"example.com", "api.core.example.com"}}, []Rule{rule(1, 1, "domain", "example.com", 1), rule(2, 2, "domain", "api.core.example.com", 1)}, 0, "conflict"},
		{"same cidr different targets conflict", RuleAsset{IP: "192.0.2.9"}, []Rule{rule(1, 1, "ip_cidr", "192.0.2.0/24", 1), rule(2, 2, "ip_cidr", "192.0.2.0/24", 1)}, 0, "conflict"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := MatchRules(tt.asset, tt.rules)
			if got.Status != tt.status {
				t.Fatalf("got %+v want status=%s", got, tt.status)
			}
			if tt.winner > 0 && (got.RuleID == nil || *got.RuleID != tt.winner) {
				t.Fatalf("winner %+v want %d", got, tt.winner)
			}
			if tt.status == "conflict" && (got.RuleID != nil || got.DepartmentID != nil) {
				t.Fatalf("conflict silently attributed: %+v", got)
			}
			if got.Reason == "" {
				t.Fatal("decision has no explanation")
			}
		})
	}
	same := []Rule{rule(1, 9, "domain", "api.example.com", 1), rule(2, 9, "ip_cidr", "192.0.2.9/32", 1)}
	got := MatchRules(RuleAsset{Domains: []string{"api.example.com"}, IP: "192.0.2.9"}, same)
	if got.Status != "matched" || got.DepartmentID == nil || *got.DepartmentID != 9 {
		t.Fatalf("same target candidates should merge: %+v", got)
	}
	same[0].Active = false
	same[1].Active = false
	if got = MatchRules(RuleAsset{Domains: []string{"api.example.com"}, IP: "192.0.2.9"}, same); got.Status != "unmatched" {
		t.Fatalf("disabled rules matched: %+v", got)
	}
}

func TestRuleValidationNormalizesInputs(t *testing.T) {
	cases := []struct{ kind, input, want string }{
		{"domain", " *.EXAMPLE.COM. ", "*.example.com"},
		{"ip_cidr", "192.0.2.99/24", "192.0.2.0/24"},
		{"ip_cidr", "2001:db8:1::99/48", "2001:db8:1::/48"},
		{"ip_cidr", "192.0.2.9", "192.0.2.9/32"},
		{"ip_cidr", "2001:db8::9", "2001:db8::9/128"},
	}
	for _, tt := range cases {
		t.Run(tt.input, func(t *testing.T) {
			r := Rule{MatchType: tt.kind, MatchValue: tt.input, DepartmentID: 1, Active: true}
			if err := ValidateRule(&r); err != nil {
				t.Fatal(err)
			}
			if r.MatchValue != tt.want {
				t.Fatalf("got %q want %q", r.MatchValue, tt.want)
			}
		})
	}
	for _, value := range []string{"*example.com", "foo.*.example.com", "https://example.com", "foo..example.com", "example.com/path"} {
		r := Rule{MatchType: "domain", MatchValue: value, DepartmentID: 1, Active: true}
		if err := ValidateRule(&r); err == nil {
			t.Errorf("accepted malformed domain %q", value)
		}
	}
	for _, value := range []string{"192.0.2.9/33", "2001:db8::9/129", "999.1.1.1/24", "example.com"} {
		r := Rule{MatchType: "ip_cidr", MatchValue: value, DepartmentID: 1, Active: true}
		if err := ValidateRule(&r); err == nil {
			t.Errorf("accepted malformed IP/CIDR %q", value)
		}
	}
}

// testRuleAsset inserts a synthetic upstream asset without a registry entry. The
// same-transaction synchronization test controls the transaction explicitly.
func testRuleAsset(t *testing.T, s *Store, ctx context.Context, domain, ip string) int64 {
	t.Helper()
	var id int64
	err := s.DB.QueryRowContext(ctx, `INSERT INTO public.assets(type,domain,ip,root_domain) VALUES('subdomain',$1,NULLIF($2,''),'example.invalid') RETURNING id`, domain, ip).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	return id
}
func testSyncRuleAsset(t *testing.T, s *Store, ctx context.Context, id int64) {
	t.Helper()
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err = SyncAssetTx(ctx, tx, id); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
}
func testRuleSnapshot(t *testing.T, s *Store, ctx context.Context) string {
	t.Helper()
	var digest string
	err := s.DB.QueryRowContext(ctx, `SELECT md5(jsonb_build_array(
 (SELECT COALESCE(jsonb_agg(to_jsonb(t) ORDER BY asset_id),'[]') FROM org.asset_registry t),
 (SELECT COALESCE(jsonb_agg(to_jsonb(t) ORDER BY id),'[]') FROM org.asset_ownership t),
 (SELECT COALESCE(jsonb_agg(to_jsonb(t) ORDER BY id),'[]') FROM org.attribution_rule t),
 (SELECT COALESCE(jsonb_agg(to_jsonb(t) ORDER BY id),'[]') FROM org.attribution_rule_revision t),
 (SELECT COALESCE(jsonb_agg(to_jsonb(t) ORDER BY id),'[]') FROM org.audit_log t),
 (SELECT COALESCE(jsonb_agg(to_jsonb(t) ORDER BY id),'[]') FROM org.change_operation t))::text)`).Scan(&digest)
	if err != nil {
		t.Fatal(err)
	}
	return digest
}
func testSavedRule(t *testing.T, s *Store, ctx context.Context, r Rule) Rule {
	t.Helper()
	if _, err := s.SaveRule(ctx, r, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	var id, revision, revisionID int64
	err := s.DB.QueryRowContext(ctx, `SELECT r.id,r.revision,h.id FROM org.attribution_rule r JOIN org.attribution_rule_revision h ON h.rule_id=r.id WHERE h.valid_to IS NULL AND h.voided_at IS NULL AND h.department_id=$1 AND COALESCE(h.domain_pattern,h.ip_cidr::text)=$2 ORDER BY r.id DESC LIMIT 1`, r.DepartmentID, r.MatchValue).Scan(&id, &revision, &revisionID)
	if err != nil {
		t.Fatal(err)
	}
	r.ID = id
	r.Revision = revision
	r.RevisionID = revisionID
	return r
}
func testCurrentRuleOwner(t *testing.T, s *Store, ctx context.Context, id int64) (dep int64, source string, records int) {
	t.Helper()
	err := s.DB.QueryRowContext(ctx, `SELECT COALESCE((SELECT department_id FROM org.asset_ownership WHERE asset_id=$1 AND valid_to IS NULL AND voided_at IS NULL ORDER BY id DESC LIMIT 1),0),COALESCE((SELECT source FROM org.asset_ownership WHERE asset_id=$1 AND valid_to IS NULL AND voided_at IS NULL ORDER BY id DESC LIMIT 1),''),(SELECT count(*) FROM org.asset_ownership WHERE asset_id=$1)`, id).Scan(&dep, &source, &records)
	if err != nil {
		t.Fatal(err)
	}
	return
}
func testApplyRules(t *testing.T, s *Store, ctx context.Context, reapply bool) {
	t.Helper()
	d, err := s.Directory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ApplyRules(ctx, reapply, "all_open", d.ConfigRevision, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
}

func TestRulePreviewIsReadonlyAndSaveDoesNotAssignExistingAssets(t *testing.T) {
	s, ctx := testOrgStore(t)
	dep := testDepartment(t, s, ctx)
	domain := uuid.NewString() + ".rules.example.invalid"
	asset := testRuleAsset(t, s, ctx, domain, "")
	testSyncRuleAsset(t, s, ctx, asset)
	r := Rule{MatchType: "domain", MatchValue: domain, DepartmentID: dep.EntityID, Priority: 10, Active: true}
	before := testRuleSnapshot(t, s, ctx)
	if _, err := s.PreviewRule(ctx, r); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApplyRulesPreview(ctx, false, "all_open"); err != nil {
		t.Fatal(err)
	}
	if got := testRuleSnapshot(t, s, ctx); got != before {
		t.Fatal("read-only preview wrote registry, rules, ownership or audit")
	}
	key := uuid.NewString()
	first, err := s.SaveRule(ctx, r, key)
	if err != nil {
		t.Fatal(err)
	}
	afterSave := testRuleSnapshot(t, s, ctx)
	again, err := s.SaveRule(ctx, r, key)
	if err != nil {
		t.Fatal(err)
	}
	firstJSON, _ := json.Marshal(first)
	againJSON, _ := json.Marshal(again)
	if string(firstJSON) != string(againJSON) || testRuleSnapshot(t, s, ctx) != afterSave {
		t.Fatal("same save key created additional rule state")
	}
	if got, _, count := testCurrentRuleOwner(t, s, ctx, asset); got != 0 || count != 0 {
		t.Fatal("saving rule silently assigned an existing asset")
	}
	before = testRuleSnapshot(t, s, ctx)
	if _, err = s.ApplyRulesPreview(ctx, false, "all_open"); err != nil {
		t.Fatal(err)
	}
	if testRuleSnapshot(t, s, ctx) != before {
		t.Fatal("application preview wrote data")
	}
	d, err := s.Directory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	applyKey := uuid.NewString()
	if _, err = s.ApplyRules(ctx, false, "all_open", d.ConfigRevision, applyKey); err != nil {
		t.Fatal(err)
	}
	if got, source, count := testCurrentRuleOwner(t, s, ctx, asset); got != dep.EntityID || source != "rule" || count != 1 {
		t.Fatalf("assignment got department=%d source=%s records=%d", got, source, count)
	}
	afterApply := testRuleSnapshot(t, s, ctx)
	if _, err = s.ApplyRules(ctx, false, "all_open", d.ConfigRevision, applyKey); err != nil {
		t.Fatal(err)
	}
	if testRuleSnapshot(t, s, ctx) != afterApply {
		t.Fatal("same application key changed historical rows")
	}
}

func TestRuleVersionsReapplyAndManualImportProtection(t *testing.T) {
	s, ctx := testOrgStore(t)
	a := testDepartment(t, s, ctx)
	b := testDepartment(t, s, ctx)
	suffix := uuid.NewString() + ".rules.example.invalid"
	rule := testSavedRule(t, s, ctx, Rule{MatchType: "domain", MatchValue: "*." + suffix, DepartmentID: a.EntityID, Active: true})
	ruleAsset := testRuleAsset(t, s, ctx, "rule."+suffix, "")
	testSyncRuleAsset(t, s, ctx, ruleAsset)
	protected := []int64{}
	for _, source := range []string{"manual", "import"} {
		asset := testRuleAsset(t, s, ctx, source+"."+suffix, "")
		_, err := s.DB.ExecContext(ctx, `INSERT INTO org.asset_registry(asset_id,asset_type,identifier,first_seen_at,source_updated_at) VALUES($1,'subdomain',$2,clock_timestamp(),clock_timestamp())`, asset, source+"."+suffix)
		if err != nil {
			t.Fatal(err)
		}
		_, err = s.DB.ExecContext(ctx, `INSERT INTO org.asset_ownership(asset_id,department_id,source,valid_from,operation_id,created_by) VALUES($1,$2,$3,clock_timestamp(),$4,'test fixture')`, asset, a.EntityID, source, a.OperationID)
		if err != nil {
			t.Fatal(err)
		}
		protected = append(protected, asset)
	}
	var beforeAt time.Time
	if err := s.DB.QueryRowContext(ctx, `SELECT clock_timestamp()`).Scan(&beforeAt); err != nil {
		t.Fatal(err)
	}
	rule.DepartmentID = b.EntityID
	rule = testSavedRule(t, s, ctx, rule)
	var versions int
	if err := s.DB.QueryRowContext(ctx, `SELECT count(*) FROM org.attribution_rule_revision WHERE rule_id=$1`, rule.ID).Scan(&versions); err != nil {
		t.Fatal(err)
	}
	if versions != 2 {
		t.Fatalf("rule edit did not retain old revision: %d", versions)
	}
	testApplyRules(t, s, ctx, false)
	if got, _, count := testCurrentRuleOwner(t, s, ctx, ruleAsset); got != a.EntityID || count != 1 {
		t.Fatal("default application overwrote an existing rule-owned asset")
	}
	testApplyRules(t, s, ctx, true)
	if got, source, count := testCurrentRuleOwner(t, s, ctx, ruleAsset); got != b.EntityID || source != "rule" || count != 2 {
		t.Fatalf("explicit reapply not appended: dep=%d source=%s records=%d", got, source, count)
	}
	past, err := s.ResolveOwner(ctx, ruleAsset, beforeAt, beforeAt)
	if err != nil {
		t.Fatal(err)
	}
	if past.DepartmentID == nil || *past.DepartmentID != a.EntityID {
		t.Fatalf("rule application rewrote past department: %+v", past)
	}
	for i, id := range protected {
		testSyncRuleAsset(t, s, ctx, id)
		got, source, count := testCurrentRuleOwner(t, s, ctx, id)
		if got != a.EntityID || source != []string{"manual", "import"}[i] || count != 1 {
			t.Fatalf("protected asset %d overwritten: %d %s %d", id, got, source, count)
		}
	}
}

func TestSyncAssetRuleAssignmentIsTransactionalAndRepeatable(t *testing.T) {
	s, ctx := testOrgStore(t)
	dep := testDepartment(t, s, ctx)
	suffix := uuid.NewString() + ".rules.example.invalid"
	testSavedRule(t, s, ctx, Rule{MatchType: "domain", MatchValue: "*." + suffix, DepartmentID: dep.EntityID, Active: true})
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var id int64
	if err = tx.QueryRowContext(ctx, `INSERT INTO public.assets(type,domain) VALUES('subdomain',$1) RETURNING id`, "rollback."+suffix).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if err = SyncAssetTx(ctx, tx, id); err != nil {
		t.Fatal(err)
	}
	var assigned int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM org.asset_ownership WHERE asset_id=$1 AND department_id=$2`, id, dep.EntityID).Scan(&assigned); err != nil {
		t.Fatal(err)
	}
	if assigned != 1 {
		t.Fatal("new asset was not assigned inside ingest transaction")
	}
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	var residual int
	if err = s.DB.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM public.assets WHERE id=$1)+(SELECT count(*) FROM org.asset_registry WHERE asset_id=$1)+(SELECT count(*) FROM org.asset_ownership WHERE asset_id=$1)`, id).Scan(&residual); err != nil {
		t.Fatal(err)
	}
	if residual != 0 {
		t.Fatal("asset transaction rollback left ownership state")
	}
	committed := testRuleAsset(t, s, ctx, "commit."+suffix, "")
	var sourceCreated time.Time
	if err = s.DB.QueryRowContext(ctx, `SELECT created_at FROM public.assets WHERE id=$1`, committed).Scan(&sourceCreated); err != nil {
		t.Fatal(err)
	}
	unregistered, err := loadRuleAssets(ctx, s.DB, committed)
	if err != nil {
		t.Fatal(err)
	}
	if len(unregistered) != 1 || unregistered[0].Registered || !unregistered[0].FirstSeenAt.Equal(sourceCreated) {
		t.Fatal("unregistered asset does not use original first seen time")
	}
	testSyncRuleAsset(t, s, ctx, committed)
	registeredFirstSeen := sourceCreated.Add(-24 * time.Hour)
	if _, err = s.DB.ExecContext(ctx, `UPDATE org.asset_registry SET first_seen_at=$2 WHERE asset_id=$1`, committed, registeredFirstSeen); err != nil {
		t.Fatal(err)
	} // Synthetic earlier evidence for a registered asset.
	testSyncRuleAsset(t, s, ctx, committed)
	registered, err := loadRuleAssets(ctx, s.DB, committed)
	if err != nil {
		t.Fatal(err)
	}
	if len(registered) != 1 || !registered[0].Registered || !registered[0].FirstSeenAt.Equal(registeredFirstSeen) {
		t.Fatalf("resync discarded registered first seen: %+v", registered)
	}
	if got, _, count := testCurrentRuleOwner(t, s, ctx, committed); got != dep.EntityID || count != 1 {
		t.Fatalf("duplicate synchronization rewrote ownership: %d %d", got, count)
	}
	var createdBy string
	if err = s.DB.QueryRowContext(ctx, `SELECT created_by FROM org.asset_ownership WHERE asset_id=$1 AND valid_to IS NULL`, committed).Scan(&createdBy); err != nil {
		t.Fatal(err)
	}
	if createdBy != "资产入库" {
		t.Fatalf("automatic history masquerades as administrator: %q", createdBy)
	}
	var actorsCorrect bool
	if err = s.DB.QueryRowContext(ctx, `SELECT COALESCE(bool_and(actor_type='system' AND actor_label='资产入库'),false) FROM org.audit_log WHERE object_type='asset' AND object_id=$1`, fmt.Sprint(committed)).Scan(&actorsCorrect); err != nil {
		t.Fatal(err)
	}
	if !actorsCorrect {
		t.Fatal("automatic audit does not identify system actor")
	}
}

func TestRuleConflictsAndDisabledTargetsAreNotAssigned(t *testing.T) {
	s, ctx := testOrgStore(t)
	a := testDepartment(t, s, ctx)
	b := testDepartment(t, s, ctx)
	domain := uuid.NewString() + ".rules.example.invalid"
	testSavedRule(t, s, ctx, Rule{MatchType: "domain", MatchValue: domain, DepartmentID: a.EntityID, Active: true})
	testSavedRule(t, s, ctx, Rule{MatchType: "domain", MatchValue: domain, DepartmentID: b.EntityID, Active: true})
	asset := testRuleAsset(t, s, ctx, domain, "")
	testSyncRuleAsset(t, s, ctx, asset)
	if got, _, count := testCurrentRuleOwner(t, s, ctx, asset); got != 0 || count != 0 {
		t.Fatal("conflicting equally precise rules silently assigned an asset")
	}
	disabled := testDepartment(t, s, ctx)
	if _, err := s.Change(ctx, Command{Kind: "department", Action: "disable", ID: disabled.EntityID, Revision: 1, ConfirmImpact: true, Scope: "all_open", IdempotencyKey: uuid.NewString()}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveRule(ctx, Rule{MatchType: "domain", MatchValue: uuid.NewString() + ".example.invalid", DepartmentID: disabled.EntityID, Active: true}, uuid.NewString()); err == nil {
		t.Fatal("rule targeting disabled department accepted")
	}
	line := testChange(t, s, ctx, "business_line", 0, 0, BusinessLine{Name: "禁用目标-" + uuid.NewString(), Code: uuid.NewString(), DepartmentID: a.EntityID, Active: true})
	if _, err := s.Change(ctx, Command{Kind: "business_line", Action: "disable", ID: line.EntityID, Revision: 1, ConfirmImpact: true, Scope: "all_open", IdempotencyKey: uuid.NewString()}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveRule(ctx, Rule{MatchType: "domain", MatchValue: uuid.NewString() + ".example.invalid", DepartmentID: a.EntityID, BusinessLineID: &line.EntityID, Active: true}, uuid.NewString()); err == nil {
		t.Fatal("rule targeting disabled business line accepted")
	}
}

func TestRuleReapplyFutureScopeKeepsExistingFindingCohort(t *testing.T) {
	s, ctx := testOrgStore(t)
	a := testDepartment(t, s, ctx)
	b := testDepartment(t, s, ctx)
	domain := uuid.NewString() + ".future-rules.example.invalid"
	rule := testSavedRule(t, s, ctx, Rule{MatchType: "domain", MatchValue: domain, DepartmentID: a.EntityID, Active: true})
	asset := testRuleAsset(t, s, ctx, domain, "")
	testSyncRuleAsset(t, s, ctx, asset)
	var born time.Time
	if err := s.DB.QueryRowContext(ctx, `SELECT clock_timestamp()`).Scan(&born); err != nil {
		t.Fatal(err)
	}
	rule.DepartmentID = b.EntityID
	testSavedRule(t, s, ctx, rule)
	d, err := s.Directory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// Empty scope is the external default: only findings discovered afterwards.
	if _, err = s.ApplyRules(ctx, true, "", d.ConfigRevision, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	var switched time.Time
	if err = s.DB.QueryRowContext(ctx, `SELECT valid_from FROM org.asset_ownership WHERE asset_id=$1 AND department_id=$2 AND valid_to IS NULL AND voided_at IS NULL`, asset, b.EntityID).Scan(&switched); err != nil {
		t.Fatal(err)
	}
	old, err := s.ResolveOwner(ctx, asset, switched, born)
	if err != nil {
		t.Fatal(err)
	}
	newer, err := s.ResolveOwner(ctx, asset, switched, switched)
	if err != nil {
		t.Fatal(err)
	}
	if old.DepartmentID == nil || *old.DepartmentID != a.EntityID || newer.DepartmentID == nil || *newer.DepartmentID != b.EntityID {
		t.Fatalf("future scope lost cohorts: old=%+v new=%+v", old, newer)
	}
}

func TestRuleApplicationRejectsChangedConfigurationAndUnavailableTarget(t *testing.T) {
	s, ctx := testOrgStore(t)
	dep := testDepartment(t, s, ctx)
	domain := uuid.NewString() + ".stale-rules.example.invalid"
	asset := testRuleAsset(t, s, ctx, domain, "")
	testSyncRuleAsset(t, s, ctx, asset)
	r := testSavedRule(t, s, ctx, Rule{MatchType: "domain", MatchValue: domain, DepartmentID: dep.EntityID, Active: true})
	preview, err := s.ApplyRulesPreview(ctx, false, "all_open")
	if err != nil {
		t.Fatal(err)
	}
	r.Priority++
	testSavedRule(t, s, ctx, r)
	before := testRuleSnapshot(t, s, ctx)
	if _, err = s.ApplyRules(ctx, false, "all_open", preview.ConfigRevision, uuid.NewString()); err == nil {
		t.Fatal("application accepted stale rules preview")
	}
	if testRuleSnapshot(t, s, ctx) != before {
		t.Fatal("rejected stale apply wrote history or audit")
	}
	if _, err = s.Change(ctx, Command{Kind: "department", Action: "disable", ID: dep.EntityID, Revision: 1, ConfirmImpact: true, Scope: "all_open", IdempotencyKey: uuid.NewString()}); err != nil {
		t.Fatal(err)
	}
	testApplyRules(t, s, ctx, false)
	if got, _, count := testCurrentRuleOwner(t, s, ctx, asset); got != 0 || count != 0 {
		t.Fatal("stored rule applied to department disabled after its creation")
	}
}

func TestRuleAssetUsesActualHostnameBeforeRootDomain(t *testing.T) {
	s, ctx := testOrgStore(t)
	dep := testDepartment(t, s, ctx)
	suffix := uuid.NewString() + ".example.invalid"
	testSavedRule(t, s, ctx, Rule{MatchType: "domain", MatchValue: suffix, DepartmentID: dep.EntityID, Active: true})
	var id int64
	if err := s.DB.QueryRowContext(ctx, `INSERT INTO public.assets(type,domain,root_domain) VALUES('subdomain',$1,$2) RETURNING id`, "api."+suffix, suffix).Scan(&id); err != nil {
		t.Fatal(err)
	}
	testSyncRuleAsset(t, s, ctx, id)
	if got, _, count := testCurrentRuleOwner(t, s, ctx, id); got != 0 || count != 0 {
		t.Fatal("exact root-domain rule was treated as matching a subdomain")
	}
}

func TestSavedRuleUndoAppendsVersionsAndLeavesAssignedAssetsUnchanged(t *testing.T) {
	s, ctx := testOrgStore(t)
	a := testDepartment(t, s, ctx)
	b := testDepartment(t, s, ctx)
	newDomain := uuid.NewString() + ".undo-new.example.invalid"
	created, err := s.SaveRule(ctx, Rule{MatchType: "domain", MatchValue: newDomain, DepartmentID: a.EntityID, Active: true}, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	asset := testRuleAsset(t, s, ctx, newDomain, "")
	testSyncRuleAsset(t, s, ctx, asset)
	var ownershipBefore string
	if err = s.DB.QueryRowContext(ctx, `SELECT jsonb_agg(to_jsonb(o) ORDER BY id)::text FROM org.asset_ownership o WHERE asset_id=$1`, asset).Scan(&ownershipBefore); err != nil {
		t.Fatal(err)
	}
	key := uuid.NewString()
	undone, err := s.Undo(ctx, created.OperationID, key)
	if err != nil {
		t.Fatal(err)
	}
	var active bool
	var versionCount int
	var revision int64
	if err = s.DB.QueryRowContext(ctx, `SELECT r.revision,h.active,(SELECT count(*) FROM org.attribution_rule_revision WHERE rule_id=r.id) FROM org.attribution_rule r JOIN org.attribution_rule_revision h ON h.rule_id=r.id AND h.valid_to IS NULL AND h.voided_at IS NULL WHERE r.id=$1`, created.Rule.ID).Scan(&revision, &active, &versionCount); err != nil {
		t.Fatal(err)
	}
	if active || revision != 2 || versionCount != 2 {
		t.Fatalf("new rule undo must append disabled revision: active=%v revision=%d versions=%d", active, revision, versionCount)
	}
	again, err := s.Undo(ctx, created.OperationID, key)
	if err != nil || again.OperationID != undone.OperationID {
		t.Fatalf("undo replay not idempotent: %+v %v", again, err)
	}
	var ownershipAfter string
	if err = s.DB.QueryRowContext(ctx, `SELECT jsonb_agg(to_jsonb(o) ORDER BY id)::text FROM org.asset_ownership o WHERE asset_id=$1`, asset).Scan(&ownershipAfter); err != nil {
		t.Fatal(err)
	}
	if ownershipAfter != ownershipBefore {
		t.Fatal("undoing rule creation rewrote assigned asset history")
	}

	editDomain := uuid.NewString() + ".undo-edit.example.invalid"
	initial, err := s.SaveRule(ctx, Rule{MatchType: "domain", MatchValue: editDomain, DepartmentID: a.EntityID, Active: true}, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	assigned := testRuleAsset(t, s, ctx, editDomain, "")
	testSyncRuleAsset(t, s, ctx, assigned)
	changedRule := initial.Rule
	changedRule.DepartmentID = b.EntityID
	changedRule.Priority = 10
	edited, err := s.SaveRule(ctx, changedRule, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	testApplyRules(t, s, ctx, true)
	if got, _, _ := testCurrentRuleOwner(t, s, ctx, assigned); got != b.EntityID {
		t.Fatal("setup did not apply edited rule")
	}
	if err = s.DB.QueryRowContext(ctx, `SELECT jsonb_agg(to_jsonb(o) ORDER BY id)::text FROM org.asset_ownership o WHERE asset_id=$1`, assigned).Scan(&ownershipBefore); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Undo(ctx, edited.OperationID, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	var target int64
	var priority int
	if err = s.DB.QueryRowContext(ctx, `SELECT r.revision,h.department_id,h.priority,h.active,(SELECT count(*) FROM org.attribution_rule_revision WHERE rule_id=r.id) FROM org.attribution_rule r JOIN org.attribution_rule_revision h ON h.rule_id=r.id AND h.valid_to IS NULL AND h.voided_at IS NULL WHERE r.id=$1`, initial.Rule.ID).Scan(&revision, &target, &priority, &active, &versionCount); err != nil {
		t.Fatal(err)
	}
	if revision != 3 || versionCount != 3 || target != a.EntityID || priority != initial.Rule.Priority || !active {
		t.Fatalf("edited rule undo lost original configuration: revision=%d versions=%d target=%d priority=%d active=%v", revision, versionCount, target, priority, active)
	}
	if err = s.DB.QueryRowContext(ctx, `SELECT jsonb_agg(to_jsonb(o) ORDER BY id)::text FROM org.asset_ownership o WHERE asset_id=$1`, assigned).Scan(&ownershipAfter); err != nil {
		t.Fatal(err)
	}
	if ownershipAfter != ownershipBefore {
		t.Fatal("rule edit undo changed existing asset attribution")
	}

	laterRule := initial.Rule
	laterRule.Revision = revision
	laterRule.Priority = 20
	earlierEdit, err := s.SaveRule(ctx, laterRule, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	laterRule = earlierEdit.Rule
	laterRule.Priority = 30
	if _, err = s.SaveRule(ctx, laterRule, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	before := testRuleSnapshot(t, s, ctx)
	if _, err = s.Undo(ctx, earlierEdit.OperationID, uuid.NewString()); err == nil {
		t.Fatal("undo overwrote a later rule edit")
	}
	if after := testRuleSnapshot(t, s, ctx); after != before {
		t.Fatal("rejected stale undo changed rules, asset attribution or audit")
	}
}
