package laptop

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

// Logos finds the image each repository uses as its own mark, for the phone
// to show beside the project's name. A repository's files rarely change, and
// finding its mark walks the tree, so a result is reused for TTL.
type Logos struct {
	Now func() time.Time
	TTL time.Duration

	mu   sync.Mutex
	seen map[string]foundLogo
}

type foundLogo struct {
	at  time.Time
	uri string
}

// Find returns the repository's mark as a data URI, or "" when it has none.
func (l *Logos) Find(dir string) string {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.Now()
	if f, ok := l.seen[dir]; ok && now.Sub(f.at) < l.TTL {
		return f.uri
	}
	if l.seen == nil {
		l.seen = map[string]foundLogo{}
	}
	uri := findLogo(dir)
	l.seen[dir] = foundLogo{at: now, uri: uri}
	return uri
}

// logoNames ranks the names a mark goes by. App icons come first: they are
// drawn square and meant to be seen small.
var logoNames = []string{
	"apple-touch-icon.png", "pwa-icon.png", "icon-512.png", "icon-192.png",
	"logo.svg", "logo.png", "icon.svg", "favicon.svg", "favicon.png",
}

var skipDirs = map[string]bool{
	"node_modules": true, "vendor": true, "dist": true, "build": true, "target": true,
	"testdata": true, "coverage": true, "test-results": true, "playwright-report": true,
}

const (
	logoDepth = 4
	logoSide  = 96
	// A data URI travels inside the project's document on every change.
	maxLogoBytes = 32 << 10
	// cmd/icon in a project made from the template writes this into the
	// placeholder it draws; the PNGs beside it are the same placeholder.
	moldMarker = "A mold: replace it with your own mark"
)

func findLogo(root string) string {
	type candidate struct {
		path        string
		rank, depth int
	}
	var found []candidate
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		depth := strings.Count(rel, string(filepath.Separator))
		if d.IsDir() {
			if path != root && (strings.HasPrefix(d.Name(), ".") || skipDirs[d.Name()] || depth >= logoDepth) {
				return filepath.SkipDir
			}
			return nil
		}
		if rank := slices.Index(logoNames, strings.ToLower(d.Name())); rank >= 0 {
			found = append(found, candidate{path, rank, depth})
		}
		return nil
	})
	slices.SortFunc(found, func(a, b candidate) int {
		if a.rank != b.rank {
			return a.rank - b.rank
		}
		if a.depth != b.depth {
			return a.depth - b.depth
		}
		return strings.Compare(a.path, b.path)
	})
	for _, c := range found {
		if isMold(filepath.Dir(c.path)) {
			continue
		}
		if uri := logoURI(c.path); uri != "" {
			return uri
		}
	}
	return ""
}

func isMold(dir string) bool {
	svg, err := os.ReadFile(filepath.Join(dir, "icon.svg"))
	return err == nil && bytes.Contains(svg, []byte(moldMarker))
}

func logoURI(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	mime := "image/svg+xml"
	if strings.HasSuffix(strings.ToLower(path), ".png") {
		img, err := png.Decode(bytes.NewReader(data))
		if err != nil {
			return ""
		}
		var buf bytes.Buffer
		if err := png.Encode(&buf, shrink(img, logoSide)); err != nil {
			return ""
		}
		data, mime = buf.Bytes(), "image/png"
	} else if !bytes.Contains(data, []byte("<svg")) {
		return ""
	}
	if len(data) > maxLogoBytes {
		return ""
	}
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data)
}

// shrink fits img within side×side by averaging the source pixels behind
// each destination pixel.
func shrink(img image.Image, side int) image.Image {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= side && h <= side {
		return img
	}
	scale := float64(side) / float64(max(w, h))
	dw := max(1, int(math.Round(float64(w)*scale)))
	dh := max(1, int(math.Round(float64(h)*scale)))
	out := image.NewNRGBA(image.Rect(0, 0, dw, dh))
	for y := range dh {
		y0, y1 := y*h/dh, max(y*h/dh+1, (y+1)*h/dh)
		for x := range dw {
			x0, x1 := x*w/dw, max(x*w/dw+1, (x+1)*w/dw)
			var r, g, bl, a, n uint64
			for sy := y0; sy < y1; sy++ {
				for sx := x0; sx < x1; sx++ {
					pr, pg, pb, pa := img.At(b.Min.X+sx, b.Min.Y+sy).RGBA()
					r, g, bl, a, n = r+uint64(pr), g+uint64(pg), bl+uint64(pb), a+uint64(pa), n+1
				}
			}
			avg := color.RGBA64{uint16(r / n), uint16(g / n), uint16(bl / n), uint16(a / n)}
			out.Set(x, y, color.NRGBAModel.Convert(avg))
		}
	}
	return out
}
