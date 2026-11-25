package server

import (
	"log"
	"net/http"
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
	if s.pg(w) == nil {
		return
	}
	writeJSON(w, http.StatusConflict, map[string]any{
		"error":       "归属配置已迁移，请在部门与业务线页面修改。漏洞归属沿用其主资产。",
		"redirect_to": "/settings/org",
	})
}
