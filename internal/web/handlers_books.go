package web

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
)

func (s *Server) mountBookRoutes(r chi.Router) {
	r.Get("/books", s.handleBooksList)
	r.Get("/books/{id}", s.handleBookDetail)
	r.Post("/books/{id}/crawl", s.handleBookCrawl)
}

type bookRow struct {
	ID           string
	Title        string
	CoverPath    string
	Hot          int
	EpisodeCount int
}

func (s *Server) handleBooksList(w http.ResponseWriter, r *http.Request) {
	limit := 50
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	offset := (page - 1) * limit

	rows, err := s.DB.QueryContext(r.Context(), `
		SELECT b.id, b.title, b.cover_path, b.hot,
		       (SELECT COUNT(*) FROM episodes e WHERE e.book_id=b.id)
		FROM books b
		ORDER BY b.title
		LIMIT ? OFFSET ?`, limit, offset)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()
	var books []bookRow
	for rows.Next() {
		var b bookRow
		if err := rows.Scan(&b.ID, &b.Title, &b.CoverPath, &b.Hot, &b.EpisodeCount); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		books = append(books, b)
	}

	var total int
	_ = s.DB.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM books`).Scan(&total)

	s.renderTemplate(w, "books_list", map[string]any{
		"Title": "Books",
		"Books": books,
		"Page":  page,
		"Total": total,
		"Limit": limit,
	})
}

func (s *Server) handleBookDetail(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var b struct {
		ID, Title, Description, CoverPath string
		Hot                               int
	}
	err := s.DB.QueryRowContext(r.Context(),
		`SELECT id, title, description, cover_path, hot FROM books WHERE id=?`, id).
		Scan(&b.ID, &b.Title, &b.Description, &b.CoverPath, &b.Hot)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	eps, err := s.loadEpisodeRowsForBook(r, id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	s.renderTemplate(w, "book_detail", map[string]any{
		"Title":    b.Title,
		"Book":     b,
		"Episodes": eps,
	})
}

func (s *Server) handleBookCrawl(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	_ = s.Queue.Enqueue(r.Context(), "find_episodes", "find_episodes:"+id,
		map[string]any{"book_id": id})
	if r.Header.Get("HX-Request") == "true" {
		w.Write([]byte(`<span class="flash">▶ CRAWL QUEUED</span>`))
		return
	}
	http.Redirect(w, r, "/books/"+id, http.StatusFound)
}
