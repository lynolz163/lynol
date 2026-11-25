package org

import (
	"database/sql"
	"net/http"
	"strconv"
	"time"
)

func (s *Store) rulesList(w http.ResponseWriter, r *http.Request) {
	rules, err := loadRules(r.Context(), s.DB)
	if err != nil {
		writeError(w, err)
		return
	}
	var rev int64
	if err = s.DB.QueryRowContext(r.Context(), "SELECT config_revision FROM org.sync_state WHERE singleton").Scan(&rev); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"items": rules, "config_revision": rev})
}
func (s *Store) ruleSave(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Rule Rule   `json:"rule"`
		Key  string `json:"idempotency_key"`
	}
	if !decode(w, r, &b) {
		return
	}
	v, e := s.SaveRule(r.Context(), b.Rule, b.Key)
	if e != nil {
		writeError(w, e)
		return
	}
	writeJSON(w, 200, v)
}
func (s *Store) rulePreview(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Rule Rule `json:"rule"`
	}
	if !decode(w, r, &b) {
		return
	}
	v, e := s.PreviewRule(r.Context(), b.Rule)
	if e != nil {
		writeError(w, e)
		return
	}
	writeJSON(w, 200, v)
}
func (s *Store) ruleApply(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Include  bool   `json:"include_rule_owned"`
		Scope    string `json:"scope"`
		Revision int64  `json:"config_revision"`
		Key      string `json:"idempotency_key"`
	}
	if !decode(w, r, &b) {
		return
	}
	var v RuleApplication
	var e error
	if r.URL.Path == "/api/org/rules/apply-preview" {
		v, e = s.ApplyRulesPreview(r.Context(), b.Include, b.Scope)
	} else {
		v, e = s.ApplyRules(r.Context(), b.Include, b.Scope, b.Revision, b.Key)
	}
	if e != nil {
		writeError(w, e)
		return
	}
	writeJSON(w, 200, v)
}
func (s *Store) assetSync(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Key string `json:"idempotency_key"`
	}
	if !decode(w, r, &b) {
		return
	}
	v, e := s.SyncAssets(r.Context(), b.Key)
	if e != nil {
		writeError(w, e)
		return
	}
	writeJSON(w, 200, v)
}
func (s *Store) ruleAssetList(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	status := q.Get("status")
	if status == "" {
		status = "unassigned"
	}
	switch status {
	case "unassigned", "all", "manual", "rule", "import", "conflict":
	default:
		writeError(w, bad("status", "请选择有效归属状态"))
		return
	}
	page, _ := strconv.Atoi(q.Get("page"))
	if page < 1 {
		page = 1
	}
	size, _ := strconv.Atoi(q.Get("page_size"))
	if size < 1 {
		size = 50
	}
	if size > 100 {
		size = 100
	}
	var from, to time.Time
	var err error
	if q.Get("from") != "" {
		from, err = time.ParseInLocation("2006-01-02", q.Get("from"), time.FixedZone("Asia/Shanghai", 8*3600))
		if err != nil {
			writeError(w, bad("from", "日期格式应为 YYYY-MM-DD"))
			return
		}
	}
	if q.Get("to") != "" {
		to, err = time.ParseInLocation("2006-01-02", q.Get("to"), time.FixedZone("Asia/Shanghai", 8*3600))
		if err != nil {
			writeError(w, bad("to", "日期格式应为 YYYY-MM-DD"))
			return
		}
		to = to.AddDate(0, 0, 1)
	}
	if !from.IsZero() && !to.IsZero() && !from.Before(to) {
		writeError(w, bad("to", "结束日期不能早于开始日期"))
		return
	}
	tx, err := s.DB.BeginTx(r.Context(), &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		writeError(w, err)
		return
	}
	defer tx.Rollback()
	assets, err := loadRuleAssets(r.Context(), tx, 0)
	if err != nil {
		writeError(w, err)
		return
	}
	rules, err := loadRules(r.Context(), tx)
	if err != nil {
		writeError(w, err)
		return
	}
	out := AssetList{Items: []RuleAssetView{}}
	if err = tx.QueryRowContext(r.Context(), "SELECT assets_last_synced_at FROM org.sync_state WHERE singleton").Scan(&out.LastSyncedAt); err != nil {
		writeError(w, err)
		return
	}
	for _, a := range assets {
		if !a.Registered {
			out.UnsyncedCount++
		}
		if q.Get("type") != "" && q.Get("type") != a.Type {
			continue
		}
		if q.Get("q") != "" && !stringsContainsFold(a.Identifier, q.Get("q")) && !stringsContainsFold(a.Candidate.IP, q.Get("q")) {
			continue
		}
		if !from.IsZero() && a.FirstSeenAt.Before(from) || !to.IsZero() && !a.FirstSeenAt.Before(to) {
			continue
		}
		a.Match = MatchRules(a.Candidate, rules)
		if status == "unassigned" && a.DepartmentID != nil {
			continue
		}
		if status == "conflict" && a.Match.Status != "conflict" {
			continue
		}
		if (status == "manual" || status == "rule" || status == "import") && a.Source != status {
			continue
		}
		if out.Total >= (page-1)*size && len(out.Items) < size {
			out.Items = append(out.Items, a)
		}
		out.Total++
	}
	writeJSON(w, 200, out)
}
