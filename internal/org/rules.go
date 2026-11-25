package org

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/netip"
	"sort"
	"strings"
	"time"

	"golang.org/x/net/idna"
)

type Rule struct {
	ID                int64  `json:"id"`
	Revision          int64  `json:"revision"`
	RevisionID        int64  `json:"revision_id"`
	MatchType         string `json:"match_type"`
	MatchValue        string `json:"match_value"`
	DepartmentID      int64  `json:"department_id"`
	BusinessLineID    *int64 `json:"business_line_id"`
	Priority          int    `json:"priority"`
	Active            bool   `json:"active"`
	TargetUnavailable bool   `json:"target_unavailable"`
}
type RuleAsset struct {
	AssetID int64
	Domains []string
	IP      string
}
type RuleMatch struct {
	Status         string `json:"status"`
	Reason         string `json:"reason"`
	RuleID         *int64 `json:"rule_id"`
	DepartmentID   *int64 `json:"department_id"`
	BusinessLineID *int64 `json:"business_line_id"`
	Candidates     []Rule `json:"candidates"`
}
type RuleSaveResult struct {
	Rule        Rule      `json:"rule"`
	OperationID string    `json:"operation_id"`
	EffectiveAt time.Time `json:"effective_at"`
}
type RulePreview struct {
	Matched         int             `json:"matched"`
	Unassigned      int             `json:"unassigned"`
	ManualConflicts int             `json:"manual_conflicts"`
	Conflicts       int             `json:"conflicts"`
	Items           []RuleAssetView `json:"items"`
}
type RuleApplication struct {
	ConfigRevision int64           `json:"config_revision"`
	Assigned       int             `json:"assigned"`
	Unchanged      int             `json:"unchanged"`
	Conflicts      int             `json:"conflicts"`
	Protected      int             `json:"protected"`
	Unmatched      int             `json:"unmatched"`
	AffectedOpen   int64           `json:"affected_open"`
	Scope          string          `json:"scope"`
	OperationID    string          `json:"operation_id,omitempty"`
	Items          []RuleAssetView `json:"items"`
}

// ValidateRule normalizes the external rule once at the public service boundary.
func ValidateRule(r *Rule) error {
	if r.ID < 0 {
		return bad("id", "规则编号不正确")
	}
	if r.DepartmentID <= 0 {
		return bad("department_id", "请选择目标部门")
	}
	if r.BusinessLineID != nil && *r.BusinessLineID <= 0 {
		return bad("business_line_id", "请选择有效业务线")
	}
	r.TargetUnavailable = false
	value := strings.TrimSpace(r.MatchValue)
	switch r.MatchType {
	case "domain":
		wildcard := strings.HasPrefix(value, "*.")
		if wildcard {
			value = value[2:]
		}
		value = strings.TrimSuffix(value, ".")
		ascii, err := idna.Lookup.ToASCII(strings.ToLower(value))
		if err != nil || len(ascii) == 0 || len(ascii) > 253 || strings.ContainsAny(ascii, "*/:@?# ") {
			return bad("match_value", "请输入域名，例如 example.com 或 *.example.com，不要填写网址或路径")
		}
		if _, err := netip.ParseAddr(ascii); err == nil {
			return bad("match_value", "IP 地址请使用 IP/CIDR 类型")
		}
		for _, label := range strings.Split(ascii, ".") {
			if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
				return bad("match_value", "域名每段须为 1～63 个字符，不能以连字符开头或结尾")
			}
			for _, c := range label {
				if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
					return bad("match_value", "域名只能包含字母、数字、连字符和点")
				}
			}
		}
		if wildcard {
			ascii = "*." + ascii
		}
		r.MatchValue = ascii
	case "ip_cidr":
		p, err := netip.ParsePrefix(value)
		if err != nil {
			a, e := netip.ParseAddr(value)
			if e != nil {
				return bad("match_value", "请输入有效 IPv4/IPv6 地址或 CIDR，例如 192.0.2.0/24")
			}
			a = a.Unmap()
			p = netip.PrefixFrom(a, a.BitLen())
		}
		if p.Addr().Is4In6() {
			return bad("match_value", "请将映射的 IPv6 地址改为普通 IPv4 地址或网络")
		}
		r.MatchValue = p.Masked().String()
	default:
		return bad("match_type", "请选择域名或 IP/CIDR")
	}
	return nil
}

func ruleSpecificity(a RuleAsset, r Rule) (int, bool) {
	if !r.Active || r.TargetUnavailable {
		return 0, false
	}
	if r.MatchType == "ip_cidr" {
		prefix, err := netip.ParsePrefix(r.MatchValue)
		if err != nil {
			return 0, false
		}
		addr, e := netip.ParseAddr(a.IP)
		if e != nil {
			return 0, false
		}
		return prefix.Bits(), prefix.Contains(addr.Unmap())
	}
	for _, domain := range a.Domains {
		domain = strings.TrimSuffix(strings.ToLower(domain), ".")
		ascii, e := idna.Lookup.ToASCII(domain)
		if e != nil {
			continue
		}
		domain = ascii
		if strings.HasPrefix(r.MatchValue, "*.") {
			suffix := r.MatchValue[1:]
			if strings.HasSuffix(domain, suffix) && len(domain) > len(suffix) {
				return strings.Count(r.MatchValue, "."), true
			}
		} else if domain == r.MatchValue {
			return 1000, true
		}
	}
	return 0, false
}

// MatchRules compares precision only within a matching mechanism. Equal priority
// across mechanisms has no invented tie-breaker; different targets stay conflicted.
func MatchRules(a RuleAsset, rules []Rule) RuleMatch {
	out := RuleMatch{Status: "unmatched", Reason: "没有匹配的已启用规则", Candidates: []Rule{}}
	bestPriority := 0
	found := false
	bestByType := map[string]int{}
	best := []Rule{}
	for _, r := range rules {
		score, ok := ruleSpecificity(a, r)
		if !ok {
			continue
		}
		if !found || r.Priority > bestPriority {
			found = true
			bestPriority = r.Priority
			bestByType = map[string]int{}
			best = nil
		}
		if r.Priority != bestPriority {
			continue
		}
		previous, exists := bestByType[r.MatchType]
		if !exists || score > previous {
			next := best[:0]
			for _, v := range best {
				if v.MatchType != r.MatchType {
					next = append(next, v)
				}
			}
			best = next
			bestByType[r.MatchType] = score
		}
		if score == bestByType[r.MatchType] {
			best = append(best, r)
		}
	}
	if !found {
		return out
	}
	sort.Slice(best, func(i, j int) bool { return best[i].ID < best[j].ID })
	out.Candidates = best
	target := best[0]
	for _, r := range best[1:] {
		if r.DepartmentID != target.DepartmentID || !sameID(r.BusinessLineID, target.BusinessLineID) {
			out.Status = "conflict"
			out.Reason = "最高优先级候选指向不同归属；同等精确度或跨域名/IP 类型无法决胜，请调整优先级"
			return out
		}
	}
	out.Status = "matched"
	out.RuleID = &target.ID
	out.DepartmentID = &target.DepartmentID
	out.BusinessLineID = target.BusinessLineID
	out.Reason = fmt.Sprintf("优先级 %d 优先；同类型按域名精确度或网段前缀长度选择", bestPriority)
	if len(best) > 1 {
		out.Reason += "；并列候选目标相同，合并为同一归属"
	}
	return out
}

func loadRules(ctx context.Context, q reader) ([]Rule, error) {
	rows, err := q.QueryContext(ctx, `SELECT r.id,r.revision,h.id,h.match_type,COALESCE(h.domain_pattern,h.ip_cidr::text),h.department_id,h.business_line_id,h.priority,h.active,
 (NOT d.active OR (h.business_line_id IS NOT NULL AND (NOT b.active OR b.department_id<>h.department_id))) target_unavailable
 FROM org.attribution_rule r JOIN org.attribution_rule_revision h ON h.rule_id=r.id AND h.valid_to IS NULL AND h.voided_at IS NULL JOIN org.department d ON d.id=h.department_id LEFT JOIN org.business_line b ON b.id=h.business_line_id ORDER BY r.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Rule{}
	for rows.Next() {
		var r Rule
		if err = rows.Scan(&r.ID, &r.Revision, &r.RevisionID, &r.MatchType, &r.MatchValue, &r.DepartmentID, &r.BusinessLineID, &r.Priority, &r.Active, &r.TargetUnavailable); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
func validateRuleTarget(ctx context.Context, q reader, r Rule) error {
	var active bool
	if err := q.QueryRowContext(ctx, "SELECT active FROM org.department WHERE id=$1", r.DepartmentID).Scan(&active); err == sql.ErrNoRows {
		return bad("department_id", "目标部门不存在")
	} else if err != nil {
		return err
	}
	if !active && r.Active {
		return bad("department_id", "目标部门已停用，请重新选择")
	}
	if r.BusinessLineID != nil {
		var department int64
		if err := q.QueryRowContext(ctx, "SELECT department_id,active FROM org.business_line WHERE id=$1", r.BusinessLineID).Scan(&department, &active); err == sql.ErrNoRows {
			return bad("business_line_id", "目标业务线不存在")
		} else if err != nil {
			return err
		}
		if department != r.DepartmentID && r.Active {
			return bad("business_line_id", "业务线不属于所选部门，请重新选择")
		}
		if !active && r.Active {
			return bad("business_line_id", "目标业务线已停用，请重新选择")
		}
	}
	return nil
}

func (s *Store) SaveRule(ctx context.Context, r Rule, key string) (RuleSaveResult, error) {
	if err := ValidateRule(&r); err != nil {
		return RuleSaveResult{}, err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return RuleSaveResult{}, err
	}
	defer tx.Rollback()
	op, at, cached, err := beginRuleOperation(ctx, tx, "rule_save", key, r)
	if err != nil {
		return RuleSaveResult{}, err
	}
	if cached != nil {
		var v RuleSaveResult
		err = json.Unmarshal(cached, &v)
		return v, err
	}
	if err = validateRuleTarget(ctx, tx, r); err != nil {
		return RuleSaveResult{}, err
	}
	var old any
	if r.ID != 0 {
		all, e := loadRules(ctx, tx)
		if e != nil {
			return RuleSaveResult{}, e
		}
		var previous *Rule
		for i := range all {
			if all[i].ID == r.ID {
				previous = &all[i]
				break
			}
		}
		if previous == nil {
			return RuleSaveResult{}, bad("id", "规则不存在")
		}
		if r.Revision != previous.Revision {
			return RuleSaveResult{}, conflict("规则已被修改，请刷新后重试")
		}
		old = *previous
		if _, err = tx.ExecContext(ctx, "UPDATE org.attribution_rule_revision SET valid_to=$2 WHERE rule_id=$1 AND valid_to IS NULL AND voided_at IS NULL", r.ID, at); err != nil {
			return RuleSaveResult{}, err
		}
		r.Revision++
		if _, err = tx.ExecContext(ctx, "UPDATE org.attribution_rule SET revision=$2 WHERE id=$1", r.ID, r.Revision); err != nil {
			return RuleSaveResult{}, err
		}
	} else {
		if err = tx.QueryRowContext(ctx, "INSERT INTO org.attribution_rule(created_by) VALUES('ARTEX 管理员') RETURNING id,revision").Scan(&r.ID, &r.Revision); err != nil {
			return RuleSaveResult{}, err
		}
	}
	var domain, cidr any
	if r.MatchType == "domain" {
		domain = r.MatchValue
	} else {
		cidr = r.MatchValue
	}
	err = tx.QueryRowContext(ctx, `INSERT INTO org.attribution_rule_revision(rule_id,match_type,domain_pattern,ip_cidr,department_id,business_line_id,priority,active,valid_from,operation_id,created_by) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,'ARTEX 管理员') RETURNING id`, r.ID, r.MatchType, domain, cidr, r.DepartmentID, r.BusinessLineID, r.Priority, r.Active, at, op).Scan(&r.RevisionID)
	if err != nil {
		return RuleSaveResult{}, err
	}
	if err = audit(ctx, tx, op, at, "rule", r.ID, "save", old, r); err != nil {
		return RuleSaveResult{}, err
	}
	v := RuleSaveResult{r, op, at}
	if err = finishRuleOperation(ctx, tx, op, v, true); err != nil {
		return v, err
	}
	return v, tx.Commit()
}
