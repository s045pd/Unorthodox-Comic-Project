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
	Hot      int    // 人气 from listing card (e.g. "396 万" → 3960000)
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
		href = strings.TrimSpace(href)
		parts := strings.Split(strings.TrimRight(href, "/"), "/")
		id := ""
		if len(parts) > 0 {
			id = parts[len(parts)-1]
		}
		img, _ := s.Find("img").Attr("data-original")
		img = strings.TrimSpace(img)
		title := strings.TrimSpace(s.Find("p.comic__title").First().Text())
		current := strings.TrimSpace(s.Find("p.comic-update a").First().Text())

		// Hot is in p.comic-count (e.g. "人气：396 万" or "人气：3.5 万").
		hotText := strings.TrimSpace(s.Find("p.comic-count").First().Text())
		hot := parseHotText(hotText)

		out = append(out, BookDTO{
			ID:       id,
			RawURL:   href,
			Title:    title,
			ImageURL: img,
			Current:  current,
			Hot:      hot,
		})
	})
	return out, nil
}

// parseHotText extracts an integer popularity count from text such as:
//   "人气：396 万"     → 3960000
//   "人气: 3.5 万"     → 35000
//   "人气：1.2 亿"     → 120000000
//   "396"              → 396
//   "收藏：339"        → 339
// Returns 0 if no number is found.
func parseHotText(s string) int {
	if s == "" {
		return 0
	}
	// Find first run of digits (with optional decimal point).
	start, end := -1, -1
	for i, r := range s {
		if (r >= '0' && r <= '9') || (r == '.' && start >= 0) {
			if start < 0 {
				start = i
			}
			end = i + 1
		} else if start >= 0 {
			break
		}
	}
	if start < 0 {
		return 0
	}
	num, err := strconv.ParseFloat(s[start:end], 64)
	if err != nil {
		return 0
	}
	if strings.Contains(s, "万") {
		num *= 10000
	} else if strings.Contains(s, "亿") {
		num *= 100000000
	}
	return int(num)
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
	// Hot lives in the comic-status <span> whose label is "人气" — the first
	// <b> in comic-status is the tag-list block, NOT the popularity number.
	// Walk each span.text and look for the one starting with "人气".
	doc.Find("div.comic-status span.text").EachWithBreak(func(_ int, s *goquery.Selection) bool {
		txt := strings.TrimSpace(s.Text())
		if !strings.HasPrefix(txt, "人气") {
			return true // keep iterating
		}
		// Take the inner <b> if present; fall back to the span text.
		if b := s.Find("b").First(); b.Length() > 0 {
			meta.Hot = parseHotText(strings.TrimSpace(b.Text()))
		} else {
			meta.Hot = parseHotText(txt)
		}
		return false // found, stop
	})
	intro := doc.Find("div.comic-intro p")
	if intro.Length() >= 3 {
		meta.Description = strings.TrimSpace(intro.Eq(2).Text())
	} else if intro.Length() > 0 {
		meta.Description = strings.TrimSpace(intro.Last().Text())
	}

	var eps []EpisodeDTO
	doc.Find("ul.chapter__list-box li").Each(func(_ int, s *goquery.Selection) {
		href, _ := s.Find("a").Attr("href")
		href = strings.TrimSpace(href)
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
		url = strings.TrimSpace(url)
		pid, err := strconv.ParseInt(strings.TrimSpace(pidStr), 10, 64)
		if err != nil {
			return
		}
		idx, _ := strconv.Atoi(strings.TrimSpace(idxStr))
		imgs = append(imgs, ImageDTO{ID: pid, Index: idx, RawURL: url})
	})
	return imgs, nil
}
