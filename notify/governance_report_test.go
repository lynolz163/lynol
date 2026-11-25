package notify

import (
	"strings"
	"testing"
)

func TestGovernanceReportRender(t *testing.T) {
	m := Message{Items: []Item{{Name: "report"}}, Report: &ReportContent{Title: "漏洞治理周报", Lines: []string{"新增 12 · 关闭 15", strings.Repeat("阶段样本", 80), "业务线 <script>alert(1)</script> [link](https://example.test)"}}, HomeURL: "https://artex.example.test/remediation-report?id=1"}
	markdown, kept := markdownBody(m, weComMarkdownLimit)
	if kept != 1 || !strings.Contains(markdown, "新增 12") || !strings.Contains(markdown, strings.Repeat("阶段样本", 80)) || strings.Contains(markdown, "[link](https://example.test)") {
		t.Fatal("report body truncated to finding-summary length or unescaped")
	}
	html := htmlBody(m, 0)
	if strings.Contains(html, "<script>") || !strings.Contains(html, "&lt;script&gt;") || htmlTitle(m) != "漏洞治理周报" {
		t.Fatal("report title or escaping incorrect")
	}
}
