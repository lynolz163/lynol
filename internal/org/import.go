package org

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
)

type ImportRequest struct {
	CSV            string `json:"csv"`
	DepartmentID   *int64 `json:"department_id"`
	IdempotencyKey string `json:"idempotency_key,omitempty"`
}

type ImportRow struct {
	Row     int    `json:"row"`
	Action  string `json:"action"`
	Data    Person `json:"data"`
	Error   string `json:"error,omitempty"`
	Warning string `json:"warning,omitempty"`
}

type ImportPreview struct {
	Rows    []ImportRow `json:"rows"`
	Created int         `json:"created"`
	Updated int         `json:"updated"`
	Errors  int         `json:"errors"`
}

type ImportResult struct {
	Created     int    `json:"created"`
	Updated     int    `json:"updated"`
	OperationID string `json:"operation_id"`
}

type importQuery interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

// PreviewPeopleImport only reads the directory. Confirmation repeats the same
// parsing against the transaction's directory before any person is written.
func (s *Store) PreviewPeopleImport(ctx context.Context, request ImportRequest) (ImportPreview, error) {
	return previewPeopleImport(ctx, s.DB, request)
}

func previewPeopleImport(ctx context.Context, q importQuery, request ImportRequest) (ImportPreview, error) {
	result := ImportPreview{Rows: []ImportRow{}}
	reader := csv.NewReader(strings.NewReader(strings.TrimPrefix(request.CSV, "\ufeff")))
	reader.TrimLeadingSpace = true
	reader.FieldsPerRecord = -1
	header, err := reader.Read()
	if err != nil {
		return result, &APIError{Status: 400, Message: "CSV 文件为空或格式不正确，请下载模板后填写"}
	}
	columns := map[string]int{}
	aliases := map[string]string{"姓名": "name", "邮箱": "email", "部门": "department", "研发组": "dev_group"}
	for i, key := range header {
		key = strings.ToLower(strings.TrimSpace(key))
		if alias, ok := aliases[key]; ok {
			key = alias
		}
		if key != "name" && key != "email" && key != "department" && key != "dev_group" {
			return result, &APIError{Status: 400, Message: fmt.Sprintf("不支持的 CSV 列 %q，请使用姓名、邮箱、部门、研发组模板", header[i])}
		}
		if _, exists := columns[key]; exists {
			return result, &APIError{Status: 400, Message: "CSV 表头不能重复"}
		}
		columns[key] = i
	}
	if _, ok := columns["name"]; !ok {
		return result, &APIError{Status: 400, Message: "CSV 缺少 name（姓名）列"}
	}
	if _, ok := columns["email"]; !ok {
		return result, &APIError{Status: 400, Message: "CSV 缺少 email（邮箱）列"}
	}

	departments := map[string]int64{}
	activeDepartments := map[int64]bool{}
	rows, err := q.QueryContext(ctx, `SELECT id,name,active FROM org.department`)
	if err != nil {
		return result, err
	}
	for rows.Next() {
		var id int64
		var name string
		var active bool
		if err = rows.Scan(&id, &name, &active); err != nil {
			rows.Close()
			return result, err
		}
		if active {
			departments[strings.ToLower(strings.TrimSpace(name))] = id
			activeDepartments[id] = true
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return result, err
	}
	if request.DepartmentID != nil && !activeDepartments[*request.DepartmentID] {
		return result, &APIError{Status: 400, Message: "默认部门不存在或已停用，请重新选择", Fields: map[string]string{"department_id": "请选择已启用部门"}}
	}
	type groupKey struct {
		department int64
		name       string
	}
	groups := map[groupKey]int64{}
	rows, err = q.QueryContext(ctx, `SELECT id,department_id,name FROM org.dev_group WHERE active`)
	if err != nil {
		return result, err
	}
	for rows.Next() {
		var id, department int64
		var name string
		if err = rows.Scan(&id, &department, &name); err != nil {
			rows.Close()
			return result, err
		}
		groups[groupKey{department, strings.ToLower(strings.TrimSpace(name))}] = id
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return result, err
	}
	people := map[string]Person{}
	rows, err = q.QueryContext(ctx, `SELECT id,name,email,department_id,dev_group_id,active,revision FROM org.app_user`)
	if err != nil {
		return result, err
	}
	for rows.Next() {
		var p Person
		p.Roles = []Role{}
		if err = rows.Scan(&p.ID, &p.Name, &p.Email, &p.DepartmentID, &p.DevGroupID, &p.Active, &p.Revision); err != nil {
			rows.Close()
			return result, err
		}
		people[strings.ToLower(strings.TrimSpace(p.Email))] = p
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return result, err
	}
	responsibility := map[int64][]string{}
	rows, err = q.QueryContext(ctx, `SELECT DISTINCT owner_id,label FROM (
  SELECT h.owner_id, '业务线主负责人：'||b.name label FROM org.business_line_owner_history h JOIN org.business_line b ON b.id=h.business_line_id WHERE h.valid_to IS NULL AND h.voided_at IS NULL AND h.owner_id IS NOT NULL
  UNION ALL SELECT h.backup_id,'业务线备选负责人：'||b.name FROM org.business_line_owner_history h JOIN org.business_line b ON b.id=h.business_line_id WHERE h.valid_to IS NULL AND h.voided_at IS NULL AND h.backup_id IS NOT NULL
  UNION ALL SELECT h.head_id,'部门负责人：'||d.name FROM org.department_head_history h JOIN org.department d ON d.id=h.department_id WHERE h.valid_to IS NULL AND h.voided_at IS NULL AND h.head_id IS NOT NULL
  UNION ALL SELECT owner_id,'全局兜底处理人' FROM org.fallback_owner_history WHERE valid_to IS NULL AND voided_at IS NULL AND owner_id IS NOT NULL
 ) r ORDER BY owner_id,label`)
	if err != nil {
		return result, err
	}
	for rows.Next() {
		var id int64
		var label string
		if err = rows.Scan(&id, &label); err != nil {
			rows.Close()
			return result, err
		}
		responsibility[id] = append(responsibility[id], label)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return result, err
	}

	seen := map[string]int{}
	for line := 2; ; line++ {
		record, readErr := reader.Read()
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return result, &APIError{Status: 400, Message: fmt.Sprintf("CSV 第 %d 行解析失败，请检查引号和分隔符", line)}
		}
		row := ImportRow{Row: line, Action: "create", Data: Person{Active: true, Roles: []Role{}}}
		problems := []string{}
		if len(record) != len(header) {
			problems = append(problems, "列数与表头不一致，请按模板填写")
		}
		value := func(key string) string {
			if i, ok := columns[key]; ok && i < len(record) {
				return strings.TrimSpace(record[i])
			}
			return ""
		}
		email := strings.ToLower(value("email"))
		if existing, ok := people[email]; ok {
			row.Data = existing
			row.Action = "update"
		}
		row.Data.Name = value("name")
		row.Data.Email = email
		if previous, ok := seen[email]; ok {
			problems = append(problems, fmt.Sprintf("邮箱与第 %d 行重复，请合并为一行", previous))
		} else {
			seen[email] = line
		}
		oldDepartment := row.Data.DepartmentID
		department := value("department")
		switch {
		case department != "":
			if id, ok := departments[strings.ToLower(department)]; ok {
				row.Data.DepartmentID = &id
			} else {
				problems = append(problems, "部门不存在或已停用，请先创建部门或更正名称")
			}
		case request.DepartmentID != nil:
			row.Data.DepartmentID = request.DepartmentID
		}
		group := value("dev_group")
		if group != "" {
			if row.Data.DepartmentID == nil {
				problems = append(problems, "填写研发组时必须选择部门")
			} else if id, ok := groups[groupKey{*row.Data.DepartmentID, strings.ToLower(group)}]; ok {
				row.Data.DevGroupID = &id
			} else {
				problems = append(problems, "该部门下没有此启用的研发组，请更正名称或先创建")
			}
		} else if (oldDepartment == nil) != (row.Data.DepartmentID == nil) || (oldDepartment != nil && row.Data.DepartmentID != nil && *oldDepartment != *row.Data.DepartmentID) {
			row.Data.DevGroupID = nil
			if row.Action == "update" {
				row.Warning = "将调整部门并移出原研发组；"
			}
		}
		if row.Data.DepartmentID != nil && !activeDepartments[*row.Data.DepartmentID] {
			problems = append(problems, "原部门已停用，请指定一个已启用部门")
		}
		if row.Action == "update" {
			if !row.Data.Active {
				row.Warning += "该人员已停用，导入后仍保持停用；"
			}
			if roles := responsibility[row.Data.ID]; len(roles) > 0 {
				row.Warning += "现有责任关系保留（" + strings.Join(roles, "；") + "）；"
			}
		}
		row.Warning = strings.TrimSuffix(row.Warning, "；")
		data, _ := json.Marshal(row.Data)
		command := Command{Kind: "person", Action: "save", ID: row.Data.ID, Revision: row.Data.Revision, Data: data, Scope: "all_open", ConfirmImpact: true, IdempotencyKey: "00000000-0000-4000-8000-000000000001"}
		if err := ValidateCommand(&command); err != nil {
			problems = append(problems, err.Error())
		}
		if len(problems) > 0 {
			row.Action = "error"
			row.Error = strings.Join(problems, "；")
			result.Errors++
		} else if row.Action == "create" {
			result.Created++
		} else {
			result.Updated++
		}
		result.Rows = append(result.Rows, row)
	}
	if len(result.Rows) == 0 {
		return result, &APIError{Status: 400, Message: "CSV 只有表头，请至少填写一行人员"}
	}
	return result, nil
}

func (s *Store) ImportPeople(ctx context.Context, request ImportRequest) (ImportResult, error) {
	var result ImportResult
	if _, err := uuid.Parse(request.IdempotencyKey); err != nil {
		return result, &APIError{Status: 400, Message: "导入请求编号无效，请重新预览"}
	}
	body, _ := json.Marshal(request)
	digest := sha256.Sum256(append([]byte("people_import\n"), body...))
	hash := hex.EncodeToString(digest[:])
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	var revision int64
	if err = tx.QueryRowContext(ctx, `SELECT config_revision FROM org.sync_state WHERE singleton FOR UPDATE`).Scan(&revision); err != nil {
		return result, err
	}
	var oldHash string
	var oldResult []byte
	err = tx.QueryRowContext(ctx, `SELECT request_hash,result FROM org.change_operation WHERE idempotency_key=$1`, request.IdempotencyKey).Scan(&oldHash, &oldResult)
	if err == nil {
		if oldHash != hash {
			return result, &APIError{Status: 409, Message: "同一导入编号对应的内容已变化，请重新预览"}
		}
		if err = json.Unmarshal(oldResult, &result); err != nil {
			return result, err
		}
		return result, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return result, err
	}
	preview, err := previewPeopleImport(ctx, tx, request)
	if err != nil {
		return result, err
	}
	if preview.Errors > 0 {
		return result, &APIError{Status: 400, Message: fmt.Sprintf("还有 %d 行错误，请修正并重新预览，尚未导入任何人员", preview.Errors), Code: "import_rows_invalid"}
	}
	var at time.Time
	if err = tx.QueryRowContext(ctx, `SELECT clock_timestamp()`).Scan(&at); err != nil {
		return result, err
	}
	result = ImportResult{Created: preview.Created, Updated: preview.Updated, OperationID: uuid.NewString()}
	if _, err = tx.ExecContext(ctx, `INSERT INTO org.change_operation(id,idempotency_key,request_hash,kind,actor_type,actor_id,actor_label,state,effective_at) VALUES($1,$2,$3,'people_import','admin','ARTEX','ARTEX 管理员','running',$4)`, result.OperationID, request.IdempotencyKey, hash, at); err != nil {
		return result, err
	}
	for _, row := range preview.Rows {
		data, _ := json.Marshal(row.Data)
		c := Command{Kind: "person", Action: "save", ID: row.Data.ID, Revision: row.Data.Revision, Data: data, Scope: "all_open", ConfirmImpact: true}
		if _, err = s.changeTx(ctx, tx, c, result.OperationID, at); err != nil {
			return ImportResult{}, err
		}
	}
	encoded, _ := json.Marshal(result)
	if _, err = tx.ExecContext(ctx, `UPDATE org.change_operation SET state='succeeded',finished_at=clock_timestamp(),result=$2 WHERE id=$1`, result.OperationID, encoded); err != nil {
		return ImportResult{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE org.sync_state SET config_revision=config_revision+1 WHERE singleton`); err != nil {
		return ImportResult{}, err
	}
	if err = tx.Commit(); err != nil {
		return ImportResult{}, err
	}
	return result, nil
}

func (s *Store) importPreview(w http.ResponseWriter, r *http.Request) {
	var request ImportRequest
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if !decode(w, r, &request) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	result, err := s.PreviewPeopleImport(ctx, request)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, 200, result)
}
func (s *Store) importPeople(w http.ResponseWriter, r *http.Request) {
	var request ImportRequest
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if !decode(w, r, &request) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Minute)
	defer cancel()
	result, err := s.ImportPeople(ctx, request)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, 200, result)
}
