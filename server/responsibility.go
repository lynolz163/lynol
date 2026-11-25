package server

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"time"

	"github.com/Autumn-27/artex/db"
)

func (s *Server) responsibilityDirectory(w http.ResponseWriter, r *http.Request) {
	pg := s.pg(w)
	if pg == nil {
		return
	}
	directory, err := pg.ResponsibilityDirectory(r.Context())
	if err != nil {
		log.Printf("[responsibility] %v", err)
		writeErr(w, 500, "读取业务线与人员配置失败")
		return
	}
	writeJSON(w, 200, directory)
}

func (s *Server) saveResponsibility(w http.ResponseWriter, r *http.Request) {
	pg := s.pg(w)
	if pg == nil {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Minute)
	defer cancel()
	r.Body = http.MaxBytesReader(w, r.Body, 65536)
	var err error
	var result any
	switch r.PathValue("kind") {
	case "business-lines":
		var line db.BusinessLine
		if json.NewDecoder(r.Body).Decode(&line) != nil {
			writeErr(w, 400, "业务线配置格式错误")
			return
		}
		err = pg.SaveBusinessLine(ctx, &line)
		result = line
	case "parties":
		var party db.ResponsibilityParty
		if json.NewDecoder(r.Body).Decode(&party) != nil {
			writeErr(w, 400, "人员或团队配置格式错误")
			return
		}
		var transferred int64
		transferred, err = pg.SaveResponsibilityParty(ctx, &party)
		result = map[string]any{"party": party, "transferred": transferred}
	case "fallback":
		var body struct {
			ID int64 `json:"id"`
		}
		if json.NewDecoder(r.Body).Decode(&body) != nil {
			writeErr(w, 400, "兜底负责人格式错误")
			return
		}
		err = pg.SetResponsibilityFallback(ctx, body.ID)
		result = body
	case "apply-rules":
		result, err = pg.ApplyResponsibilityRules(ctx)
	default:
		writeErr(w, 404, "配置不存在")
		return
	}
	if err != nil {
		if errors.Is(err, db.ErrResponsibilityInput) {
			writeErr(w, 400, err.Error())
		} else {
			log.Printf("[responsibility] save: %v", err)
			writeErr(w, 500, "保存失败，请检查配置后重试")
		}
		return
	}
	metricsErr := pg.RefreshFindingMetrics(ctx)
	if metricsErr != nil {
		log.Printf("[responsibility] refresh: %v", metricsErr)
	}
	writeJSON(w, 200, map[string]any{"result": result, "metrics_updated": metricsErr == nil})
}
