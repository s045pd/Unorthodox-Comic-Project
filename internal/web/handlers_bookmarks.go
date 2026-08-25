package web

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/s045pd/se8/internal/auth"
)

type bookmarkPayload struct {
	BookID     string  `json:"book_id"`
	EpisodeID  int64   `json:"episode_id"`
	ImageIdx   int     `json:"image_idx"`
	ScrollPct  float64 `json:"scroll_pct"`
	TotalPages int     `json:"total_pages"`
}

func (s *Server) mountBookmarkRoutes(r chi.Router) {
	r.Post("/api/bookmark", s.handleBookmarkUpsert)
	r.Get("/api/bookmark/{book_id}", s.handleBookmarkGet)
	r.Post("/api/bookmark/{book_id}/delete", s.handleBookmarkDelete)
	r.Get("/me/bookmarks", s.handleBookmarksList)
}

func (s *Server) handleBookmarkUpsert(w http.ResponseWriter, r *http.Request) {
	sess := SessionFromContext(r.Context())
	if sess == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var p bookmarkPayload
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&p); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	if p.BookID == "" || p.EpisodeID == 0 {
		http.Error(w, "missing book_id or episode_id", http.StatusBadRequest)
		return
	}
	bm := auth.Bookmark{
		UserID:     sess.UserID,
		BookID:     p.BookID,
		EpisodeID:  p.EpisodeID,
		ImageIdx:   p.ImageIdx,
		ScrollPct:  p.ScrollPct,
		TotalPages: p.TotalPages,
	}
	if err := s.Auth.UpsertBookmark(r.Context(), bm); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}

func (s *Server) handleBookmarkGet(w http.ResponseWriter, r *http.Request) {
	sess := SessionFromContext(r.Context())
	if sess == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	bookID := chi.URLParam(r, "book_id")
	bm, err := s.Auth.GetBookmark(r.Context(), sess.UserID, bookID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if bm == nil {
		w.Write([]byte(`null`))
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"book_id":     bm.BookID,
		"book_title":  bm.BookTitle,
		"episode_id":  bm.EpisodeID,
		"ep_title":    bm.EpTitle,
		"image_idx":   bm.ImageIdx,
		"scroll_pct":  bm.ScrollPct,
		"total_pages": bm.TotalPages,
		"updated_at":  bm.UpdatedAt.Unix(),
	})
}

func (s *Server) handleBookmarkDelete(w http.ResponseWriter, r *http.Request) {
	sess := SessionFromContext(r.Context())
	if sess == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	bookID := chi.URLParam(r, "book_id")
	if err := s.Auth.DeleteBookmark(r.Context(), sess.UserID, bookID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if r.Header.Get("HX-Request") == "true" {
		w.Write([]byte(`<span class="flash">REMOVED</span>`))
		return
	}
	http.Redirect(w, r, "/me/bookmarks", http.StatusFound)
}

func (s *Server) handleBookmarksList(w http.ResponseWriter, r *http.Request) {
	sess := SessionFromContext(r.Context())
	if sess == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	bms, err := s.Auth.ListBookmarks(r.Context(), sess.UserID, 200)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.renderPage(w, r, "bookmarks_list", map[string]any{
		"Title":     "My Reading",
		"Bookmarks": bms,
		"Session":   sess,
	})
}
