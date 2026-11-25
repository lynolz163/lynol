package org

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Autumn-27/artex/notify"
	"github.com/google/uuid"
)

func notificationCommand(t *testing.T, department Department, drafts []map[string]any, confirmed []int64, key string) Command {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"kind": "department", "action": "save", "id": department.ID, "revision": department.Revision,
		"data": department, "channel_drafts": drafts, "shared_channel_confirmed_ids": confirmed, "scope": "all_open", "confirm_impact": true, "idempotency_key": key})
	if err != nil {
		t.Fatal(err)
	}
	var c Command
	if err = json.Unmarshal(raw, &c); err != nil {
		t.Fatal(err)
	}
	return c
}

func notificationEmailDraft(name, secret string) map[string]any {
	return map[string]any{"client_key": uuid.NewString(), "id": 0, "name": name, "kind": "email", "enabled": true,
		"config": map[string]any{"host": "smtp.example.invalid", "port": 465, "from": "artex@example.invalid", "to": []any{"shared@example.invalid"}, "username": "fixture", "password": secret, "tls": true}}
}

func TestOrganizationNotificationDraftSaveIsAtomicIdempotentAndUndoable(t *testing.T) {
	s, ctx := testOrgStore(t)
	base := testDepartment(t, s, ctx)
	directory, err := s.Directory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var existing Department
	for _, dep := range directory.Departments {
		if dep.ID == base.EntityID {
			existing = dep
		}
	}
	name := "draft-atomic-" + uuid.NewString()
	draft := notificationEmailDraft(name, "draft-secret-"+uuid.NewString())
	key := uuid.NewString()
	dup := Department{Name: existing.Name, Code: uuid.NewString(), Active: true}
	command := notificationCommand(t, dup, []map[string]any{draft}, nil, key)
	var beforeOps, beforeAudit int
	if err = s.DB.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM org.change_operation),(SELECT count(*) FROM org.audit_log)`).Scan(&beforeOps, &beforeAudit); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Change(ctx, command); err == nil {
		t.Fatal("duplicate organization name unexpectedly saved")
	}
	var channels, ops, audit int
	if err = s.DB.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM notification_channels WHERE name=$1),(SELECT count(*) FROM org.change_operation),(SELECT count(*) FROM org.audit_log)`, name).Scan(&channels, &ops, &audit); err != nil || channels != 0 || ops != beforeOps || audit != beforeAudit {
		t.Fatalf("failed organization save leaked channel/operation/audit: channel=%d ops=%d/%d audit=%d/%d err=%v", channels, ops, beforeOps, audit, beforeAudit, err)
	}
	dup.Name = "draft-department-" + uuid.NewString()
	command = notificationCommand(t, dup, []map[string]any{draft}, nil, key)
	saved, err := s.Change(ctx, command)
	if err != nil {
		t.Fatal(err)
	}
	again, err := s.Change(ctx, command)
	if err != nil || again.OperationID != saved.OperationID || again.EntityID != saved.EntityID {
		t.Fatalf("same submission created a second operation: %+v %+v %v", saved, again, err)
	}
	var channel int64
	var managed, linked bool
	if err = s.DB.QueryRowContext(ctx, `SELECT c.id,c.org_managed,c.id=ANY(d.channel_ids) FROM notification_channels c JOIN org.department d ON d.id=$2 WHERE c.name=$1`, name, saved.EntityID).Scan(&channel, &managed, &linked); err != nil || !managed || !linked {
		t.Fatalf("new channel was not transactionally associated/scoped: id=%d managed=%v linked=%v err=%v", channel, managed, linked, err)
	}
	if err = s.DB.QueryRowContext(ctx, `SELECT count(*) FROM notification_channels WHERE name=$1`, name).Scan(&channels); err != nil || channels != 1 {
		t.Fatalf("duplicate retry channels=%d err=%v", channels, err)
	}
	if _, err = s.Undo(ctx, saved.OperationID, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	var enabled, departmentActive bool
	if err = s.DB.QueryRowContext(ctx, `SELECT c.enabled,d.active FROM notification_channels c JOIN org.department d ON d.id=$2 WHERE c.id=$1`, channel, saved.EntityID).Scan(&enabled, &departmentActive); err != nil || enabled || departmentActive {
		t.Fatalf("undo must retain and disable newly created channel/department: enabled=%v active=%v err=%v", enabled, departmentActive, err)
	}
}

func TestOrganizationNotificationSharedEditPreservesSecretsAndUndo(t *testing.T) {
	s, ctx := testOrgStore(t)
	secret := "never-expose-" + uuid.NewString()
	name := "shared-channel-" + uuid.NewString()
	first := Department{Name: "first-" + uuid.NewString(), Code: uuid.NewString(), Active: true}
	created, err := s.Change(ctx, notificationCommand(t, first, []map[string]any{notificationEmailDraft(name, secret)}, nil, uuid.NewString()))
	if err != nil {
		t.Fatal(err)
	}
	var channel int64
	var updated time.Time
	if err = s.DB.QueryRowContext(ctx, `SELECT id,updated_at FROM notification_channels WHERE name=$1`, name).Scan(&channel, &updated); err != nil {
		t.Fatal(err)
	}
	second := Department{Name: "second-" + uuid.NewString(), Code: uuid.NewString(), Active: true, ChannelIDs: []int64{channel}}
	testChange(t, s, ctx, "department", 0, 0, second)
	directory, err := s.Directory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, dep := range directory.Departments {
		if dep.ID == created.EntityID {
			first = dep
		}
	}
	draft := notificationEmailDraft(name+" edited", notify.MaskedPrefix)
	draft["id"], draft["expected_updated_at"] = channel, updated.Format(time.RFC3339Nano)
	unconfirmed := notificationCommand(t, first, []map[string]any{draft}, nil, uuid.NewString())
	if _, err = s.Change(ctx, unconfirmed); err == nil {
		t.Fatal("shared channel edit saved without impact confirmation")
	}
	var unchangedName, actualPassword string
	if err = s.DB.QueryRowContext(ctx, `SELECT name,config->>'password' FROM notification_channels WHERE id=$1`, channel).Scan(&unchangedName, &actualPassword); err != nil || unchangedName != name || actualPassword != secret {
		t.Fatal("rejected shared edit changed channel")
	}
	saved, err := s.Change(ctx, notificationCommand(t, first, []map[string]any{draft}, []int64{channel}, uuid.NewString()))
	if err != nil {
		t.Fatal(err)
	}
	var changedName, auditJSON string
	if err = s.DB.QueryRowContext(ctx, `SELECT name,config->>'password' FROM notification_channels WHERE id=$1`, channel).Scan(&changedName, &actualPassword); err != nil || changedName != name+" edited" || actualPassword != secret {
		t.Fatalf("masked edit lost existing secret: name=%q err=%v", changedName, err)
	}
	if err = s.DB.QueryRowContext(ctx, `SELECT COALESCE(jsonb_agg(a ORDER BY id)::text,'') FROM org.audit_log a WHERE operation_id=$1`, saved.OperationID).Scan(&auditJSON); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(auditJSON, secret) || strings.Contains(auditJSON, "smtp.example.invalid") || strings.Contains(auditJSON, notify.MaskedPrefix) {
		t.Fatalf("public audit contains channel credentials/config values: %s", auditJSON)
	}
	if !strings.Contains(auditJSON, fmt.Sprint(channel)) {
		t.Fatal("channel edit missing from audit")
	}
	if _, err = s.Undo(ctx, saved.OperationID, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	if err = s.DB.QueryRowContext(ctx, `SELECT name,config->>'password' FROM notification_channels WHERE id=$1`, channel).Scan(&changedName, &actualPassword); err != nil || changedName != name || actualPassword != secret {
		t.Fatalf("undo did not restore shared channel configuration: name=%q err=%v", changedName, err)
	}
}

func TestOrganizationNotificationDraftTestDoesNotPersistOrExposeSecrets(t *testing.T) {
	s, ctx := testOrgStore(t)
	secret := "draft-api-secret-" + uuid.NewString()
	name := "draft-api-" + uuid.NewString()
	department := Department{Name: "draft-api-department-" + uuid.NewString(), Code: uuid.NewString(), Active: true}
	if _, err := s.Change(ctx, notificationCommand(t, department, []map[string]any{notificationEmailDraft(name, secret)}, nil, uuid.NewString())); err != nil {
		t.Fatal(err)
	}
	var channel int64
	var updated time.Time
	if err := s.DB.QueryRowContext(ctx, `SELECT id,updated_at FROM notification_channels WHERE name=$1`, name).Scan(&channel, &updated); err != nil {
		t.Fatal(err)
	}
	calls := 0
	fail := false
	handler := HandlerWithNotificationSender(s.DB, func(_ context.Context, kind string, config map[string]any, message notify.Message) error {
		calls++
		if kind != notify.KindEmail || config["password"] != secret || len(message.Items) != 1 {
			t.Errorf("draft test did not merge preserved credential into intended test message")
		}
		if fail {
			return errors.New("fake sender rejected " + secret)
		}
		return nil
	})
	request := func(method, path string, body any) *httptest.ResponseRecorder {
		t.Helper()
		raw, _ := json.Marshal(body)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(method, path, strings.NewReader(string(raw))))
		if strings.Contains(w.Body.String(), secret) {
			t.Fatal("draft API exposed secret in response")
		}
		return w
	}
	listed := request(http.MethodGet, "/api/org/notification-channels", nil)
	var list struct {
		TestMode string `json:"test_mode"`
		Channels []struct {
			ID     int64          `json:"id"`
			Config map[string]any `json:"config"`
		} `json:"channels"`
	}
	if err := json.Unmarshal(listed.Body.Bytes(), &list); err != nil || listed.Code != http.StatusOK || list.TestMode != "fake" {
		t.Fatalf("draft test mode/metadata: code=%d err=%v", listed.Code, err)
	}
	found := false
	for _, value := range list.Channels {
		if value.ID == channel {
			found = notify.IsMasked(fmt.Sprint(value.Config["password"]))
		}
	}
	if !found {
		t.Fatal("existing channel password not masked in organization API")
	}
	snapshot := func() string {
		t.Helper()
		var value string
		if err := s.DB.QueryRowContext(ctx, `SELECT md5(jsonb_build_array(
 (SELECT jsonb_agg(c ORDER BY id) FROM notification_channels c),
 (SELECT jsonb_agg(a ORDER BY id) FROM org.audit_log a),
 (SELECT jsonb_agg(o ORDER BY id) FROM org.change_operation o))::text)`).Scan(&value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	before := snapshot()
	draft := notificationEmailDraft("unsaved draft", secret)
	if got := request(http.MethodPost, "/api/org/notification-channels/test", draft); got.Code != http.StatusOK || !strings.Contains(got.Body.String(), `"simulated":true`) {
		t.Fatalf("new draft test failed: %d %s", got.Code, got.Body.String())
	}
	draft["id"], draft["expected_updated_at"] = channel, updated.Format(time.RFC3339Nano)
	draft["config"].(map[string]any)["password"] = notify.MaskedPrefix
	if got := request(http.MethodPost, "/api/org/notification-channels/test", draft); got.Code != http.StatusOK {
		t.Fatalf("existing masked draft test failed: %d %s", got.Code, got.Body.String())
	}
	fail = true
	if got := request(http.MethodPost, "/api/org/notification-channels/test", draft); got.Code != http.StatusBadGateway {
		t.Fatalf("failed sender was reported successful: %d", got.Code)
	}
	if calls != 3 || snapshot() != before {
		t.Fatalf("draft testing saved configuration/audit or called unexpected sender count: %d", calls)
	}
}
