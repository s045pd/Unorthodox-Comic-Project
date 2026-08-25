package web

import (
	"database/sql"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
)

// 5s in-memory cache for the unfiltered /images dashboard counts. With 800K+
// images, COUNT(*) FROM images is a slow full sweep that should not run on
// every page hit.
var (
	imagesCacheMu      sync.Mutex
	imagesCacheTotal   int
	imagesCacheMissing int
	imagesCacheT       time.Time
)

func cachedImagesTotalMissing(r *http.Request, db *sql.DB) (total, missing int) {
	imagesCacheMu.Lock()
	if time.Since(imagesCacheT) < 5*time.Second {
		t, m := imagesCacheTotal, imagesCacheMissing
		imagesCacheMu.Unlock()
		return t, m
	}
	imagesCacheMu.Unlock()

	ctx := r.Context()
	_ = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM images`).Scan(&total)
	_ = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM images WHERE file_path = ''`).Scan(&missing)

	imagesCacheMu.Lock()
	imagesCacheTotal, imagesCacheMissing, imagesCacheT = total, missing, time.Now()
	imagesCacheMu.Unlock()
	return
}


type imgRow struct {
	ID, EpisodeID int64
	Index, Bytes  int
	HasFile       bool
}

func (s *Server) mountImageRoutes(r chi.Router) {
	// Asset inventory is admin-only — operational view, not for end readers.
	r.Group(func(r chi.Router) {
		r.Use(requireAdmin)
		r.Get("/images", s.handleImagesList)
		r.Post("/images/{id}/redownload", s.handleImageRedownload)
	})
}

func (s *Server) handleImagesList(w http.ResponseWriter, r *http.Request) {
	epFilter := r.URL.Query().Get("episode")
	missingOnly := r.URL.Query().Get("missing") == "1"

	listSQL := `SELECT id, episode_id, idx, bytes, (file_path != '') FROM images`
	countSQL := `SELECT COUNT(*) FROM images`
	missCountSQL := `SELECT COUNT(*) FROM images WHERE file_path = ''`
	var conds []string
	args := []any{}
	if epFilter != "" {
		conds = append(conds, "episode_id=?")
		args = append(args, epFilter)
	}
	if missingOnly {
		conds = append(conds, "file_path = ''")
	}
	where := ""
	if len(conds) > 0 {
		where = " WHERE "
		for i, c := range conds {
			if i > 0 {
				where += " AND "
			}
			where += c
		}
	}
	listSQL += where + " ORDER BY episode_id DESC, idx LIMIT 500"
	countSQL += where

	rows, err := s.DB.QueryContext(r.Context(), listSQL, args...)
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

	var total, missing int
	if epFilter == "" && !missingOnly {
		// Hot path: full-table COUNT(*) is slow and cacheable for 5s.
		total, missing = cachedImagesTotalMissing(r, s.DB)
	} else {
		_ = s.DB.QueryRowContext(r.Context(), countSQL, args...).Scan(&total)
		if epFilter != "" {
			_ = s.DB.QueryRowContext(r.Context(),
				`SELECT COUNT(*) FROM images WHERE episode_id=? AND file_path=''`, epFilter).Scan(&missing)
		} else {
			_ = s.DB.QueryRowContext(r.Context(), missCountSQL).Scan(&missing)
		}
	}

	s.renderPage(w, r, "images_list", map[string]any{
		"Title":         "Images",
		"Images":        imgs,
		"EpisodeFilter": epFilter,
		"MissingOnly":   missingOnly,
		"Total":         total,
		"Missing":       missing,
	})
}

func (s *Server) handleImageRedownload(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	_ = s.Queue.Enqueue(r.Context(), "download_image",
		"download_image:"+strconv.FormatInt(id, 10),
		map[string]any{"image_id": id})
	if r.Header.Get("HX-Request") == "true" {
		w.Write([]byte(`<span class="flash">↻ REFETCH QUEUED</span>`))
		return
	}
	http.Redirect(w, r, safeReferer(r, "/images"), http.StatusFound)
}
