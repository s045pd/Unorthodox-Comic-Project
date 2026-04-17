package crawler

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseBooks_Fixture(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("testdata", "books_page.html"))
	if err != nil {
		t.Fatal(err)
	}
	books, err := ParseBooks(string(b))
	if err != nil {
		t.Fatalf("ParseBooks: %v", err)
	}
	if len(books) != 2 {
		t.Fatalf("got %d books, want 2", len(books))
	}
	if books[0].ID != "abc-123" {
		t.Errorf("ID[0] = %q", books[0].ID)
	}
	if books[0].Title != "Test Title A" {
		t.Errorf("Title[0] = %q", books[0].Title)
	}
	if books[0].ImageURL != "https://cdn.se8.us/cover/abc.jpg" {
		t.Errorf("ImageURL[0] = %q", books[0].ImageURL)
	}
	if books[0].Current != "Chapter 10" {
		t.Errorf("Current[0] = %q", books[0].Current)
	}
	if books[1].ID != "xyz-999" {
		t.Errorf("ID[1] = %q", books[1].ID)
	}
}

func TestParseMaxPage_Fixture(t *testing.T) {
	b, _ := os.ReadFile(filepath.Join("testdata", "books_page.html"))
	max, err := ParseMaxPage(string(b))
	if err != nil {
		t.Fatal(err)
	}
	if max != 42 {
		t.Errorf("max = %d, want 42", max)
	}
}

func TestParseEpisodes_Fixture(t *testing.T) {
	b, _ := os.ReadFile(filepath.Join("testdata", "episodes_page.html"))
	meta, eps, err := ParseEpisodes(string(b))
	if err != nil {
		t.Fatal(err)
	}
	if len(meta.Tags) != 2 || meta.Tags[0] != "Tag Foo" {
		t.Errorf("tags = %v", meta.Tags)
	}
	if meta.Hot != 4 {
		t.Errorf("hot = %d, want 4", meta.Hot)
	}
	if meta.Description != "This is the description line." {
		t.Errorf("desc = %q", meta.Description)
	}
	if len(eps) != 3 {
		t.Fatalf("got %d episodes, want 3", len(eps))
	}
	if eps[0].ID != 100 || eps[0].Title != "Ep 1" {
		t.Errorf("ep[0] = %+v", eps[0])
	}
}

func TestParseImages_Fixture(t *testing.T) {
	b, _ := os.ReadFile(filepath.Join("testdata", "images_page.html"))
	imgs, err := ParseImages(string(b))
	if err != nil {
		t.Fatal(err)
	}
	if len(imgs) != 2 {
		t.Fatalf("got %d imgs, want 2", len(imgs))
	}
	if imgs[0].ID != 5001 || imgs[0].Index != 1 {
		t.Errorf("imgs[0] = %+v", imgs[0])
	}
	if imgs[0].RawURL != "https://cdn.se8.us/img/1.jpg" {
		t.Errorf("url = %q", imgs[0].RawURL)
	}
}
