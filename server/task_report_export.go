package server

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Autumn-27/artex/db"
	reporttemplate "github.com/Autumn-27/artex/report/templates"
)

// exportTaskReport downloads a report without changing task, finding or delivery
// state. The database selects the task's complete own findings, not the UI page.
func (s *Server) exportTaskReport(w http.ResponseWriter, r *http.Request) {
	pg := s.pg(w)
	if pg == nil {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeErr(w, http.StatusBadRequest, "无效的任务编号")
		return
	}
	var input struct {
		Template        string `json:"template"`
		Title           string `json:"title"`
		ReportDate      string `json:"report_date"`
		Author          string `json:"author"`
		Organization    string `json:"organization"`
		IncludeExcluded bool   `json:"include_excluded"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if err := decode(r, &input); err != nil {
		writeErr(w, http.StatusBadRequest, "无效的报告导出参数")
		return
	}
	var contentType, extension string
	switch input.Template {
	case "haixing-html":
		contentType, extension = "text/html; charset=utf-8", "html"
	case "ola-docx":
		contentType, extension = "application/vnd.openxmlformats-officedocument.wordprocessingml.document", "docx"
	default:
		writeErr(w, http.StatusBadRequest, "请选择 HTML 或 Word 报告模板")
		return
	}
	for _, field := range []struct {
		name  string
		value *string
	}{{"报告标题", &input.Title}, {"编写人", &input.Author}, {"测试单位", &input.Organization}} {
		*field.value = strings.TrimSpace(*field.value)
		if utf8.RuneCountInString(*field.value) > 200 {
			writeErr(w, http.StatusBadRequest, field.name+"不能超过 200 个字符")
			return
		}
	}
	now := time.Now().In(db.FindingMetricsLocation)
	if input.ReportDate == "" {
		input.ReportDate = now.Format("2006-01-02")
	} else if _, err := time.Parse("2006-01-02", input.ReportDate); err != nil {
		writeErr(w, http.StatusBadRequest, "报告日期请填写有效的 YYYY-MM-DD 日期")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	task, err := pg.GetTask(id)
	if err != nil {
		log.Printf("[report] 读取任务失败 task=%d: %v", id, err)
		writeErr(w, http.StatusInternalServerError, "读取任务报告数据失败")
		return
	}
	if task == nil {
		writeErr(w, http.StatusNotFound, "任务不存在或已归档")
		return
	}
	findings, err := pg.ListFindingsForExport(db.FindingFilter{TaskID: strconv.FormatInt(id, 10)}, nil)
	if err != nil {
		log.Printf("[report] 读取任务漏洞失败 task=%d: %v", id, err)
		writeErr(w, http.StatusInternalServerError, "读取任务漏洞失败")
		return
	}
	if !input.IncludeExcluded {
		included := findings[:0]
		for _, finding := range findings {
			switch finding.Status {
			case db.FindingFalsePositive, db.FindingDuplicate, db.FindingIgnored:
				continue
			}
			included = append(included, finding)
		}
		findings = included
	}
	taskAssets, err := pg.Assets().QueryByTask(id, "", 0, 0)
	if err != nil {
		log.Printf("[report] 读取任务资产失败 task=%d: %v", id, err)
		writeErr(w, http.StatusInternalServerError, "读取任务资产失败")
		return
	}
	// A finding can retain an explicit asset reference even if the task's asset
	// list was edited later. Keep that reference on its finding, not in scope totals.
	assetIDs := make([]int64, 0)
	seen := map[int64]bool{}
	for _, finding := range findings {
		for _, assetID := range finding.AssetIDs {
			if !seen[assetID] {
				seen[assetID] = true
				assetIDs = append(assetIDs, assetID)
			}
		}
	}
	linkedAssets, err := pg.Assets().GetByIDs(assetIDs)
	if err != nil {
		log.Printf("[report] 读取漏洞关联资产失败 task=%d: %v", id, err)
		writeErr(w, http.StatusInternalServerError, "读取漏洞关联资产失败")
		return
	}
	if err := s.evidenceStore().StageFindingsExport(ctx, findings, "", false); err != nil {
		evidenceError(w, err)
		return
	}
	name := strings.TrimSpace(task.Name)
	if name == "" {
		name = strings.TrimSpace(task.Description)
	}
	if name == "" {
		name = fmt.Sprintf("任务 #%d", id)
	}
	if input.Title == "" {
		input.Title = name + "渗透测试报告"
	}
	document := reporttemplate.Report{
		TaskID: id, TaskName: name, Title: input.Title, ReportDate: input.ReportDate,
		Author: input.Author, Organization: input.Organization,
		TaskDescription: task.Description, Goal: task.Goal, TaskStatus: task.Status,
		GeneratedAt: now, StartedAt: task.FirstRunAt, CompletedAt: task.CompletedAt,
	}
	toAsset := func(asset *db.Asset) reporttemplate.Asset {
		return reporttemplate.Asset{ID: asset.ID, Type: asset.Type, Label: assetLabel(asset),
			URL: asset.URL, Domain: asset.Domain, IP: asset.IP, Port: asset.Port}
	}
	for _, asset := range taskAssets {
		document.Assets = append(document.Assets, toAsset(asset))
	}
	assetByID := make(map[int64]reporttemplate.Asset, len(linkedAssets))
	for _, asset := range linkedAssets {
		assetByID[asset.ID] = toAsset(asset)
	}
	for _, finding := range findings {
		item := reporttemplate.Finding{
			ID: finding.ID, Name: finding.Name, VulnClass: finding.VulnClass,
			Severity: finding.Severity, Status: finding.Status, Summary: finding.Summary,
			Evidence: finding.Evidence, ReportMarkdown: finding.Report, CreatedAt: finding.CreatedAt,
			EvidenceVersion: finding.EvidenceVersion, ReportEvidenceVersion: finding.ReportEvidenceVersion,
		}
		for _, assetID := range finding.AssetIDs {
			if asset, ok := assetByID[assetID]; ok {
				item.Assets = append(item.Assets, asset)
			}
		}
		for _, binding := range finding.TrafficBindings {
			item.Traffic = append(item.Traffic, reporttemplate.Traffic{
				ID: binding.ID, Role: binding.Role, Note: binding.Note, Position: binding.Position,
				Method: binding.Snapshot.Method, URL: binding.Snapshot.URL, Status: binding.Snapshot.Status,
				ReqHash: binding.Snapshot.ReqHash, RespHash: binding.Snapshot.RespHash,
			})
		}
		document.Findings = append(document.Findings, item)
	}
	content, err := reporttemplate.Render(input.Template, document)
	if err != nil {
		log.Printf("[report] 生成任务报告失败 task=%d template=%s: %v", id, input.Template, err)
		writeErr(w, http.StatusInternalServerError, "报告生成失败，请重试")
		return
	}
	filename := fmt.Sprintf("task-%d-%s-%s.%s", id, input.Template, now.Format("20060102-150405"), extension)
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(content)
}
