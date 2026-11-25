package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOrganizationRoutesRequireAdministratorSession(t *testing.T) {
	h := (&Server{}).Handler()
	for _, route := range []struct{ method, path string }{
		{"GET", "/api/org/directory"},
		{"GET", "/api/org/findings/1/primary-asset"},
		{"PUT", "/api/org/findings/1/primary-asset"},
		{"GET", "/api/org/history?kind=person&id=1"},
		{"POST", "/api/org/change"},
		{"POST", "/api/org/people/import"},
		{"GET", "/api/org/rules"},
		{"POST", "/api/org/rules/save"},
		{"POST", "/api/org/rules/preview"},
		{"POST", "/api/org/rules/apply-preview"},
		{"POST", "/api/org/rules/apply"},
		{"GET", "/api/org/assets"},
		{"POST", "/api/org/assets/sync"},
		{"POST", "/api/org/assignments/preview"},
		{"POST", "/api/org/assignments/confirm"},
		{"GET", "/api/org/assignments/jobs"},
		{"GET", "/api/org/assignments/jobs/00000000-0000-0000-0000-000000000001"},
		{"POST", "/api/org/assignments/jobs/00000000-0000-0000-0000-000000000001/ack"},
		{"POST", "/api/org/assignments/00000000-0000-0000-0000-000000000001/undo"},
		{"POST", "/api/org/operations/00000000-0000-0000-0000-000000000001/undo"},
	} {
		r := httptest.NewRequest(route.method, route.path, strings.NewReader("{}"))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("%s %s: got %d, want unauthorized", route.method, route.path, w.Code)
		}
	}
}
