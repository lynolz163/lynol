package server

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/artex/internal/org"
	"github.com/Autumn-27/artex/notify"
	"github.com/google/uuid"
)

// This fixture requires an explicitly supplied disposable database. It never
// starts Notifier.Run or a channel sender, and does not clear shared tables.
func notifierOrgFixture(t *testing.T) (*Notifier, *org.Store, context.Context, *db.NotificationChannel, org.Department, org.Person, org.Person) {
	t.Helper()
	dsn := os.Getenv("ARTEX_PG_DSN")
	if dsn == "" {
		t.Skip("ARTEX_PG_DSN is required for isolated organization notification tests")
	}
	pg, e := db.Open(dsn)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { pg.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	t.Cleanup(cancel)
	store := org.New(pg.DB)
	disabled := false
	channel := &db.NotificationChannel{Name: "org-config-test-" + uuid.NewString(), Kind: notify.KindEmail, Config: json.RawMessage(`{"to":["shared@example.invalid"],"host":"smtp.example.invalid"}`), Enabled: &disabled}
	channel.ID, e = pg.SaveNotificationChannel(ctx, channel)
	if e != nil {
		t.Fatal(e)
	}
	change := func(kind string, id, rev int64, value any) org.ChangeResult {
		raw, _ := json.Marshal(value)
		result, err := store.Change(ctx, org.Command{Kind: kind, Action: "save", ID: id, Revision: rev, Data: raw, Scope: "all_open", ConfirmImpact: true, IdempotencyKey: uuid.NewString()})
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	dep := org.Department{Name: "通知部门-" + uuid.NewString(), Code: uuid.NewString(), Active: true, ChannelIDs: []int64{channel.ID}}
	result := change("department", 0, 0, dep)
	dep.ID = result.EntityID
	dep.Revision = 1
	people := []org.Person{}
	for _, name := range []string{"原负责人", "新负责人"} {
		p := org.Person{Name: name, Email: uuid.NewString() + "@example.invalid", DepartmentID: &dep.ID, Active: true}
		saved := change("person", 0, 0, p)
		p.ID = saved.EntityID
		p.Revision = 1
		people = append(people, p)
	}
	dep.HeadID = &people[0].ID
	change("department", dep.ID, dep.Revision, dep)
	dep.Revision++
	return &Notifier{pg: pg}, store, ctx, channel, dep, people[0], people[1]
}
func TestDepartmentReportConfigCopiesSMTPAndUsesCurrentHead(t *testing.T) {
	n, store, ctx, ch, dep, first, second := notifierOrgFixture(t)
	original := map[string]any{"to": []any{"shared@example.invalid"}, "host": "smtp.example.invalid", "password": "fixture-only"}
	cfg, e := n.departmentReportConfig(ctx, ch, original, dep.ID, nil)
	if e != nil {
		t.Fatal(e)
	}
	if !reflect.DeepEqual(cfg["to"], []any{first.Email}) {
		t.Fatalf("actual email adapter expects JSON array []any, got %#v", cfg["to"])
	}
	if !reflect.DeepEqual(original["to"], []any{"shared@example.invalid"}) {
		t.Fatal("shared SMTP recipients mutated")
	}
	cfg["host"] = "changed-fixture"
	if original["host"] != "smtp.example.invalid" {
		t.Fatal("shared SMTP map reused")
	}
	dep.HeadID = &second.ID
	raw, _ := json.Marshal(dep)
	if _, e = store.Change(ctx, org.Command{Kind: "department", Action: "save", ID: dep.ID, Revision: dep.Revision, Data: raw, Scope: "all_open", ConfirmImpact: true, IdempotencyKey: uuid.NewString()}); e != nil {
		t.Fatal(e)
	}
	latest, e := n.departmentReportConfig(ctx, ch, original, dep.ID, nil)
	if e != nil || !reflect.DeepEqual(latest["to"], []any{second.Email}) {
		t.Fatalf("did not route current head: %#v %v", latest, e)
	}
	var persisted []byte
	if e = n.pg.QueryRowContext(ctx, `SELECT config FROM notification_channels WHERE id=$1`, ch.ID).Scan(&persisted); e != nil {
		t.Fatal(e)
	}
	var stored map[string]any
	if e = json.Unmarshal(persisted, &stored); e != nil || !reflect.DeepEqual(stored["to"], []any{"shared@example.invalid"}) {
		t.Fatalf("stored SMTP changed %s %v", persisted, e)
	}
}
func TestDepartmentReportConfigRejectsUnavailableDestination(t *testing.T) {
	n, store, ctx, ch, dep, head, _ := notifierOrgFixture(t)
	cfg := map[string]any{"to": []any{"shared@example.invalid"}}
	missing := org.Department{Name: "无负责人-" + uuid.NewString(), Code: uuid.NewString(), Active: true, ChannelIDs: []int64{ch.ID}}
	raw, _ := json.Marshal(missing)
	saved, e := store.Change(ctx, org.Command{Kind: "department", Action: "save", Data: raw, Scope: "all_open", IdempotencyKey: uuid.NewString()})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = n.departmentReportConfig(ctx, ch, cfg, saved.EntityID, nil); e == nil {
		t.Fatal("missing department head accepted")
	}
	unrelated := *ch
	unrelated.ID = -1234
	if _, e = n.departmentReportConfig(ctx, &unrelated, cfg, dep.ID, nil); e == nil {
		t.Fatal("channel outside department accepted")
	}
	// Simulate a stale external personnel projection without sending any message.
	if _, e = n.pg.ExecContext(ctx, `UPDATE org.app_user SET active=false WHERE id=$1`, head.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = n.departmentReportConfig(ctx, ch, cfg, dep.ID, nil); e == nil {
		t.Fatal("inactive head accepted")
	}
	if _, e = n.pg.ExecContext(ctx, `UPDATE org.app_user SET active=true WHERE id=$1`, head.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = store.Change(ctx, org.Command{Kind: "department", Action: "disable", ID: dep.ID, Revision: dep.Revision, Data: json.RawMessage(`{}`), Scope: "all_open", ConfirmImpact: true, IdempotencyKey: uuid.NewString()}); e != nil {
		t.Fatal(e)
	}
	if _, e = n.departmentReportConfig(ctx, ch, cfg, dep.ID, nil); e == nil {
		t.Fatal("inactive department accepted")
	}
}
