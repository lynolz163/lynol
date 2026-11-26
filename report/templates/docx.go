package reporttemplate

import (
	"archive/zip"
	"bytes"
	_ "embed"
	"encoding/xml"
	"fmt"
	"io"
	"strings"
	"text/template"
	"time"
)

// The shell retains the reference's styles, page geometry and page fields, but
// contains no customer report text, original metadata or preview thumbnail.
//
//go:embed ola-base.docx
var olaBase []byte

//go:embed ola-document.xml.tmpl
var olaDocument string

//go:embed ola-header.xml.tmpl
var olaHeader string

type wordFinding struct {
	Finding
	Number                                                           int
	SeverityLabel, StatusLabel, DiscoveredLabel, AssetText, Location string
	Content                                                          FindingContent
}

type wordReport struct {
	Report
	DateLabel                          string
	Critical, High, Medium, Low, Other int
	Counts                             []SeverityCount
	ScopeRows                          []wordScope
	Details                            []wordFinding
}

type wordScope struct {
	Kind, Value, Source string
}

// RenderDOCX writes a real OOXML document. No office process or external
// resource is needed on the server; every dynamic text node is XML-escaped.
func RenderDOCX(in Report) ([]byte, error) {
	view := wordReport{Report: in, Counts: SeverityCounts(in.Findings), DateLabel: in.ReportDate}
	if date, err := time.Parse("2006-01-02", in.ReportDate); err == nil {
		view.DateLabel = date.Format("2006 年 01 月 02 日")
	}
	for _, count := range view.Counts {
		switch count.Key {
		case "critical":
			view.Critical = count.Count
		case "high":
			view.High = count.Count
		case "medium":
			view.Medium = count.Count
		case "low":
			view.Low = count.Count
		default:
			view.Other += count.Count
		}
	}
	seenAssets := map[string]bool{}
	for _, asset := range in.Assets {
		label := AssetLabel(asset)
		if label != "" && !seenAssets[label] {
			seenAssets[label] = true
			view.ScopeRows = append(view.ScopeRows, wordScope{Kind: asset.Type, Value: label, Source: "任务关联资产"})
		}
	}
	if len(view.ScopeRows) == 0 {
		view.ScopeRows = []wordScope{{Kind: "测试范围", Value: "未记录", Source: "任务记录"}}
	}
	for index, finding := range in.Findings {
		item := wordFinding{Finding: finding, Number: index + 1,
			SeverityLabel: DisplaySeverity(finding.Severity), StatusLabel: DisplayStatus(finding.Status),
			DiscoveredLabel: FormatDate(finding.CreatedAt), Content: ParseFindingContent(finding)}
		item.Name = FindingTitle(finding)
		labels, locations := []string{}, []string{}
		for _, asset := range finding.Assets {
			labels = append(labels, AssetLabel(asset))
		}
		seen := map[string]bool{}
		for _, traffic := range finding.Traffic {
			location := strings.TrimSpace(traffic.Method + " " + traffic.URL)
			if location != "" && !seen[location] {
				seen[location] = true
				locations = append(locations, location)
			}
		}
		item.AssetText = strings.Join(labels, "；")
		item.Location = strings.Join(locations, "；")
		if item.AssetText == "" {
			item.AssetText = "未记录"
		}
		if item.Location == "" {
			item.Location = "未记录"
		}
		view.Details = append(view.Details, item)
	}

	core := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<cp:coreProperties xmlns:cp="http://schemas.openxmlformats.org/package/2006/metadata/core-properties" xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:dcterms="http://purl.org/dc/terms/" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"><dc:title>{{xml .Title}}</dc:title><dc:subject>渗透测试报告</dc:subject><dc:creator>{{xml .Author}}</dc:creator><cp:lastModifiedBy>{{xml .Author}}</cp:lastModifiedBy><cp:revision>1</cp:revision><dcterms:created xsi:type="dcterms:W3CDTF">{{.GeneratedAt.UTC.Format "2006-01-02T15:04:05Z"}}</dcterms:created><dcterms:modified xsi:type="dcterms:W3CDTF">{{.GeneratedAt.UTC.Format "2006-01-02T15:04:05Z"}}</dcterms:modified></cp:coreProperties>`
	replacements := map[string][]byte{}
	for name, layout := range map[string]string{"word/document.xml": olaDocument, "word/header1.xml": olaHeader, "docProps/core.xml": core} {
		part, err := template.New(name).Funcs(template.FuncMap{"xml": wordXMLText, "blocks": RenderBlocksDOCX}).Parse(layout)
		if err != nil {
			return nil, fmt.Errorf("Word 模板解析失败: %w", err)
		}
		var content bytes.Buffer
		if err := part.Execute(&content, view); err != nil {
			return nil, fmt.Errorf("Word 模板填充失败: %w", err)
		}
		replacements[name] = content.Bytes()
	}
	shell, err := zip.NewReader(bytes.NewReader(olaBase), int64(len(olaBase)))
	if err != nil {
		return nil, err
	}
	var output bytes.Buffer
	writer := zip.NewWriter(&output)
	for _, file := range shell.File {
		entry, err := writer.CreateHeader(&zip.FileHeader{Name: file.Name, Method: zip.Deflate})
		if err != nil {
			return nil, err
		}
		if replacement, ok := replacements[file.Name]; ok {
			if _, err = entry.Write(replacement); err != nil {
				return nil, err
			}
			continue
		}
		source, err := file.Open()
		if err != nil {
			return nil, err
		}
		_, err = io.Copy(entry, source)
		source.Close()
		if err != nil {
			return nil, err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

func wordXMLText(value string) string {
	var out bytes.Buffer
	_ = xml.EscapeText(&out, []byte(value))
	return out.String()
}
