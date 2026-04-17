package crawler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

var tinyPNG = []byte{
	0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0x00, 0x00, 0x00, 0x0D,
	0x49, 0x48, 0x44, 0x52, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
	0x08, 0x06, 0x00, 0x00, 0x00, 0x1F, 0x15, 0xC4, 0x89, 0x00, 0x00, 0x00,
	0x0D, 0x49, 0x44, 0x41, 0x54, 0x08, 0x99, 0x63, 0x00, 0x01, 0x00, 0x00,
	0x05, 0x00, 0x01, 0x0D, 0x0A, 0x2D, 0xB4, 0x00, 0x00, 0x00, 0x00, 0x49,
	0x45, 0x4E, 0x44, 0xAE, 0x42, 0x60, 0x82,
}

func TestDownloadImage_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Write(tinyPNG)
	}))
	defer srv.Close()

	c := NewClient("https://se8.us", 5*time.Second)
	data, ct, err := c.DownloadImage(context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != len(tinyPNG) {
		t.Errorf("len = %d", len(data))
	}
	if ct != "png" {
		t.Errorf("ct = %q, want png", ct)
	}
}

func TestDownloadImage_NotImage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte("not an image"))
	}))
	defer srv.Close()

	c := NewClient("https://se8.us", 5*time.Second)
	_, _, err := c.DownloadImage(context.Background(), srv.URL)
	if err == nil {
		t.Error("expected error for non-image content-type")
	}
}
