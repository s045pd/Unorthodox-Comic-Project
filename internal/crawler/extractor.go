package crawler

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

type BookDTO struct {
	ID       string
	RawURL   string
	Title    string
	ImageURL string
	Current  string // latest episode title (for outdated-check)
}

type BookMeta struct {
	Tags        []string
	Hot         int
	Description string
}

type EpisodeDTO struct {
	ID     int64
	RawURL string
	Title  string
}

type ImageDTO struct {
	ID     int64
	Index  int
	RawURL string
}

type Extractor struct {
	client *Client
}

func NewExtractor(c *Client) *Extractor {
	return &Extractor{client: c}
}

func (e *Extractor) Origin() string { return e.client.Origin() }

func (e *Extractor) FetchBooks(ctx context.Context, page int) ([]BookDTO, error) {
	url := fmt.Sprintf("%s/index.php/category/page/%d", e.client.Origin(), page)
	html, err := e.client.GetHTML(ctx, url)
	if err != nil {
		return nil, err
	}
	return ParseBooks(html)
}

func (e *Extractor) FetchEpisodes(ctx context.Context, url string) (BookMeta, []EpisodeDTO, error) {
	html, err := e.client.GetHTML(ctx, url)
	if err != nil {
		return BookMeta{}, nil, err
	}
	return ParseEpisodes(html)
}

func (e *Extractor) FetchImages(ctx context.Context, url string) ([]ImageDTO, error) {
	html, err := e.client.GetHTML(ctx, url)
	if err != nil {
		return nil, err
	}
	return ParseImages(html)
}

func (e *Extractor) FetchMaxPage(ctx context.Context) (int, error) {
	url := fmt.Sprintf("%s/index.php/category/page/1", e.client.Origin())
	html, err := e.client.GetHTML(ctx, url)
	if err != nil {
		return 0, err
	}
	return ParseMaxPage(html)
}

// ParseBooks extracts book cards from a category listing page.
func ParseBooks(html string) ([]BookDTO, error) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return nil, err
	}
	var out []BookDTO
	doc.Find("div.common-comic-item").Each(func(_ int, s *goquery.Selection) {
		href, _ := s.Find("a.cover").Attr("href")
		parts := strings.Split(strings.TrimRight(href, "/"), "/")
		id := ""
		if len(parts) > 0 {
			id = parts[len(parts)-1]
		}
		img, _ := s.Find("img").Attr("data-original")
		title := strings.TrimSpace(s.Find("p.comic__title").First().Text())
		current := strings.TrimSpace(s.Find("p.comic-update a").First().Text())
		out = append(out, BookDTO{
			ID:       id,
			RawURL:   href,
			Title:    title,
			ImageURL: img,
			Current:  current,
		})
	})
	return out, nil
}

// ParseMaxPage returns the last page number from "end" pagination link.
func ParseMaxPage(html string) (int, error) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return 0, err
	}
	href, ok := doc.Find("a.end").Attr("href")
	if !ok {
		return 0, fmt.Errorf("no a.end in page")
	}
	parts := strings.Split(strings.TrimRight(href, "/"), "/")
	return strconv.Atoi(parts[len(parts)-1])
}

// ParseEpisodes extracts meta + episode list from a book detail page.
func ParseEpisodes(html string) (BookMeta, []EpisodeDTO, error) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return BookMeta{}, nil, err
	}

	meta := BookMeta{}
	doc.Find("div.comic-status a").Each(func(_ int, s *goquery.Selection) {
		if t := strings.TrimSpace(s.Text()); t != "" {
			meta.Tags = append(meta.Tags, t)
		}
	})
	// Hot is inside a <b> within div.comic-status (e.g. "<span><b>4.5 分</b></span>")
	hotText := strings.TrimSpace(doc.Find("div.comic-status b").First().Text())
	if hotText != "" {
		hotText = strings.Fields(hotText)[0]
		if f, err := strconv.ParseFloat(hotText, 64); err == nil {
			meta.Hot = int(f)
		}
	}
	intro := doc.Find("div.comic-intro p")
	if intro.Length() >= 3 {
		meta.Description = strings.TrimSpace(intro.Eq(2).Text())
	} else if intro.Length() > 0 {
		meta.Description = strings.TrimSpace(intro.Last().Text())
	}

	var eps []EpisodeDTO
	doc.Find("ul.chapter__list-box li").Each(func(_ int, s *goquery.Selection) {
		href, _ := s.Find("a").Attr("href")
		parts := strings.Split(strings.TrimRight(href, "/"), "/")
		if len(parts) == 0 {
			return
		}
		id, err := strconv.ParseInt(parts[len(parts)-1], 10, 64)
		if err != nil {
			return
		}
		title := strings.TrimSpace(s.Text())
		eps = append(eps, EpisodeDTO{ID: id, RawURL: href, Title: title})
	})
	return meta, eps, nil
}

// ParseImages extracts image pid/index/url from a reading page.
func ParseImages(html string) ([]ImageDTO, error) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return nil, err
	}
	var imgs []ImageDTO
	doc.Find("div.rd-article__pic.hide").Each(func(_ int, s *goquery.Selection) {
		pidStr, _ := s.Attr("data-pid")
		idxStr, _ := s.Attr("data-index")
		url, _ := s.Find("img").Attr("data-original")
		pid, err := strconv.ParseInt(pidStr, 10, 64)
		if err != nil {
			return
		}
		idx, _ := strconv.Atoi(idxStr)
		imgs = append(imgs, ImageDTO{ID: pid, Index: idx, RawURL: url})
	})
	return imgs, nil
}
