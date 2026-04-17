package imaging

import (
	"bytes"
	"errors"
	"image"
	"image/jpeg"
	"io"
	"math"

	"github.com/go-pdf/fpdf"
)

// ToPDF slices a tall image into A4 pages and writes PDF to out.
// Each page is scaled so the image width fills the page width.
func ToPDF(img image.Image, out io.Writer) error {
	if img == nil {
		return errors.New("nil image")
	}
	b := img.Bounds()
	iw, ih := b.Dx(), b.Dy()
	if iw <= 0 || ih <= 0 {
		return errors.New("empty image")
	}

	pdf := fpdf.New("P", "mm", "A4", "")
	pageW, pageH := 210.0, 297.0
	scale := pageW / float64(iw)
	sliceHeightPx := int(math.Floor(pageH / scale))
	if sliceHeightPx < 1 {
		sliceHeightPx = 1
	}
	pageCount := int(math.Ceil(float64(ih) / float64(sliceHeightPx)))

	for p := 0; p < pageCount; p++ {
		top := p * sliceHeightPx
		bottom := top + sliceHeightPx
		if bottom > ih {
			bottom = ih
		}
		if top >= bottom {
			continue
		}

		sub := image.NewRGBA(image.Rect(0, 0, iw, bottom-top))
		for y := top; y < bottom; y++ {
			for x := 0; x < iw; x++ {
				sub.Set(x, y-top, img.At(b.Min.X+x, b.Min.Y+y))
			}
		}
		var jbuf bytes.Buffer
		if err := jpeg.Encode(&jbuf, sub, &jpeg.Options{Quality: 85}); err != nil {
			return err
		}

		pdf.AddPage()
		pdf.RegisterImageOptionsReader(
			"s"+itoa(p),
			fpdf.ImageOptions{ImageType: "JPG"},
			bytes.NewReader(jbuf.Bytes()),
		)
		renderedH := float64(bottom-top) * scale
		pdf.ImageOptions("s"+itoa(p), 0, 0, pageW, renderedH, false,
			fpdf.ImageOptions{ImageType: "JPG"}, 0, "")
	}

	return pdf.Output(out)
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b [11]byte
	n := len(b)
	for i > 0 {
		n--
		b[n] = byte('0' + i%10)
		i /= 10
	}
	return string(b[n:])
}
