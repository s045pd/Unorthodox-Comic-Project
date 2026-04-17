package crawler

import (
	"context"
	"fmt"
	"strings"
)

// DownloadImage fetches bytes and returns (data, ext, error).
// Returns error if Content-Type does not start with "image/".
// ext is one of "jpg", "png", "webp", "gif" (fallback "jpg").
func (c *Client) DownloadImage(ctx context.Context, url string) ([]byte, string, error) {
	data, ct, err := c.GetBytes(ctx, url)
	if err != nil {
		return nil, "", err
	}
	if !strings.HasPrefix(ct, "image/") {
		return nil, "", fmt.Errorf("not an image: %q", ct)
	}
	return data, extFromContentType(ct), nil
}

func extFromContentType(ct string) string {
	ct = strings.ToLower(ct)
	switch {
	case strings.Contains(ct, "jpeg"), strings.Contains(ct, "jpg"):
		return "jpg"
	case strings.Contains(ct, "png"):
		return "png"
	case strings.Contains(ct, "webp"):
		return "webp"
	case strings.Contains(ct, "gif"):
		return "gif"
	default:
		return "jpg"
	}
}

// ExtFromMagic detects image format from first bytes; used by the migration
// tool when content-type is unknown. Returns "" if not recognized.
func ExtFromMagic(b []byte) string {
	if len(b) < 4 {
		return ""
	}
	switch {
	case b[0] == 0xFF && b[1] == 0xD8:
		return "jpg"
	case b[0] == 0x89 && b[1] == 0x50 && b[2] == 0x4E && b[3] == 0x47:
		return "png"
	case len(b) >= 12 && string(b[0:4]) == "RIFF" && string(b[8:12]) == "WEBP":
		return "webp"
	case len(b) >= 6 && (string(b[0:6]) == "GIF87a" || string(b[0:6]) == "GIF89a"):
		return "gif"
	}
	return ""
}
