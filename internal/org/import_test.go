package org

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestPeopleCSVPreviewAndAtomicImport(t *testing.T) {
	s, ctx := testOrgStore(t)
	dep := testDepartment(t, s, ctx)
	suffix := uuid.NewString()
	oldEmail := "old-" + suffix + "@example.invalid"
	old := testPerson(t, s, ctx, dep.EntityID, "原负责人", oldEmail)
	line := BusinessLine{Name: "CSV责任-" + suffix, Code: "csv-" + suffix, DepartmentID: dep.EntityID, PrimaryID: &old.EntityID, Active: true, GroupIDs: []int64{}, ChannelIDs: []int64{}}
	testChange(t, s, ctx, "business_line", 0, 0, line)
	var beforeUsers, beforeAudit, beforeOps int
	count := func() {
		t.Helper()
		if err := s.DB.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM org.app_user),(SELECT count(*) FROM org.audit_log),(SELECT count(*) FROM org.change_operation)`).Scan(&beforeUsers, &beforeAudit, &beforeOps); err != nil {
			t.Fatal(err)
		}
	}
	count()
	newEmail := "new-" + suffix + "@example.invalid"
	csvText := "name,email,department,dev_group\n更新姓名," + strings.ToUpper(oldEmail) + ",,\n新人员," + newEmail + ",,\n"
	req := ImportRequest{CSV: csvText, DepartmentID: &dep.EntityID, IdempotencyKey: uuid.NewString()}
	preview, err := s.PreviewPeopleImport(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if preview.Created != 1 || preview.Updated != 1 || preview.Errors != 0 {
		t.Fatalf("unexpected preview %+v", preview)
	}
	if !strings.Contains(preview.Rows[0].Warning, "责任关系保留") {
		t.Fatalf("responsibility warning missing: %+v", preview.Rows[0])
	}
	var users, audit, ops int
	if err = s.DB.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM org.app_user),(SELECT count(*) FROM org.audit_log),(SELECT count(*) FROM org.change_operation)`).Scan(&users, &audit, &ops); err != nil {
		t.Fatal(err)
	}
	if users != beforeUsers || audit != beforeAudit || ops != beforeOps {
		t.Fatalf("preview wrote rows: %d/%d %d/%d %d/%d", users, beforeUsers, audit, beforeAudit, ops, beforeOps)
	}
	result, err := s.ImportPeople(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if result.Created != 1 || result.Updated != 1 {
		t.Fatalf("wrong import count %+v", result)
	}
	again, err := s.ImportPeople(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if again.OperationID != result.OperationID {
		t.Fatal("same import key produced another operation")
	}
	req.CSV += "其他人,other-" + suffix + "@example.invalid,,\n"
	if _, err = s.ImportPeople(ctx, req); err == nil {
		t.Fatal("same key accepted different CSV")
	}
	var name string
	var primary int64
	if err = s.DB.QueryRowContext(ctx, `SELECT name FROM org.app_user WHERE id=$1`, old.EntityID).Scan(&name); err != nil {
		t.Fatal(err)
	}
	if name != "更新姓名" {
		t.Fatalf("update not applied: %q", name)
	}
	if err = s.DB.QueryRowContext(ctx, `SELECT owner_id FROM org.business_line_owner_history WHERE owner_id=$1 AND valid_to IS NULL AND voided_at IS NULL LIMIT 1`, old.EntityID).Scan(&primary); err != nil {
		t.Fatal(err)
	}
	if primary != old.EntityID {
		t.Fatal("import altered responsibility")
	}
	var audits int
	if err = s.DB.QueryRowContext(ctx, `SELECT count(*) FROM org.audit_log WHERE operation_id=$1`, result.OperationID).Scan(&audits); err != nil {
		t.Fatal(err)
	}
	if audits != 2 {
		t.Fatalf("expected 2 row audits, got %d", audits)
	}
}

func TestPeopleCSVErrorRowsDoNotWrite(t *testing.T) {
	s, ctx := testOrgStore(t)
	dep := testDepartment(t, s, ctx)
	suffix := uuid.NewString()
	validEmail := "valid-" + suffix + "@example.invalid"
	req := ImportRequest{CSV: "姓名,邮箱,部门,研发组\n正常," + validEmail + ",,\n错误,not-an-email,,\n重复," + validEmail + ",,\n", DepartmentID: &dep.EntityID, IdempotencyKey: uuid.NewString()}
	preview, err := s.PreviewPeopleImport(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if preview.Errors != 2 || preview.Created != 1 {
		t.Fatalf("unexpected preview %+v", preview)
	}
	if _, err = s.ImportPeople(ctx, req); err == nil {
		t.Fatal("invalid rows imported")
	}
	var count int
	if err = s.DB.QueryRowContext(ctx, `SELECT count(*) FROM org.app_user WHERE email_key=$1`, validEmail).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("valid row was partially imported despite errors")
	}
	if err = s.DB.QueryRowContext(ctx, `SELECT count(*) FROM org.change_operation WHERE idempotency_key=$1`, req.IdempotencyKey).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("failed import retained a successful operation")
	}
}

func TestPeopleCSVPreservesDisabledStateAndUpdatesMembership(t *testing.T) {
	s, ctx := testOrgStore(t)
	dep := testDepartment(t, s, ctx)
	dep2 := testDepartment(t, s, ctx)
	group := testChange(t, s, ctx, "dev_group", 0, 0, Group{Name: "原组-" + uuid.NewString(), DepartmentID: dep.EntityID, Active: true})
	email := "membership-" + uuid.NewString() + "@example.invalid"
	p := Person{Name: "调部门人员", Email: email, DepartmentID: &dep.EntityID, DevGroupID: &group.EntityID, Active: true}
	created := testChange(t, s, ctx, "person", 0, 0, p)
	var departmentName string
	if err := s.DB.QueryRowContext(ctx, `SELECT name FROM org.department WHERE id=$1`, dep2.EntityID).Scan(&departmentName); err != nil {
		t.Fatal(err)
	}
	request := ImportRequest{CSV: fmt.Sprintf("name,email,department,dev_group\n调部门人员,%s,%s,\n", email, departmentName), IdempotencyKey: uuid.NewString()}
	preview, err := s.PreviewPeopleImport(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if preview.Errors != 0 || preview.Rows[0].Data.DevGroupID != nil || !strings.Contains(preview.Rows[0].Warning, "移出原研发组") {
		t.Fatalf("bad membership preview %+v", preview)
	}
	if _, err = s.ImportPeople(ctx, request); err != nil {
		t.Fatal(err)
	}
	var groupID *int64
	var department int64
	if err = s.DB.QueryRowContext(ctx, `SELECT department_id,dev_group_id FROM org.app_user WHERE id=$1`, created.EntityID).Scan(&department, &groupID); err != nil {
		t.Fatal(err)
	}
	if department != dep2.EntityID || groupID != nil {
		t.Fatal("membership move not applied")
	}
	var revision int64
	if err = s.DB.QueryRowContext(ctx, `SELECT revision FROM org.app_user WHERE id=$1`, created.EntityID).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	c := Command{Kind: "person", Action: "disable", ID: created.EntityID, Revision: revision, Scope: "all_open", ConfirmImpact: true, IdempotencyKey: uuid.NewString()}
	if _, err = s.Change(ctx, c); err != nil {
		t.Fatal(err)
	}
	request.CSV = fmt.Sprintf("name,email\n仍停用,%s\n", email)
	request.IdempotencyKey = uuid.NewString()
	preview, err = s.PreviewPeopleImport(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if preview.Rows[0].Data.Active || !strings.Contains(preview.Rows[0].Warning, "保持停用") {
		t.Fatalf("disabled state lost %+v", preview)
	}
	if _, err = s.ImportPeople(ctx, request); err != nil {
		t.Fatal(err)
	}
	var active bool
	if err = s.DB.QueryRowContext(ctx, `SELECT active FROM org.app_user WHERE id=$1`, created.EntityID).Scan(&active); err != nil {
		t.Fatal(err)
	}
	if active {
		t.Fatal("import reactivated person")
	}
}

func testChange(t *testing.T, s *Store, ctx context.Context, kind string, id, revision int64, data any) ChangeResult {
	t.Helper()
	raw, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	c := Command{Kind: kind, Action: "save", ID: id, Revision: revision, Data: raw, Scope: "all_open", ConfirmImpact: true, IdempotencyKey: uuid.NewString()}
	result, err := s.Change(ctx, c)
	if err != nil {
		t.Fatalf("save %s: %v", kind, err)
	}
	return result
}
func testDepartment(t *testing.T, s *Store, ctx context.Context) ChangeResult {
	t.Helper()
	suffix := uuid.NewString()
	return testChange(t, s, ctx, "department", 0, 0, Department{Name: "测试部门-" + suffix, Code: "dep-" + suffix, Active: true, ChannelIDs: []int64{}})
}
func testPerson(t *testing.T, s *Store, ctx context.Context, dep int64, name, email string) ChangeResult {
	t.Helper()
	return testChange(t, s, ctx, "person", 0, 0, Person{Name: name, Email: email, DepartmentID: &dep, Active: true, Roles: []Role{}})
}

func TestPeopleCSVRejectsMalformedHeadersBeforeDirectoryReads(t *testing.T) {
	s := New(nil)
	for _, input := range []string{"", "name\n姓名\n", "email\nname@example.invalid\n", "name,email,email\n", "name,email,unknown\n"} {
		if _, err := s.PreviewPeopleImport(context.Background(), ImportRequest{CSV: input}); err == nil {
			t.Errorf("accepted invalid CSV header %q", input)
		}
	}
	if _, err := s.ImportPeople(context.Background(), ImportRequest{CSV: "name,email\n", IdempotencyKey: "not-a-uuid"}); err == nil {
		t.Fatal("invalid import key reached database")
	}
}

func TestPeopleImportCannotUseSingleObjectUndo(t *testing.T) {
	s, ctx := testOrgStore(t)
	request := ImportRequest{CSV: "name,email\n甲," + uuid.NewString() + "@example.invalid\n乙," + uuid.NewString() + "@example.invalid\n", IdempotencyKey: uuid.NewString()}
	imported, err := s.ImportPeople(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Undo(ctx, imported.OperationID, uuid.NewString()); err == nil {
		t.Fatal("batch import accepted partial single-object undo")
	}
	var state string
	if err = s.DB.QueryRowContext(ctx, `SELECT state FROM org.change_operation WHERE id=$1`, imported.OperationID).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != "succeeded" {
		t.Fatalf("rejected undo changed import state: %s", state)
	}
}
