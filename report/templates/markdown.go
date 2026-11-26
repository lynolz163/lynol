package reporttemplate

import (
	"bufio"
	"bytes"
	"fmt"
	"html"
	"regexp"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	extast "github.com/yuin/goldmark/extension/ast"
	gmhtml "github.com/yuin/goldmark/renderer/html"
	"github.com/yuin/goldmark/text"
)

// ParseMarkdown uses CommonMark/GFM syntax, then converts only supported document
// structures. It never renders source HTML, fetches images, or follows links.
func ParseMarkdown(markdown string) []Block {
	source := []byte(markdown)
	md := goldmark.New(goldmark.WithExtensions(extension.GFM))
	doc := md.Parser().Parse(text.NewReader(source))
	return markdownBlocks(doc, source)
}

func markdownBlocks(parent ast.Node, source []byte) []Block {
	var out []Block
	for node := parent.FirstChild(); node != nil; node = node.NextSibling() {
		var block Block
		switch n := node.(type) {
		case *ast.Heading:
			block = Block{Kind: "heading", Level: n.Level, Inlines: markdownInlines(n, source)}
		case *ast.Paragraph, *ast.TextBlock:
			block = Block{Kind: "paragraph", Inlines: markdownInlines(n, source)}
		case *ast.FencedCodeBlock:
			block = Block{Kind: "code", Text: string(n.Lines().Value(source)), Language: string(n.Language(source))}
		case *ast.CodeBlock:
			block = Block{Kind: "code", Text: string(n.Lines().Value(source))}
		case *ast.HTMLBlock:
			// Preserve payloads as text, including closing script/style tags.
			var raw strings.Builder
			raw.Write(n.Lines().Value(source))
			if n.HasClosure() {
				raw.Write(n.ClosureLine.Value(source))
			}
			block = Block{Kind: "code", Text: raw.String()}
		case *ast.List:
			block = Block{Kind: "list", Ordered: n.IsOrdered(), Start: n.Start}
			for item := n.FirstChild(); item != nil; item = item.NextSibling() {
				block.Items = append(block.Items, markdownBlocks(item, source))
			}
		case *ast.Blockquote:
			block = Block{Kind: "quote", Children: markdownBlocks(n, source)}
		case *ast.ThematicBreak:
			block = Block{Kind: "rule"}
		case *extast.Table:
			block = Block{Kind: "table"}
			for row := n.FirstChild(); row != nil; row = row.NextSibling() {
				_, header := row.(*extast.TableHeader)
				parsed := TableRow{Header: header}
				for cell := row.FirstChild(); cell != nil; cell = cell.NextSibling() {
					parsed.Cells = append(parsed.Cells, markdownInlines(cell, source))
				}
				block.Rows = append(block.Rows, parsed)
			}
		default:
			if node.HasChildren() {
				out = append(out, markdownBlocks(node, source)...)
				continue
			}
			block = Block{Kind: "paragraph", Inlines: []Inline{{Kind: "text", Text: string(node.Lines().Value(source))}}}
		}
		out = append(out, block)
	}
	return out
}

func markdownInlines(parent ast.Node, source []byte) []Inline {
	var out []Inline
	for node := parent.FirstChild(); node != nil; node = node.NextSibling() {
		var inline Inline
		switch n := node.(type) {
		case *ast.Text:
			value := string(n.Value(source))
			if !n.IsRaw() {
				value = markdownText(n.Value(source))
			}
			out = append(out, Inline{Kind: "text", Text: value})
			if n.HardLineBreak() {
				out = append(out, Inline{Kind: "break"})
			} else if n.SoftLineBreak() {
				out = append(out, Inline{Kind: "text", Text: "\n"})
			}
			continue
		case *ast.String:
			value := string(n.Value)
			if !n.IsRaw() && !n.IsCode() {
				value = markdownText(n.Value)
			}
			inline = Inline{Kind: "text", Text: value}
		case *ast.Emphasis:
			kind := "emphasis"
			if n.Level == 2 {
				kind = "strong"
			}
			inline = Inline{Kind: kind, Children: markdownInlines(n, source)}
		case *extast.Strikethrough:
			inline = Inline{Kind: "strike", Children: markdownInlines(n, source)}
		case *ast.CodeSpan:
			var code strings.Builder
			for child := n.FirstChild(); child != nil; child = child.NextSibling() {
				code.WriteString(strings.ReplaceAll(string(child.(*ast.Text).Value(source)), "\n", " "))
			}
			inline = Inline{Kind: "code", Text: code.String()}
		case *ast.Link:
			inline = Inline{Kind: "link", Destination: markdownText(n.Destination), Children: markdownInlines(n, source)}
		case *ast.AutoLink:
			inline = Inline{Kind: "link", Destination: string(n.URL(source)), Children: []Inline{{Kind: "text", Text: string(n.Label(source))}}}
		case *ast.Image:
			inline = Inline{Kind: "image", Destination: markdownText(n.Destination), Children: markdownInlines(n, source)}
		case *ast.RawHTML:
			inline = Inline{Kind: "text", Text: string(n.Segments.Value(source))}
		case *extast.TaskCheckBox:
			label := "☐ "
			if n.IsChecked {
				label = "☑ "
			}
			inline = Inline{Kind: "text", Text: label}
		default:
			inline = Inline{Kind: "text", Text: string(node.Text(source)), Children: markdownInlines(node, source)}
		}
		out = append(out, inline)
	}
	return out
}

// Resolve Markdown escapes/entities through the parser's writer, then return
// ordinary text. Output renderers still escape this text for their own format.
func markdownText(raw []byte) string {
	var buffer bytes.Buffer
	writer := bufio.NewWriter(&buffer)
	gmhtml.DefaultWriter.Write(writer, raw)
	_ = writer.Flush()
	return html.UnescapeString(buffer.String())
}

var sectionNumber = regexp.MustCompile(`^[\s0-9一二三四五六七八九十百.．、()（）:：-]+`)

func sectionKind(title string) string {
	title = strings.ToLower(strings.TrimSpace(title))
	title = strings.TrimSpace(sectionNumber.ReplaceAllString(title, ""))
	title = strings.TrimRight(title, ":：")
	title = strings.ReplaceAll(title, " ", "")
	switch title {
	case "漏洞描述", "漏洞介绍", "漏洞概述", "问题描述", "描述", "概述", "description", "summary":
		return "description"
	case "风险影响", "风险危害", "漏洞危害", "危害", "影响", "impact", "riskimpact":
		return "impact"
	case "漏洞复现及poc", "漏洞复现与poc", "复现poc", "漏洞复现", "复现步骤", "复现过程", "复现与证据", "验证过程", "poc", "proof of concept", "proofofconcept", "证据", "poc/证据":
		return "poc"
	case "整改建议", "修复建议", "修复方法", "修复方案", "整改措施", "remediation", "recommendation", "recommendations":
		return "remediation"
	}
	return ""
}

// ParseFindingContent maps explicitly named report sections to the fixed layout.
// Unrecognized content remains in Additional; no risk or remediation is inferred.
func ParseFindingContent(f Finding) FindingContent {
	var result FindingContent
	active, level := "", 0
	for _, block := range ParseMarkdown(f.ReportMarkdown) {
		if block.Kind == "heading" {
			if kind := sectionKind(inlineText(block.Inlines)); kind != "" {
				active, level = kind, block.Level
				continue
			}
			if block.Level <= level {
				active = ""
			}
		}
		switch active {
		case "description":
			result.Description = append(result.Description, block)
		case "impact":
			result.Impact = append(result.Impact, block)
		case "poc":
			result.PoC = append(result.PoC, block)
		case "remediation":
			result.Remediation = append(result.Remediation, block)
		default:
			result.Additional = append(result.Additional, block)
		}
	}
	if summary := strings.TrimSpace(f.Summary); summary != "" && !strings.Contains(blocksText(result.Description), summary) {
		result.Description = append(ParseMarkdown(summary), result.Description...)
	}
	if evidence := strings.TrimSpace(f.Evidence); evidence != "" && !strings.Contains(blocksText(result.PoC), evidence) {
		result.PoC = append(result.PoC, Block{Kind: "code", Text: f.Evidence})
	}
	if len(f.Traffic) > 0 {
		result.PoC = append(result.PoC, Block{Kind: "heading", Level: 4, Inlines: []Inline{{Kind: "text", Text: "关联流量证据"}}})
		for _, traffic := range f.Traffic {
			label := fmt.Sprintf("证据 #%d · %s · %s %s · 状态码 %d", traffic.ID, traffic.Role, traffic.Method, traffic.URL, traffic.Status)
			result.PoC = append(result.PoC, Block{Kind: "paragraph", Inlines: []Inline{{Kind: "text", Text: label}}})
			if traffic.Note != "" {
				result.PoC = append(result.PoC, Block{Kind: "paragraph", Inlines: []Inline{{Kind: "text", Text: traffic.Note}}})
			}
			if traffic.ReqHash != "" || traffic.RespHash != "" {
				result.PoC = append(result.PoC, Block{Kind: "code", Text: "请求 SHA-256: " + traffic.ReqHash + "\n响应 SHA-256: " + traffic.RespHash})
			}
		}
	}
	return result
}

func inlineText(inlines []Inline) string {
	var out strings.Builder
	for _, in := range inlines {
		if in.Kind == "break" {
			out.WriteByte('\n')
		} else {
			out.WriteString(in.Text)
			out.WriteString(inlineText(in.Children))
		}
	}
	return out.String()
}

func blocksText(blocks []Block) string {
	var out strings.Builder
	for _, block := range blocks {
		out.WriteString(block.Text)
		out.WriteString(inlineText(block.Inlines))
		out.WriteString(blocksText(block.Children))
		for _, item := range block.Items {
			out.WriteString(blocksText(item))
		}
		for _, row := range block.Rows {
			for _, cell := range row.Cells {
				out.WriteString(inlineText(cell))
			}
		}
		out.WriteByte('\n')
	}
	return out.String()
}
