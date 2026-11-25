package org

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
)

var orgTestSchema struct {
	sync.Once
	err error
}

func testOrgStore(t *testing.T) (*Store, context.Context) {
	t.Helper()
	dsn := os.Getenv("ARTEX_ORG_TEST_DSN")
	if dsn == "" {
		t.Skip("ARTEX_ORG_TEST_DSN is not set; no implicit production database")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(4)
	t.Cleanup(func() { db.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	t.Cleanup(cancel)
	orgTestSchema.Do(func() {
		combined := ""
		for _, name := range []string{"schema.sql", "finding_history.sql", "finding_reports.sql", "responsibility.sql"} {
			b, e := os.ReadFile(filepath.Join("..", "..", "db", name))
			if e != nil {
				orgTestSchema.err = e
				return
			}
			combined += string(b) + "\n"
		}
		_, orgTestSchema.err = db.ExecContext(ctx, combined+SchemaSQL)
	})
	if orgTestSchema.err != nil {
		t.Fatal(orgTestSchema.err)
	}
	return New(db), ctx
}

func testAsset(t *testing.T, s *Store, ctx context.Context, dep, line int64, base ChangeResult) int64 {
	t.Helper()
	var id int64
	err := s.DB.QueryRowContext(ctx, `INSERT INTO public.assets(type,domain) VALUES('subdomain',$1) RETURNING id`, uuid.NewString()+".example.invalid").Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.DB.ExecContext(ctx, `INSERT INTO org.asset_registry(asset_id,asset_type,identifier,first_seen_at,source_updated_at) VALUES($1,'subdomain',$2,$3,$3)`, id, "synthetic-"+uuid.NewString(), base.EffectiveAt)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.DB.ExecContext(ctx, `INSERT INTO org.asset_ownership(asset_id,department_id,business_line_id,source,valid_from,operation_id,created_by) VALUES($1,$2,$3,'manual',$4,$5,'test fixture')`, id, dep, line, base.EffectiveAt, base.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	return id
}
func testLine(t *testing.T, s *Store, ctx context.Context, id int64) BusinessLine {
	t.Helper()
	d, e := s.Directory(ctx)
	if e != nil {
		t.Fatal(e)
	}
	for _, v := range d.BusinessLines {
		if v.ID == id {
			return v
		}
	}
	t.Fatalf("line %d missing", id)
	return BusinessLine{}
}
func testOwner(t *testing.T, s *Store, ctx context.Context, asset int64, at, born time.Time, want int64, reason string) {
	t.Helper()
	a, e := s.ResolveOwner(ctx, asset, at, born)
	if e != nil {
		t.Fatal(e)
	}
	if a.OwnerID == nil || *a.OwnerID != want || a.OwnerResolution != reason {
		t.Fatalf("resolve at %v born %v: got %+v want %d/%s", at, born, a, want, reason)
	}
}

func TestOwnerFutureCohortsPreserveThreeGenerations(t *testing.T) {
	s, ctx := testOrgStore(t)
	dep := testDepartment(t, s, ctx)
	people := []ChangeResult{}
	for _, name := range []string{"甲", "乙", "丙"} {
		people = append(people, testPerson(t, s, ctx, dep.EntityID, name, uuid.NewString()+"@example.invalid"))
	}
	base := testChange(t, s, ctx, "business_line", 0, 0, BusinessLine{Name: "分段-" + uuid.NewString(), Code: uuid.NewString(), DepartmentID: dep.EntityID, PrimaryID: &people[0].EntityID, Active: true})
	asset := testAsset(t, s, ctx, dep.EntityID, base.EntityID, base)
	oldBorn := base.EffectiveAt.Add(-time.Hour)
	line := testLine(t, s, ctx, base.EntityID)
	line.PrimaryID = &people[1].EntityID
	raw, _ := json.Marshal(line)
	c := Command{Kind: "business_line", Action: "save", ID: line.ID, Revision: line.Revision, Data: raw, Scope: "future_findings", ConfirmImpact: true, IdempotencyKey: uuid.NewString()}
	first, err := s.Change(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	repeated, err := s.Change(ctx, c)
	if err != nil || repeated.OperationID != first.OperationID {
		t.Fatalf("idempotent replay: %+v %v", repeated, err)
	}
	line = testLine(t, s, ctx, base.EntityID)
	line.PrimaryID = &people[2].EntityID
	raw, _ = json.Marshal(line)
	c.Revision = line.Revision
	c.Data = raw
	c.IdempotencyKey = uuid.NewString()
	second, err := s.Change(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	at := second.EffectiveAt.Add(time.Second)
	testOwner(t, s, ctx, asset, at, oldBorn, people[0].EntityID, "primary")
	testOwner(t, s, ctx, asset, at, first.EffectiveAt, people[1].EntityID, "primary")
	testOwner(t, s, ctx, asset, at, second.EffectiveAt, people[2].EntityID, "primary")
	testOwner(t, s, ctx, asset, first.EffectiveAt.Add(-time.Microsecond), oldBorn, people[0].EntityID, "primary")
	var recordedID int64
	if err = s.DB.QueryRowContext(ctx, `SELECT (new_value->>'primary_id')::bigint FROM org.audit_log WHERE operation_id=$1 AND object_type='business_line'`, second.OperationID).Scan(&recordedID); err != nil {
		t.Fatal(err)
	}
	if recordedID != people[2].EntityID {
		t.Fatalf("audit captures stale owner %d", recordedID)
	}
}

func TestOwnerResolutionOrderAndDepartmentOnlyAssets(t *testing.T) {
	s, ctx := testOrgStore(t)
	dep := testDepartment(t, s, ctx)
	p := []ChangeResult{}
	for _, name := range []string{"主", "备", "部", "兜"} {
		p = append(p, testPerson(t, s, ctx, dep.EntityID, name, uuid.NewString()+"@example.invalid"))
	}
	directory, err := s.Directory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var department Department
	for _, d := range directory.Departments {
		if d.ID == dep.EntityID {
			department = d
		}
	}
	department.HeadID = &p[2].EntityID
	testChange(t, s, ctx, "department", department.ID, department.Revision, department)
	directory, err = s.Directory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	testChange(t, s, ctx, "fallback", 1, directory.ConfigRevision, map[string]int64{"owner_id": p[3].EntityID})
	base := testChange(t, s, ctx, "business_line", 0, 0, BusinessLine{Name: "顺序-" + uuid.NewString(), Code: uuid.NewString(), DepartmentID: dep.EntityID, PrimaryID: &p[0].EntityID, BackupID: &p[1].EntityID, Active: true})
	asset := testAsset(t, s, ctx, dep.EntityID, base.EntityID, base)
	born := base.EffectiveAt
	testOwner(t, s, ctx, asset, base.EffectiveAt, born, p[0].EntityID, "primary")
	line := testLine(t, s, ctx, base.EntityID)
	line.PrimaryID = nil
	backup := testChange(t, s, ctx, "business_line", line.ID, line.Revision, line)
	testOwner(t, s, ctx, asset, backup.EffectiveAt, born, p[1].EntityID, "backup")
	line = testLine(t, s, ctx, base.EntityID)
	line.BackupID = nil
	head := testChange(t, s, ctx, "business_line", line.ID, line.Revision, line)
	testOwner(t, s, ctx, asset, head.EffectiveAt, born, p[2].EntityID, "department_head")
	// A department-only assignment must use the department head directly.
	var onlyAsset int64
	if err = s.DB.QueryRowContext(ctx, `INSERT INTO org.asset_registry(asset_id,asset_type,identifier,first_seen_at,source_updated_at) VALUES(9000000000+(SELECT COALESCE(max(asset_id),0) FROM org.asset_registry),'ip','192.0.2.1',$1,$1) RETURNING asset_id`, head.EffectiveAt).Scan(&onlyAsset); err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.ExecContext(ctx, `INSERT INTO org.asset_ownership(asset_id,department_id,source,valid_from,operation_id,created_by) VALUES($1,$2,'manual',$3,$4,'test fixture')`, onlyAsset, dep.EntityID, head.EffectiveAt, head.OperationID); err != nil {
		t.Fatal(err)
	}
	testOwner(t, s, ctx, onlyAsset, head.EffectiveAt, born, p[2].EntityID, "department_head")
	directory, err = s.Directory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range directory.Departments {
		if d.ID == dep.EntityID {
			department = d
		}
	}
	department.HeadID = nil
	fallback := testChange(t, s, ctx, "department", department.ID, department.Revision, department)
	testOwner(t, s, ctx, asset, fallback.EffectiveAt, born, p[3].EntityID, "fallback")
	// Past attribution still resolves the original primary after all current edits.
	testOwner(t, s, ctx, asset, base.EffectiveAt, born, p[0].EntityID, "primary")
}

func TestDisableHandoverPreservesHistoryAndRejectsStaleEdits(t *testing.T) {
	s, ctx := testOrgStore(t)
	dep := testDepartment(t, s, ctx)
	old := testPerson(t, s, ctx, dep.EntityID, "离职前", uuid.NewString()+"@example.invalid")
	successor := testPerson(t, s, ctx, dep.EntityID, "接手", uuid.NewString()+"@example.invalid")
	base := testChange(t, s, ctx, "business_line", 0, 0, BusinessLine{Name: "交接-" + uuid.NewString(), Code: uuid.NewString(), DepartmentID: dep.EntityID, PrimaryID: &old.EntityID, BackupID: &successor.EntityID, Active: true})
	asset := testAsset(t, s, ctx, dep.EntityID, base.EntityID, base)
	if _, err := s.DB.ExecContext(ctx, `INSERT INTO public.findings(name,severity,asset_id,asset_ids,created_at,assignment_source) VALUES('synthetic handover finding','high',$1,jsonb_build_array($1::bigint),$2,'manual')`, asset, base.EffectiveAt); err != nil {
		t.Fatal(err)
	}
	c := Command{Kind: "person", Action: "disable", ID: old.EntityID, Revision: 1, Scope: "all_open", ConfirmImpact: true, IdempotencyKey: uuid.NewString()}
	if _, err := s.Change(ctx, c); err == nil {
		t.Fatal("disable responsible person without successor succeeded")
	}
	c.ReceiverID = &successor.EntityID
	transferred, err := s.Change(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	testOwner(t, s, ctx, asset, transferred.EffectiveAt, base.EffectiveAt, successor.EntityID, "primary")
	testOwner(t, s, ctx, asset, base.EffectiveAt, base.EffectiveAt, old.EntityID, "primary")
	line := testLine(t, s, ctx, base.EntityID)
	if line.BackupID != nil {
		t.Fatal("successor remains both primary and backup")
	}
	stale := Person{Name: "不应覆盖", Email: uuid.NewString() + "@example.invalid", DepartmentID: &dep.EntityID, Active: true}
	raw, _ := json.Marshal(stale)
	if _, err = s.Change(ctx, Command{Kind: "person", Action: "save", ID: old.EntityID, Revision: 1, Data: raw, Scope: "all_open", ConfirmImpact: true, IdempotencyKey: uuid.NewString()}); err == nil {
		t.Fatal("stale person edit accepted")
	}
}

func TestUndoSaveAndNonemptyGroupProtection(t *testing.T) {
	s, ctx := testOrgStore(t)
	dep := testDepartment(t, s, ctx)
	created := testPerson(t, s, ctx, dep.EntityID, "原名称", uuid.NewString()+"@example.invalid")
	d, err := s.Directory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var person Person
	for _, p := range d.People {
		if p.ID == created.EntityID {
			person = p
		}
	}
	person.Name = "修改后"
	changed := testChange(t, s, ctx, "person", person.ID, person.Revision, person)
	undone, err := s.Undo(ctx, changed.OperationID, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	if undone.OperationID == changed.OperationID {
		t.Fatal("undo has no distinct audit operation")
	}
	var name string
	if err = s.DB.QueryRowContext(ctx, `SELECT name FROM org.app_user WHERE id=$1`, person.ID).Scan(&name); err != nil {
		t.Fatal(err)
	}
	if name != "原名称" {
		t.Fatalf("undo did not restore original name: %s", name)
	}
	group := testChange(t, s, ctx, "dev_group", 0, 0, Group{Name: "非空-" + uuid.NewString(), DepartmentID: dep.EntityID, Active: true})
	testChange(t, s, ctx, "person", 0, 0, Person{Name: "组成员", Email: uuid.NewString() + "@example.invalid", DepartmentID: &dep.EntityID, DevGroupID: &group.EntityID, Active: true})
	c := Command{Kind: "dev_group", Action: "disable", ID: group.EntityID, Revision: 1, Scope: "all_open", ConfirmImpact: true, IdempotencyKey: uuid.NewString()}
	if _, err = s.Change(ctx, c); err == nil {
		t.Fatal("nonempty group disabled")
	}
}

func TestOrgRestartSchemaPreservesFacts(t *testing.T) {
	s, ctx := testOrgStore(t)
	dep := testDepartment(t, s, ctx)
	person := testPerson(t, s, ctx, dep.EntityID, "启动保留", uuid.NewString()+"@example.invalid")
	testChange(t, s, ctx, "business_line", 0, 0, BusinessLine{Name: "启动检查-" + uuid.NewString(), Code: uuid.NewString(), DepartmentID: dep.EntityID, PrimaryID: &person.EntityID, Active: true})
	rows, err := s.DB.QueryContext(ctx, `SELECT quote_ident(schemaname)||'.'||quote_ident(tablename) FROM pg_tables WHERE schemaname IN ('org','report') OR (schemaname='public' AND tablename IN ('finding_history','finding_reports')) ORDER BY 1`)
	if err != nil {
		t.Fatal(err)
	}
	var tables []string
	for rows.Next() {
		var table string
		if err = rows.Scan(&table); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		tables = append(tables, table)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		t.Fatal(err)
	}
	rows.Close()
	digest := func() map[string]string {
		t.Helper()
		result := map[string]string{}
		for _, table := range tables {
			var hash string
			if err := s.DB.QueryRowContext(ctx, `SELECT md5(COALESCE(jsonb_agg(to_jsonb(t) ORDER BY to_jsonb(t)::text)::text,'[]')) FROM `+table+` t`).Scan(&hash); err != nil {
				t.Fatal(err)
			}
			result[table] = hash
		}
		return result
	}
	before := digest()
	original := ""
	for _, name := range []string{"schema.sql", "finding_history.sql", "finding_reports.sql", "responsibility.sql"} {
		data, e := os.ReadFile(filepath.Join("..", "..", "db", name))
		if e != nil {
			t.Fatal(e)
		}
		original += string(data) + "\n"
	}
	if _, err = s.DB.ExecContext(ctx, original+SchemaSQL); err != nil {
		t.Fatal(err)
	}
	after := digest()
	for _, table := range tables {
		if before[table] != after[table] {
			t.Errorf("restart changed %s", table)
		}
	}
}

func TestBusinessLineMigrationRetainsPastDepartmentAndRequiresDedicatedAction(t *testing.T) {
	s, ctx := testOrgStore(t)
	from := testDepartment(t, s, ctx)
	to := testDepartment(t, s, ctx)
	person := testPerson(t, s, ctx, from.EntityID, "迁移负责人", uuid.NewString()+"@example.invalid")
	original := testChange(t, s, ctx, "business_line", 0, 0, BusinessLine{Name: "迁移-" + uuid.NewString(), Code: uuid.NewString(), DepartmentID: from.EntityID, PrimaryID: &person.EntityID, Active: true})
	asset := testAsset(t, s, ctx, from.EntityID, original.EntityID, original)
	line := testLine(t, s, ctx, original.EntityID)
	line.DepartmentID = to.EntityID
	raw, _ := json.Marshal(line)
	edit := Command{Kind: "business_line", Action: "save", ID: line.ID, Revision: line.Revision, Data: raw, Scope: "all_open", ConfirmImpact: true, IdempotencyKey: uuid.NewString()}
	if _, err := s.Change(ctx, edit); err == nil {
		t.Fatal("direct department overwrite accepted")
	}
	migrate := Command{Kind: "business_line", Action: "migrate", ID: line.ID, Revision: line.Revision, Scope: "all_open", ReceiverID: &to.EntityID, ConfirmImpact: true, IdempotencyKey: uuid.NewString()}
	changed, err := s.Change(ctx, migrate)
	if err != nil {
		t.Fatal(err)
	}
	before, err := s.ResolveOwner(ctx, asset, original.EffectiveAt, original.EffectiveAt)
	if err != nil {
		t.Fatal(err)
	}
	after, err := s.ResolveOwner(ctx, asset, changed.EffectiveAt, original.EffectiveAt)
	if err != nil {
		t.Fatal(err)
	}
	if before.DepartmentID == nil || *before.DepartmentID != from.EntityID || after.DepartmentID == nil || *after.DepartmentID != to.EntityID {
		t.Fatalf("migration lost attribution: before=%+v after=%+v", before, after)
	}
	if got := testLine(t, s, ctx, line.ID); got.DepartmentID != to.EntityID {
		t.Fatal("current projection not moved")
	}
	var records int
	if err = s.DB.QueryRowContext(ctx, `SELECT count(*) FROM org.asset_ownership WHERE asset_id=$1`, asset).Scan(&records); err != nil {
		t.Fatal(err)
	}
	if records != 2 {
		t.Fatalf("expected retained old and new ownership, got %d", records)
	}
}

func TestDepartmentDisableTransfersLinesAndDirectAssetsAtomically(t *testing.T) {
	s, ctx := testOrgStore(t)
	from := testDepartment(t, s, ctx)
	to := testDepartment(t, s, ctx)
	line := testChange(t, s, ctx, "business_line", 0, 0, BusinessLine{Name: "部门停用-" + uuid.NewString(), Code: uuid.NewString(), DepartmentID: from.EntityID, Active: true})
	lineAsset := testAsset(t, s, ctx, from.EntityID, line.EntityID, line)
	directAsset := testAsset(t, s, ctx, from.EntityID, line.EntityID, line)
	if _, err := s.DB.ExecContext(ctx, `UPDATE org.asset_ownership SET business_line_id=NULL WHERE asset_id=$1`, directAsset); err != nil {
		t.Fatal(err)
	} // Synthetic department-only fixture.
	command := Command{Kind: "department", Action: "disable", ID: from.EntityID, Revision: 1, Scope: "all_open", ConfirmImpact: true, IdempotencyKey: uuid.NewString()}
	if _, err := s.Change(ctx, command); err == nil {
		t.Fatal("department disabled without receiver")
	}
	command.ReceiverID = &to.EntityID
	disabled, err := s.Change(ctx, command)
	if err != nil {
		t.Fatal(err)
	}
	for _, asset := range []int64{lineAsset, directAsset} {
		before, e := s.ResolveOwner(ctx, asset, line.EffectiveAt, line.EffectiveAt)
		if e != nil {
			t.Fatal(e)
		}
		after, e := s.ResolveOwner(ctx, asset, disabled.EffectiveAt, line.EffectiveAt)
		if e != nil {
			t.Fatal(e)
		}
		if before.DepartmentID == nil || *before.DepartmentID != from.EntityID || after.DepartmentID == nil || *after.DepartmentID != to.EntityID {
			t.Fatalf("asset %d wrong department before/after %+v %+v", asset, before, after)
		}
		if asset == lineAsset && (after.BusinessLineID == nil || *after.BusinessLineID != line.EntityID) {
			t.Fatal("migrated line asset lost business line")
		}
		if asset == directAsset && after.BusinessLineID != nil {
			t.Fatal("department-only asset acquired an invented line")
		}
	}
	var active bool
	if err = s.DB.QueryRowContext(ctx, `SELECT active FROM org.department WHERE id=$1`, from.EntityID).Scan(&active); err != nil {
		t.Fatal(err)
	}
	if active {
		t.Fatal("department remains active")
	}
}

func TestUndoOwnerEditRejectsDisabledPriorOwner(t *testing.T) {
	s, ctx := testOrgStore(t)
	dep := testDepartment(t, s, ctx)
	original := testPerson(t, s, ctx, dep.EntityID, "已离职的原负责人", uuid.NewString()+"@example.invalid")
	replacement := testPerson(t, s, ctx, dep.EntityID, "当前负责人", uuid.NewString()+"@example.invalid")
	lineCreated := testChange(t, s, ctx, "business_line", 0, 0, BusinessLine{Name: "撤销停用检查-" + uuid.NewString(), Code: uuid.NewString(), DepartmentID: dep.EntityID, PrimaryID: &original.EntityID, Active: true})
	line := testLine(t, s, ctx, lineCreated.EntityID)
	line.PrimaryID = &replacement.EntityID
	edit := testChange(t, s, ctx, "business_line", line.ID, line.Revision, line)
	disable := Command{Kind: "person", Action: "disable", ID: original.EntityID, Revision: 1, Scope: "all_open", ConfirmImpact: true, IdempotencyKey: uuid.NewString()}
	if _, err := s.Change(ctx, disable); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Undo(ctx, edit.OperationID, uuid.NewString()); err == nil {
		t.Fatal("undo re-assigned inactive owner")
	}
	current := testLine(t, s, ctx, line.ID)
	if current.PrimaryID == nil || *current.PrimaryID != replacement.EntityID {
		t.Fatal("rejected undo changed current owner")
	}
}
