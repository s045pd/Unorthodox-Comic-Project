package jobs

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/s045pd/se8/internal/crawler"
	"github.com/s045pd/se8/internal/storage"
)

const categoryPageHTML = `<html><body>
<div class="common-comic-item">
  <a class="cover" href="/index.php/comic/book-1"></a>
  <img data-original="https://cdn.example/1.jpg" />
  <p class="comic__title">Book One</p>
  <p class="comic-update"><a>Chapter 1</a></p>
</div>
<a class="end" href="/index.php/category/page/1"></a>
</body></html>`

func TestHandleFindBooks_EnqueuesForNewBooks(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/index.php/category/page/") {
			w.Write([]byte(categoryPageHTML))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	ctx := context.Background()
	db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	q := NewQueue(db)
	client := crawler.NewClient(srv.URL, 0)
	ext := crawler.NewExtractor(client)

	d := &Deps{DB: db, Queue: q, Extractor: ext, Client: client, VolDir: t.TempDir(), MaxPage: 1}
	if err := d.HandleFindBooks(ctx, nil); err != nil {
		t.Fatalf("HandleFindBooks: %v", err)
	}

	var books int
	db.QueryRowContext(ctx, `SELECT COUNT(*) FROM books`).Scan(&books)
	if books != 1 {
		t.Errorf("books = %d, want 1", books)
	}
	var jobs int
	db.QueryRowContext(ctx, `SELECT COUNT(*) FROM jobs WHERE kind='find_episodes'`).Scan(&jobs)
	if jobs != 1 {
		t.Errorf("find_episodes jobs = %d, want 1", jobs)
	}
}
