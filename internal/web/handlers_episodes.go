package web

import (
	"strings"
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
	// Read-only — any user.
	r.Get("/episodes", s.handleEpisodesList)
	r.Get("/episodes/{id}", s.handleEpisodeRead)
	// Mutating — admin only.
	r.Group(func(r chi.Router) {
		r.Use(requireAdmin)
		r.Post("/episodes/{id}/fetch", s.handleEpisodeFetch)
		r.Post("/episodes/{id}/pdf", s.handleEpisodePDF)
	})
}

// loadEpisodePageForBook returns a single page of episodes for a book, plus
// the total number of episodes that match (used by paginators / lazy load).
//
// Pagination keeps book_detail fast even when a series has hundreds of
// chapters — the original code loaded every chapter on every page hit. The
// per-row image-count subqueries are replaced by a single GROUP BY scoped to
// the visible episode IDs, so the work is O(visible) instead of O(book).
//
// q is an optional substring filter against episode title.
func (s *Server) loadEpisodePageForBook(r *http.Request, bookID, q string, limit, offset int) (eps []episodeRow, total int, err error) {
	args := []any{bookID}
	whereExtra := ""
	if q != "" {
		whereExtra = " AND e.title LIKE ?"
		args = append(args, "%"+q+"%")
	}

	if err := s.DB.QueryRowContext(r.Context(),
		`SELECT COUNT(*) FROM episodes e WHERE e.book_id=?`+whereExtra,
		args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	if total == 0 {
		return nil, 0, nil
	}

	pageArgs := append(append([]any{}, args...), limit, offset)
	rows, err := s.DB.QueryContext(r.Context(), `
		SELECT e.id, e.title, e.pdf_path, e.book_id
		FROM episodes e WHERE e.book_id=?`+whereExtra+`
		ORDER BY e.id LIMIT ? OFFSET ?`, pageArgs...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var epIDs []any
	for rows.Next() {
		var e episodeRow
		if err := rows.Scan(&e.ID, &e.Title, &e.PDFPath, &e.BookID); err != nil {
			return nil, 0, err
		}
		eps = append(eps, e)
		epIDs = append(epIDs, e.ID)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}

	// One GROUP BY for the visible page only — covered by idx_images_episode.
	if len(epIDs) > 0 {
		placeholders := strings.Repeat(",?", len(epIDs))[1:]
		cRows, cErr := s.DB.QueryContext(r.Context(), `
			SELECT episode_id,
			       COUNT(*) AS total,
			       SUM(CASE WHEN file_path != '' THEN 1 ELSE 0 END) AS completed
			FROM images WHERE episode_id IN (`+placeholders+`)
			GROUP BY episode_id`, epIDs...)
		if cErr == nil {
			counts := make(map[int64][2]int, len(eps))
			for cRows.Next() {
				var epID int64
				var tot, comp int
				if err := cRows.Scan(&epID, &tot, &comp); err == nil {
					counts[epID] = [2]int{tot, comp}
				}
			}
			cRows.Close()
			for i := range eps {
				if c, ok := counts[eps[i].ID]; ok {
					eps[i].Total = c[0]
					eps[i].Completed = c[1]
				}
			}
		}
	}
	return eps, total, nil
}

func (s *Server) handleEpisodesList(w http.ResponseWriter, r *http.Request) {
	limit := 100
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	offset := (page - 1) * limit
	bookFilter := r.URL.Query().Get("book")

	// Step 1: fetch the page of episodes WITHOUT the per-row image-count
	// correlated subqueries (those scanned the 800K-row images table 100×
	// per page hit and were the main /episodes slowness).
	listSQL := `
		SELECT e.id, e.title, e.pdf_path, e.book_id
		FROM episodes e`
	countSQL := "SELECT COUNT(*) FROM episodes e"
	args := []any{}
	if bookFilter != "" {
		listSQL += " WHERE e.book_id=?"
		countSQL += " WHERE e.book_id=?"
		args = append(args, bookFilter)
	}
	listSQL += " ORDER BY e.id DESC LIMIT ? OFFSET ?"
	listArgs := append(append([]any{}, args...), limit, offset)

	rows, err := s.DB.QueryContext(r.Context(), listSQL, listArgs...)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()
	var eps []episodeRow
	var epIDs []any
	for rows.Next() {
		var e episodeRow
		if err := rows.Scan(&e.ID, &e.Title, &e.PDFPath, &e.BookID); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		eps = append(eps, e)
		epIDs = append(epIDs, e.ID)
	}

	// Step 2: a single grouped query gets total + completed counts for all
	// episodes on the page in one index seek. Uses idx_images_episode.
	if len(epIDs) > 0 {
		placeholders := strings.Repeat(",?", len(epIDs))[1:]
		countQ := `SELECT episode_id,
		                  COUNT(*) AS total,
		                  SUM(CASE WHEN file_path != '' THEN 1 ELSE 0 END) AS completed
		           FROM images WHERE episode_id IN (` + placeholders + `)
		           GROUP BY episode_id`
		cRows, err := s.DB.QueryContext(r.Context(), countQ, epIDs...)
		if err == nil {
			counts := make(map[int64][2]int)
			for cRows.Next() {
				var epID int64
				var tot, comp int
				if err := cRows.Scan(&epID, &tot, &comp); err == nil {
					counts[epID] = [2]int{tot, comp}
				}
			}
			cRows.Close()
			for i := range eps {
				if c, ok := counts[eps[i].ID]; ok {
					eps[i].Total = c[0]
					eps[i].Completed = c[1]
				}
			}
		}
	}

	var total int
	_ = s.DB.QueryRowContext(r.Context(), countSQL, args...).Scan(&total)

	s.renderPage(w, r, "episodes_list", map[string]any{
		"Title":      "Episodes",
		"Episodes":   eps,
		"Page":       page,
		"Limit":      limit,
		"Total":      total,
		"BookFilter": bookFilter,
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

	// Discover prev/next chapter ids within the same book.
	// Episodes are sorted by id ASC in the book detail view, so smaller id = prev.
	var prevID, nextID int64
	var prevTitle, nextTitle string
	_ = s.DB.QueryRowContext(r.Context(),
		`SELECT id, title FROM episodes WHERE book_id=? AND id < ? ORDER BY id DESC LIMIT 1`,
		bookID, id).Scan(&prevID, &prevTitle)
	_ = s.DB.QueryRowContext(r.Context(),
		`SELECT id, title FROM episodes WHERE book_id=? AND id > ? ORDER BY id ASC LIMIT 1`,
		bookID, id).Scan(&nextID, &nextTitle)

	s.renderPage(w, r, "episode_read", map[string]any{
		"Title":     title,
		"ID":        id,
		"BookID":    bookID,
		"Paths":     paths,
		"PrevID":    prevID,
		"PrevTitle": prevTitle,
		"NextID":    nextID,
		"NextTitle": nextTitle,
	})
}

func (s *Server) handleEpisodeFetch(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	_ = s.Queue.Enqueue(r.Context(), "find_images",
		"find_images:"+strconv.FormatInt(id, 10),
		map[string]any{"episode_id": id, "force": true})
	if r.Header.Get("HX-Request") == "true" {
		w.Write([]byte(`<span class="flash">↻ FETCH QUEUED</span>`))
		return
	}
	http.Redirect(w, r, safeReferer(r, "/episodes"), http.StatusFound)
}

func (s *Server) handleEpisodePDF(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	force := r.URL.Query().Get("force") == "1"
	_ = s.Queue.Enqueue(r.Context(), "convert_pdf",
		"convert_pdf:"+strconv.FormatInt(id, 10),
		map[string]any{"episode_id": id, "force": force})
	if r.Header.Get("HX-Request") == "true" {
		w.Write([]byte(`<span class="flash">▾ PDF QUEUED</span>`))
		return
	}
	http.Redirect(w, r, safeReferer(r, "/episodes"), http.StatusFound)
}
