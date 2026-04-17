package web

import (
	"net/http"
	"path/filepath"
	"strings"

	"github.com/go-chi/chi/v5"
)

func (s *Server) mountMediaRoutes(r chi.Router) {
	r.Get("/media/*", s.handleMedia)
}

// handleMedia serves files under vol/media/ with a path-traversal guard.
func (s *Server) handleMedia(w http.ResponseWriter, r *http.Request) {
	rel := strings.TrimPrefix(r.URL.Path, "/media/")
	if strings.Contains(rel, "..") {
		http.Error(w, "bad path", http.StatusBadRequest)
		return
	}
	full := filepath.Join(s.VolDir, "media", rel)
	abs, err := filepath.Abs(full)
	if err != nil {
		http.Error(w, "bad path", http.StatusBadRequest)
		return
	}
	mediaRoot, _ := filepath.Abs(filepath.Join(s.VolDir, "media"))
	if !strings.HasPrefix(abs, mediaRoot+string(filepath.Separator)) && abs != mediaRoot {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	http.ServeFile(w, r, abs)
}
