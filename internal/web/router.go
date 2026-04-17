package web

import (
	"database/sql"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/httprate"

	"github.com/s045pd/se8/internal/auth"
	"github.com/s045pd/se8/internal/jobs"
)

type Server struct {
	DB        *sql.DB
	Auth      *auth.Store
	Queue     *jobs.Queue
	VolDir    string
	Templates *template.Template
	Logger    *slog.Logger
}

// Router builds the full http.Handler.
func (s *Server) Router() http.Handler {
	r := chi.NewRouter()
	r.Use(loggingMiddleware(s.Logger))

	// Static assets
	r.Handle("/static/*", http.StripPrefix("/static/",
		http.FileServerFS(fs.FS(Static()))))

	// Health
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte("ok"))
	})

	// Login / logout (login rate-limited)
	r.Group(func(r chi.Router) {
		r.Use(httprate.LimitByIP(10, time.Minute))
		r.Get("/login", s.handleLoginForm)
		r.Post("/login", s.handleLoginSubmit)
	})
	r.Post("/logout", s.handleLogout)

	// Everything else behind session auth
	r.Group(func(r chi.Router) {
		r.Use(sessionMiddleware(s.Auth))
		r.Get("/", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/books", http.StatusFound)
		})
		s.mountBookRoutes(r)
		s.mountEpisodeRoutes(r)
		s.mountImageRoutes(r)
		s.mountTagRoutes(r)
		s.mountJobRoutes(r)
		s.mountMediaRoutes(r)
		s.mountAdminRoutes(r)
	})
	return r
}
