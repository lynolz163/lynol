package reporttemplate

import (
	"bytes"
	_ "embed"
	"html/template"
)

//go:embed haixing.html.tmpl
var haixingTemplate string

func RenderHTML(in Report) ([]byte, error) {
	view, err := template.New("haixing").Funcs(template.FuncMap{
		"blocks": RenderBlocksHTML, "content": ParseFindingContent,
		"severity": DisplaySeverity, "severityClass": SeverityClass,
		"status": DisplayStatus, "date": FormatDate, "asset": AssetLabel,
		"counts": SeverityCounts, "findingTitle": FindingTitle,
		"add": func(a, b int) int { return a + b },
	}).Parse(haixingTemplate)
	if err != nil {
		return nil, err
	}
	var output bytes.Buffer
	if err := view.Execute(&output, in); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}
