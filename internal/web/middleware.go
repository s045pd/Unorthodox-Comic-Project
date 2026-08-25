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
	ctxUser    ctxKey = iota // username (string), kept for backward compat
	ctxSession               // *auth.Session — full session data including role + user id
)

// SessionFromContext returns the *auth.Session stored on the request context
// by sessionMiddleware. nil if the request didn't go through the middleware.
func SessionFromContext(ctx context.Context) *auth.Session {
	if sess, ok := ctx.Value(ctxSession).(*auth.Session); ok {
		return sess
	}
	return nil
}

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
			ctx = context.WithValue(ctx, ctxSession, sess)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// requireAdmin gates routes that only admins may use. Must be chained AFTER
// sessionMiddleware (so the session is already on the context).
func requireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sess := SessionFromContext(r.Context())
		if sess == nil || !sess.IsAdmin() {
			if strings.HasPrefix(r.URL.Path, "/api/") {
				http.Error(w, "forbidden: admin only", http.StatusForbidden)
				return
			}
			http.Error(w, "Forbidden — admin only", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
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
