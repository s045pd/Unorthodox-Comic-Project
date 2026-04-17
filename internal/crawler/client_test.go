package crawler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestClient_Get_SetsReferer(t *testing.T) {
	got := ""
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Referer")
		w.Write([]byte("<html>ok</html>"))
	}))
	defer srv.Close()

	c := NewClient("https://se8.us", 5*time.Second)
	body, err := c.GetHTML(context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if body == "" {
		t.Error("empty body")
	}
	if got != "https://se8.us/" {
		t.Errorf("Referer = %q", got)
	}
}
