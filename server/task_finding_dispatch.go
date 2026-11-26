package server

import (
	"context"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Autumn-27/artex/db"
)

func (s *Server) taskFindingDispatch(w http.ResponseWriter, r *http.Request) {
	s.taskFindingMilestone(w, r, "dispatched")
}

func (s *Server) taskFindingDeveloperFixed(w http.ResponseWriter, r *http.Request) {
	s.taskFindingMilestone(w, r, "developer_fixed")
}

func (s *Server) taskFindingMilestone(w http.ResponseWriter, r *http.Request, milestone string) {
	action := "下发"
	if milestone == "developer_fixed" {
		action = "开发反馈修复"
	}
	pg := s.pg(w)
	if pg == nil {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeErr(w, http.StatusBadRequest, "无效的任务编号")
		return
	}
	var body struct {
		RecordedAt *time.Time `json:"recorded_at"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	if err := decode(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, "无效的请求，"+action+"时间需包含时区")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	var result any
	preview := strings.HasSuffix(r.URL.Path, "-preview")
	if milestone == "developer_fixed" && preview {
		result, err = pg.PreviewTaskFindingDeveloperFixed(ctx, id, body.RecordedAt)
	} else if milestone == "developer_fixed" {
		result, err = pg.RecordTaskFindingDeveloperFixed(ctx, id, body.RecordedAt)
	} else if preview {
		result, err = pg.PreviewTaskFindingDispatch(ctx, id, body.RecordedAt)
	} else {
		result, err = pg.DispatchTaskFindings(ctx, id, body.RecordedAt)
	}
	switch {
	case errors.Is(err, db.ErrTaskNotFound):
		writeErr(w, http.StatusNotFound, err.Error())
	case errors.Is(err, db.ErrTaskNotCompleted), errors.Is(err, db.ErrTaskDeveloperFixedNotCompleted):
		writeErr(w, http.StatusConflict, err.Error())
	case errors.Is(err, db.ErrInvalidFindingMilestoneTime):
		writeErr(w, http.StatusBadRequest, err.Error())
	case err != nil:
		log.Printf("[finding] 任务批量节点记录失败 task=%d milestone=%s: %v", id, milestone, err)
		writeErr(w, http.StatusInternalServerError, "任务漏洞"+action+"未完成，请重试")
	default:
		writeJSON(w, http.StatusOK, result)
	}
}
