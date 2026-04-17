package imaging

import (
	"bytes"
	"image"
	"image/color"
	"testing"
)

func TestToPDF_MultiPage(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 800, 2400))
	for y := 0; y < 2400; y++ {
		for x := 0; x < 800; x++ {
			img.Set(x, y, color.RGBA{uint8(x), uint8(y), 128, 255})
		}
	}
	var buf bytes.Buffer
	if err := ToPDF(img, &buf); err != nil {
		t.Fatal(err)
	}
	if buf.Len() < 100 {
		t.Fatalf("PDF too small: %d bytes", buf.Len())
	}
	if !bytes.HasPrefix(buf.Bytes(), []byte("%PDF-")) {
		t.Error("output is not a PDF")
	}
}

func TestToPDF_Empty(t *testing.T) {
	var buf bytes.Buffer
	err := ToPDF(nil, &buf)
	if err == nil {
		t.Error("expected error for nil image")
	}
}
