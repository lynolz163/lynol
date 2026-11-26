// Package reporttemplate renders task reports using the bundled HTML and Word
// layouts. It receives report data only; rendering never reads a DB or network.
package reporttemplate

import "time"

type Report struct {
	TaskID          int64
	TaskName        string
	Title           string
	ReportDate      string
	Author          string
	Organization    string
	TaskDescription string
	Goal            string
	TaskStatus      string
	GeneratedAt     time.Time
	StartedAt       *time.Time
	CompletedAt     *time.Time
	Assets          []Asset
	Findings        []Finding
}

type Asset struct {
	ID                           int64
	Type, Label, URL, Domain, IP string
	Port                         *int
}

type Finding struct {
	ID                     int64
	Name, Severity, Status string
	VulnClass              string
	Summary, Evidence      string
	ReportMarkdown         string
	CreatedAt              time.Time
	Assets                 []Asset
	EvidenceVersion        int64
	ReportEvidenceVersion  int64
	Traffic                []Traffic
}

type Traffic struct {
	ID                      int64
	Role, Note, Method, URL string
	Status, Position        int
	ReqHash, RespHash       string
}

// Block is the small shared representation produced from Markdown. Raw HTML is
// converted to text and images are represented by their label and destination.
type Block struct {
	Kind     string // paragraph, heading, code, list, quote, table, rule
	Level    int
	Text     string
	Language string
	Inlines  []Inline
	Children []Block
	Items    [][]Block
	Ordered  bool
	Start    int
	Rows     []TableRow
}

type TableRow struct {
	Header bool
	Cells  [][]Inline
}

type Inline struct {
	Kind        string // text, strong, emphasis, strike, code, link, image, break
	Text        string
	Destination string
	Children    []Inline
}

type FindingContent struct {
	Description []Block
	Impact      []Block
	PoC         []Block
	Remediation []Block
	Additional  []Block
}

type SeverityCount struct {
	Key, Label string
	Count      int
}
