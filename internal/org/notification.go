package org

import (
	"context"
	"database/sql"
	"encoding/json"
	"maps"
	"net/http"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/Autumn-27/artex/notify"
)

type NotificationSender func(context.Context, string, map[string]any, notify.Message) error

type NotificationChannelDraft struct {
	ClientKey         string         `json:"client_key"`
	ID                int64          `json:"id,omitempty"`
	ExpectedUpdatedAt *time.Time     `json:"expected_updated_at,omitempty"`
	Name              string         `json:"name"`
	Kind              string         `json:"kind"`
	Enabled           bool           `json:"enabled"`
	Config            map[string]any `json:"config"`
}

type notificationChannel struct {
	ID         int64           `json:"id"`
	Name       string          `json:"name"`
	Kind       string          `json:"kind"`
	Enabled    bool            `json:"enabled"`
	OrgManaged bool            `json:"org_managed"`
	Mode       string          `json:"mode"`
	Config     map[string]any  `json:"config"`
	Filter     json.RawMessage `json:"filter"`
	RatePerMin int             `json:"rate_per_min"`
	CreatedAt  time.Time       `json:"created_at"`
	UpdatedAt  time.Time       `json:"updated_at"`
}

func validateNotificationDraft(d *NotificationChannelDraft) error {
	d.Name = strings.TrimSpace(d.Name)
	field := "channel_drafts." + d.ClientKey
	if d.ClientKey == "" || len(d.ClientKey) > 100 || d.ID < 0 {
		return bad("channel_ids", "通知草稿编号不正确，请重新打开编辑")
	}
	if d.Name == "" {
		return bad(field+".name", "请填写通知渠道名称")
	}
	if !notify.ValidKind(d.Kind) {
		return bad(field+".kind", "请选择支持的通知渠道类型")
	}
	if d.ID > 0 && d.ExpectedUpdatedAt == nil {
		return bad(field+".config", "渠道信息已过期，请重新打开编辑")
	}
	return nil
}

func notificationChannelByID(ctx context.Context, q reader, id int64, lock bool) (notificationChannel, error) {
	var c notificationChannel
	var raw []byte
	query := `SELECT to_jsonb(c) FROM public.notification_channels c WHERE id=$1`
	if lock {
		query += ` FOR UPDATE`
	}
	err := q.QueryRowContext(ctx, query, id).Scan(&raw)
	if err == sql.ErrNoRows {
		return c, bad("channel_ids", "通知渠道不存在，请刷新后重新选择")
	}
	if err != nil {
		return c, err
	}
	err = json.Unmarshal(raw, &c)
	return c, err
}

func prepareNotificationConfig(current notificationChannel, d NotificationChannelDraft) (map[string]any, error) {
	field := "channel_drafts." + d.ClientKey + ".config"
	adapter, _ := notify.Get(d.Kind)
	if d.ID > 0 {
		if current.Kind != d.Kind {
			return nil, bad(field, "渠道类型不能直接修改，请新增渠道")
		}
		if !current.UpdatedAt.Equal(*d.ExpectedUpdatedAt) {
			return nil, bad(field, "该渠道已被修改，请重新打开后编辑")
		}
	} else {
		incoming := maps.Clone(d.Config)
		if incoming == nil {
			incoming = map[string]any{}
		}
		for _, key := range adapter.SecretKeys() {
			if value, ok := incoming[key].(string); ok && notify.IsMasked(value) {
				return nil, bad(field, "新建渠道请填写实际凭据，不能沿用其他渠道的掩码")
			}
			if _, exists := incoming[key]; !exists {
				incoming[key] = ""
			}
		}
		d.Config = incoming
	}
	cfg, err := notify.PrepareConfigUpdate(d.Kind, current.Config, d.Config)
	if err != nil {
		return nil, bad(field, err.Error())
	}
	if err = adapter.Validate(cfg); err != nil {
		return nil, bad(field, err.Error())
	}
	return cfg, nil
}

func (s *Store) notificationChannels(w http.ResponseWriter, r *http.Request) {
	rows, err := s.DB.QueryContext(r.Context(), `SELECT to_jsonb(c)||jsonb_build_object('usage',jsonb_build_object(
 'global',NOT c.org_managed,
 'departments',COALESCE((SELECT jsonb_agg(jsonb_build_object('id',d.id,'name',d.name)) FROM org.department d WHERE c.id=ANY(d.channel_ids)),'[]'),
 'business_lines',COALESCE((SELECT jsonb_agg(jsonb_build_object('id',b.id,'name',b.name)) FROM org.business_line b WHERE c.id=ANY(b.channel_ids)),'[]'),
 'schedules',(SELECT count(*) FROM public.finding_report_schedules s WHERE s.channel_id=c.id)))
 FROM public.notification_channels c ORDER BY c.enabled DESC,c.id`)
	if err != nil {
		writeError(w, err)
		return
	}
	defer rows.Close()
	channels := []map[string]any{}
	for rows.Next() {
		var raw []byte
		if err = rows.Scan(&raw); err != nil {
			writeError(w, err)
			return
		}
		var c map[string]any
		if err = json.Unmarshal(raw, &c); err != nil {
			writeError(w, err)
			return
		}
		kind, _ := c["kind"].(string)
		cfg, _ := c["config"].(map[string]any)
		c["config"] = notify.MaskConfig(kind, cfg)
		if adapter, ok := notify.Get(kind); ok {
			c["secret_keys"] = adapter.SecretKeys()
		}
		channels = append(channels, c)
	}
	if err = rows.Err(); err != nil {
		writeError(w, err)
		return
	}
	kinds := []map[string]any{}
	for _, kind := range notify.Kinds() {
		a, _ := notify.Get(kind)
		kinds = append(kinds, map[string]any{"kind": kind, "default_rate_per_min": a.DefaultRatePerMin(), "secret_keys": a.SecretKeys()})
	}
	mode := "live"
	if s.NotificationSender != nil {
		mode = "fake"
	}
	writeJSON(w, 200, map[string]any{"channels": channels, "kinds": kinds, "test_mode": mode})
}

func (s *Store) testNotificationChannel(w http.ResponseWriter, r *http.Request) {
	var d NotificationChannelDraft
	if !decode(w, r, &d) {
		return
	}
	if err := validateNotificationDraft(&d); err != nil {
		writeError(w, err)
		return
	}
	var current notificationChannel
	var err error
	if d.ID > 0 {
		current, err = notificationChannelByID(r.Context(), s.DB, d.ID, false)
		if err != nil {
			writeError(w, err)
			return
		}
	}
	cfg, err := prepareNotificationConfig(current, d)
	if err != nil {
		writeError(w, err)
		return
	}
	msg := notify.Message{Items: []notify.Item{{Name: "测试消息 · 通知草稿连通性验证", Severity: "low", VulnClass: "连通性测试", Summary: "这是 ARTEX 通知渠道测试。测试不会保存当前草稿。"}}}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	start := time.Now()
	if s.NotificationSender != nil {
		err = s.NotificationSender(ctx, d.Kind, cfg, msg)
	} else {
		adapter, _ := notify.Get(d.Kind)
		_, err = adapter.Send(ctx, cfg, msg)
	}
	if err != nil {
		writeJSON(w, 502, map[string]any{"error": "测试消息发送失败，请检查渠道地址、凭据及网络连通性；草稿已保留"})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "latency_ms": time.Since(start).Milliseconds(), "simulated": s.NotificationSender != nil})
}

// Saving the containing organization object is the sole write boundary. A
// failed organization validation rolls all channel writes back with it.
func (s *Store) saveNotificationDrafts(ctx context.Context, tx *sql.Tx, c *Command, op string, at time.Time) error {
	var data map[string]json.RawMessage
	if err := json.Unmarshal(c.Data, &data); err != nil {
		return err
	}
	var selected []int64
	if raw := data["channel_ids"]; len(raw) > 0 {
		if err := json.Unmarshal(raw, &selected); err != nil {
			return bad("channel_ids", "请选择通知渠道")
		}
	}
	for _, d := range c.ChannelDrafts {
		var previous notificationChannel
		var old any
		field := "channel_drafts." + d.ClientKey
		if d.ID > 0 {
			if !slices.Contains(selected, d.ID) {
				return bad(field+".config", "请先勾选要修改的通知渠道")
			}
			var err error
			previous, err = notificationChannelByID(ctx, tx, d.ID, true)
			if err != nil {
				return err
			}
			old = previous
			var shared bool
			err = tx.QueryRowContext(ctx, `SELECT NOT c.org_managed OR
    EXISTS(SELECT 1 FROM org.department d WHERE c.id=ANY(d.channel_ids) AND NOT($2='department' AND d.id=$3)) OR
    EXISTS(SELECT 1 FROM org.business_line b WHERE c.id=ANY(b.channel_ids) AND NOT($2='business_line' AND b.id=$3)) OR
    EXISTS(SELECT 1 FROM public.finding_report_schedules s WHERE s.channel_id=c.id)
    FROM public.notification_channels c WHERE c.id=$1`, d.ID, c.Kind, c.ID).Scan(&shared)
			if err != nil {
				return err
			}
			if shared && !slices.Contains(c.SharedChannelConfirmedIDs, d.ID) {
				return bad(field+".shared", "修改会影响共享此渠道的对象及通用推送，请先确认影响范围")
			}
		}
		cfg, err := prepareNotificationConfig(previous, d)
		if err != nil {
			return err
		}
		var id int64
		var updated time.Time
		if d.ID == 0 {
			adapter, _ := notify.Get(d.Kind)
			err = tx.QueryRowContext(ctx, `INSERT INTO public.notification_channels(name,kind,enabled,config,mode,filter,rate_per_min,org_managed)
   VALUES($1,$2,$3,$4,'realtime','{}',$5,true) RETURNING id,updated_at`, d.Name, d.Kind, d.Enabled, jsonValue(cfg), adapter.DefaultRatePerMin()).Scan(&id, &updated)
			if err == nil {
				selected = append(selected, id)
			}
		} else {
			id = d.ID
			err = tx.QueryRowContext(ctx, `UPDATE public.notification_channels SET name=$2,enabled=$3,config=$4 WHERE id=$1 RETURNING updated_at`, id, d.Name, d.Enabled, jsonValue(cfg)).Scan(&updated)
		}
		if err != nil {
			return err
		}
		if !d.Enabled {
			if _, err = tx.ExecContext(ctx, `UPDATE public.notification_deliveries SET state='skipped',last_error='渠道已停用' WHERE channel_id=$1 AND state IN ('pending','sending')`, id); err != nil {
				return err
			}
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO org.notification_channel_change(operation_id,channel_id,before_value,after_updated_at) VALUES($1,$2,$3,$4)`, op, id, jsonValue(old), updated); err != nil {
			return err
		}
		keys := []string{}
		for key, value := range cfg {
			if jsonValue(value) != jsonValue(previous.Config[key]) {
				keys = append(keys, key)
			}
		}
		for key := range previous.Config {
			if _, exists := cfg[key]; !exists {
				keys = append(keys, key)
			}
		}
		sort.Strings(keys)
		// Public audit excludes values, including masked or partial credentials.
		if err = audit(ctx, tx, op, at, "notification_channel", id, "save", map[string]any{"name": previous.Name, "kind": previous.Kind, "enabled": previous.Enabled}, map[string]any{"name": d.Name, "kind": d.Kind, "enabled": d.Enabled, "changed_config_keys": keys}); err != nil {
			return err
		}
	}
	data["channel_ids"] = json.RawMessage(jsonValue(selected))
	c.Data = json.RawMessage(jsonValue(data))
	return nil
}

func undoNotificationDrafts(ctx context.Context, tx *sql.Tx, original, op string, at time.Time) error {
	rows, err := tx.QueryContext(ctx, `SELECT channel_id,before_value,after_updated_at FROM org.notification_channel_change WHERE operation_id=$1 ORDER BY channel_id`, original)
	if err != nil {
		return err
	}
	type change struct {
		id      int64
		before  json.RawMessage
		updated time.Time
	}
	changes := []change{}
	for rows.Next() {
		var c change
		if err = rows.Scan(&c.id, &c.before, &c.updated); err != nil {
			rows.Close()
			return err
		}
		changes = append(changes, c)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, c := range changes {
		current, err := notificationChannelByID(ctx, tx, c.id, true)
		if err != nil {
			return err
		}
		if !current.UpdatedAt.Equal(c.updated) {
			return conflict("通知渠道之后已有修改，不能覆盖新配置，请重新编辑")
		}
		var previous notificationChannel
		created := len(c.before) == 0 || string(c.before) == "null"
		if created {
			var used bool
			if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM org.department WHERE active AND $1=ANY(channel_ids)) OR EXISTS(SELECT 1 FROM org.business_line WHERE active AND $1=ANY(channel_ids)) OR EXISTS(SELECT 1 FROM public.finding_report_schedules WHERE channel_id=$1)`, c.id).Scan(&used); err != nil {
				return err
			}
			if used {
				return conflict("新建渠道已被其他对象使用，请先解除关联后再撤销")
			}
			if _, err = tx.ExecContext(ctx, `UPDATE public.notification_channels SET enabled=false WHERE id=$1`, c.id); err != nil {
				return err
			}
		} else {
			if err = json.Unmarshal(c.before, &previous); err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, `UPDATE public.notification_channels SET name=$2,enabled=$3,config=$4 WHERE id=$1`, c.id, previous.Name, previous.Enabled, jsonValue(previous.Config)); err != nil {
				return err
			}
		}
		if created || !previous.Enabled {
			if _, err = tx.ExecContext(ctx, `UPDATE public.notification_deliveries SET state='skipped',last_error='通知配置已撤销' WHERE channel_id=$1 AND state IN ('pending','sending')`, c.id); err != nil {
				return err
			}
		}
		if err = audit(ctx, tx, op, at, "notification_channel", c.id, "undo", map[string]any{"name": current.Name, "enabled": current.Enabled}, map[string]any{"name": previous.Name, "enabled": !created && previous.Enabled, "configuration_restored": !created}); err != nil {
			return err
		}
	}
	return nil
}
