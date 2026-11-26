package reporttemplate

import (
	"errors"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
	"time"
)

var ErrUnknownTemplate = errors.New("未知的报告模板")

func Render(templateID string, in Report) ([]byte, error) {
	in.Findings = append([]Finding(nil), in.Findings...)
	rank := map[string]int{"critical": 0, "high": 1, "medium": 2, "low": 3, "info": 4, "unknown": 5}
	sort.SliceStable(in.Findings, func(i, j int) bool {
		return rank[SeverityClass(in.Findings[i].Severity)] < rank[SeverityClass(in.Findings[j].Severity)]
	})
	if in.Title == "" {
		name := in.TaskName
		if name == "" {
			name = in.TaskDescription
		}
		if name == "" {
			name = fmt.Sprintf("任务 #%d", in.TaskID)
		}
		in.Title = name + "渗透测试报告"
	}
	if in.ReportDate == "" {
		in.ReportDate = in.GeneratedAt.In(reportTimezone).Format("2006-01-02")
	}
	switch templateID {
	case "haixing-html":
		return RenderHTML(in)
	case "ola-docx":
		return RenderDOCX(in)
	default:
		return nil, ErrUnknownTemplate
	}
}

var reportTimezone = time.FixedZone("Asia/Shanghai", 8*3600)

func DisplaySeverity(severity string) string {
	switch strings.ToLower(severity) {
	case "critical":
		return "严重"
	case "high":
		return "高危"
	case "medium":
		return "中危"
	case "low":
		return "低危"
	case "info", "informational":
		return "信息"
	case "":
		return "未知"
	default:
		return "未知（" + severity + "）"
	}
}

func SeverityClass(severity string) string {
	switch strings.ToLower(severity) {
	case "critical", "high", "medium", "low":
		return strings.ToLower(severity)
	case "info", "informational":
		return "info"
	default:
		return "unknown"
	}
}

func SeverityCounts(findings []Finding) []SeverityCount {
	counts := map[string]int{}
	for _, finding := range findings {
		counts[SeverityClass(finding.Severity)]++
	}
	result := make([]SeverityCount, 0, 6)
	for _, key := range []string{"critical", "high", "medium", "low", "info", "unknown"} {
		if (key == "info" || key == "unknown") && counts[key] == 0 {
			continue
		}
		label := DisplaySeverity(key)
		if key == "unknown" {
			label = "未知"
		}
		result = append(result, SeverityCount{Key: key, Label: label, Count: counts[key]})
	}
	return result
}

func DisplayStatus(status string) string {
	switch status {
	case "pending":
		return "待处理"
	case "in_progress":
		return "处理中"
	case "confirmed":
		return "已确认"
	case "resolved":
		return "已处理"
	case "fixed":
		return "已修复"
	case "false_positive":
		return "误报"
	case "ignored":
		return "已忽略"
	case "duplicate":
		return "重复"
	case "risk_accepted":
		return "风险接受"
	case "":
		return "未记录"
	default:
		return status
	}
}

func FindingTitle(f Finding) string {
	if f.Name != "" {
		return f.Name
	}
	if f.VulnClass != "" {
		return f.VulnClass
	}
	return fmt.Sprintf("漏洞 #%d", f.ID)
}

func FormatDate(value any) string {
	var stamp time.Time
	switch at := value.(type) {
	case time.Time:
		stamp = at
	case *time.Time:
		if at != nil {
			stamp = *at
		}
	}
	if stamp.IsZero() {
		return "未记录"
	}
	return stamp.In(reportTimezone).Format("2006-01-02 15:04:05")
}

func AssetLabel(asset Asset) string {
	if asset.Label != "" {
		return asset.Label
	}
	if asset.URL != "" {
		return asset.URL
	}
	host := asset.Domain
	if host == "" {
		host = asset.IP
	}
	if host != "" {
		if asset.Port != nil {
			return net.JoinHostPort(host, strconv.Itoa(*asset.Port))
		}
		return host
	}
	return fmt.Sprintf("资产 #%d", asset.ID)
}
