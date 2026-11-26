package reporttemplate

import (
	"fmt"
	"html"
	"html/template"
	"strings"
)

// RenderBlocksHTML returns markup built from fixed tags and escaped text only.
// In particular, source HTML and image URLs never become active document nodes.
func RenderBlocksHTML(blocks []Block) template.HTML {
	var out strings.Builder
	writeHTMLBlocks(&out, blocks)
	return template.HTML(out.String())
}

func writeHTMLBlocks(out *strings.Builder, blocks []Block) {
	for _, block := range blocks {
		switch block.Kind {
		case "heading":
			level := min(6, max(3, block.Level+2))
			fmt.Fprintf(out, "<h%d>", level)
			writeHTMLInlines(out, block.Inlines)
			fmt.Fprintf(out, "</h%d>\n", level)
		case "code":
			out.WriteString("<pre><code>")
			out.WriteString(html.EscapeString(block.Text))
			out.WriteString("</code></pre>\n")
		case "list":
			tag := "ul"
			if block.Ordered {
				tag = "ol"
				fmt.Fprintf(out, "<ol start=\"%d\">\n", block.Start)
			} else {
				out.WriteString("<ul>\n")
			}
			for _, item := range block.Items {
				out.WriteString("<li>")
				writeHTMLBlocks(out, item)
				out.WriteString("</li>\n")
			}
			fmt.Fprintf(out, "</%s>\n", tag)
		case "quote":
			out.WriteString("<blockquote>")
			writeHTMLBlocks(out, block.Children)
			out.WriteString("</blockquote>\n")
		case "table":
			out.WriteString("<div class=\"markdown-table\"><table>\n")
			for _, row := range block.Rows {
				out.WriteString("<tr>")
				tag := "td"
				if row.Header {
					tag = "th"
				}
				for _, cell := range row.Cells {
					fmt.Fprintf(out, "<%s>", tag)
					writeHTMLInlines(out, cell)
					fmt.Fprintf(out, "</%s>", tag)
				}
				out.WriteString("</tr>\n")
			}
			out.WriteString("</table></div>\n")
		case "rule":
			out.WriteString("<hr>\n")
		default:
			out.WriteString("<p>")
			out.WriteString(html.EscapeString(block.Text))
			writeHTMLInlines(out, block.Inlines)
			out.WriteString("</p>\n")
		}
	}
}

func writeHTMLInlines(out *strings.Builder, inlines []Inline) {
	for _, in := range inlines {
		switch in.Kind {
		case "strong", "emphasis", "strike":
			tag := map[string]string{"strong": "strong", "emphasis": "em", "strike": "s"}[in.Kind]
			fmt.Fprintf(out, "<%s>", tag)
			writeHTMLInlines(out, in.Children)
			fmt.Fprintf(out, "</%s>", tag)
		case "code":
			out.WriteString("<code>" + html.EscapeString(in.Text) + "</code>")
		case "break":
			out.WriteString("<br>\n")
		case "link", "image":
			out.WriteString(html.EscapeString(referenceText(in)))
		default:
			out.WriteString(html.EscapeString(in.Text))
			writeHTMLInlines(out, in.Children)
		}
	}
}

func referenceText(in Inline) string {
	label := inlineText(in.Children)
	if in.Kind == "image" {
		if label == "" {
			label = "图片"
		}
		return "[图片：" + label + "] " + in.Destination
	}
	if label == "" || label == in.Destination {
		return in.Destination
	}
	return label + "（" + in.Destination + "）"
}
