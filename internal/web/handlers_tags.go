package web

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

type tagRow struct {
	Name  string
	Count int
}

func (s *Server) mountTagRoutes(r chi.Router) {
	r.Get("/tags", s.handleTagsList)
}

func (s *Server) handleTagsList(w http.ResponseWriter, r *http.Request) {
	rows, err := s.DB.QueryContext(r.Context(), `
		SELECT t.name, COUNT(bt.book_id)
		FROM tags t LEFT JOIN book_tags bt ON bt.tag_id=t.id
		GROUP BY t.id ORDER BY t.name`)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()
	var tags []tagRow
	for rows.Next() {
		var t tagRow
		if err := rows.Scan(&t.Name, &t.Count); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		tags = append(tags, t)
	}
	s.renderTemplate(w, "tags_list", map[string]any{
		"Title": "Tags",
		"Tags":  tags,
	})
}
