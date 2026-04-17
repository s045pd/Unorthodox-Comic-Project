package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/s045pd/se8/internal/auth"
	"github.com/s045pd/se8/internal/jobs"
	"github.com/s045pd/se8/internal/storage"
)

func newTestServer(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	ctx := context.Background()
	db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	authStore := auth.NewStore(db)
	pw, _, err := authStore.EnsureFirstRunAdmin(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tpl, err := LoadTemplates()
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{
		DB:           db,
		Auth:         authStore,
		Queue:        jobs.NewQueue(db),
		VolDir:       t.TempDir(),
		Templates:    tpl,
		Logger:       testLogger(t),
		SessionTTL:   30 * 24 * time.Hour,
		SecureCookie: false,
	}
	srv := httptest.NewServer(s.Router())
	t.Cleanup(srv.Close)
	return srv, pw
}

func TestLoginFlow(t *testing.T) {
	srv, pw := newTestServer(t)

	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := client.Get(srv.URL + "/books")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusFound {
		t.Errorf("expected 302, got %d", resp.StatusCode)
	}

	form := url.Values{"username": {"admin"}, "password": {pw}}
	resp, err = client.PostForm(srv.URL+"/login", form)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("login status = %d", resp.StatusCode)
	}
	var sessionCookie *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == "se8_session" {
			sessionCookie = c
			break
		}
	}
	if sessionCookie == nil {
		t.Fatal("no session cookie set")
	}

	req, _ := http.NewRequest("GET", srv.URL+"/books", nil)
	req.AddCookie(sessionCookie)
	resp, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("books status = %d", resp.StatusCode)
	}
}

func TestLogin_BadPassword(t *testing.T) {
	srv, _ := newTestServer(t)
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	form := url.Values{"username": {"admin"}, "password": {"nope"}}
	resp, err := client.PostForm(srv.URL+"/login", form)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 with error banner, got %d", resp.StatusCode)
	}
	body, _ := readBody(resp)
	if !strings.Contains(body, "Invalid") {
		t.Errorf("expected error message, got: %s", body)
	}
}
