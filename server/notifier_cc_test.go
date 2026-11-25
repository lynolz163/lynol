package server

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/mail"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/artex/internal/org"
	"github.com/Autumn-27/artex/notify"
	"github.com/google/uuid"
)

type notificationCCFixture struct {
	n          *Notifier
	store      *org.Store
	ctx        context.Context
	channel    *db.NotificationChannel
	department org.Department
	head, next org.Person
	lines      []org.BusinessLine
	members    []org.Person
	findings   []int64
}

func newNotificationCCFixture(t *testing.T) notificationCCFixture {
	t.Helper()
	n, store, ctx, ch, dep, head, next := notifierOrgFixture(t)
	f := notificationCCFixture{n: n, store: store, ctx: ctx, channel: ch, department: dep, head: head, next: next}
	change := func(kind string, id, revision int64, value any) org.ChangeResult {
		t.Helper()
		raw, _ := json.Marshal(value)
		out, err := store.Change(ctx, org.Command{Kind: kind, Action: "save", ID: id, Revision: revision, Data: raw, Scope: "all_open", ConfirmImpact: true, IdempotencyKey: uuid.NewString()})
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	for i := 0; i < 2; i++ {
		group := change("dev_group", 0, 0, org.Group{Name: "抄送组-" + uuid.NewString(), DepartmentID: dep.ID, Active: true})
		person := org.Person{Name: "抄送成员-" + uuid.NewString(), Email: uuid.NewString() + "@example.invalid", DepartmentID: &dep.ID, DevGroupID: &group.EntityID, Active: true}
		person.ID = change("person", 0, 0, person).EntityID
		person.Revision = 1
		f.members = append(f.members, person)
		line := org.BusinessLine{Name: fmt.Sprintf("抄送业务线%d-", i) + uuid.NewString(), Code: uuid.NewString(), DepartmentID: dep.ID, PrimaryID: &head.ID, GroupIDs: []int64{group.EntityID}, ChannelIDs: []int64{ch.ID}, Active: true}
		saved := change("business_line", 0, 0, line)
		line.ID = saved.EntityID
		line.Revision = 1
		f.lines = append(f.lines, line)
		var asset, finding int64
		if err := n.pg.QueryRowContext(ctx, `INSERT INTO assets(type,domain) VALUES('subdomain',$1) RETURNING id`, uuid.NewString()+".example.invalid").Scan(&asset); err != nil {
			t.Fatal(err)
		}
		if _, err := n.pg.ExecContext(ctx, `INSERT INTO org.asset_registry(asset_id,asset_type,identifier,first_seen_at,source_updated_at) VALUES($1,'subdomain',$2,now(),now())`, asset, fmt.Sprint(asset)); err != nil {
			t.Fatal(err)
		}
		if _, err := n.pg.ExecContext(ctx, `INSERT INTO org.asset_ownership(asset_id,department_id,business_line_id,source,valid_from,operation_id,created_by) VALUES($1,$2,$3,'manual',$4,$5,'isolated notification fixture')`, asset, dep.ID, line.ID, saved.EffectiveAt, saved.OperationID); err != nil {
			t.Fatal(err)
		}
		if err := n.pg.QueryRowContext(ctx, `INSERT INTO findings(asset_ids,name,severity) VALUES($1,$2,'high') RETURNING id`, fmt.Sprintf("[%d]", asset), fmt.Sprintf("CC scope %d", i)).Scan(&finding); err != nil {
			t.Fatal(err)
		}
		f.findings = append(f.findings, finding)
	}
	return f
}

func TestFindingNotificationUsesCurrentOwnerAndGroupCC(t *testing.T) {
	f := newNotificationCCFixture(t)
	cfg := map[string]any{"to": []any{"shared@example.invalid"}, "cc": []any{"outsider@example.invalid"}, "host": "smtp.example.invalid"}
	got, key, err := f.n.findingDeliveryConfig(f.ctx, f.channel, cfg, f.findings[0])
	if err != nil || key == "global" || !reflect.DeepEqual(got["to"], []any{f.head.Email}) || !reflect.DeepEqual(got["cc"], []string{f.members[0].Email}) {
		t.Fatalf("wrong finding scope: %#v %s %v", got, key, err)
	}
	reportCfg, err := f.n.departmentReportConfig(f.ctx, f.channel, cfg, f.department.ID, &f.lines[0].ID)
	if err != nil || !reflect.DeepEqual(reportCfg["cc"], []string{f.members[0].Email}) {
		t.Fatalf("wrong report scope: %#v %v", reportCfg, err)
	}
	// A queued finding must follow an actual owner change at send time.
	line := f.lines[0]
	line.PrimaryID = &f.next.ID
	raw, _ := json.Marshal(line)
	if _, err = f.store.Change(f.ctx, org.Command{Kind: "business_line", Action: "save", ID: line.ID, Revision: line.Revision, Data: raw, Scope: "all_open", ConfirmImpact: true, IdempotencyKey: uuid.NewString()}); err != nil {
		t.Fatal(err)
	}
	if _, err = f.n.pg.ExecContext(f.ctx, `UPDATE org.app_user SET active=false WHERE id=$1`, f.members[0].ID); err != nil {
		t.Fatal(err)
	}
	got, _, err = f.n.findingDeliveryConfig(f.ctx, f.channel, cfg, f.findings[0])
	if err != nil || !reflect.DeepEqual(got["to"], []any{f.next.Email}) || !reflect.DeepEqual(got["cc"], []string{}) {
		t.Fatalf("stale finding recipients: %#v %v", got, err)
	}
	reportCfg, err = f.n.departmentReportConfig(f.ctx, f.channel, cfg, f.department.ID, &line.ID)
	if err != nil || !reflect.DeepEqual(reportCfg["cc"], []string{}) {
		t.Fatalf("stale report member: %#v %v", reportCfg, err)
	}
	if !reflect.DeepEqual(cfg["to"], []any{"shared@example.invalid"}) || !reflect.DeepEqual(cfg["cc"], []any{"outsider@example.invalid"}) {
		t.Fatal("shared SMTP configuration changed")
	}
	f.channel.OrgManaged = true
	// Removing both line and department associations must reject an old delivery.
	if _, err = f.n.pg.ExecContext(f.ctx, `UPDATE org.business_line SET channel_ids='{}' WHERE id=$1`, line.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = f.n.pg.ExecContext(f.ctx, `UPDATE org.department SET channel_ids='{}' WHERE id=$1`, f.department.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err = f.n.findingDeliveryConfig(f.ctx, f.channel, cfg, f.findings[0]); err == nil {
		t.Fatal("detached managed channel used static recipients")
	}
}

// This receiver accepts only the expected local messages, with socket deadlines
// and cleanup waiting for its goroutine. It cannot connect to an external SMTP.
func notificationSMTPReceiver(t *testing.T, count int) (string, int, <-chan string) {
	t.Helper()
	ln, err := net.ListenTCP("tcp", &net.TCPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatal(err)
	}
	_ = ln.SetDeadline(time.Now().Add(10 * time.Second))
	messages := make(chan string, count)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < count; i++ {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
			reader := bufio.NewReader(conn)
			write := func(text string) { _, _ = io.WriteString(conn, text+"\r\n") }
			write("220 isolated.example ESMTP ready")
			for {
				line, err := reader.ReadString('\n')
				if err != nil {
					break
				}
				line = strings.TrimSpace(line)
				if strings.HasPrefix(line, "EHLO") || strings.HasPrefix(line, "HELO") {
					write("250 isolated.example")
					continue
				}
				if line == "DATA" {
					write("354 send data")
					var body strings.Builder
					for {
						part, e := reader.ReadString('\n')
						if e != nil {
							break
						}
						if part == ".\r\n" {
							break
						}
						body.WriteString(part)
					}
					messages <- body.String()
					write("250 accepted")
					continue
				}
				if line == "QUIT" {
					write("221 bye")
					break
				}
				write("250 OK")
			}
			_ = conn.Close()
		}
	}()
	t.Cleanup(func() {
		_ = ln.Close()
		select {
		case <-done:
		case <-time.After(6 * time.Second):
			t.Error("local SMTP receiver did not exit")
		}
	})
	return "127.0.0.1", ln.Addr().(*net.TCPAddr).Port, messages
}

func TestFindingNotificationDigestSeparatesBusinessLinesAndGlobalRecipients(t *testing.T) {
	f := newNotificationCCFixture(t)
	t.Setenv(notify.AllowLocalTargetsEnv, "1")
	host, port, messages := notificationSMTPReceiver(t, 3)
	cfg := map[string]any{"host": host, "port": port, "from": "fixture@example.invalid", "to": []any{"shared@example.invalid"}, "cc": []any{"global-copy@example.invalid"}}
	raw, _ := json.Marshal(cfg)
	enabled := true
	f.channel.Enabled = &enabled
	f.channel.Config = raw
	f.channel.Mode = db.NotifyModeDigest
	f.channel.OrgManaged = false
	if _, err := f.n.pg.SaveNotificationChannel(f.ctx, f.channel); err != nil {
		t.Fatal(err)
	}
	// Intentionally use a legacy shared global channel. Two scoped events have
	// the same primary recipient, different groups, plus one unassigned event.
	names := []string{"ONLY_SCOPE_A", "ONLY_SCOPE_B", "ONLY_GLOBAL"}
	for i, finding := range append(f.findings, 0) {
		id, err := f.n.pg.AddNotificationEvent(f.ctx, notify.EventFindingCreated, finding, notify.Snapshot{Kind: notify.EventFindingCreated, FindingID: finding, Name: names[i], Severity: "high"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = f.n.pg.ExecContext(f.ctx, `INSERT INTO notification_deliveries(event_id,channel_id,created_at) VALUES($1,$2,now()-interval '2 hours')`, id, f.channel.ID); err != nil {
			t.Fatal(err)
		}
	}
	f.n.stepDigest(f.ctx, f.channel, 50, "")
	for i := 0; i < 3; i++ {
		var raw string
		select {
		case raw = <-messages:
		case <-time.After(time.Second):
			t.Fatal("missing separated SMTP message")
		}
		message, err := mail.ReadMessage(strings.NewReader(raw))
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := io.ReadAll(message.Body)
		if err != nil {
			t.Fatal(err)
		}
		body, err := base64.StdEncoding.DecodeString(string(encoded))
		if err != nil {
			t.Fatal(err)
		}
		expectedTo, expectedCC := f.head.Email, ""
		if i < 2 {
			expectedCC = f.members[i].Email
		} else {
			expectedTo = "shared@example.invalid"
			expectedCC = "global-copy@example.invalid"
		}
		if message.Header.Get("To") != expectedTo || message.Header.Get("Cc") != expectedCC {
			t.Fatalf("message %d recipients: %s / %s", i, message.Header.Get("To"), message.Header.Get("Cc"))
		}
		for j, name := range names {
			if strings.Contains(string(body), name) != (i == j) {
				t.Fatalf("message %d leaked/omitted %s: %s", i, name, body)
			}
		}
	}
	var sent int
	if err := f.n.pg.QueryRowContext(f.ctx, `SELECT count(*) FROM notification_deliveries WHERE channel_id=$1 AND state='sent'`, f.channel.ID).Scan(&sent); err != nil || sent != 3 {
		t.Fatalf("sent=%d err=%v", sent, err)
	}
}
