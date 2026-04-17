package imaging

import (
	"bytes"
	"errors"
	"image"
	"image/draw"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"log/slog"

	_ "golang.org/x/image/webp"
)

var ErrNoImages = errors.New("no images provided")

// CombineImages decodes each byte slice and stacks vertically left-aligned.
// Broken images are skipped with a log warning. Returns ErrNoImages if all
// inputs were broken or input was empty.
func CombineImages(inputs [][]byte) (image.Image, error) {
	if len(inputs) == 0 {
		return nil, ErrNoImages
	}

	type decoded struct {
		img  image.Image
		w, h int
	}
	var parts []decoded
	var maxW, totalH int

	for i, raw := range inputs {
		img, _, err := image.Decode(bytes.NewReader(raw))
		if err != nil {
			slog.Warn("combine: skipping broken image", "index", i, "err", err)
			continue
		}
		b := img.Bounds()
		w, h := b.Dx(), b.Dy()
		parts = append(parts, decoded{img: img, w: w, h: h})
		if w > maxW {
			maxW = w
		}
		totalH += h
	}
	if len(parts) == 0 {
		return nil, ErrNoImages
	}
	if totalH > 30000 {
		slog.Warn("combined image very tall", "height", totalH)
	}

	dst := image.NewRGBA(image.Rect(0, 0, maxW, totalH))
	y := 0
	for _, p := range parts {
		r := image.Rect(0, y, p.w, y+p.h)
		draw.Draw(dst, r, p.img, image.Point{}, draw.Src)
		y += p.h
	}
	return dst, nil
}
