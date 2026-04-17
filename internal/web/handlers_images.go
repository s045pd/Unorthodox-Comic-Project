package web

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
)

type imgRow struct {
	ID, EpisodeID int64
	Index, Bytes  int
	HasFile       bool
}

func (s *Server) mountImageRoutes(r chi.Router) {
	r.Get("/images", s.handleImagesList)
	r.Post("/images/{id}/redownload", s.handleImageRedownload)
}

func (s *Server) handleImagesList(w http.ResponseWriter, r *http.Request) {
	epFilter := r.URL.Query().Get("episode")
	q := `SELECT id, episode_id, idx, bytes, (file_path != '') FROM images`
	args := []any{}
	if epFilter != "" {
		q += " WHERE episode_id=?"
		args = append(args, epFilter)
	}
	q += " ORDER BY episode_id DESC, idx LIMIT 500"
	rows, err := s.DB.QueryContext(r.Context(), q, args...)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()
	var imgs []imgRow
	for rows.Next() {
		var im imgRow
		if err := rows.Scan(&im.ID, &im.EpisodeID, &im.Index, &im.Bytes, &im.HasFile); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		imgs = append(imgs, im)
	}
	s.renderTemplate(w, "images_list", map[string]any{
		"Title":  "Images",
		"Images": imgs,
	})
}

func (s *Server) handleImageRedownload(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	_ = s.Queue.Enqueue(r.Context(), "download_image",
		"download_image:"+strconv.FormatInt(id, 10),
		map[string]any{"image_id": id})
	if r.Header.Get("HX-Request") == "true" {
		w.Write([]byte(`<span>Queued ✓</span>`))
		return
	}
	http.Redirect(w, r, safeReferer(r, "/images"), http.StatusFound)
}
