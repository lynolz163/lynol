package reporttemplate

import (
	"encoding/xml"
	"fmt"
	"strings"
)

// RenderBlocksDOCX writes editable Word paragraphs and tables, using styles from
// the bundled reference layout. It does not create external relationships.
func RenderBlocksDOCX(blocks []Block) string {
	var out strings.Builder
	writeDOCXBlocks(&out, blocks, 0)
	return out.String()
}

func writeDOCXBlocks(out *strings.Builder, blocks []Block, depth int) {
	for _, block := range blocks {
		switch block.Kind {
		case "list":
			for index, item := range block.Items {
				prefix := "• "
				if block.Ordered {
					prefix = fmt.Sprintf("%d. ", block.Start+index)
				}
				if len(item) > 0 && item[0].Kind == "paragraph" {
					first := item[0]
					inlines := append([]Inline{{Kind: "text", Text: prefix}}, first.Inlines...)
					writeDOCXParagraph(out, "ReportList", inlines, depth+1)
					writeDOCXBlocks(out, item[1:], depth+1)
				} else {
					writeDOCXParagraph(out, "ReportList", []Inline{{Kind: "text", Text: prefix}}, depth+1)
					writeDOCXBlocks(out, item, depth+1)
				}
			}
		case "quote":
			writeDOCXBlocks(out, block.Children, depth+1)
		case "table":
			writeDOCXTable(out, block.Rows)
		case "code":
			writeDOCXParagraph(out, "Evidence", []Inline{{Kind: "code", Text: strings.TrimSuffix(block.Text, "\n")}}, depth)
		case "heading":
			style := "Heading4"
			if block.Level <= 2 {
				style = "Heading3"
			}
			writeDOCXParagraph(out, style, block.Inlines, depth)
		case "rule":
			out.WriteString(`<w:p><w:pPr><w:pBdr><w:bottom w:val="single" w:sz="4" w:color="D9D9D9"/></w:pBdr></w:pPr></w:p>`)
		default:
			inlines := block.Inlines
			if block.Text != "" {
				inlines = append([]Inline{{Kind: "text", Text: block.Text}}, inlines...)
			}
			writeDOCXParagraph(out, "Normal", inlines, depth)
		}
	}
}

func writeDOCXParagraph(out *strings.Builder, style string, inlines []Inline, depth int) {
	out.WriteString(`<w:p><w:pPr><w:pStyle w:val="` + style + `"/>`)
	if depth > 0 {
		fmt.Fprintf(out, `<w:ind w:left="%d"/>`, depth*280)
	}
	out.WriteString(`</w:pPr>`)
	writeDOCXInlines(out, inlines, "")
	out.WriteString(`</w:p>`)
}

func writeDOCXInlines(out *strings.Builder, inlines []Inline, properties string) {
	for _, in := range inlines {
		switch in.Kind {
		case "strong":
			writeDOCXInlines(out, in.Children, properties+`<w:b/>`)
		case "emphasis":
			writeDOCXInlines(out, in.Children, properties+`<w:i/>`)
		case "strike":
			writeDOCXInlines(out, in.Children, properties+`<w:strike/>`)
		case "break":
			out.WriteString(`<w:r><w:br/></w:r>`)
		case "code":
			writeDOCXText(out, in.Text, properties+`<w:rFonts w:ascii="Menlo" w:hAnsi="Menlo"/><w:sz w:val="17"/>`)
		case "link", "image":
			writeDOCXText(out, referenceText(in), properties)
		default:
			// A Markdown soft break is whitespace; hard breaks have their own node.
			writeDOCXText(out, strings.ReplaceAll(in.Text, "\n", " "), properties)
			writeDOCXInlines(out, in.Children, properties)
		}
	}
}

func writeDOCXText(out *strings.Builder, value, properties string) {
	out.WriteString(`<w:r>`)
	if properties != "" {
		out.WriteString(`<w:rPr>` + properties + `</w:rPr>`)
	}
	value = strings.ReplaceAll(value, "\r\n", "\n")
	for i, line := range strings.Split(value, "\n") {
		if i > 0 {
			out.WriteString(`<w:br/>`)
		}
		for j, piece := range strings.Split(line, "\t") {
			if j > 0 {
				out.WriteString(`<w:tab/>`)
			}
			out.WriteString(`<w:t xml:space="preserve">`)
			_ = xml.EscapeText(out, []byte(piece))
			out.WriteString(`</w:t>`)
		}
	}
	out.WriteString(`</w:r>`)
}

func writeDOCXTable(out *strings.Builder, rows []TableRow) {
	columns := 0
	for _, row := range rows {
		columns = max(columns, len(row.Cells))
	}
	if columns == 0 {
		return
	}
	out.WriteString(`<w:tbl><w:tblPr><w:tblW w:w="5000" w:type="pct"/><w:tblBorders>`)
	for _, edge := range []string{"top", "left", "bottom", "right", "insideH", "insideV"} {
		out.WriteString(`<w:` + edge + ` w:val="single" w:sz="4" w:color="D9D9D9"/>`)
	}
	out.WriteString(`</w:tblBorders><w:tblCellMar><w:top w:w="80" w:type="dxa"/><w:left w:w="100" w:type="dxa"/><w:bottom w:w="80" w:type="dxa"/><w:right w:w="100" w:type="dxa"/></w:tblCellMar></w:tblPr><w:tblGrid>`)
	for range columns {
		fmt.Fprintf(out, `<w:gridCol w:w="%d"/>`, 9000/columns)
	}
	out.WriteString(`</w:tblGrid>`)
	for index, row := range rows {
		out.WriteString(`<w:tr><w:trPr><w:cantSplit/>`)
		fill, properties := "FFFFFF", ""
		if row.Header {
			fill, properties = "20384E", `<w:b/><w:color w:val="FFFFFF"/>`
			out.WriteString(`<w:tblHeader/>`)
		} else if index%2 == 0 {
			fill = "F3F6F8"
		}
		out.WriteString(`</w:trPr>`)
		for _, cell := range row.Cells {
			fmt.Fprintf(out, `<w:tc><w:tcPr><w:tcW w:w="%d" w:type="pct"/><w:shd w:fill="%s"/></w:tcPr><w:p><w:pPr><w:pStyle w:val="Normal"/></w:pPr>`, 5000/columns, fill)
			writeDOCXInlines(out, cell, properties)
			out.WriteString(`</w:p></w:tc>`)
		}
		out.WriteString(`</w:tr>`)
	}
	out.WriteString(`</w:tbl><w:p/>`)
}
