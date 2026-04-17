package web

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/s045pd/se8/internal/jobs"
)

func (s *Server) mountAdminRoutes(r chi.Router) {
	r.Post("/admin/start-crawl", s.handleStartCrawl)
}

func (s *Server) handleStartCrawl(w http.ResponseWriter, r *http.Request) {
	if err := s.Queue.Enqueue(r.Context(), jobs.KindFindBooks, "find_books:manual", nil); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/jobs", http.StatusFound)
}
