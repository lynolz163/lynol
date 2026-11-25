package org

import (
	"database/sql"
	"net/http"

	"github.com/google/uuid"
)

func (s *Store) assignmentPreviewHTTP(w http.ResponseWriter, r *http.Request) {
	var b AssignmentRequest
	if !decode(w, r, &b) {
		return
	}
	v, e := s.PreviewAssignment(r.Context(), b)
	if e != nil {
		writeError(w, e)
		return
	}
	writeJSON(w, 200, v)
}
func (s *Store) assignmentConfirmHTTP(w http.ResponseWriter, r *http.Request) {
	var b AssignmentConfirm
	if !decode(w, r, &b) {
		return
	}
	v, e := s.ConfirmAssignment(r.Context(), b)
	if e != nil {
		writeError(w, e)
		return
	}
	status := 200
	if v.State == "queued" {
		status = 202
	}
	writeJSON(w, status, v)
}
func (s *Store) assignmentUndoHTTP(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Key string `json:"idempotency_key"`
	}
	if !decode(w, r, &b) {
		return
	}
	v, e := s.UndoAssignment(r.Context(), r.PathValue("id"), b.Key)
	if e != nil {
		writeError(w, e)
		return
	}
	writeJSON(w, 200, v)
}
func (s *Store) assignmentJobsHTTP(w http.ResponseWriter, r *http.Request) {
	v, e := s.AssignmentJobs(r.Context())
	if e != nil {
		writeError(w, e)
		return
	}
	writeJSON(w, 200, map[string]any{"items": v})
}
func (s *Store) assignmentJobHTTP(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, e := uuid.Parse(id); e != nil {
		writeError(w, bad("id", "任务编号不正确"))
		return
	}
	if r.Method == http.MethodPost {
		if e := s.AcknowledgeAssignment(r.Context(), id); e != nil {
			writeError(w, e)
			return
		}
		writeJSON(w, 200, map[string]bool{"acknowledged": true})
		return
	}
	v, e := s.AssignmentJob(r.Context(), id)
	if e == sql.ErrNoRows {
		writeError(w, &APIError{Status: 404, Message: "任务不存在"})
		return
	}
	if e != nil {
		writeError(w, e)
		return
	}
	writeJSON(w, 200, v)
}
