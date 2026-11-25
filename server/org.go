package server

import (
	"context"
	"net/http"

	"github.com/Autumn-27/artex/internal/org"
)

func (s *Server) startOrganizationWorker() {
	if s.m == nil || s.m.pg == nil {
		return
	}
	s.orgWorkerDone = make(chan struct{})
	go func() {
		defer close(s.orgWorkerDone)
		org.New(s.m.pg.DB).RunAssignmentWorker(s.ctx)
	}()
}

// WaitOrganizationWorker lets a canceled assignment finish rolling back before
// the shared database closes. Committed jobs remain available after a restart.
func (s *Server) WaitOrganizationWorker(ctx context.Context) error {
	if s.orgWorkerDone == nil {
		return nil
	}
	select {
	case <-s.orgWorkerDone:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Organization routes share the application's existing administrator session.
func (s *Server) organization(w http.ResponseWriter, r *http.Request) {
	pg := s.pg(w)
	if pg == nil {
		return
	}
	org.Handler(pg.DB).ServeHTTP(w, r)
}
