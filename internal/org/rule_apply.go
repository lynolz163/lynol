package org

import (
	"context"
	"database/sql"
	"encoding/json"
)

func (s *Store) PreviewRule(ctx context.Context, r Rule) (RulePreview, error) {
	out := RulePreview{Items: []RuleAssetView{}}
	if err := ValidateRule(&r); err != nil {
		return out, err
	}
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	if err = validateRuleTarget(ctx, tx, r); err != nil {
		return out, err
	}
	rules, err := loadRules(ctx, tx)
	if err != nil {
		return out, err
	}
	replaced := []Rule{}
	for _, old := range rules {
		if old.ID != r.ID {
			replaced = append(replaced, old)
		}
	}
	rules = append(replaced, r)
	assets, err := loadRuleAssets(ctx, tx, 0)
	if err != nil {
		return out, err
	}
	for _, a := range assets {
		if _, ok := ruleSpecificity(a.Candidate, r); !ok {
			continue
		}
		out.Matched++
		if a.DepartmentID == nil {
			out.Unassigned++
		}
		if a.ManualProtected {
			out.ManualConflicts++
		}
		a.Match = MatchRules(a.Candidate, rules)
		if a.Match.Status == "conflict" {
			out.Conflicts++
		}
		if len(out.Items) < 100 {
			out.Items = append(out.Items, a)
		}
	}
	return out, nil
}

func previewApplication(ctx context.Context, q reader, include bool, scope string) (RuleApplication, []RuleAssetView, []Rule, error) {
	out := RuleApplication{Scope: scope, Items: []RuleAssetView{}}
	if err := q.QueryRowContext(ctx, "SELECT config_revision FROM org.sync_state WHERE singleton").Scan(&out.ConfigRevision); err != nil {
		return out, nil, nil, err
	}
	rules, err := loadRules(ctx, q)
	if err != nil {
		return out, nil, nil, err
	}
	assets, err := loadRuleAssets(ctx, q, 0)
	if err != nil {
		return out, nil, nil, err
	}
	eligible := []RuleAssetView{}
	for _, a := range assets {
		if err = ctx.Err(); err != nil {
			return out, nil, nil, err
		}
		a.Match = MatchRules(a.Candidate, rules)
		switch {
		case a.ManualProtected:
			out.Protected++
		case a.DepartmentID != nil && (!include || a.Source != "rule"):
			out.Unchanged++
		case a.Match.Status == "conflict":
			out.Conflicts++
		case a.Match.Status == "unmatched":
			out.Unmatched++
		default:
			var desired Rule
			for _, r := range rules {
				if a.Match.RuleID != nil && r.ID == *a.Match.RuleID {
					desired = r
					break
				}
			}
			same := a.RuleRevisionID != nil && *a.RuleRevisionID == desired.RevisionID && sameID(a.DepartmentID, a.Match.DepartmentID) && sameID(a.BusinessLineID, a.Match.BusinessLineID)
			if same && scope == "all_open" { // Existing future-only segments may leave older findings on another version.
				var complete bool
				err = q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM org.asset_ownership WHERE asset_id=$1 AND valid_to IS NULL AND voided_at IS NULL AND finding_from IS NULL AND finding_to IS NULL AND rule_revision_id=$2)`, a.AssetID, desired.RevisionID).Scan(&complete)
				if err != nil {
					return out, nil, nil, err
				}
				same = complete
			}
			if same {
				out.Unchanged++
			} else {
				out.Assigned++
				if scope == "all_open" {
					out.AffectedOpen += a.Open
				}
				eligible = append(eligible, a)
			}
		}
		if len(out.Items) < 100 {
			out.Items = append(out.Items, a)
		}
	}
	return out, eligible, rules, nil
}
func (s *Store) ApplyRulesPreview(ctx context.Context, include bool, scope string) (RuleApplication, error) {
	scope, err := normalizedScope(scope)
	if err != nil {
		return RuleApplication{}, err
	}
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return RuleApplication{}, err
	}
	defer tx.Rollback()
	out, _, _, err := previewApplication(ctx, tx, include, scope)
	return out, err
}
func (s *Store) ApplyRules(ctx context.Context, include bool, scope string, revision int64, key string) (RuleApplication, error) {
	scope, err := normalizedScope(scope)
	if err != nil {
		return RuleApplication{}, err
	}
	request := struct {
		Include  bool
		Scope    string
		Revision int64
	}{include, scope, revision}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return RuleApplication{}, err
	}
	defer tx.Rollback()
	op, at, cached, err := beginRuleOperation(ctx, tx, "rules_apply", key, request)
	if err != nil {
		return RuleApplication{}, err
	}
	if cached != nil {
		var out RuleApplication
		err = json.Unmarshal(cached, &out)
		return out, err
	}
	out, assets, rules, err := previewApplication(ctx, tx, include, scope)
	if err != nil {
		return out, err
	}
	if out.ConfigRevision != revision {
		return out, conflict("配置已变化，请重新预览")
	}
	out.OperationID = op
	for _, a := range assets {
		if _, err = registerAsset(ctx, tx, a); err != nil {
			return out, err
		}
		// Every rule/organization writer serializes on the same config row; registry
		// row locking also coordinates with the upcoming asset-assignment writer.
		if _, err = tx.ExecContext(ctx, "SELECT asset_id FROM org.asset_registry WHERE asset_id=$1 FOR UPDATE", a.AssetID); err != nil {
			return out, err
		}
		for _, rule := range rules {
			if a.Match.RuleID != nil && rule.ID == *a.Match.RuleID {
				if err = assignRuleTx(ctx, tx, a, rule, scope, op, at); err != nil {
					return out, err
				}
				break
			}
		}
	}
	if err = finishRuleOperation(ctx, tx, op, out, true); err != nil {
		return out, err
	}
	return out, tx.Commit()
}
