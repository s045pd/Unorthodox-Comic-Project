package web

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
)

func (s *Server) mountBookRoutes(r chi.Router) {
	// Read-only paths — every authenticated user.
	r.Get("/books", s.handleBooksList)
	r.Get("/books/{id}", s.handleBookDetail)
	r.Get("/books/{id}/episodes", s.handleBookEpisodesPartial)
	// Mutating paths — admin only.
	r.Group(func(r chi.Router) {
		r.Use(requireAdmin)
		r.Post("/books/{id}/crawl", s.handleBookCrawl)
	})
}

// episodePageSize is the number of chapters rendered per book_detail page hit
// and per htmx lazy-load. Kept small so the initial book page paints quickly
// even on slow connections — the rest streams in via infinite scroll.
const episodePageSize = 10

type bookRow struct {
	ID           string
	Title        string
	CoverPath    string // local path (preferred)
	ImageURL     string // remote CDN fallback
	Hot          int
	EpisodeCount int

	// Optional: filled in when the request has an authenticated user with a
	// bookmark on this book. Empty otherwise.
	BookmarkEpisodeID  int64
	BookmarkImageIdx   int
	BookmarkTotalPages int
	BookmarkEpTitle    string
}

// HasBookmark is a template helper.
func (b bookRow) HasBookmark() bool { return b.BookmarkEpisodeID != 0 }

func (s *Server) handleBooksList(w http.ResponseWriter, r *http.Request) {
	limit := 50
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	offset := (page - 1) * limit

	tagFilter := strings.TrimSpace(r.URL.Query().Get("tag"))
	qFilter := strings.TrimSpace(r.URL.Query().Get("q"))
	statusFilter := strings.TrimSpace(r.URL.Query().Get("status")) // "", "complete", "partial", "empty"

	// Sorting. Whitelisted to avoid SQL injection — never interpolate raw input.
	sortKey := strings.TrimSpace(r.URL.Query().Get("sort"))
	orderBy := ""
	switch sortKey {
	case "title":
		orderBy = "b.title ASC"
	case "title_desc":
		orderBy = "b.title DESC"
	case "recent":
		orderBy = "b.updated_at DESC, b.hot DESC"
	case "episodes":
		orderBy = "(SELECT COUNT(*) FROM episodes e WHERE e.book_id=b.id) DESC, b.hot DESC"
	case "hot_asc":
		orderBy = "b.hot ASC, b.title ASC"
	default: // "hot" or empty
		sortKey = "hot"
		orderBy = "b.hot DESC, b.title ASC"
	}

	// Build query dynamically based on filters.
	var (
		whereClauses []string
		args         []any
	)
	from := "FROM books b"
	if tagFilter != "" {
		from = `FROM books b
		         JOIN book_tags bt ON bt.book_id = b.id
		         JOIN tags t       ON t.id       = bt.tag_id`
		whereClauses = append(whereClauses, "t.name = ?")
		args = append(args, tagFilter)
	}
	if qFilter != "" {
		whereClauses = append(whereClauses, "(b.title LIKE ? OR b.id LIKE ?)")
		args = append(args, "%"+qFilter+"%", "%"+qFilter+"%")
	}
	// Crawl-completeness filter. Uses EXISTS+indexes; "missing" path hits
	// idx_images_missing (partial index on file_path=''), so it's quick even
	// across 800K rows.
	switch statusFilter {
	case "complete":
		// has at least one episode AND no missing images anywhere
		whereClauses = append(whereClauses,
			`EXISTS (SELECT 1 FROM episodes e WHERE e.book_id=b.id)`,
			`NOT EXISTS (SELECT 1 FROM images i JOIN episodes e ON e.id=i.episode_id WHERE e.book_id=b.id AND i.file_path='')`,
		)
	case "partial":
		// at least one missing image
		whereClauses = append(whereClauses,
			`EXISTS (SELECT 1 FROM images i JOIN episodes e ON e.id=i.episode_id WHERE e.book_id=b.id AND i.file_path='')`,
		)
	case "empty":
		// no episodes at all
		whereClauses = append(whereClauses,
			`NOT EXISTS (SELECT 1 FROM episodes e WHERE e.book_id=b.id)`,
		)
	default:
		statusFilter = "" // normalize unknown values to "all"
	}
	where := ""
	if len(whereClauses) > 0 {
		where = " WHERE " + strings.Join(whereClauses, " AND ")
	}

	listSQL := `SELECT b.id, b.title, b.cover_path, b.image_url, b.hot,
		       (SELECT COUNT(*) FROM episodes e WHERE e.book_id=b.id) ` +
		from + where + ` ORDER BY ` + orderBy + ` LIMIT ? OFFSET ?`
	listArgs := append(append([]any{}, args...), limit, offset)
	rows, err := s.DB.QueryContext(r.Context(), listSQL, listArgs...)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()
	var books []bookRow
	for rows.Next() {
		var b bookRow
		if err := rows.Scan(&b.ID, &b.Title, &b.CoverPath, &b.ImageURL, &b.Hot, &b.EpisodeCount); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		books = append(books, b)
	}

	var total int
	countSQL := "SELECT COUNT(DISTINCT b.id) " + from + where
	_ = s.DB.QueryRowContext(r.Context(), countSQL, args...).Scan(&total)

	// Decorate visible books with the current user's bookmark (if any).
	if sess := SessionFromContext(r.Context()); sess != nil && len(books) > 0 {
		ids := make([]any, 0, len(books))
		for _, b := range books {
			ids = append(ids, b.ID)
		}
		placeholders := strings.Repeat(",?", len(ids))[1:]
		bmRows, err := s.DB.QueryContext(r.Context(),
			`SELECT bm.book_id, bm.episode_id, bm.image_idx, bm.total_pages,
			        COALESCE(e.title,'')
			 FROM bookmarks bm LEFT JOIN episodes e ON e.id = bm.episode_id
			 WHERE bm.user_id=? AND bm.book_id IN (`+placeholders+`)`,
			append([]any{sess.UserID}, ids...)...)
		if err == nil {
			bmMap := make(map[string]struct {
				EpID, ImgIdx, Total int64
				EpTitle             string
			}, len(books))
			for bmRows.Next() {
				var bid string
				var epID, imgIdx, total int64
				var epTitle string
				if err := bmRows.Scan(&bid, &epID, &imgIdx, &total, &epTitle); err == nil {
					bmMap[bid] = struct {
						EpID, ImgIdx, Total int64
						EpTitle             string
					}{epID, imgIdx, total, epTitle}
				}
			}
			bmRows.Close()
			for i := range books {
				if bm, ok := bmMap[books[i].ID]; ok {
					books[i].BookmarkEpisodeID = bm.EpID
					books[i].BookmarkImageIdx = int(bm.ImgIdx)
					books[i].BookmarkTotalPages = int(bm.Total)
					books[i].BookmarkEpTitle = bm.EpTitle
				}
			}
		}
	}

	s.renderPage(w, r, "books_list", map[string]any{
		"Title":        "Books",
		"Books":        books,
		"Page":         page,
		"Total":        total,
		"Limit":        limit,
		"TagFilter":    tagFilter,
		"Q":            qFilter,
		"Sort":         sortKey,
		"StatusFilter": statusFilter,
	})
}

func (s *Server) handleBookDetail(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var b struct {
		ID, Title, Description, CoverPath, ImageURL, RawURL string
		Hot                                                 int
	}
	err := s.DB.QueryRowContext(r.Context(),
		`SELECT id, title, description, cover_path, image_url, hot, raw_url FROM books WHERE id=?`, id).
		Scan(&b.ID, &b.Title, &b.Description, &b.CoverPath, &b.ImageURL, &b.Hot, &b.RawURL)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	q := strings.TrimSpace(r.URL.Query().Get("q"))
	eps, totalEps, err := s.loadEpisodePageForBook(r, id, q, episodePageSize, 0)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	hasMore := totalEps > episodePageSize

	// Per-user "continue reading" pointer.
	var bookmark any
	if sess := SessionFromContext(r.Context()); sess != nil {
		if bm, _ := s.Auth.GetBookmark(r.Context(), sess.UserID, id); bm != nil {
			bookmark = map[string]any{
				"EpisodeID":  bm.EpisodeID,
				"EpTitle":    bm.EpTitle,
				"ImageIdx":   bm.ImageIdx,
				"TotalPages": bm.TotalPages,
				"UpdatedAt":  bm.UpdatedAt,
			}
		}
	}

	s.renderPage(w, r, "book_detail", map[string]any{
		"Title":      b.Title,
		"Book":       b,
		"Episodes":   eps,
		"Bookmark":   bookmark,
		"Q":          q,
		"TotalEps":   totalEps,
		"NextOffset": episodePageSize,
		"HasMore":    hasMore,
		"PageSize":   episodePageSize,
	})
}

// handleBookEpisodesPartial returns just the next page of episode rows for
// htmx infinite-scroll. The response is HTML fragments, not a full page.
func (s *Server) handleBookEpisodesPartial(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if offset < 0 {
		offset = 0
	}
	eps, total, err := s.loadEpisodePageForBook(r, id, q, episodePageSize, offset)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	nextOffset := offset + len(eps)
	hasMore := nextOffset < total

	s.renderTemplate(w, "book_episodes_partial", map[string]any{
		"Episodes":   eps,
		"BookID":     id,
		"Q":          q,
		"NextOffset": nextOffset,
		"HasMore":    hasMore,
		"Session":    SessionFromContext(r.Context()),
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
