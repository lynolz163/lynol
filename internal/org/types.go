package org

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"net/mail"
	"strings"
	"time"
)

//go:embed schema.sql
var tablesSQL string

//go:embed resolver.sql
var resolverSQL string

//go:embed assignment_schema.sql
var assignmentSQL string

//go:embed events.sql
var eventsSQL string

//go:embed notification_schema.sql
var notificationSQL string

//go:embed backfill_schema.sql
var backfillSQL string

var SchemaSQL = tablesSQL + "\n" + resolverSQL + "\n" + assignmentSQL + "\n" + eventsSQL + "\n" + backfillSQL + "\n" + notificationSQL

type Store struct {
	DB                 *sql.DB
	NotificationSender NotificationSender
}

func New(db *sql.DB) *Store { return &Store{DB: db} }

type Role struct {
	Kind     string `json:"kind"`
	ObjectID int64  `json:"object_id"`
	Name     string `json:"name"`
	Open     int64  `json:"open"`
}
type Department struct {
	ID         int64   `json:"id"`
	Name       string  `json:"name"`
	Code       string  `json:"code"`
	Active     bool    `json:"active"`
	Revision   int64   `json:"revision"`
	HeadID     *int64  `json:"head_id"`
	Open       int64   `json:"open"`
	ChannelIDs []int64 `json:"channel_ids"`
}
type Group struct {
	ID           int64  `json:"id"`
	Name         string `json:"name"`
	DepartmentID int64  `json:"department_id"`
	Active       bool   `json:"active"`
	Revision     int64  `json:"revision"`
	Members      int64  `json:"members"`
}
type Person struct {
	ID           int64  `json:"id"`
	Name         string `json:"name"`
	Email        string `json:"email"`
	DepartmentID *int64 `json:"department_id"`
	DevGroupID   *int64 `json:"dev_group_id"`
	Active       bool   `json:"active"`
	Revision     int64  `json:"revision"`
	Open         int64  `json:"open"`
	Roles        []Role `json:"roles"`
}
type SLA map[string]int
type BusinessLine struct {
	ID           int64   `json:"id"`
	Name         string  `json:"name"`
	Code         string  `json:"code"`
	DepartmentID int64   `json:"department_id"`
	PrimaryID    *int64  `json:"primary_id"`
	BackupID     *int64  `json:"backup_id"`
	GroupIDs     []int64 `json:"group_ids"`
	Active       bool    `json:"active"`
	Revision     int64   `json:"revision"`
	Open         int64   `json:"open"`
	SLA          SLA     `json:"sla"`
	ChannelIDs   []int64 `json:"channel_ids"`
}
type Directory struct {
	Departments    []Department   `json:"departments"`
	BusinessLines  []BusinessLine `json:"business_lines"`
	People         []Person       `json:"people"`
	Groups         []Group        `json:"groups"`
	FallbackID     *int64         `json:"fallback_id"`
	SLA            SLA            `json:"sla"`
	CanEdit        bool           `json:"can_edit"`
	UnassignedOpen int64          `json:"unassigned_open"`
	ConfigRevision int64          `json:"config_revision"`
}
type Command struct {
	ChannelDrafts             []NotificationChannelDraft `json:"channel_drafts,omitempty"`
	SharedChannelConfirmedIDs []int64                    `json:"shared_channel_confirmed_ids,omitempty"`
	Kind                      string                     `json:"kind"`
	Action                    string                     `json:"action"`
	ID                        int64                      `json:"id"`
	Revision                  int64                      `json:"revision"`
	Data                      json.RawMessage            `json:"data"`
	Scope                     string                     `json:"scope"`
	ReceiverID                *int64                     `json:"receiver_id"`
	ConfirmImpact             bool                       `json:"confirm_impact"`
	IdempotencyKey            string                     `json:"idempotency_key"`
}
type ChangeResult struct {
	OperationID  string    `json:"operation_id"`
	EntityID     int64     `json:"entity_id"`
	EffectiveAt  time.Time `json:"effective_at"`
	AffectedOpen int64     `json:"affected_open"`
}
type Impact struct {
	Open          int64  `json:"open"`
	Assets        int64  `json:"assets"`
	BusinessLines []Role `json:"business_lines"`
	Roles         []Role `json:"roles"`
}
type APIError struct {
	Status  int
	Message string
	Fields  map[string]string
	Code    string
}

func (e *APIError) Error() string { return e.Message }
func bad(field, message string) error {
	return &APIError{400, message, map[string]string{field: message}, "invalid"}
}
func conflict(message string) error { return &APIError{409, message, nil, "conflict"} }
func sameID(a, b *int64) bool       { return a == nil && b == nil || a != nil && b != nil && *a == *b }
func jsonValue(v any) string        { b, _ := json.Marshal(v); return string(b) }

// ValidateCommand is the boundary for both HTTP and CSV callers.
func ValidateCommand(c *Command) error {
	if _, err := uuid.Parse(c.IdempotencyKey); err != nil {
		return bad("idempotency_key", "请刷新页面后重新提交")
	}
	if c.ID < 0 {
		return bad("id", "对象编号不正确")
	}
	if c.Action == "" {
		c.Action = "save"
	}
	if c.Scope == "" {
		c.Scope = "future_findings"
	}
	if c.Scope != "future_findings" && c.Scope != "all_open" {
		return bad("scope", "请选择仅新漏洞或同时处理存量")
	}
	if c.Action != "save" && c.Action != "disable" && c.Action != "migrate" {
		return bad("action", "不支持此操作")
	}
	if len(c.ChannelDrafts) > 0 {
		if c.Action != "save" || (c.Kind != "department" && c.Kind != "business_line") {
			return bad("channel_ids", "通知配置只能与部门或业务线一起保存")
		}
		seenKeys, seenIDs := map[string]bool{}, map[int64]bool{}
		for i := range c.ChannelDrafts {
			d := &c.ChannelDrafts[i]
			if err := validateNotificationDraft(d); err != nil {
				return err
			}
			if seenKeys[d.ClientKey] || (d.ID > 0 && seenIDs[d.ID]) {
				return bad("channel_ids", "同一通知渠道请只编辑一次")
			}
			seenKeys[d.ClientKey], seenIDs[d.ID] = true, true
		}
	}
	if c.Action != "save" {
		if c.ID == 0 {
			return bad("id", "请先保存对象")
		}
		if c.Action == "migrate" && c.Kind != "business_line" {
			return bad("action", "仅业务线支持迁移")
		}
		switch c.Kind {
		case "department", "business_line", "person", "dev_group":
			return nil
		}
		return bad("kind", "不支持此操作")
	}
	if len(c.Data) == 0 {
		return bad("data", "请填写表单")
	}
	required := func(name string) error {
		if strings.TrimSpace(name) == "" {
			return bad("name", "请填写名称")
		}
		if len([]rune(name)) > 100 {
			return bad("name", "名称最多 100 个字符")
		}
		return nil
	}
	switch c.Kind {
	case "department":
		var d Department
		if json.Unmarshal(c.Data, &d) != nil {
			return bad("data", "部门格式错误")
		}
		d.Name = strings.TrimSpace(d.Name)
		d.Code = strings.TrimSpace(d.Code)
		if err := required(d.Name); err != nil {
			return err
		}
		if d.Code == "" {
			return bad("code", "请填写部门编码")
		}
		c.Data = []byte(jsonValue(d))
	case "dev_group":
		var g Group
		if json.Unmarshal(c.Data, &g) != nil {
			return bad("data", "研发组格式错误")
		}
		g.Name = strings.TrimSpace(g.Name)
		if err := required(g.Name); err != nil {
			return err
		}
		if g.DepartmentID <= 0 {
			return bad("department_id", "请选择所属部门")
		}
		c.Data = []byte(jsonValue(g))
	case "person":
		var p Person
		if json.Unmarshal(c.Data, &p) != nil {
			return bad("data", "人员格式错误")
		}
		p.Name = strings.TrimSpace(p.Name)
		p.Email = strings.ToLower(strings.TrimSpace(p.Email))
		if err := required(p.Name); err != nil {
			return err
		}
		a, e := mail.ParseAddress(p.Email)
		if e != nil || a.Address != p.Email || !strings.Contains(p.Email, "@") {
			return bad("email", "请填写有效邮箱，例如 name@example.com")
		}
		c.Data = []byte(jsonValue(p))
	case "business_line":
		var b BusinessLine
		if json.Unmarshal(c.Data, &b) != nil {
			return bad("data", "业务线格式错误")
		}
		b.Name = strings.TrimSpace(b.Name)
		b.Code = strings.TrimSpace(b.Code)
		if err := required(b.Name); err != nil {
			return err
		}
		if b.Code == "" {
			return bad("code", "请填写业务线编码")
		}
		if b.DepartmentID <= 0 {
			return bad("department_id", "请选择所属部门")
		}
		if b.PrimaryID != nil && sameID(b.PrimaryID, b.BackupID) {
			return bad("backup_id", "备选负责人必须与主负责人不同")
		}
		if b.SLA != nil {
			prev := 0
			for _, k := range []string{"critical", "high", "medium", "low"} {
				v := b.SLA[k]
				if v < 1 || v > 365 || v < prev {
					return bad("sla", "SLA 必须为 1～365 天，并满足严重 ≤ 高危 ≤ 中危 ≤ 低危")
				}
				prev = v
			}
		}
		c.Data = []byte(jsonValue(b))
	case "fallback":
		var v struct {
			OwnerID int64 `json:"owner_id"`
		}
		if json.Unmarshal(c.Data, &v) != nil || v.OwnerID <= 0 {
			return bad("owner_id", "请选择已启用的兜底人员")
		}
		c.ID = 1
	default:
		return bad("kind", "配置类型不存在")
	}
	return nil
}

func activeRef(ctx context.Context, tx *sql.Tx, table, field string, id *int64) error {
	if id == nil {
		return nil
	}
	var active bool
	if err := tx.QueryRowContext(ctx, "SELECT active FROM org."+table+" WHERE id=$1", *id).Scan(&active); err == sql.ErrNoRows {
		return bad(field, "请选择存在且已启用的对象")
	} else if err != nil {
		return err
	}
	if !active {
		return bad(field, "该对象已停用，请重新选择")
	}
	return nil
}
func checkRevision(got, want int64) error {
	if got != want {
		return conflict("数据已被修改，请刷新后重试")
	}
	return nil
}
func tableFor(kind string) (string, error) {
	switch kind {
	case "department":
		return "department", nil
	case "business_line":
		return "business_line", nil
	case "person":
		return "app_user", nil
	case "dev_group":
		return "dev_group", nil
	}
	return "", fmt.Errorf("unknown object kind")
}
