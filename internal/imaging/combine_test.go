package imaging

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"
)

func makePNG(t *testing.T, w, h int, c color.Color) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestCombineImages(t *testing.T) {
	a := makePNG(t, 200, 300, color.RGBA{255, 0, 0, 255})
	b := makePNG(t, 200, 300, color.RGBA{0, 255, 0, 255})
	c := makePNG(t, 200, 300, color.RGBA{0, 0, 255, 255})

	out, err := CombineImages([][]byte{a, b, c})
	if err != nil {
		t.Fatal(err)
	}
	bounds := out.Bounds()
	if bounds.Dx() != 200 || bounds.Dy() != 900 {
		t.Errorf("bounds = %v, want 200x900", bounds)
	}
}

func TestCombineImages_SkipsBroken(t *testing.T) {
	good := makePNG(t, 100, 100, color.RGBA{0, 0, 0, 255})
	out, err := CombineImages([][]byte{{0x00, 0x01}, good})
	if err != nil {
		t.Fatal(err)
	}
	if out.Bounds().Dy() != 100 {
		t.Errorf("expected skipped broken image; got height %d", out.Bounds().Dy())
	}
}

func TestCombineImages_EmptyInput(t *testing.T) {
	_, err := CombineImages(nil)
	if err == nil {
		t.Error("expected error for nil input")
	}
}
