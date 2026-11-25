package org

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/netip"
	"sort"
	"strings"
	"time"
)

func validateAssignment(r *AssignmentRequest) error {
	if r.DepartmentID <= 0 {
		return bad("department_id", "请选择接收部门")
	}
	if r.Scope == "" {
		r.Scope = "all_open"
	}
	if r.Scope != "all_open" && r.Scope != "future_findings" {
		return bad("scope", "请选择存量一起划归或仅新漏洞")
	}
	if r.BackdateFirstAssignment && r.Scope != "all_open" {
		return bad("backdate_first_assignment", "回溯补录不能与仅新漏洞同时选择")
	}
	if r.Selection.ExpectedCount <= 0 {
		return bad("selection", "请先选择资产")
	}
	switch r.Selection.Mode {
	case "ids":
		if len(r.Selection.IDs) == 0 {
			return bad("selection", "请先选择资产")
		}
		sort.Slice(r.Selection.IDs, func(i, j int) bool { return r.Selection.IDs[i] < r.Selection.IDs[j] })
		for i, id := range r.Selection.IDs {
			if id <= 0 || i > 0 && id == r.Selection.IDs[i-1] {
				return bad("selection", "资产编号不能为空或重复")
			}
		}
		if len(r.Selection.IDs) != r.Selection.ExpectedCount {
			return conflict("选择数量已变化，请刷新后重选")
		}
	case "filter":
		if len(r.Selection.IDs) != 0 {
			return bad("selection", "选择全部时请仅提交筛选条件")
		}
	default:
		return bad("selection", "请选择指定资产或全部筛选结果")
	}
	if r.Selection.Filter.Status == "" {
		r.Selection.Filter.Status = "unassigned"
	}
	switch r.Selection.Filter.Status {
	case "all", "unassigned", "manual", "rule", "import", "conflict":
	default:
		return bad("selection", "归属筛选无效")
	}
	for _, value := range []string{r.Selection.Filter.From, r.Selection.Filter.To} {
		if value != "" {
			if _, err := time.Parse("2006-01-02", value); err != nil {
				return bad("selection", "日期格式应为 YYYY-MM-DD")
			}
		}
	}
	if r.Selection.Filter.From != "" && r.Selection.Filter.To != "" && r.Selection.Filter.From > r.Selection.Filter.To {
		return bad("selection", "结束日期不能早于开始日期")
	}
	for i := range r.GeneratedRules {
		g := &r.GeneratedRules[i]
		if g.ID != 0 {
			return bad("generated_rules", "批量划归只能同时新增规则，编辑已有规则请使用规则抽屉")
		}
		g.DepartmentID = r.DepartmentID
		g.BusinessLineID = r.BusinessLineID
		g.Active = true
		if err := ValidateRule(g); err != nil {
			return err
		}
	}
	return nil
}
func assignmentTarget(ctx context.Context, q reader, r AssignmentRequest) error {
	return validateRuleTarget(ctx, q, Rule{DepartmentID: r.DepartmentID, BusinessLineID: r.BusinessLineID, Active: true})
}
func selectAssignmentAssets(ctx context.Context, q reader, s AssignmentSelection) ([]RuleAssetView, error) {
	all, err := loadRuleAssets(ctx, q, 0)
	if err != nil {
		return nil, err
	}
	rules, err := loadRules(ctx, q)
	if err != nil {
		return nil, err
	}
	ids := map[int64]bool{}
	for _, id := range s.IDs {
		ids[id] = true
	}
	out := []RuleAssetView{}
	for _, a := range all {
		if s.Mode == "ids" {
			if !ids[a.AssetID] {
				continue
			}
		} else {
			f := s.Filter
			if f.Type != "" && a.Type != f.Type {
				continue
			}
			if f.Q != "" && !stringsContainsFold(a.Identifier, f.Q) && !stringsContainsFold(a.Candidate.IP, f.Q) {
				continue
			}
			day := a.FirstSeenAt.In(time.FixedZone("Asia/Shanghai", 28800)).Format("2006-01-02")
			if f.From != "" && day < f.From || f.To != "" && day > f.To {
				continue
			}
			if f.Status == "unassigned" && a.DepartmentID != nil {
				continue
			}
			if f.Status == "manual" || f.Status == "rule" || f.Status == "import" {
				if a.Source != f.Status {
					continue
				}
			}
			if f.Status == "conflict" && MatchRules(a.Candidate, rules).Status != "conflict" {
				continue
			}
		}
		a.Match = MatchRules(a.Candidate, rules)
		out = append(out, a)
	}
	if len(out) != s.ExpectedCount {
		return nil, conflict("筛选结果数量已变化，请刷新后重新选择")
	}
	return out, nil
}
func assignmentMembers(ctx context.Context, q reader, assets []RuleAssetView) ([]assignmentMember, string, error) {
	members := make([]assignmentMember, 0, len(assets))
	for _, a := range assets {
		var revision int64
		if a.Registered {
			if err := q.QueryRowContext(ctx, "SELECT event_revision FROM org.asset_registry WHERE asset_id=$1", a.AssetID).Scan(&revision); err != nil {
				return nil, "", err
			}
		}
		members = append(members, assignmentMember{a.AssetID, a.OwnershipRevision, revision, a.UpdatedAt, a.FirstSeenAt, a.Registered})
	}
	hash := sha256.Sum256([]byte(jsonValue(members)))
	return members, hex.EncodeToString(hash[:]), nil
}
func previewSecret(ctx context.Context, q reader) ([]byte, error) {
	var secret string
	err := q.QueryRowContext(ctx, "SELECT secret FROM org.assignment_signing_key WHERE singleton").Scan(&secret)
	return []byte(secret), err
}
func signAssignment(secret []byte, p assignmentToken) string {
	payload := base64.RawURLEncoding.EncodeToString([]byte(jsonValue(p)))
	m := hmac.New(sha256.New, secret)
	m.Write([]byte(payload))
	return payload + "." + base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}
func readAssignmentToken(secret []byte, token string) (assignmentToken, error) {
	var p assignmentToken
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return p, bad("token", "预览凭据不正确，请重新预览")
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return p, bad("token", "预览凭据不正确，请重新预览")
	}
	m := hmac.New(sha256.New, secret)
	m.Write([]byte(parts[0]))
	if !hmac.Equal(signature, m.Sum(nil)) {
		return p, bad("token", "预览内容已变化，请重新预览")
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil || json.Unmarshal(raw, &p) != nil {
		return p, bad("token", "预览凭据不正确，请重新预览")
	}
	return p, nil
}

func (s *Store) PreviewAssignment(ctx context.Context, r AssignmentRequest) (AssignmentPreview, error) {
	out := AssignmentPreview{Severity: map[string]int64{"critical": 0, "high": 0, "medium": 0, "low": 0, "unknown": 0}, Transfers: []AssignmentTransfer{}, SuggestedRules: []Rule{}, BackdateEligible: true}
	if err := validateAssignment(&r); err != nil {
		return out, err
	}
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	if err = assignmentTarget(ctx, tx, r); err != nil {
		return out, err
	}
	assets, err := selectAssignmentAssets(ctx, tx, r.Selection)
	if err != nil {
		return out, err
	}
	_, fingerprint, err := assignmentMembers(ctx, tx, assets)
	if err != nil {
		return out, err
	}
	var configRevision int64
	if err = tx.QueryRowContext(ctx, "SELECT config_revision FROM org.sync_state WHERE singleton").Scan(&configRevision); err != nil {
		return out, err
	}
	out.AssetCount = len(assets)
	transfers := map[string]*AssignmentTransfer{}
	suggested := map[string]bool{}
	ids := make([]int64, 0, len(assets))
	for _, a := range assets {
		ids = append(ids, a.AssetID)
		out.OpenCount += a.Open
		var ever bool
		if err = tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM org.asset_ownership WHERE asset_id=$1 AND department_id IS NOT NULL)", a.AssetID).Scan(&ever); err != nil {
			return out, err
		}
		if ever {
			out.BackdateEligible = false
		}
		var live []struct {
			DepartmentID   *int64 `json:"department_id"`
			BusinessLineID *int64 `json:"business_line_id"`
		}
		var raw []byte
		if err = tx.QueryRowContext(ctx, `SELECT COALESCE(jsonb_agg(DISTINCT jsonb_build_object('department_id',department_id,'business_line_id',business_line_id)),'[]') FROM org.asset_ownership WHERE asset_id=$1 AND valid_to IS NULL AND voided_at IS NULL AND department_id IS NOT NULL`, a.AssetID).Scan(&raw); err != nil {
			return out, err
		}
		if err = json.Unmarshal(raw, &live); err != nil {
			return out, err
		}
		if len(live) > 0 {
			out.AlreadyAssigned++
		}
		for _, old := range live {
			k := jsonValue(old)
			if transfers[k] == nil {
				transfers[k] = &AssignmentTransfer{old.DepartmentID, old.BusinessLineID, 0}
			}
			transfers[k].Count++
		}
		if a.Match.Status == "conflict" || a.Match.Status == "matched" && (!sameID(a.Match.DepartmentID, &r.DepartmentID) || !sameID(a.Match.BusinessLineID, r.BusinessLineID)) {
			out.RuleConflicts++
		}
		if len(out.SuggestedRules) < 20 {
			for _, domain := range a.Candidate.Domains {
				g := Rule{MatchType: "domain", MatchValue: domain, DepartmentID: r.DepartmentID, BusinessLineID: r.BusinessLineID, Active: true}
				if ValidateRule(&g) == nil && !suggested[g.MatchType+g.MatchValue] {
					out.SuggestedRules = append(out.SuggestedRules, g)
					suggested[g.MatchType+g.MatchValue] = true
					break
				}
			}
			if ip, e := netip.ParseAddr(a.Candidate.IP); e == nil {
				g := Rule{MatchType: "ip_cidr", MatchValue: netip.PrefixFrom(ip.Unmap(), ip.Unmap().BitLen()).String(), DepartmentID: r.DepartmentID, BusinessLineID: r.BusinessLineID, Active: true}
				if len(out.SuggestedRules) < 20 && !suggested[g.MatchType+g.MatchValue] {
					out.SuggestedRules = append(out.SuggestedRules, g)
					suggested[g.MatchType+g.MatchValue] = true
				}
			}
		}
	}
	for _, v := range transfers {
		out.Transfers = append(out.Transfers, *v)
	}
	sort.Slice(out.Transfers, func(i, j int) bool { return jsonValue(out.Transfers[i]) < jsonValue(out.Transfers[j]) })
	if r.Scope == "all_open" {
		out.AffectedOpen = out.OpenCount
	}
	rows, err := tx.QueryContext(ctx, "SELECT severity,count(*) FROM public.findings WHERE asset_id=ANY($1) AND status IN ('pending','in_progress','confirmed') GROUP BY severity", ids)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var severity string
		var count int64
		if err = rows.Scan(&severity, &count); err != nil {
			rows.Close()
			return out, err
		}
		if _, ok := out.Severity[severity]; !ok {
			severity = "unknown"
		}
		out.Severity[severity] += count
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	if r.BackdateFirstAssignment && !out.BackdateEligible {
		return out, bad("backdate_first_assignment", "所选资产存在历史归属，只有从未归属的资产才可回溯")
	}
	out.ExpiresAt = time.Now().UTC().Add(15 * time.Minute)
	secret, err := previewSecret(ctx, tx)
	if err != nil {
		return out, err
	}
	out.Token = signAssignment(secret, assignmentToken{r, fingerprint, configRevision, out.ExpiresAt.Unix(), out.AlreadyAssigned, out.BackdateEligible})
	return out, nil
}
