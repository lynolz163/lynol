package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/artex/internal/org"
	orgreport "github.com/Autumn-27/artex/internal/report"
	"github.com/Autumn-27/artex/notify"
)

func validFindingMetricsFilter(f db.FindingMetricsFilter) bool {
	return orgreport.ValidateFilter(f) == nil
}

func (s *Server) findingResponsibilityOptions(w http.ResponseWriter, r *http.Request) {
	pg := s.pg(w)
	if pg == nil {
		return
	}
	out, err := org.New(pg.DB).Directory(r.Context())
	if err != nil {
		writeErr(w, 500, "读取归属选项失败")
		return
	}
	writeJSON(w, 200, out)
}

func (s *Server) setFindingResponsibility(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusConflict, map[string]any{"error": "漏洞归属沿用主资产，请到资产归属页面调整", "code": "asset_ownership_required", "redirect": "/settings/org?tab=assets"})
}

func (s *Server) findingReportSchedules(w http.ResponseWriter, r *http.Request) {
	pg := s.pg(w)
	if pg == nil {
		return
	}
	if r.Method == http.MethodGet {
		schedules, err := pg.FindingReportSchedules(r.Context())
		if err != nil {
			writeErr(w, 500, "读取报告计划失败")
			return
		}
		writeJSON(w, 200, map[string]any{"items": schedules})
		return
	}
	var schedule db.FindingReportSchedule
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if err := json.NewDecoder(r.Body).Decode(&schedule); err != nil ||
		(schedule.Kind != "weekly" && schedule.Kind != "monthly") || schedule.Weekday < 1 || schedule.Weekday > 7 ||
		schedule.Monthday < 1 || schedule.Monthday > 28 || schedule.Hour < 0 || schedule.Hour > 23 || schedule.Minute < 0 || schedule.Minute > 59 || !validFindingMetricsFilter(schedule.Filter) {
		writeErr(w, 400, "报告计划参数无效")
		return
	}
	if err := orgreport.New(pg.DB).ValidateSchedule(r.Context(), schedule); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	schedule.Filter.AttributionVersion = 1
	schedule.Filter.DepartmentID = nil
	schedule.ChannelID = nil
	if err := pg.SaveFindingReportSchedule(r.Context(), schedule); err != nil {
		writeErr(w, 500, "保存报告计划失败")
		return
	}
	writeJSON(w, 200, schedule)
}

func (s *Server) findingReports(w http.ResponseWriter, r *http.Request) {
	pg := s.pg(w)
	if pg == nil {
		return
	}
	if r.Method == http.MethodGet {
		items, err := pg.ListFindingReports(r.Context())
		if err != nil {
			writeErr(w, 500, "读取报告历史失败")
			return
		}
		writeJSON(w, 200, map[string]any{"items": items})
		return
	}
	var body struct {
		Kind   string                  `json:"kind"`
		Filter db.FindingMetricsFilter `json:"filter"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || (body.Kind != "weekly" && body.Kind != "monthly") || !validFindingMetricsFilter(body.Filter) {
		writeErr(w, 400, "无效报告参数")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Minute)
	defer cancel()
	if err := orgreport.New(pg.DB).Refresh(ctx, time.Now()); err != nil {
		writeErr(w, 500, "汇总失败，请稍后重试")
		return
	}
	id, err := orgreport.New(pg.DB).StoreReport(ctx, body.Kind, time.Now(), body.Filter)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]int64{"id": id})
}

func (s *Server) findingReportDetail(w http.ResponseWriter, r *http.Request) {
	pg := s.pg(w)
	if pg == nil {
		return
	}
	id, ok := pathInt(r, "id")
	if !ok || id <= 0 {
		writeErr(w, 400, "报告编号无效")
		return
	}
	report, err := pg.GetFindingReport(r.Context(), id)
	if err != nil {
		writeErr(w, 500, "读取报告失败")
		return
	}
	if report == nil {
		writeErr(w, 404, "报告不存在")
		return
	}
	writeJSON(w, 200, report)
}

func findingReportContent(report *db.SavedFindingReport) notify.ReportContent {
	kind := "周报"
	if report.Kind == "monthly" {
		kind = "月报"
	}
	title := fmt.Sprintf("漏洞治理%s · %s 至 %s", kind, report.From, report.To)
	current := report.Current
	previous := report.Previous
	summary := current.Summary
	line := fmt.Sprintf("新增 %d · 关闭 %d · 重开 %d", summary.New, summary.Closed, summary.Reopened)
	if previous != nil && previous.GeneratedAt != nil {
		line += fmt.Sprintf("；较上期新增 %+d、关闭 %+d", summary.New-previous.Summary.New, summary.Closed-previous.Summary.Closed)
	}
	lines := []string{line}
	scope := []string{}
	for _, part := range []struct{ label, value string }{{"等级", report.Filter.Severity}, {"业务线", report.Filter.BusinessLine}, {"负责人", report.Filter.Assignee}} {
		if part.value != "" {
			value := part.value
			if value == "__none__" {
				value = "业务线未归属"
				if part.label == "负责人" {
					value = "负责人未指派"
				}
			}
			scope = append(scope, part.label+"："+notify.OneLine(value, 40))
		}
	}
	if len(scope) > 0 {
		lines = append(lines, strings.Join(scope, " · "))
	}
	if current.StockKnown {
		lines = append(lines, fmt.Sprintf("期末待处置 %d · 逾期 %d", summary.Open, summary.Overdue))
	} else {
		lines = append(lines, "期末存量未知：该期间尚未开始完整历史采集。")
	}
	if total := summary.SLAClosed + summary.Overdue; current.StockKnown && total > 0 {
		lines = append(lines, fmt.Sprintf("SLA 全量达标率 %.1f%%（%d / %d，有效关闭 + 期末逾期）", 100*float64(summary.SLAMet)/float64(total), summary.SLAMet, total))
	} else {
		lines = append(lines, "SLA 全量达标率：暂无完整数据或有效样本")
	}
	if summary.SLAClosed > 0 {
		lines = append(lines, fmt.Sprintf("已关闭口径 %.1f%%（%d / %d）", 100*float64(summary.SLAMet)/float64(summary.SLAClosed), summary.SLAMet, summary.SLAClosed))
	}
	for _, duration := range current.Durations {
		if duration.Key == "closure" && duration.Average != nil {
			lines = append(lines, fmt.Sprintf("闭环平均 %.1f 天 · P90 %.1f 天", *duration.Average/24, *duration.P90/24))
		}
	}
	if total := summary.RetestPassed + summary.RetestFailed; total > 0 {
		lines = append(lines, fmt.Sprintf("复测不通过率 %.1f%%（%d / %d）", 100*float64(summary.RetestFailed)/float64(total), summary.RetestFailed, total))
	}
	qualityNote := ""
	if !current.QualityKnown {
		qualityNote = "（已记录样本）"
	}
	if summary.FirstRetestTotal > 0 {
		lines = append(lines, fmt.Sprintf("一次修复通过率 %.1f%%（%d / %d）", 100*float64(summary.FirstRetestPassed)/float64(summary.FirstRetestTotal), summary.FirstRetestPassed, summary.FirstRetestTotal)+qualityNote)
	}
	if summary.VerifiedFixes > 0 {
		lines = append(lines, fmt.Sprintf("平均返工 %.2f 轮（%d 次返工 / %d 次确认修复）", float64(summary.ReworkRounds)/float64(summary.VerifiedFixes), summary.ReworkRounds, summary.VerifiedFixes)+qualityNote)
	}
	count := 0
	for _, group := range current.Groups {
		if group.Dimension != "business_line" || count >= 3 {
			continue
		}
		name := group.Name
		if name == "" {
			name = "业务线未归属"
		}
		if current.StockKnown {
			lines = append(lines, fmt.Sprintf("业务线 %s：待处置 %d，逾期 %d，关闭 %d", notify.OneLine(name, 30), group.Open, group.Overdue, group.Closed))
		} else {
			lines = append(lines, fmt.Sprintf("业务线 %s：关闭 %d，存量未知", notify.OneLine(name, 30), group.Closed))
		}
		count++
	}
	if !current.StockKnown {
		lines = append(lines, "部分历史时间缺失，未知指标不参与排名。")
	}
	return notify.ReportContent{ID: report.ID, Title: title, Lines: lines}
}

func (s *Server) runFindingReports(ctx context.Context) {
	tick := time.NewTicker(time.Minute)
	defer tick.Stop()
	for {
		runCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		if err := s.generateDueFindingReports(runCtx, time.Now()); err != nil && ctx.Err() == nil {
			log.Printf("[finding-reports] %v", err)
		}
		cancel()
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

func (s *Server) generateDueFindingReports(ctx context.Context, now time.Time) error {
	schedules, err := s.m.pg.FindingReportSchedules(ctx)
	if err != nil {
		return err
	}
	store := orgreport.New(s.m.pg.DB)
	refreshed := false
	for _, schedule := range schedules {
		// Legacy schedules cannot silently acquire a new organization identity.
		if schedule.ScopeMode != "departments" || !db.FindingReportScheduleDue(schedule, now) {
			continue
		}
		if !refreshed {
			if err = store.Refresh(ctx, now); err != nil {
				return err
			}
			refreshed = true
		}
		for _, department := range schedule.DepartmentIDs {
			filter := schedule.Filter
			filter.DepartmentID = &department
			filter.AttributionVersion = 1
			id, e := store.StoreReport(ctx, schedule.Kind, now, filter)
			if e != nil {
				return e
			}
			saved, e := s.m.pg.GetFindingReport(ctx, id)
			if e != nil {
				return e
			}
			if e = store.QueueReport(ctx, id, findingReportContent(saved)); e != nil {
				return e
			}
		}
	}
	return nil
}
