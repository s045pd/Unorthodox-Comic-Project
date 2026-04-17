package crawler

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
)

type Client struct {
	http   *http.Client
	origin string
}

func NewClient(origin string, timeout time.Duration) *Client {
	tr := &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 20,
		IdleConnTimeout:     60 * time.Second,
	}
	return &Client{
		http:   &http.Client{Transport: tr, Timeout: timeout},
		origin: origin,
	}
}

const userAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:91.0) Gecko/20100101 Firefox/91.0"

const (
	maxHTMLBytes  = 5 * 1024 * 1024  // 5MB
	maxImageBytes = 50 * 1024 * 1024 // 50MB
)

func (c *Client) do(ctx context.Context, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Referer", c.origin+"/")
	req.Header.Set("Accept-Language", "en-GB,en;q=0.9,zh-CN;q=0.8,zh;q=0.7")
	return c.http.Do(req)
}

func (c *Client) GetHTML(ctx context.Context, url string) (string, error) {
	resp, err := c.do(ctx, url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("status %d for %s", resp.StatusCode, url)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxHTMLBytes+1))
	if err != nil {
		return "", err
	}
	if len(b) > maxHTMLBytes {
		return "", fmt.Errorf("HTML response exceeds %d bytes", maxHTMLBytes)
	}
	return string(b), nil
}

// GetBytes fetches raw bytes and returns (body, content-type, err).
func (c *Client) GetBytes(ctx context.Context, url string) ([]byte, string, error) {
	resp, err := c.do(ctx, url)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("status %d for %s", resp.StatusCode, url)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxImageBytes+1))
	if err != nil {
		return nil, "", err
	}
	if len(b) > maxImageBytes {
		return nil, "", fmt.Errorf("image response exceeds %d bytes", maxImageBytes)
	}
	return b, resp.Header.Get("Content-Type"), nil
}

func (c *Client) Origin() string { return c.origin }
