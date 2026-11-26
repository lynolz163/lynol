package server

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Autumn-27/artex/db"
	"golang.org/x/net/html"
)

func taskReportFacts(t *testing.T, d *db.DB) string {
	t.Helper()
	var value string
	if err := d.QueryRow(`SELECT concat_ws(':',
	 (SELECT md5(COALESCE(string_agg(to_jsonb(t)::text,',' ORDER BY id),'')) FROM tasks t),
	 (SELECT md5(COALESCE(string_agg(to_jsonb(f)::text,',' ORDER BY id),'')) FROM findings f),
	 (SELECT md5(COALESCE(string_agg(to_jsonb(a)::text,',' ORDER BY id),'')) FROM assets a),
	 (SELECT md5(COALESCE(string_agg(to_jsonb(h)::text,',' ORDER BY id),'')) FROM finding_history h),
	 (SELECT md5(COALESCE(string_agg(to_jsonb(b)::text,',' ORDER BY id),'')) FROM finding_traffic_bindings b),
	 (SELECT md5(COALESCE(string_agg(to_jsonb(s)::text,',' ORDER BY key),'')) FROM settings s),
	 (SELECT count(*) FROM notification_events),(SELECT count(*) FROM notification_deliveries),
	 (SELECT count(*) FROM finding_reports),(SELECT count(*) FROM org.audit_log))`).Scan(&value); err != nil {
		t.Fatal(err)
	}
	return value
}

func taskReportHTMLText(t *testing.T, body []byte) string {
	t.Helper()
	doc, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	var text strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			switch n.Data {
			case "script", "iframe", "object", "embed", "img", "base":
				t.Fatalf("download contains active or externally loaded element %q", n.Data)
			}
			for _, attr := range n.Attr {
				if strings.HasPrefix(strings.ToLower(attr.Key), "on") || (attr.Key == "href" && strings.HasPrefix(strings.ToLower(strings.TrimSpace(attr.Val)), "javascript:")) {
					t.Fatalf("unsafe dynamic HTML attribute: %s", attr.Key)
				}
			}
		}
		if n.Type == html.TextNode && (n.Parent == nil || n.Parent.Data != "style") {
			text.WriteString(n.Data)
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(doc)
	return text.String()
}

func taskReportDOCXText(t *testing.T, body []byte) string {
	t.Helper()
	archive, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	var text strings.Builder
	for _, entry := range archive.File {
		lower := strings.ToLower(entry.Name)
		if strings.HasPrefix(entry.Name, "/") || strings.Contains(entry.Name, "..") || strings.Contains(lower, "vbaproject") || strings.HasPrefix(lower, "word/embeddings/") {
			t.Fatalf("unexpected document package entry: %s", entry.Name)
		}
		seen[entry.Name] = true
		r, err := entry.Open()
		if err != nil {
			t.Fatal(err)
		}
		raw, err := io.ReadAll(r)
		r.Close()
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasSuffix(entry.Name, ".xml") || strings.HasSuffix(entry.Name, ".rels") {
			decoder := xml.NewDecoder(bytes.NewReader(raw))
			for {
				token, err := decoder.Token()
				if err == io.EOF {
					break
				}
				if err != nil {
					t.Fatalf("invalid XML in %s: %v", entry.Name, err)
				}
				if start, ok := token.(xml.StartElement); ok {
					// The source's empty bibliography style metadata is harmless;
					// actual bibliography records must not survive in the template.
					if entry.Name == "customXml/item1.xml" && start.Name.Local == "Source" {
						t.Fatal("Word template retained a source bibliography record")
					}
					if start.Name.Local == "altChunk" {
						t.Fatal("Word report embeds HTML instead of native document content")
					}
					for _, attr := range start.Attr {
						if attr.Name.Local == "TargetMode" && attr.Value == "External" {
							t.Fatalf("external Word relationship in %s", entry.Name)
						}
					}
				}
				if data, ok := token.(xml.CharData); ok && entry.Name == "word/document.xml" {
					text.Write(data)
				}
			}
		}
	}
	for _, required := range []string{"[Content_Types].xml", "_rels/.rels", "word/document.xml", "word/styles.xml"} {
		if !seen[required] {
			t.Fatalf("Word package is missing %s", required)
		}
	}
	return text.String()
}

func TestTaskReportExportIncludesCompleteOwnScopeWithoutWrites(t *testing.T) {
	if os.Getenv("ARTEX_PG_DSN") == "" {
		t.Skip("task report integration tests require explicit ARTEX_PG_DSN")
	}
	s, initial, request := trafficEvidenceServer(t)
	finding, err := s.m.pg.GetFinding(initial.FindingID)
	if err != nil || finding.TaskID == nil {
		t.Fatalf("finding task: %+v %v", finding, err)
	}
	taskID := *finding.TaskID
	for _, marker := range []string{"OTHER-TASK-SECRET-MARKER", "SOURCE-TASK-SECRET-MARKER"} {
		other, err := s.m.pg.CreateTask(marker, "isolated report fixture", nil, 0, 0)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { s.m.pg.Exec(`DELETE FROM findings WHERE task_id=$1`, other.ID); s.m.pg.DeleteTask(other.ID) })
		if _, err := s.m.pg.AddFinding(other.ID, 0, "test", marker, "high", marker, "", "test", nil); err != nil {
			t.Fatal(err)
		}
		if _, err := s.m.pg.Assets().UpsertSubdomain(db.UpsertSubdomainReq{Domain: strings.ToLower(marker) + ".invalid", TaskID: other.ID}); err != nil {
			t.Fatal(err)
		}
		if strings.HasPrefix(marker, "SOURCE") {
			if _, err := s.m.pg.Exec(`INSERT INTO task_relations(task_id,source_task_id) VALUES($1,$2)`, taskID, other.ID); err != nil {
				t.Fatal(err)
			}
		}
	}
	assetID, err := s.m.pg.Assets().UpsertSubdomain(db.UpsertSubdomainReq{Domain: "own-report-fixture.invalid", TaskID: taskID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.m.pg.Exec(`UPDATE assets SET auth=ARRAY['{"password":"ASSET-CREDENTIAL-MUST-NOT-EXPORT"}'::jsonb],extra='{"private":"ASSET-EXTRA-MUST-NOT-EXPORT"}' WHERE id=$1`, assetID); err != nil {
		t.Fatal(err)
	}
	var fetches atomic.Int32
	remoteImage := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fetches.Add(1)
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("image fixture must never be requested"))
	}))
	t.Cleanup(remoteImage.Close)
	markdown := "## 漏洞介绍\nDESCRIPTION-CANARY **正确加粗**\n\n## 危害\nIMPACT-CANARY\n\n## 复现 POC\n```http\nGET /?q=<script>alert(1)</script> HTTP/1.1\nPOC-CANARY\n```\n\n| 参数 | 说明 |\n| --- | --- |\n| TABLE-CANARY | 值 & <> |\n\n## 修复方法\n1. REMEDIATION-CANARY\n2. 二次检查\n\n## 补充分析\nADDITIONAL-CANARY\n\n![外链截图](" + remoteImage.URL + "/never-fetch.png)\n\n[不可执行链接](javascript:alert(1))\n\n<script>RAW-HTML-CANARY</script>"
	if _, err := s.m.pg.Exec(`UPDATE findings SET name='TASK-CASE-0000 <script>TITLE-CANARY</script>',summary='SUMMARY-CANARY',evidence='EVIDENCE-CANARY <xml>&',report=$2,asset_ids=jsonb_build_array($3::bigint) WHERE id=$1`, initial.FindingID, markdown, assetID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.m.pg.Exec(`INSERT INTO findings(task_id,name,severity,status,summary)
	 SELECT $1,'TASK-CASE-'||lpad(n::text,4,'0'),CASE n WHEN 1203 THEN 'info' WHEN 1204 THEN 'unclassified' ELSE 'high' END,
	 CASE WHEN n=1201 THEN 'resolved' WHEN n=1202 THEN 'risk_accepted'
	 ELSE CASE n%3 WHEN 0 THEN 'pending' WHEN 1 THEN 'confirmed' ELSE 'fixed' END END,'synthetic report fixture'
	 FROM generate_series(1,1204) n`, taskID); err != nil {
		t.Fatal(err)
	}
	for _, status := range []string{db.FindingFalsePositive, db.FindingDuplicate, db.FindingIgnored} {
		if _, err := s.m.pg.Exec(`INSERT INTO findings(task_id,name,severity,status,summary) VALUES($1,$2,'high',$3,'excluded by default')`, taskID, "EXCLUDED-"+status, status); err != nil {
			t.Fatal(err)
		}
	}
	seedServerEvidenceFlow(t, s, "report-template-proof", []byte("RAW-TRAFFIC-BODY-MUST-NOT-EXPORT"))
	response := request("POST", fmt.Sprintf("/api/exploration/findings/%d/traffic", initial.FindingID), `{"traffic_refs":[{"traffic_id":"report-template-proof","role":"proof","note":"TRAFFIC-SUMMARY-CANARY"}]}`)
	if response.Code != 200 {
		t.Fatalf("bind evidence: %d %s", response.Code, response.Body.String())
	}
	before := taskReportFacts(t, s.m.pg)
	for _, template := range []string{"haixing-html", "ola-docx"} {
		t.Run(template, func(t *testing.T) {
			options := map[string]any{"template": template, "title": "中文报告 <script>REPORT-TITLE</script> & Ω", "report_date": "2026-11-26", "author": "作者 <xml>", "organization": "测试单位 & 公司"}
			body, _ := json.Marshal(options)
			response := request("POST", fmt.Sprintf("/api/tasks/%d/report/export", taskID), string(body))
			if response.Code != 200 {
				t.Fatalf("report export: %d %s", response.Code, response.Body.String())
			}
			if !strings.HasPrefix(response.Header().Get("Content-Disposition"), "attachment;") || response.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("unsafe download response headers: %+v", response.Header())
			}
			var text string
			if template == "haixing-html" {
				if !strings.Contains(response.Header().Get("Content-Type"), "text/html") {
					t.Fatal("wrong HTML content type")
				}
				text = taskReportHTMLText(t, response.Body.Bytes())
			} else {
				if !strings.Contains(response.Header().Get("Content-Type"), "wordprocessingml.document") {
					t.Fatal("wrong Word content type")
				}
				text = taskReportDOCXText(t, response.Body.Bytes())
			}
			for i := 0; i < 1205; i++ {
				if marker := fmt.Sprintf("TASK-CASE-%04d", i); !strings.Contains(text, marker) {
					t.Fatalf("task report omitted own finding %s", marker)
				}
			}
			for _, marker := range []string{"DESCRIPTION-CANARY", "IMPACT-CANARY", "POC-CANARY", "TABLE-CANARY", "REMEDIATION-CANARY", "ADDITIONAL-CANARY", "EVIDENCE-CANARY", "RAW-HTML-CANARY", "own-report-fixture.invalid", "evidence.local", "中文报告 <script>REPORT-TITLE</script> & Ω", "已修复", "已处理", "风险接受"} {
				if !strings.Contains(text, marker) {
					t.Fatalf("report lost recorded content %q", marker)
				}
			}
			for _, marker := range []string{"OTHER-TASK-SECRET-MARKER", "SOURCE-TASK-SECRET-MARKER", "other-task-secret-marker.invalid", "source-task-secret-marker.invalid", "ASSET-CREDENTIAL-MUST-NOT-EXPORT", "ASSET-EXTRA-MUST-NOT-EXPORT", "RAW-TRAFFIC-BODY-MUST-NOT-EXPORT", "## 漏洞介绍", "**正确加粗**"} {
				if strings.Contains(text, marker) {
					t.Fatalf("report exposed excluded data or unrendered Markdown: %q", marker)
				}
			}
			if !strings.Contains(text, "1205") || strings.Contains(text, "EXCLUDED-") {
				t.Fatal("default report count or excluded-status scope is incorrect")
			}
			options["include_excluded"] = true
			body, _ = json.Marshal(options)
			all := request("POST", fmt.Sprintf("/api/tasks/%d/report/export", taskID), string(body))
			if all.Code != 200 {
				t.Fatalf("include excluded: %d %s", all.Code, all.Body.String())
			}
			if template == "haixing-html" {
				text = taskReportHTMLText(t, all.Body.Bytes())
			} else {
				text = taskReportDOCXText(t, all.Body.Bytes())
			}
			for _, marker := range []string{"1208", "EXCLUDED-false_positive", "EXCLUDED-duplicate", "EXCLUDED-ignored", "TASK-CASE-1204"} {
				if !strings.Contains(text, marker) {
					t.Fatalf("complete original-record export omitted %q", marker)
				}
			}
			if fetches.Load() != 0 {
				t.Fatal("report generation fetched a remote image")
			}
			if after := taskReportFacts(t, s.m.pg); after != before {
				t.Fatal("export changed persisted data or queued notification")
			}
		})
	}
}

func TestTaskReportExportAuthorizationValidationAndEmptyTask(t *testing.T) {
	if os.Getenv("ARTEX_PG_DSN") == "" {
		t.Skip("task report integration tests require explicit ARTEX_PG_DSN")
	}
	s, initial, request := trafficEvidenceServer(t)
	finding, err := s.m.pg.GetFinding(initial.FindingID)
	if err != nil || finding.TaskID == nil {
		t.Fatalf("fixture task: %+v %v", finding, err)
	}
	path := fmt.Sprintf("/api/tasks/%d/report/export", *finding.TaskID)
	before := taskReportFacts(t, s.m.pg)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest("POST", path, strings.NewReader(`{"template":"haixing-html"}`)))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated export: %d", w.Code)
	}
	for _, body := range []string{`{`, `{}`, `{"template":"../../private"}`, `{"template":"ola-docx","report_date":"2026-02-30"}`, `{"template":"haixing-html","report_date":"26-11-2026"}`, `{"template":"haixing-html","title":"` + strings.Repeat("长", 201) + `"}`, `{"template":"haixing-html","author":"` + strings.Repeat("a", 17000) + `"}`} {
		response := request("POST", path, body)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("invalid export request: %d %s", response.Code, response.Body.String())
		}
	}
	response := request("POST", fmt.Sprintf("/api/tasks/%d/report/export", *finding.TaskID+1000000), `{"template":"haixing-html"}`)
	if response.Code != http.StatusNotFound {
		t.Fatalf("missing task: %d %s", response.Code, response.Body.String())
	}
	if taskReportFacts(t, s.m.pg) != before {
		t.Fatal("rejected export request wrote data")
	}
	empty, err := s.m.pg.CreateTask("空白导出测试", "没有漏洞数据，不代表安全结论", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.m.pg.DeleteTask(empty.ID) })
	for _, template := range []string{"haixing-html", "ola-docx"} {
		response := request("POST", fmt.Sprintf("/api/tasks/%d/report/export", empty.ID), fmt.Sprintf(`{"template":%q}`, template))
		if response.Code != 200 {
			t.Fatalf("empty report %s: %d %s", template, response.Code, response.Body.String())
		}
		if template == "haixing-html" {
			taskReportHTMLText(t, response.Body.Bytes())
		} else {
			taskReportDOCXText(t, response.Body.Bytes())
		}
	}
}
