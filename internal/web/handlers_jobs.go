package web

import (
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
)

// jobStatsCache memoizes the heavy dashboard aggregate query (GROUP BY status
// over 800K+ jobs + a full-scan COUNT of images). Pages re-render every 3s via
// HTMX polling; serving each hit fresh would saturate the writer pool.
var (
	jobStatsCacheMu  sync.Mutex
	jobStatsCacheVal jobStats
	jobStatsCacheT   time.Time
)

const jobStatsTTL = 5 * time.Second

type jobRow struct {
	ID          int64
	Kind        string
	TaskKey     string
	Status      string
	Attempts    int
	MaxAttempts int
	LastError   string
	CreatedAt   time.Time
	FinishedAt  *time.Time
}

// jobStats is the dashboard summary at top of /jobs.
type jobStats struct {
	Pending      int
	Running      int
	Done         int
	Failed       int
	Total        int
	DonePerMin   int // jobs done in last 60s
	FailedPerMin int // jobs failed in last 60s
	ImagesSaved  int // total images with file_path
	ImagesTotal  int
}

func (s *Server) loadJobStats(r *http.Request) (jobStats, error) {
	jobStatsCacheMu.Lock()
	if time.Since(jobStatsCacheT) < jobStatsTTL {
		st := jobStatsCacheVal
		jobStatsCacheMu.Unlock()
		return st, nil
	}
	jobStatsCacheMu.Unlock()

	var st jobStats
	rows, err := s.DB.QueryContext(r.Context(),
		`SELECT status, COUNT(*) FROM jobs GROUP BY status`)
	if err != nil {
		return st, err
	}
	for rows.Next() {
		var status string
		var n int
		if err := rows.Scan(&status, &n); err != nil {
			rows.Close()
			return st, err
		}
		switch status {
		case "pending":
			st.Pending = n
		case "running":
			st.Running = n
		case "done":
			st.Done = n
		case "failed":
			st.Failed = n
		}
		st.Total += n
	}
	rows.Close()

	now := time.Now().Unix()
	_ = s.DB.QueryRowContext(r.Context(),
		`SELECT COUNT(*) FROM jobs WHERE status='done' AND finished_at > ?`,
		now-60).Scan(&st.DonePerMin)
	_ = s.DB.QueryRowContext(r.Context(),
		`SELECT COUNT(*) FROM jobs WHERE status='failed' AND finished_at > ?`,
		now-60).Scan(&st.FailedPerMin)

	// images: total via simple COUNT, saved derived as total minus missing —
	// missing has a partial index (idx_images_missing) so it's a few rows scan.
	_ = s.DB.QueryRowContext(r.Context(),
		`SELECT COUNT(*) FROM images`).Scan(&st.ImagesTotal)
	var missing int
	_ = s.DB.QueryRowContext(r.Context(),
		`SELECT COUNT(*) FROM images WHERE file_path = ''`).Scan(&missing)
	st.ImagesSaved = st.ImagesTotal - missing

	jobStatsCacheMu.Lock()
	jobStatsCacheVal = st
	jobStatsCacheT = time.Now()
	jobStatsCacheMu.Unlock()
	return st, nil
}

func (s *Server) mountJobRoutes(r chi.Router) {
	// Job queue surface is admin-only — operational, not relevant to readers.
	r.Group(func(r chi.Router) {
		r.Use(requireAdmin)
		r.Get("/jobs", s.handleJobsPage)
		r.Get("/jobs/fragment", s.handleJobsFragment)
		r.Post("/jobs/{id}/retry", s.handleJobRetry)
		r.Delete("/jobs/{id}", s.handleJobDelete)
	})
}

func (s *Server) loadJobs(r *http.Request) ([]jobRow, error) {
	// Show running first, then failed, then newest pending, then a few done —
	// fetched as separate index-backed queries instead of a CASE-WHEN ORDER BY
	// (which forces a full-table sort on the 800K-row jobs table).
	const total = 200
	jobs := make([]jobRow, 0, total)

	const baseQ = `SELECT id, kind, task_key, status, attempts, max_attempts, last_error,
	                      created_at, finished_at
	               FROM jobs WHERE status = ? ORDER BY id DESC LIMIT ?`

	for _, status := range []string{"running", "failed", "pending", "done"} {
		remain := total - len(jobs)
		if remain <= 0 {
			break
		}
		rows, err := s.DB.QueryContext(r.Context(), baseQ, status, remain)
		if err != nil {
			return nil, fmt.Errorf("loadJobs %s: %w", status, err)
		}
		for rows.Next() {
			var j jobRow
			var created int64
			var finished *int64
			if err := rows.Scan(&j.ID, &j.Kind, &j.TaskKey, &j.Status, &j.Attempts,
				&j.MaxAttempts, &j.LastError, &created, &finished); err != nil {
				rows.Close()
				return nil, fmt.Errorf("scan job: %w", err)
			}
			j.CreatedAt = time.Unix(created, 0)
			if finished != nil {
				t := time.Unix(*finished, 0)
				j.FinishedAt = &t
			}
			jobs = append(jobs, j)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}
	return jobs, nil
}

func (s *Server) handleJobsPage(w http.ResponseWriter, r *http.Request) {
	jobs, err := s.loadJobs(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	stats, _ := s.loadJobStats(r)
	s.renderPage(w, r, "jobs_page", map[string]any{
		"Title": "Jobs", "Jobs": jobs, "Stats": stats,
	})
}

func (s *Server) handleJobsFragment(w http.ResponseWriter, r *http.Request) {
	jobs, err := s.loadJobs(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	stats, _ := s.loadJobStats(r)
	s.renderTemplate(w, "jobs_fragment", map[string]any{
		"Jobs": jobs, "Stats": stats,
	})
}

func (s *Server) handleJobRetry(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	_, err := s.DB.ExecContext(r.Context(),
		`UPDATE jobs SET status='pending', run_at=?, attempts=0, last_error='' WHERE id=?`,
		time.Now().Unix(), id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/jobs", http.StatusFound)
}

func (s *Server) handleJobDelete(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	_, err := s.DB.ExecContext(r.Context(), `DELETE FROM jobs WHERE id=?`, id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
