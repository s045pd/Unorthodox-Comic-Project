package web

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/s045pd/se8/internal/auth"
)

func (s *Server) mountUserRoutes(r chi.Router) {
	// All admin/users routes require admin role on top of session middleware.
	r.Route("/admin/users", func(r chi.Router) {
		r.Use(requireAdmin)
		r.Get("/", s.handleUsersList)
		r.Post("/", s.handleUsersCreate)
		r.Post("/{id}/reset", s.handleUsersResetPassword)
		r.Post("/{id}/active", s.handleUsersToggleActive)
		r.Post("/{id}/role", s.handleUsersSetRole)
		r.Post("/{id}/delete", s.handleUsersDelete)
	})
}

func (s *Server) handleUsersList(w http.ResponseWriter, r *http.Request) {
	users, err := s.Auth.ListUsers(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// Optional flash message via query (e.g. ?new_user=alice&new_pw=...).
	flash := map[string]string{
		"NewUsername": r.URL.Query().Get("new_user"),
		"NewPassword": r.URL.Query().Get("new_pw"),
		"ResetUser":   r.URL.Query().Get("reset_user"),
		"ResetPW":     r.URL.Query().Get("reset_pw"),
		"Notice":      r.URL.Query().Get("notice"),
		"Error":       r.URL.Query().Get("err"),
	}
	s.renderPage(w, r, "users_list", map[string]any{
		"Title":   "Users",
		"Users":   users,
		"Flash":   flash,
		"Session": SessionFromContext(r.Context()),
	})
}

func (s *Server) handleUsersCreate(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	username := r.FormValue("username")
	role := r.FormValue("role")
	password := r.FormValue("password") // optional — empty = auto-generate
	pw, err := s.Auth.CreateUser(r.Context(), username, role, password)
	if err != nil {
		http.Redirect(w, r, "/admin/users?err="+url.QueryEscape(err.Error()), http.StatusFound)
		return
	}
	customSet := password != ""
	q := "/admin/users?new_user=" + url.QueryEscape(username)
	if customSet {
		q += "&notice=" + url.QueryEscape("User created with the password you specified")
	} else {
		q += "&new_pw=" + url.QueryEscape(pw)
	}
	http.Redirect(w, r, q, http.StatusFound)
}

func (s *Server) handleUsersResetPassword(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	password := r.FormValue("password") // optional — empty = auto-generate
	pw, err := s.Auth.ResetPassword(r.Context(), id, password)
	if err != nil {
		http.Redirect(w, r, "/admin/users?err="+url.QueryEscape(err.Error()), http.StatusFound)
		return
	}
	username := s.lookupUsername(r, id)
	if password != "" {
		http.Redirect(w, r, "/admin/users?notice="+
			url.QueryEscape("Password updated for "+username+"; their existing sessions were cleared"),
			http.StatusFound)
		return
	}
	http.Redirect(w, r, "/admin/users?reset_user="+url.QueryEscape(username)+"&reset_pw="+url.QueryEscape(pw),
		http.StatusFound)
}

func (s *Server) handleUsersToggleActive(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	to := r.FormValue("to")
	active := to != "0"
	if err := s.Auth.SetActive(r.Context(), id, active); err != nil {
		http.Redirect(w, r, "/admin/users?err="+err.Error(), http.StatusFound)
		return
	}
	notice := "User activated"
	if !active {
		notice = "User deactivated · sessions cleared"
	}
	http.Redirect(w, r, "/admin/users?notice="+notice, http.StatusFound)
}

func (s *Server) handleUsersSetRole(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	role := r.FormValue("role")
	if err := s.Auth.SetRole(r.Context(), id, role); err != nil {
		http.Redirect(w, r, "/admin/users?err="+err.Error(), http.StatusFound)
		return
	}
	http.Redirect(w, r, "/admin/users?notice=Role updated", http.StatusFound)
}

func (s *Server) handleUsersDelete(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	sess := SessionFromContext(r.Context())
	if sess != nil && sess.UserID == id {
		http.Redirect(w, r, "/admin/users?err=Cannot delete your own account", http.StatusFound)
		return
	}
	if err := s.Auth.DeleteUser(r.Context(), id); err != nil {
		if errors.Is(err, auth.ErrLastAdmin) {
			http.Redirect(w, r, "/admin/users?err=Cannot delete the last admin", http.StatusFound)
			return
		}
		http.Redirect(w, r, "/admin/users?err="+err.Error(), http.StatusFound)
		return
	}
	http.Redirect(w, r, "/admin/users?notice=User deleted", http.StatusFound)
}

func (s *Server) lookupUsername(r *http.Request, id int64) string {
	var name string
	_ = s.DB.QueryRowContext(r.Context(),
		`SELECT username FROM users WHERE id=?`, id).Scan(&name)
	return name
}
