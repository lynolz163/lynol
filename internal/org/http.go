package org

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

func Handler(db *sql.DB) http.Handler {
	return organizationHandler(New(db))
}

// The local acceptance harness injects a sender; production uses the adapters.
func HandlerWithNotificationSender(db *sql.DB, sender NotificationSender) http.Handler {
	s := New(db)
	s.NotificationSender = sender
	return organizationHandler(s)
}

func organizationHandler(s *Store) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/org/notification-channels", s.notificationChannels)
	mux.HandleFunc("POST /api/org/notification-channels/test", s.testNotificationChannel)
	mux.HandleFunc("GET /api/org/directory", func(w http.ResponseWriter, r *http.Request) {
		v, e := s.Directory(r.Context())
		if e != nil {
			writeError(w, e)
			return
		}
		writeJSON(w, 200, v)
	})
	mux.HandleFunc("POST /api/org/change", func(w http.ResponseWriter, r *http.Request) {
		var c Command
		if !decode(w, r, &c) {
			return
		}
		v, e := s.Change(r.Context(), c)
		if e != nil {
			writeError(w, e)
			return
		}
		writeJSON(w, 200, v)
	})
	mux.HandleFunc("POST /api/org/impact", func(w http.ResponseWriter, r *http.Request) {
		var c Command
		if !decode(w, r, &c) {
			return
		}
		v, e := s.Impact(r.Context(), c)
		if e != nil {
			writeError(w, e)
			return
		}
		writeJSON(w, 200, v)
	})
	mux.HandleFunc("GET /api/org/check", s.check)
	mux.HandleFunc("GET /api/org/history", s.history)
	mux.HandleFunc("POST /api/org/operations/{id}/undo", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Key string `json:"idempotency_key"`
		}
		if !decode(w, r, &b) {
			return
		}
		v, e := s.Undo(r.Context(), r.PathValue("id"), b.Key)
		if e != nil {
			writeError(w, e)
			return
		}
		writeJSON(w, 200, v)
	})
	mux.HandleFunc("GET /api/org/findings/{id}/primary-asset", s.findingAssetHTTP)
	mux.HandleFunc("PUT /api/org/findings/{id}/primary-asset", s.findingAssetHTTP)
	mux.HandleFunc("POST /api/org/assignments/preview", s.assignmentPreviewHTTP)
	mux.HandleFunc("POST /api/org/assignments/confirm", s.assignmentConfirmHTTP)
	mux.HandleFunc("POST /api/org/assignments/{id}/undo", s.assignmentUndoHTTP)
	mux.HandleFunc("GET /api/org/assignments/jobs", s.assignmentJobsHTTP)
	mux.HandleFunc("GET /api/org/assignments/jobs/{id}", s.assignmentJobHTTP)
	mux.HandleFunc("POST /api/org/assignments/jobs/{id}/ack", s.assignmentJobHTTP)
	mux.HandleFunc("GET /api/org/rules", s.rulesList)
	mux.HandleFunc("POST /api/org/rules/save", s.ruleSave)
	mux.HandleFunc("POST /api/org/rules/preview", s.rulePreview)
	mux.HandleFunc("POST /api/org/rules/apply-preview", s.ruleApply)
	mux.HandleFunc("POST /api/org/rules/apply", s.ruleApply)
	mux.HandleFunc("GET /api/org/assets", s.ruleAssetList)
	mux.HandleFunc("POST /api/org/assets/sync", s.assetSync)
	mux.HandleFunc("POST /api/org/people/import-preview", s.importPreview)
	mux.HandleFunc("POST /api/org/people/import", s.importPeople)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		mux.ServeHTTP(w, r.WithContext(ctx))
	})
}
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	d := json.NewDecoder(r.Body)
	if err := d.Decode(v); err != nil {
		writeError(w, bad("data", "请求内容不正确或超过 1MB"))
		return false
	}
	return true
}
func writeError(w http.ResponseWriter, err error) {
	var a *APIError
	if errors.As(err, &a) {
		writeJSON(w, a.Status, map[string]any{"error": a.Message, "fields": a.Fields, "code": a.Code})
		return
	}
	var p *pgconn.PgError
	if errors.As(err, &p) {
		switch p.Code {
		case "23505":
			field := "name"
			msg := "名称已存在（包含已停用对象），请修改名称"
			if strings.Contains(p.ConstraintName, "email") {
				field = "email"
				msg = "该邮箱已存在，请使用其他邮箱或编辑已有人员"
			} else if strings.Contains(p.ConstraintName, "code") {
				field = "code"
				msg = "编码已存在，请修改编码"
			}
			writeError(w, bad(field, msg))
			return
		case "23503":
			writeError(w, bad("data", "关联对象已发生变化，请刷新后重新选择"))
			return
		case "23P01":
			writeError(w, conflict("生效区间已发生变化，请刷新后重试"))
			return
		}
	}
	log.Printf("[org] %v", err)
	writeJSON(w, 500, map[string]string{"error": "保存或读取失败，请稍后重试"})
}
func (s *Store) check(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	table, err := tableFor(q.Get("kind"))
	if err != nil {
		writeError(w, bad("kind", "类型不存在"))
		return
	}
	id, _ := strconv.ParseInt(q.Get("id"), 10, 64)
	dep, _ := strconv.ParseInt(q.Get("department_id"), 10, 64)
	fields := map[string]string{}
	name := strings.TrimSpace(q.Get("name"))
	email := strings.TrimSpace(q.Get("email"))
	var found bool
	if name != "" && table != "app_user" {
		sqlText := "SELECT EXISTS(SELECT 1 FROM org." + table + " WHERE id<>$1 AND name_key=lower(btrim($2))"
		args := []any{id, name}
		if table == "business_line" || table == "dev_group" {
			sqlText += " AND department_id=$3"
			args = append(args, dep)
		}
		sqlText += ")"
		if err = s.DB.QueryRowContext(r.Context(), sqlText, args...).Scan(&found); err != nil {
			writeError(w, err)
			return
		}
		if found {
			fields["name"] = "同级名称已存在（包含已停用对象），请修改名称"
		}
	}
	if email != "" && table == "app_user" {
		if err = s.DB.QueryRowContext(r.Context(), "SELECT EXISTS(SELECT 1 FROM org.app_user WHERE id<>$1 AND email_key=lower(btrim($2)))", id, email).Scan(&found); err != nil {
			writeError(w, err)
			return
		}
		if found {
			fields["email"] = "该邮箱已存在，请编辑已有人员或使用其他邮箱"
		}
	}
	writeJSON(w, 200, map[string]any{"errors": fields})
}
func (s *Store) history(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if _, err := tableFor(q.Get("kind")); err != nil && q.Get("kind") != "fallback" && q.Get("kind") != "asset" && q.Get("kind") != "rule" {
		writeError(w, bad("kind", "类型不存在"))
		return
	}
	var raw []byte
	err := s.DB.QueryRowContext(r.Context(), `SELECT COALESCE(jsonb_agg(to_jsonb(a) ORDER BY id DESC),'[]') FROM (SELECT id,actor_label,action,effective_at,recorded_at,old_value,new_value FROM org.audit_log WHERE object_type=$1 AND object_id=$2 ORDER BY id DESC LIMIT 200) a`, q.Get("kind"), q.Get("id")).Scan(&raw)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"items": json.RawMessage(raw)})
}
