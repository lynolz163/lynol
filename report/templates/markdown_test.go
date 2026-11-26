package reporttemplate

import (
	"encoding/xml"
	"io"
	"strings"
	"testing"
	"time"

	xhtml "golang.org/x/net/html"
)

func TestMarkdownBlocksPreserveStructureAndPayloadText(t *testing.T) {
	input := "## 验证步骤\n\n**加粗**、*斜体*、~~旧结论~~ 与 `a<b && c`。\n\n" +
		"3. 第一项\n   - 嵌套项\n4. 第二项\n\n> 引用证据\n\n" +
		"| 参数 | 取值 |\n| --- | --- |\n| name | a\\|b |\n\n" +
		"```http\nGET /?x=<script>&y=1 HTTP/1.1\nHost: example.invalid\n```\n\n" +
		"<script>alert('payload')</script>\n\n" +
		"![截图](https://images.invalid/probe.png) [证据](javascript:alert(1))\n"
	blocks := ParseMarkdown(input)
	counts := map[string]int{}
	var visit func([]Block)
	visit = func(items []Block) {
		for _, block := range items {
			counts[block.Kind]++
			visit(block.Children)
			for _, item := range block.Items {
				visit(item)
			}
		}
	}
	visit(blocks)
	for kind, minimum := range map[string]int{"heading": 1, "paragraph": 6, "list": 2, "quote": 1, "table": 1, "code": 2} {
		if counts[kind] < minimum {
			t.Fatalf("lost %s structure: counts=%v", kind, counts)
		}
	}
	html := string(RenderBlocksHTML(blocks))
	for _, text := range []string{`<ol start="3">`, "<strong>加粗</strong>", "<em>斜体</em>", "<s>旧结论</s>", "<table>", "a|b", "Host: example.invalid", "&lt;script&gt;alert", "[图片：截图] https://images.invalid/probe.png", "javascript:alert(1)"} {
		if !strings.Contains(html, text) {
			t.Errorf("HTML lost %q: %s", text, html)
		}
	}
	assertPassiveHTML(t, html)
	word := RenderBlocksDOCX(blocks)
	if rows := strings.Count(word, "<w:tr>"); rows == 0 || strings.Count(word, "<w:cantSplit/>") != rows {
		t.Fatal("Word table rows may split across pages")
	}
	for _, text := range []string{`<w:pStyle w:val="Evidence"/>`, `<w:pStyle w:val="ReportList"/>`, `<w:tbl>`, `<w:tblHeader/>`, "Host: example.invalid", "&lt;script&gt;alert", "javascript:alert(1)"} {
		if !strings.Contains(word, text) {
			t.Errorf("Word blocks lost %q: %s", text, word)
		}
	}
	decoder := xml.NewDecoder(strings.NewReader(`<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>` + word + `</w:body></w:document>`))
	for {
		_, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("invalid Word XML: %v", err)
		}
	}
}

func TestFindingSectionsUseExplicitHeadingsAndKeepUnknownContent(t *testing.T) {
	finding := Finding{
		Summary:  "数据库保存的摘要",
		Evidence: "POST /demo HTTP/1.1\nX-Probe: <payload>",
		ReportMarkdown: "# 自定义报告标题\n\n未分类说明\n\n" +
			"## 1. 漏洞描述\n\n已经记录的描述\n\n" +
			"## 2. 风险影响\n\n- 已确认的影响\n\n" +
			"## 3. 漏洞复现及 PoC\n\n### 对照测试\n\n测试结果原文\n\n" +
			"## 4. 整改建议\n\n1. 已记录的建议\n\n" +
			"## 未识别章节\n\n必须保留的其他内容\n",
	}
	content := ParseFindingContent(finding)
	for name, check := range map[string]struct {
		blocks []Block
		texts  []string
	}{
		"description": {content.Description, []string{"数据库保存的摘要", "已经记录的描述"}},
		"impact":      {content.Impact, []string{"已确认的影响"}},
		"poc":         {content.PoC, []string{"对照测试", "测试结果原文", "POST /demo HTTP/1.1\nX-Probe: <payload>"}},
		"remediation": {content.Remediation, []string{"已记录的建议"}},
		"additional":  {content.Additional, []string{"自定义报告标题", "未分类说明", "未识别章节", "必须保留的其他内容"}},
	} {
		for _, text := range check.texts {
			if !strings.Contains(blocksText(check.blocks), text) {
				t.Errorf("%s lost %q", name, text)
			}
		}
	}
	empty := ParseFindingContent(Finding{Summary: "仅有摘要", Evidence: "仅有证据"})
	if len(empty.Impact) != 0 || len(empty.Remediation) != 0 || blocksText(empty.Description) != "仅有摘要\n" || blocksText(empty.PoC) != "仅有证据\n" {
		t.Fatalf("invented missing sections: %+v", empty)
	}
}

func TestMarkdownEscapesDoNotChangeCodeOrBecomeActive(t *testing.T) {
	blocks := ParseMarkdown("正文 &amp; \\*字面星号\\* <b>原始标签</b>\n\n`&amp; \\* <b>`\n")
	got := string(RenderBlocksHTML(blocks))
	for _, want := range []string{"正文 &amp; *字面星号* &lt;b&gt;原始标签&lt;/b&gt;", "<code>&amp;amp; \\* &lt;b&gt;</code>"} {
		if !strings.Contains(got, want) {
			t.Errorf("escape changed semantics, missing %q: %s", want, got)
		}
	}
	assertPassiveHTML(t, got)
}

func TestReportHTMLCountsAndDynamicFieldsRemainPassive(t *testing.T) {
	in := Report{
		TaskID: 12, Title: `<script>title</script>`, TaskName: "测试任务", ReportDate: "2026-11-26",
		Author: `A&B <author>`, Organization: `<img src="https://invalid.example/image">`,
		GeneratedAt: time.Date(2026, 11, 26, 2, 0, 0, 0, time.UTC),
		Findings: []Finding{
			{ID: 1, Severity: "critical", Status: "confirmed", Summary: "摘要", ReportMarkdown: "## 危害\n\n明确影响\n", Name: `<svg onload="test">`},
			{ID: 2, Severity: "info", Status: "ignored", VulnClass: "未命名类型"},
			{ID: 3, Severity: "custom", Status: "pending"},
		},
	}
	data, err := RenderHTML(in)
	if err != nil {
		t.Fatal(err)
	}
	assertPassiveHTML(t, string(data))
	for _, want := range []string{"&lt;script&gt;title&lt;/script&gt;", "A&amp;B &lt;author&gt;", "明确影响", "未命名类型", "漏洞 #3"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("report lost %q", want)
		}
	}
	count := 0
	for _, severity := range SeverityCounts(in.Findings) {
		count += severity.Count
	}
	if count != len(in.Findings) {
		t.Fatalf("summary total differs from details: %d", count)
	}
}

func assertPassiveHTML(t *testing.T, source string) {
	t.Helper()
	doc, err := xhtml.Parse(strings.NewReader(source))
	if err != nil {
		t.Fatal(err)
	}
	var walk func(*xhtml.Node)
	walk = func(node *xhtml.Node) {
		if node.Type == xhtml.ElementNode {
			switch node.Data {
			case "script", "iframe", "img", "svg", "object", "embed":
				t.Errorf("active source content became %s element", node.Data)
			}
			for _, attr := range node.Attr {
				if strings.HasPrefix(attr.Key, "on") || (attr.Key == "href" && !strings.HasPrefix(attr.Val, "#")) || attr.Key == "src" {
					t.Errorf("unexpected active/resource attribute: %s=%q", attr.Key, attr.Val)
				}
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(doc)
}
