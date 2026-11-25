package report

import (
	"encoding/json"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/artex/internal/org"
	"github.com/Autumn-27/artex/notify"
	"github.com/google/uuid"
)

// This test verifies routing facts and queue scope only. SMTP protocol delivery
// is covered by the notify/server fake-receiver tests; no sender runs here.
func TestOrganizationReportCCScopeAndQueueUseSavedFilter(t *testing.T) {
	s, ctx := testStore(t)
	organization := org.New(s.DB)
	change := func(kind string, id, revision int64, value any) org.ChangeResult {
		t.Helper()
		raw, _ := json.Marshal(value)
		result, err := organization.Change(ctx, org.Command{Kind: kind, Action: "save", ID: id, Revision: revision, Data: raw, Scope: "all_open", ConfirmImpact: true, IdempotencyKey: uuid.NewString()})
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	department := testDepartment(t, s, ctx)
	otherDepartment := testDepartment(t, s, ctx)
	groups := []int64{}
	for _, name := range []string{"第一组", "第二组", "停用组", "未关联组"} {
		created := change("dev_group", 0, 0, org.Group{Name: name + uuid.NewString(), DepartmentID: department, Active: true})
		groups = append(groups, created.EntityID)
	}
	person := func(name string, group int64) org.Person {
		t.Helper()
		p := org.Person{Name: name, Email: uuid.NewString() + "@example.invalid", DepartmentID: &department, DevGroupID: &group, Active: true}
		created := change("person", 0, 0, p)
		p.ID = created.EntityID
		return p
	}
	head := person("负责人同时也是组员", groups[0])
	first := person("第一组成员", groups[0])
	second := person("第二组成员", groups[1])
	inactive := person("已停用人员", groups[0])
	person("停用组成员", groups[2])
	person("未关联组成员", groups[3])
	crossDepartment := person("已转到其他部门的人员", groups[0])
	line := func(name string, ids []int64) int64 {
		t.Helper()
		created := change("business_line", 0, 0, org.BusinessLine{Name: name + uuid.NewString(), Code: uuid.NewString(), DepartmentID: department, GroupIDs: ids, Active: true})
		return created.EntityID
	}
	firstLine := line("第一业务线", []int64{groups[0], groups[2]})
	line("第二业务线", []int64{groups[0], groups[1]})
	foreignGroup := change("dev_group", 0, 0, org.Group{Name: "另一部门研发组-" + uuid.NewString(), DepartmentID: otherDepartment, Active: true})
	change("business_line", 0, 0, org.BusinessLine{Name: "另一部门业务线-" + uuid.NewString(), Code: uuid.NewString(), DepartmentID: otherDepartment, GroupIDs: []int64{foreignGroup.EntityID}, Active: true})
	// Isolated fixtures represent disabled and legitimately transferred members.
	// The current department's recipients must not retain either of them.
	if _, err := s.DB.ExecContext(ctx, `UPDATE org.app_user SET active=false WHERE id=$1`, inactive.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.ExecContext(ctx, `UPDATE org.app_user SET department_id=$2,dev_group_id=$3 WHERE id=$1`, crossDepartment.ID, otherDepartment, foreignGroup.EntityID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.ExecContext(ctx, `UPDATE org.dev_group SET active=false WHERE id=$1`, groups[2]); err != nil {
		t.Fatal(err)
	}
	var channel int64
	if err := s.DB.QueryRowContext(ctx, `INSERT INTO notification_channels(name,kind,enabled,config,mode)
 VALUES($1,'email',true,'{"host":"smtp.example.invalid","port":465,"from":"fixture@example.invalid","to":["shared@example.invalid"],"cc":["must-not-inherit@example.invalid"],"tls":true}','realtime') RETURNING id`, uuid.NewString()).Scan(&channel); err != nil {
		t.Fatal(err)
	}
	directory, err := organization.Directory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, dep := range directory.Departments {
		if dep.ID == department {
			dep.HeadID, dep.ChannelIDs = &head.ID, []int64{channel}
			change("department", dep.ID, dep.Revision, dep)
		}
	}
	zero := int64(0)
	for _, tc := range []struct {
		name string
		line *int64
		want []string
	}{{"department deduplicates overlapping groups", nil, []string{first.Email, second.Email}}, {"one business line", &firstLine, []string{first.Email}}, {"unassigned business line", &zero, []string{}}} {
		t.Run(tc.name, func(t *testing.T) {
			route, err := s.DeliveryRoute(ctx, department, tc.line)
			if err != nil || route.State != "ready" || route.HeadID == nil || *route.HeadID != head.ID {
				t.Fatalf("route unavailable: %+v %v", route, err)
			}
			got := append([]string{}, route.CCEmails...)
			sort.Strings(got)
			sort.Strings(tc.want)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("CC escaped the selected active department/line groups: got=%v want=%v", got, tc.want)
			}
		})
	}
	if err = s.Refresh(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	reportID, err := s.StoreReport(ctx, "weekly", time.Now(), db.FindingMetricsFilter{DepartmentID: &department, BusinessLineID: &firstLine})
	if err != nil {
		t.Fatal(err)
	}
	// The caller cannot enlarge a stored one-line report's recipient scope.
	var content notify.ReportContent
	if err = json.Unmarshal([]byte(jsonValue(map[string]any{"id": reportID + 999, "title": "isolated CC report", "department_id": otherDepartment, "business_line_id": 0})), &content); err != nil {
		t.Fatal(err)
	}
	if err = s.QueueReport(ctx, reportID, content); err != nil {
		t.Fatal(err)
	}
	var queuedDepartment, queuedLine, queuedReport int64
	var count int
	var raw []byte
	if err = s.DB.QueryRowContext(ctx, `SELECT (e.snapshot->'report'->>'department_id')::bigint,(e.snapshot->'report'->>'business_line_id')::bigint,(e.snapshot->'report'->>'id')::bigint,r.delivery_context,
 (SELECT count(*) FROM notification_deliveries d WHERE d.event_id=e.id AND d.state='pending')
 FROM finding_reports r JOIN notification_events e ON e.id=r.event_id WHERE r.id=$1`, reportID).Scan(&queuedDepartment, &queuedLine, &queuedReport, &raw, &count); err != nil {
		t.Fatal(err)
	}
	var route DeliveryContext
	if err = json.Unmarshal(raw, &route); err != nil {
		t.Fatal(err)
	}
	if queuedDepartment != department || queuedLine != firstLine || queuedReport != reportID || count != 1 || !reflect.DeepEqual(route.CCEmails, []string{first.Email}) {
		t.Fatalf("queue accepted caller routing instead of saved report: report=%d dep=%d line=%d count=%d route=%+v", queuedReport, queuedDepartment, queuedLine, count, route)
	}
}
