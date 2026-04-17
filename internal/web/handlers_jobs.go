package web

import (
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
)

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

func (s *Server) mountJobRoutes(r chi.Router) {
	r.Get("/jobs", s.handleJobsPage)
	r.Get("/jobs/fragment", s.handleJobsFragment)
	r.Post("/jobs/{id}/retry", s.handleJobRetry)
	r.Delete("/jobs/{id}", s.handleJobDelete)
}

func (s *Server) loadJobs(r *http.Request) ([]jobRow, error) {
	rows, err := s.DB.QueryContext(r.Context(), `
		SELECT id, kind, task_key, status, attempts, max_attempts, last_error,
		       created_at, finished_at
		FROM jobs ORDER BY id DESC LIMIT 200`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var jobs []jobRow
	for rows.Next() {
		var j jobRow
		var created int64
		var finished *int64
		if err := rows.Scan(&j.ID, &j.Kind, &j.TaskKey, &j.Status, &j.Attempts,
			&j.MaxAttempts, &j.LastError, &created, &finished); err != nil {
			return nil, err
		}
		j.CreatedAt = time.Unix(created, 0)
		if finished != nil {
			t := time.Unix(*finished, 0)
			j.FinishedAt = &t
		}
		jobs = append(jobs, j)
	}
	return jobs, nil
}

func (s *Server) handleJobsPage(w http.ResponseWriter, r *http.Request) {
	jobs, err := s.loadJobs(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.renderTemplate(w, "jobs_page", map[string]any{"Title": "Jobs", "Jobs": jobs})
}

func (s *Server) handleJobsFragment(w http.ResponseWriter, r *http.Request) {
	jobs, err := s.loadJobs(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.renderTemplate(w, "jobs_fragment", map[string]any{"Jobs": jobs})
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
