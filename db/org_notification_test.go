package db

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/Autumn-27/artex/internal/org"
	"github.com/Autumn-27/artex/notify"
	"github.com/google/uuid"
)

func TestOrganizationManagedChannelsFanOutOnlyToTheirCurrentDepartments(t *testing.T) {
	if os.Getenv("ARTEX_PG_DSN") == "" {
		t.Skip("explicit isolated ARTEX_PG_DSN required")
	}
	d, assets, _ := testSetup(t)
	defer d.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	channels := []int64{}
	for _, enabled := range []bool{true, true, false} {
		var id int64
		if err := d.QueryRowContext(ctx, `INSERT INTO notification_channels(name,kind,enabled,org_managed,config,mode)
 VALUES($1,'email',$2,true,'{"host":"smtp.example.invalid","port":465,"from":"fixture@example.invalid","to":["unused@example.invalid"]}','realtime') RETURNING id`, uuid.NewString(), enabled).Scan(&id); err != nil {
			t.Fatal(err)
		}
		channels = append(channels, id)
	}
	shared, unused, disabled := channels[0], channels[1], channels[2]
	var selectedGlobal, legacyLine int64
	if err := d.QueryRowContext(ctx, `INSERT INTO notification_channels(name,kind,enabled,org_managed,config,mode)
 VALUES($1,'email',true,false,'{"host":"smtp.example.invalid","port":465,"from":"fixture@example.invalid","to":["global@example.invalid"]}','realtime') RETURNING id`, uuid.NewString()).Scan(&selectedGlobal); err != nil {
		t.Fatal(err)
	}
	channels = append(channels, selectedGlobal)
	if err := d.QueryRowContext(ctx, `INSERT INTO business_lines(name,code,channel_ids) VALUES($1,$1,jsonb_build_array($2::bigint)) RETURNING id`, uuid.NewString(), unused).Scan(&legacyLine); err != nil {
		t.Fatal(err)
	}
	events := []int64{}
	for i := 0; i < 3; i++ {
		department, domain, _ := organizationAssetRules(t, d)
		if i < 2 {
			directory, err := org.New(d.DB).Directory(ctx)
			if err != nil {
				t.Fatal(err)
			}
			for _, dep := range directory.Departments {
				if dep.ID == department {
					dep.ChannelIDs = []int64{shared, disabled, selectedGlobal}
					raw, _ := json.Marshal(dep)
					if _, err = org.New(d.DB).Change(ctx, org.Command{Kind: "department", Action: "save", ID: dep.ID, Revision: dep.Revision, Data: raw, Scope: "all_open", IdempotencyKey: uuid.NewString()}); err != nil {
						t.Fatal(err)
					}
				}
			}
		}
		asset, err := assets.UpsertRootDomain(UpsertRootDomainReq{Domain: domain})
		if err != nil {
			t.Fatal(err)
		}
		finding, err := d.AddFinding(0, 0, "managed-routing-"+uuid.NewString(), "scoped notification", "high", "", "", "test", []int64{asset})
		if err != nil {
			t.Fatal(err)
		}
		// Preserved legacy routing must not hide an explicitly selected current
		// organization channel. For the third, unconfigured department, ordinary
		// legacy filtering still applies to this existing global channel.
		if _, err = d.ExecContext(ctx, `UPDATE findings SET business_line_id=$2 WHERE id=$1`, finding, legacyLine); err != nil {
			t.Fatal(err)
		}
		event, err := d.AddNotificationEvent(ctx, notify.EventFindingCreated, finding, notify.Snapshot{FindingID: finding, Severity: "high", Name: "isolated managed routing"})
		if err != nil {
			t.Fatal(err)
		}
		events = append(events, event)
	}
	if _, _, err := d.FanOutPendingEvents(ctx, 500); err != nil {
		t.Fatal(err)
	}
	for i, event := range events {
		var count, incorrect, global int
		if err := d.QueryRowContext(ctx, `SELECT count(*) FILTER(WHERE channel_id=$2),count(*) FILTER(WHERE channel_id=ANY($3::bigint[])),count(*) FILTER(WHERE channel_id=$4)
 FROM notification_deliveries WHERE event_id=$1`, event, shared, []int64{unused, disabled}, selectedGlobal).Scan(&count, &incorrect, &global); err != nil {
			t.Fatal(err)
		}
		want := 1
		if i == 2 {
			want = 0
		}
		if count != want || incorrect != 0 || global != want {
			t.Fatalf("event %d ignored current/legacy channel scope: shared=%d selected-global=%d want=%d orphan/disabled=%d", event, count, global, want, incorrect)
		}
	}
	if _, _, err := d.FanOutPendingEvents(ctx, 500); err != nil {
		t.Fatal(err)
	}
	var total int
	if err := d.QueryRowContext(ctx, `SELECT count(*) FROM notification_deliveries WHERE event_id=ANY($1::bigint[]) AND channel_id=ANY($2::bigint[])`, events, channels).Scan(&total); err != nil || total != 4 {
		t.Fatalf("repeat fanout duplicated or broadened deliveries: %d %v", total, err)
	}
}
