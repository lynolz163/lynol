package server

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/Autumn-27/artex/db"
	orgreport "github.com/Autumn-27/artex/internal/report"
)

func (s *Server) findingHistory(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt(r, "id")
	before, err := strconv.ParseInt(r.URL.Query().Get("before"), 10, 64)
	if r.URL.Query().Get("before") == "" {
		before, err = 0, nil
	}
	if !ok || id <= 0 || err != nil || before < 0 {
		writeErr(w, 400, "无效的漏洞编号或历史游标")
		return
	}
	pg := s.pg(w)
	if pg == nil {
		return
	}
	items, err := pg.FindingHistory(r.Context(), id, before)
	if err != nil {
		log.Printf("[finding-history] %v", err)
		writeErr(w, 500, "读取流转历史失败")
		return
	}
	writeJSON(w, 200, map[string]any{"items": items})
}

func (s *Server) findingMetrics(w http.ResponseWriter, r *http.Request) {
	pg := s.pg(w)
	if pg == nil {
		return
	}
	q := r.URL.Query()
	from, fromErr := time.ParseInLocation("2006-01-02", q.Get("from"), db.FindingMetricsLocation)
	to, toErr := time.ParseInLocation("2006-01-02", q.Get("to"), db.FindingMetricsLocation)
	today := time.Now().In(db.FindingMetricsLocation).Format("2006-01-02")
	filter := db.FindingMetricsFilter{Severity: q.Get("severity"), BusinessLine: q.Get("business_line"), Assignee: q.Get("assignee")}
	for name, target := range map[string]**int64{"department_id": &filter.DepartmentID, "business_line_id": &filter.BusinessLineID, "owner_id": &filter.OwnerID} {
		if raw := q.Get(name); raw != "" {
			id, err := strconv.ParseInt(raw, 10, 64)
			if err != nil || id < 0 {
				writeErr(w, 400, "筛选编号应为非负整数")
				return
			}
			*target = &id
		}
	}
	if fromErr != nil || toErr != nil || from.After(to) || to.Sub(from) > 365*24*time.Hour || q.Get("to") > today {
		writeErr(w, 400, "请选择截至今天、跨度不超过一年的日期范围")
		return
	}
	if !validFindingMetricsFilter(filter) {
		writeErr(w, 400, "无效的严重等级")
		return
	}
	report, err := orgreport.New(pg.DB).Metrics(r.Context(), from.Format("2006-01-02"), to.Format("2006-01-02"), filter)
	if err != nil {
		log.Printf("[finding-metrics] %v", err)
		writeErr(w, 500, "读取治理指标失败")
		return
	}
	writeJSON(w, 200, report)
}

func (s *Server) refreshFindingMetrics(w http.ResponseWriter, r *http.Request) {
	pg := s.pg(w)
	if pg == nil {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Minute)
	defer cancel()
	if err := orgreport.New(pg.DB).Refresh(ctx, time.Now()); err != nil {
		log.Printf("[finding-metrics] refresh: %v", err)
		writeErr(w, 500, "更新统计失败，请稍后重试")
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) findingSLA(w http.ResponseWriter, r *http.Request) {
	pg := s.pg(w)
	if pg == nil {
		return
	}
	if r.Method == http.MethodGet {
		value, _, err := pg.GetSetting("finding_sla_days")
		if err != nil {
			writeErr(w, 500, "读取 SLA 规则失败")
			return
		}
		var rules db.FindingSLA
		if err := json.Unmarshal([]byte(value), &rules); err != nil {
			writeErr(w, 500, "SLA 规则格式错误")
			return
		}
		writeJSON(w, 200, rules)
		return
	}
	var rules db.FindingSLA
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if err := json.NewDecoder(r.Body).Decode(&rules); err != nil || len(rules) != 4 {
		writeErr(w, 400, "请填写四个等级的 SLA 天数")
		return
	}
	for _, severity := range []string{"critical", "high", "medium", "low"} {
		if days := rules[severity]; days < 1 || days > 365 {
			writeErr(w, 400, "SLA 天数应为 1 至 365 的整数")
			return
		}
	}
	value, _ := json.Marshal(rules)
	if err := pg.SetSetting("finding_sla_days", string(value)); err != nil {
		writeErr(w, 500, "保存 SLA 规则失败")
		return
	}
	writeJSON(w, 200, rules)
}

func (s *Server) runFindingMetrics(ctx context.Context) {
	for {
		runCtx, cancel := context.WithTimeout(ctx, time.Minute)
		err := orgreport.New(s.m.pg.DB).Refresh(runCtx, time.Now())
		cancel()
		if ctx.Err() != nil {
			return
		}
		local := time.Now().In(db.FindingMetricsLocation)
		next := time.Date(local.Year(), local.Month(), local.Day()+1, 0, 5, 0, 0, db.FindingMetricsLocation)
		delay := time.Until(next)
		if err != nil {
			log.Printf("[finding-metrics] scheduled refresh: %v", err)
			delay = 5 * time.Minute
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}
