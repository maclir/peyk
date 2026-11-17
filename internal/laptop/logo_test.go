package laptop

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func pngOf(t *testing.T, side int, c color.Color) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, side, side))
	for y := range side {
		for x := range side {
			img.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func decodeURI(t *testing.T, uri string) (string, []byte) {
	t.Helper()
	mime, data, ok := strings.Cut(strings.TrimPrefix(uri, "data:"), ";base64,")
	if !ok {
		t.Fatalf("not a data URI: %.40q", uri)
	}
	raw, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		t.Fatal(err)
	}
	return mime, raw
}

func TestFindLogoPrefersAppIconsAndSkipsPlaceholders(t *testing.T) {
	root := t.TempDir()
	svg := []byte(`<svg xmlns="http://www.w3.org/2000/svg"/>`)
	writeFile(t, filepath.Join(root, "repo", "logo.svg"), svg)
	writeFile(t, filepath.Join(root, "repo", "node_modules", "x", "apple-touch-icon.png"), pngOf(t, 8, color.Black))
	writeFile(t, filepath.Join(root, "repo", "static", "apple-touch-icon.png"), pngOf(t, 180, color.NRGBA{0x2f, 0x54, 0xc8, 0xff}))

	mime, raw := decodeURI(t, findLogo(filepath.Join(root, "repo")))
	if mime != "image/png" {
		t.Fatalf("mime = %s, want the app icon", mime)
	}
	img, err := png.Decode(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if b := img.Bounds(); b.Dx() != logoSide || b.Dy() != logoSide {
		t.Fatalf("logo is %v, want %d square", b, logoSide)
	}
	if r, g, b, _ := img.At(10, 10).RGBA(); r>>8 != 0x2f || g>>8 != 0x54 || b>>8 != 0xc8 {
		t.Fatalf("colour changed in shrinking: %x %x %x", r>>8, g>>8, b>>8)
	}

	// The template's placeholder is not the project's mark.
	writeFile(t, filepath.Join(root, "repo", "static", "icon.svg"), []byte("<!-- "+moldMarker+" --><svg/>"))
	if mime, raw := decodeURI(t, findLogo(filepath.Join(root, "repo"))); mime != "image/svg+xml" || !bytes.Equal(raw, svg) {
		t.Fatalf("got %s %q, want logo.svg", mime, raw)
	}

	bare := filepath.Join(root, "bare")
	writeFile(t, filepath.Join(bare, "node_modules", "x", "apple-touch-icon.png"), pngOf(t, 8, color.Black))
	if got := findLogo(bare); got != "" {
		t.Fatalf("a dependency's icon is not the project's: %.40q", got)
	}
}

func TestLogosReuseAFindUntilItIsStale(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC)
	l := &Logos{Now: func() time.Time { return now }, TTL: time.Minute}
	if l.Find(dir) != "" {
		t.Fatal("empty repository has a logo")
	}
	writeFile(t, filepath.Join(dir, "logo.svg"), []byte("<svg/>"))
	if l.Find(dir) != "" {
		t.Fatal("a fresh result was not reused")
	}
	now = now.Add(time.Minute)
	if l.Find(dir) == "" {
		t.Fatal("a stale result was reused")
	}
}

func TestFindLogoTakesAFileNamedAfterTheRepository(t *testing.T) {
	root := filepath.Join(t.TempDir(), "eqo")
	writeFile(t, filepath.Join(root, "eqo.png"), pngOf(t, 1024, color.NRGBA{0x10, 0x80, 0x40, 0xff}))
	writeFile(t, filepath.Join(root, "other.png"), pngOf(t, 8, color.Black))

	mime, raw := decodeURI(t, findLogo(root))
	if mime != "image/png" {
		t.Fatalf("mime = %s", mime)
	}
	img, err := png.Decode(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if r, g, b, _ := img.At(5, 5).RGBA(); r>>8 != 0x10 || g>>8 != 0x80 || b>>8 != 0x40 {
		t.Fatalf("got colour %x %x %x, want eqo.png", r>>8, g>>8, b>>8)
	}
}

func TestFindLogoFallsBackToAPagesInlineFavicon(t *testing.T) {
	root := t.TempDir()
	page := `<html><head>
<link rel="stylesheet" href="data:text/css,a{}">
<link rel="icon" type="image/svg+xml" href="data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg'%3E%3Crect fill='%23333'/%3E%3C/svg%3E">
</head></html>`
	writeFile(t, filepath.Join(root, "internal", "templates", "dashboard.html"), []byte(page))

	mime, raw := decodeURI(t, findLogo(root))
	if want := `<svg xmlns='http://www.w3.org/2000/svg'><rect fill='#333'/></svg>`; mime != "image/svg+xml" || string(raw) != want {
		t.Fatalf("got %s %q, want %q", mime, raw, want)
	}

	// An image file is the project's mark before a browser tab's icon.
	svg := []byte(`<svg xmlns="http://www.w3.org/2000/svg"/>`)
	writeFile(t, filepath.Join(root, "logo.svg"), svg)
	if _, raw := decodeURI(t, findLogo(root)); !bytes.Equal(raw, svg) {
		t.Fatalf("got %q, want logo.svg", raw)
	}
}
