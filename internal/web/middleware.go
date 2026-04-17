package web

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/s045pd/se8/internal/auth"
)

type ctxKey int

const (
	ctxUser ctxKey = iota
)

// sessionMiddleware looks up the session cookie; on miss, redirects HTML
// routes to /login and returns 401 for /api/ routes.
func sessionMiddleware(store *auth.Store) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/login" || r.URL.Path == "/healthz" ||
				strings.HasPrefix(r.URL.Path, "/static/") {
				next.ServeHTTP(w, r)
				return
			}
			c, err := r.Cookie("se8_session")
			if err != nil {
				unauthorized(w, r)
				return
			}
			sess, err := store.LookupSession(r.Context(), c.Value)
			if err != nil || sess == nil {
				unauthorized(w, r)
				return
			}
			ctx := context.WithValue(r.Context(), ctxUser, sess.Username)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func unauthorized(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	http.Redirect(w, r, "/login", http.StatusFound)
}

// loggingMiddleware writes a structured access log line per request.
func loggingMiddleware(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ww := &statusRecorder{ResponseWriter: w, status: 200}
			next.ServeHTTP(ww, r)
			logger.Info("http",
				"method", r.Method,
				"path", r.URL.Path,
				"status", ww.status,
				"dur_ms", time.Since(start).Milliseconds())
		})
	}
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}
