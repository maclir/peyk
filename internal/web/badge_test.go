package web

import (
	"image"
	_ "image/png"
	"os"
	"regexp"
	"testing"
)

// Android renders the notification badge from its alpha channel only, so an
// opaque image shows as a solid white square in the status bar.
func TestNotificationBadgeIsAlphaMask(t *testing.T) {
	sw, err := os.ReadFile("static/sw.js")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`badge: "/static/([^"]+\.png)"`).FindSubmatch(sw)
	if m == nil {
		t.Fatal("sw.js does not set a PNG notification badge")
	}
	f, err := os.Open("static/" + string(m[1]))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	b := img.Bounds()
	if b.Dx() != b.Dy() || b.Dx() < 72 {
		t.Fatalf("badge is %dx%d; want a square of at least 72px", b.Dx(), b.Dy())
	}
	var transparent, opaque int
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			switch _, _, _, a := img.At(x, y).RGBA(); a {
			case 0:
				transparent++
			case 0xffff:
				opaque++
			}
		}
	}
	if total := b.Dx() * b.Dy(); transparent < total/4 || opaque == 0 {
		t.Fatalf("badge has %d transparent and %d opaque of %d pixels; want a glyph on a transparent background", transparent, opaque, total)
	}
}
