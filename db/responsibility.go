package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/mail"
	"net/netip"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

var ErrResponsibilityInput = errors.New("归属配置无效")

type ResponsibilityRule struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
}
type ResponsibilityParty struct {
	ID      int64    `json:"id"`
	Kind    string   `json:"kind"`
	Name    string   `json:"name"`
	Aliases []string `json:"aliases"`
	Email   string   `json:"email"`
	TeamID  *int64   `json:"team_id"`
	LeadID  *int64   `json:"lead_id"`
	Active  bool     `json:"active"`
	Open    int      `json:"open"`
}
type BusinessLine struct {
	ID         int64                `json:"id"`
	Name       string               `json:"name"`
	Code       string               `json:"code"`
	Aliases    []string             `json:"aliases"`
	PrimaryID  *int64               `json:"primary_id"`
	BackupID   *int64               `json:"backup_id"`
	TeamIDs    []int64              `json:"team_ids"`
	ChannelIDs []int64              `json:"channel_ids"`
	SLA        FindingSLA           `json:"sla"`
	Rules      []ResponsibilityRule `json:"rules"`
	Active     bool                 `json:"active"`
	Open       int                  `json:"open"`
}
type ResponsibilityDirectory struct {
	BusinessLines []BusinessLine        `json:"business_lines"`
	Parties       []ResponsibilityParty `json:"parties"`
	FallbackID    int64                 `json:"fallback_id"`
	SLA           FindingSLA            `json:"sla"`
	Unassigned    int                   `json:"unassigned"`
	Ownerless     int                   `json:"ownerless"`
	Conflicts     int                   `json:"conflicts"`
}

func (d *DB) ResponsibilityDirectory(ctx context.Context) (ResponsibilityDirectory, error) {
	var out ResponsibilityDirectory
	var raw []byte
	err := d.QueryRowContext(ctx, `SELECT jsonb_build_object(
 'business_lines',COALESCE((SELECT jsonb_agg(to_jsonb(b)||jsonb_build_object('open',
   (SELECT count(*) FROM findings f WHERE f.business_line_id=b.id AND f.status IN ('pending','in_progress','confirmed'))) ORDER BY b.active DESC,b.id) FROM business_lines b),'[]'),
 'parties',COALESCE((SELECT jsonb_agg(to_jsonb(p)||jsonb_build_object('open',
   (SELECT count(*) FROM findings f WHERE f.assignee_id=p.id AND f.status IN ('pending','in_progress','confirmed'))) ORDER BY p.active DESC,p.id) FROM responsibility_parties p),'[]'),
 'fallback_id',(SELECT value::bigint FROM settings WHERE key='responsibility_fallback_id'),
 'sla',(SELECT value::jsonb FROM settings WHERE key='finding_sla_days'),
 'unassigned',(SELECT count(*) FROM findings WHERE business_line_id IS NULL AND status IN ('pending','in_progress','confirmed')),
 'ownerless',(SELECT count(*) FROM findings WHERE assignee_id IS NULL AND status IN ('pending','in_progress','confirmed')),
 'conflicts',(SELECT count(*) FROM findings WHERE assignment_conflict AND status IN ('pending','in_progress','confirmed'))
 )`).Scan(&raw)
	if err != nil {
		return out, err
	}
	err = json.Unmarshal(raw, &out)
	return out, err
}

var responsibilityCode = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$`)
var responsibilityDomain = regexp.MustCompile(`^(\*\.)?([a-z0-9]([a-z0-9-]*[a-z0-9])?\.)+[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)

func validResponsibilityName(name string) bool {
	return name != "" && utf8.RuneCountInString(name) <= 120 && !strings.ContainsAny(name, "\x00\r\n") && !strings.HasPrefix(name, "__")
}

func (d *DB) SaveBusinessLine(ctx context.Context, b *BusinessLine) error {
	b.Name = strings.TrimSpace(b.Name)
	b.Code = strings.TrimSpace(b.Code)
	if b.ID < 0 || !validResponsibilityName(b.Name) || !responsibilityCode.MatchString(b.Code) || len(b.Rules) > 100 {
		return fmt.Errorf("%w: 请填写业务线名称、唯一标识（字母、数字、横线）及最多 100 条规则", ErrResponsibilityInput)
	}
	if b.SLA != nil {
		if len(b.SLA) != 4 {
			return fmt.Errorf("%w: 请填写四个等级的 SLA", ErrResponsibilityInput)
		}
		for _, s := range []string{"critical", "high", "medium", "low"} {
			if b.SLA[s] < 1 || b.SLA[s] > 365 {
				return fmt.Errorf("%w: SLA 天数为 1 至 365", ErrResponsibilityInput)
			}
		}
	}
	for i := range b.Rules {
		r := &b.Rules[i]
		r.Value = strings.ToLower(strings.TrimSpace(r.Value))
		switch r.Kind {
		case "domain":
			r.Value = strings.TrimSuffix(r.Value, ".")
			if len(r.Value) > 253 || !responsibilityDomain.MatchString(r.Value) {
				return fmt.Errorf("%w: 无效域名规则 %s", ErrResponsibilityInput, r.Value)
			}
		case "cidr":
			prefix, err := netip.ParsePrefix(r.Value)
			if err != nil {
				return fmt.Errorf("%w: 无效 IP 网段 %s", ErrResponsibilityInput, r.Value)
			}
			r.Value = prefix.Masked().String()
		default:
			return fmt.Errorf("%w: 规则只支持域名或 IP 网段", ErrResponsibilityInput)
		}
	}
	if b.TeamIDs == nil {
		b.TeamIDs = []int64{}
	}
	if b.ChannelIDs == nil {
		b.ChannelIDs = []int64{}
	}
	if b.Rules == nil {
		b.Rules = []ResponsibilityRule{}
	}
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var valid bool
	err = tx.QueryRowContext(ctx, `SELECT
 NOT EXISTS(SELECT 1 FROM business_lines WHERE id<>$1 AND (name=$2 OR $2=ANY(aliases) OR code=$3)) AND
 ($4::bigint IS NULL OR EXISTS(SELECT 1 FROM responsibility_parties WHERE id=$4 AND kind='person' AND active)) AND
 ($5::bigint IS NULL OR EXISTS(SELECT 1 FROM responsibility_parties WHERE id=$5 AND kind='person' AND active)) AND
 NOT EXISTS(SELECT 1 FROM unnest($6::bigint[]) n WHERE NOT EXISTS(SELECT 1 FROM responsibility_parties WHERE id=n AND kind='team' AND active)) AND
 NOT EXISTS(SELECT 1 FROM unnest($7::bigint[]) n WHERE NOT EXISTS(SELECT 1 FROM notification_channels WHERE id=n AND kind IN ('email','wecom') AND enabled))`,
		b.ID, b.Name, b.Code, b.PrimaryID, b.BackupID, b.TeamIDs, b.ChannelIDs).Scan(&valid)
	if err != nil {
		return err
	}
	if !valid {
		return fmt.Errorf("%w: 名称/标识已被使用，或人员、团队、通知渠道不可用", ErrResponsibilityInput)
	}
	teams, _ := json.Marshal(b.TeamIDs)
	channels, _ := json.Marshal(b.ChannelIDs)
	rules, _ := json.Marshal(b.Rules)
	var sla any
	if b.SLA != nil {
		raw, _ := json.Marshal(b.SLA)
		sla = string(raw)
	}
	args := []any{b.Name, b.Code, b.PrimaryID, b.BackupID, string(teams), string(channels), sla, string(rules), b.Active}
	if b.ID == 0 {
		err = tx.QueryRowContext(ctx, `INSERT INTO business_lines(name,code,primary_id,backup_id,team_ids,channel_ids,sla,rules,active,aliases)
  VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,ARRAY[$1]) RETURNING id`, args...).Scan(&b.ID)
	} else {
		args = append(args, b.ID)
		err = tx.QueryRowContext(ctx, `UPDATE business_lines SET name=$1,code=$2,primary_id=$3,backup_id=$4,team_ids=$5,channel_ids=$6,sla=$7,rules=$8,active=$9,
  aliases=ARRAY(SELECT DISTINCT unnest(aliases||ARRAY[$1])) WHERE id=$10 RETURNING id`, args...).Scan(&b.ID)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: 业务线不存在", ErrResponsibilityInput)
	}
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE findings SET business_line=$2,responsibility_sla=$3 WHERE business_line_id=$1`, b.ID, b.Name, sla)
	if err != nil {
		return err
	}
	// Explicitly assigned owners stay in place. Automatically assigned open work follows the configured owner.
	_, err = tx.ExecContext(ctx, `UPDATE findings SET assignee_id=responsibility_default_owner($1),
 assignee=COALESCE((SELECT name FROM responsibility_parties WHERE id=responsibility_default_owner($1)),'')
 WHERE business_line_id=$1 AND assignment_source='auto' AND status IN ('pending','in_progress','confirmed')`, b.ID)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (d *DB) SaveResponsibilityParty(ctx context.Context, p *ResponsibilityParty) (int64, error) {
	p.Name = strings.TrimSpace(p.Name)
	p.Email = strings.TrimSpace(p.Email)
	if p.ID < 0 || !validResponsibilityName(p.Name) || (p.Kind != "person" && p.Kind != "team") {
		return 0, fmt.Errorf("%w: 请填写有效的人员或团队名称", ErrResponsibilityInput)
	}
	if p.Email != "" {
		address, err := mail.ParseAddress(p.Email)
		if err != nil || address.Address != p.Email {
			return 0, fmt.Errorf("%w: 邮箱格式不正确", ErrResponsibilityInput)
		}
	}
	if p.Kind == "person" {
		p.LeadID = nil
	} else {
		p.TeamID = nil
		p.Email = ""
	}
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var valid bool
	err = tx.QueryRowContext(ctx, `SELECT
 NOT EXISTS(SELECT 1 FROM responsibility_parties WHERE id<>$1 AND (name=$2 OR $2=ANY(aliases))) AND
 NOT EXISTS(SELECT 1 FROM responsibility_parties WHERE id=$1 AND kind<>$3) AND
 ($4::bigint IS NULL OR EXISTS(SELECT 1 FROM responsibility_parties WHERE id=$4 AND kind='team' AND active)) AND
 ($5::bigint IS NULL OR EXISTS(SELECT 1 FROM responsibility_parties WHERE id=$5 AND kind='person' AND active)) AND
 ($6 OR NOT EXISTS(SELECT 1 FROM settings WHERE key='responsibility_fallback_id' AND value=$1::text)) AND
 ($6 OR $3<>'team' OR NOT EXISTS(SELECT 1 FROM responsibility_parties WHERE team_id=$1 AND active))`, p.ID, p.Name, p.Kind, p.TeamID, p.LeadID, p.Active).Scan(&valid)
	if err != nil {
		return 0, err
	}
	if !valid {
		return 0, fmt.Errorf("%w: 名称已使用或关联不可用；停用兜底负责人须先替换，停用团队须先转移成员", ErrResponsibilityInput)
	}
	args := []any{p.Kind, p.Name, p.Email, p.TeamID, p.LeadID, p.Active}
	if p.ID == 0 {
		err = tx.QueryRowContext(ctx, `INSERT INTO responsibility_parties(kind,name,email,team_id,lead_id,active,aliases)
  VALUES($1,$2,$3,$4,$5,$6,ARRAY[$2]) RETURNING id`, args...).Scan(&p.ID)
	} else {
		args = append(args, p.ID)
		err = tx.QueryRowContext(ctx, `UPDATE responsibility_parties SET name=$2,email=$3,team_id=$4,lead_id=$5,active=$6,
  aliases=ARRAY(SELECT DISTINCT unnest(aliases||ARRAY[$2])) WHERE id=$7 AND kind=$1 RETURNING id`, args...).Scan(&p.ID)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("%w: 人员或团队不存在", ErrResponsibilityInput)
	}
	if err != nil {
		return 0, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE findings SET assignee=$2 WHERE assignee_id=$1`, p.ID, p.Name); err != nil {
		return 0, err
	}
	var transferred int64
	if !p.Active {
		result, e := tx.ExecContext(ctx, `UPDATE findings SET assignee_id=responsibility_default_owner(business_line_id),
  assignee=COALESCE((SELECT name FROM responsibility_parties WHERE id=responsibility_default_owner(findings.business_line_id)),'')
  WHERE assignee_id=$1 AND status IN ('pending','in_progress','confirmed')`, p.ID)
		if e != nil {
			return 0, e
		}
		transferred, _ = result.RowsAffected()
	}
	return transferred, tx.Commit()
}

func (d *DB) SetResponsibilityFallback(ctx context.Context, id int64) error {
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var valid bool
	if err = tx.QueryRowContext(ctx, `SELECT $1::bigint=0 OR EXISTS(SELECT 1 FROM responsibility_parties WHERE id=$1 AND kind='person' AND active)`, id).Scan(&valid); err != nil {
		return err
	}
	if !valid {
		return fmt.Errorf("%w: 请选择已启用的人员", ErrResponsibilityInput)
	}
	if _, err = tx.ExecContext(ctx, `UPDATE settings SET value=$1::text WHERE key='responsibility_fallback_id'`, strconv.FormatInt(id, 10)); err != nil {
		return err
	}
	if id > 0 {
		_, err = tx.ExecContext(ctx, `UPDATE findings SET assignee_id=$1,assignee=(SELECT name FROM responsibility_parties WHERE id=$1)
  WHERE assignee_id IS NULL AND assignment_source='auto' AND status IN ('pending','in_progress','confirmed')`, id)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

type ResponsibilityRuleResult struct {
	Assigned  int `json:"assigned"`
	Unmatched int `json:"unmatched"`
	Conflicts int `json:"conflicts"`
}

func (d *DB) ApplyResponsibilityRules(ctx context.Context) (ResponsibilityRuleResult, error) {
	var out ResponsibilityRuleResult
	// Deliberately re-run only unassigned findings. Existing/manual line assignments are preserved.
	err := d.QueryRowContext(ctx, `WITH candidates AS MATERIALIZED (
 SELECT id,responsibility_matching_lines(asset_ids) matches FROM findings WHERE business_line_id IS NULL AND status IN ('pending','in_progress','confirmed')
 ), updated AS (
 UPDATE findings f SET business_line_id=CASE WHEN cardinality(c.matches)=1 THEN c.matches[1] END,
 business_line=COALESCE((SELECT name FROM business_lines WHERE id=c.matches[1] AND cardinality(c.matches)=1),''),
 responsibility_sla=(SELECT sla FROM business_lines WHERE id=c.matches[1] AND cardinality(c.matches)=1),
 assignee_id=CASE WHEN cardinality(c.matches)=1 THEN responsibility_default_owner(c.matches[1]) ELSE f.assignee_id END,
 assignee=CASE WHEN cardinality(c.matches)=1 THEN COALESCE((SELECT name FROM responsibility_parties WHERE id=responsibility_default_owner(c.matches[1])),'') ELSE f.assignee END,
 assignment_source='auto',assignment_conflict=cardinality(c.matches)>1 FROM candidates c WHERE f.id=c.id RETURNING f.id
 ) SELECT count(*) FILTER(WHERE cardinality(matches)=1),count(*) FILTER(WHERE cardinality(matches)=0),count(*) FILTER(WHERE cardinality(matches)>1) FROM candidates`).Scan(&out.Assigned, &out.Unmatched, &out.Conflicts)
	return out, err
}

// Aliases join renamed entities at query time; journal labels and saved reports stay untouched.
func (d *DB) responsibilityAliases(ctx context.Context) (map[string]string, map[string]string, error) {
	lines, people := map[string]string{}, map[string]string{}
	rows, err := d.QueryContext(ctx, `SELECT 'line',name,unnest(aliases) FROM business_lines UNION ALL SELECT 'party',name,unnest(aliases) FROM responsibility_parties`)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var kind, name, alias string
		if err = rows.Scan(&kind, &name, &alias); err != nil {
			return nil, nil, err
		}
		if kind == "line" {
			lines[alias] = name
		} else {
			people[alias] = name
		}
	}
	return lines, people, rows.Err()
}
