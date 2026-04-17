package web

import (
	"net/http"
	"net/url"

	"github.com/go-chi/chi/v5"

	"github.com/s045pd/se8/internal/jobs"
)

// safeReferer returns r.Referer() if it is a same-origin absolute URL or a
// relative path. Otherwise it returns fallback.
func safeReferer(r *http.Request, fallback string) string {
	ref := r.Referer()
	if ref == "" {
		return fallback
	}
	u, err := url.Parse(ref)
	if err != nil {
		return fallback
	}
	if u.Host != "" && u.Host != r.Host {
		return fallback
	}
	return ref
}

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
