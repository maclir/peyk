package web

import (
	"image"
	_ "image/png"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/maclir/peyk/internal/notify"
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

// Every event icon a note names must be served, and stay transparent at its
// edge so the shade, not a square, shows behind the mark.
func TestNotificationIconsAreServedMarks(t *testing.T) {
	for _, path := range []string{notify.IconNeeds, notify.IconDone, notify.IconError, notify.IconCI} {
		f, err := staticFS.Open(strings.TrimPrefix(path, "/"))
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		img, _, err := image.Decode(f)
		f.Close()
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		b := img.Bounds()
		if _, _, _, a := img.At(b.Min.X, b.Min.Y).RGBA(); a != 0 {
			t.Errorf("%s has an opaque corner", path)
		}
	}
}
