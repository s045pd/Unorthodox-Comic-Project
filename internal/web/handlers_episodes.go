package web

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
)

type episodeRow struct {
	ID        int64
	Title     string
	PDFPath   string
	Total     int
	Completed int
	BookID    string
}

func (s *Server) mountEpisodeRoutes(r chi.Router) {
	r.Get("/episodes", s.handleEpisodesList)
	r.Get("/episodes/{id}", s.handleEpisodeRead)
	r.Post("/episodes/{id}/fetch", s.handleEpisodeFetch)
	r.Post("/episodes/{id}/pdf", s.handleEpisodePDF)
}

func (s *Server) loadEpisodeRowsForBook(r *http.Request, bookID string) ([]episodeRow, error) {
	rows, err := s.DB.QueryContext(r.Context(), `
		SELECT e.id, e.title, e.pdf_path,
		       (SELECT COUNT(*) FROM images i WHERE i.episode_id=e.id),
		       (SELECT COUNT(*) FROM images i WHERE i.episode_id=e.id AND i.file_path != ''),
		       e.book_id
		FROM episodes e WHERE e.book_id=? ORDER BY e.id`, bookID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []episodeRow
	for rows.Next() {
		var e episodeRow
		if err := rows.Scan(&e.ID, &e.Title, &e.PDFPath, &e.Total, &e.Completed, &e.BookID); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, nil
}

func (s *Server) handleEpisodesList(w http.ResponseWriter, r *http.Request) {
	limit := 100
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	offset := (page - 1) * limit
	bookFilter := r.URL.Query().Get("book")

	q := `
		SELECT e.id, e.title, e.pdf_path,
		       (SELECT COUNT(*) FROM images i WHERE i.episode_id=e.id),
		       (SELECT COUNT(*) FROM images i WHERE i.episode_id=e.id AND i.file_path != ''),
		       e.book_id
		FROM episodes e`
	args := []any{}
	if bookFilter != "" {
		q += " WHERE e.book_id=?"
		args = append(args, bookFilter)
	}
	q += " ORDER BY e.id DESC LIMIT ? OFFSET ?"
	args = append(args, limit, offset)

	rows, err := s.DB.QueryContext(r.Context(), q, args...)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()
	var eps []episodeRow
	for rows.Next() {
		var e episodeRow
		if err := rows.Scan(&e.ID, &e.Title, &e.PDFPath, &e.Total, &e.Completed, &e.BookID); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		eps = append(eps, e)
	}

	s.renderTemplate(w, "episodes_list", map[string]any{
		"Title":    "Episodes",
		"Episodes": eps,
		"Page":     page,
	})
}

func (s *Server) handleEpisodeRead(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	var title, bookID string
	if err := s.DB.QueryRowContext(r.Context(),
		`SELECT title, book_id FROM episodes WHERE id=?`, id).
		Scan(&title, &bookID); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	rows, err := s.DB.QueryContext(r.Context(),
		`SELECT file_path FROM images WHERE episode_id=? ORDER BY idx`, id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()
	var paths []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		paths = append(paths, p)
	}
	s.renderTemplate(w, "episode_read", map[string]any{
		"Title":  title,
		"ID":     id,
		"BookID": bookID,
		"Paths":  paths,
	})
}

func (s *Server) handleEpisodeFetch(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	_ = s.Queue.Enqueue(r.Context(), "find_images",
		"find_images:"+strconv.FormatInt(id, 10),
		map[string]any{"episode_id": id, "force": true})
	if r.Header.Get("HX-Request") == "true" {
		w.Write([]byte(`<span>Fetch queued ✓</span>`))
		return
	}
	http.Redirect(w, r, r.Referer(), http.StatusFound)
}

func (s *Server) handleEpisodePDF(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	force := r.URL.Query().Get("force") == "1"
	_ = s.Queue.Enqueue(r.Context(), "convert_pdf",
		"convert_pdf:"+strconv.FormatInt(id, 10),
		map[string]any{"episode_id": id, "force": force})
	if r.Header.Get("HX-Request") == "true" {
		w.Write([]byte(`<span>PDF queued ✓</span>`))
		return
	}
	http.Redirect(w, r, r.Referer(), http.StatusFound)
}
