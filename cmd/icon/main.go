// Command icon draws this project's mark at every size a browser or an app
// launcher asks for.
//
// The mark is a chat bubble whose three dots are set the way پ (pe) carries
// them, two above one: peyk is پیک, a messenger. A launcher needs PNGs — iOS
// will not take an SVG for a home screen icon — and generating them here means
// building the app never requires a rasteriser, a design tool, or a binary
// nobody can diff.
//
// The output is checked in and regenerated with `make icons`.
package main

import (
	"bytes"
	"encoding/binary"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"log"
	"math"
	"os"
	"path/filepath"
)

const (
	grid = 32.0 // the design grid every coordinate below is expressed in

	// The bubble, its tail, and the dots cut out of it, on a 32-unit grid.
	bubbleX, bubbleY, bubbleW, bubbleH, bubbleR = 3.0, 3.0, 26.0, 21.0, 8.0
	dotR                                        = 2.7 // half the diagonal of each diamond

	tileR = 7.0 // corner radius of the app tile, a shade over a fifth of the side

	// A launcher tile wants more air around the mark than a favicon does, where
	// every pixel of the sixteen counts.
	tileScale = 0.82

	// A maskable icon may be cropped to the circle that fits inside the middle
	// 80% of the square. The mark's corners sit further out than that, so it is
	// drawn smaller in that one file.
	maskableScale = 0.75

	samples = 8 // supersampling per axis
)

var (
	ink         = color.NRGBA{0x18, 0x21, 0x2e, 0xff} // the page's text colour
	inkDark     = color.NRGBA{0xe4, 0xe9, 0xf1, 0xff} // its dark-mode text, for a dark tab strip
	accent      = color.NRGBA{0x2f, 0x54, 0xc8, 0xff} // the page's accent, the launcher tile
	white       = color.NRGBA{0xff, 0xff, 0xff, 0xff}
	transparent = color.NRGBA{}
)

// A shape answers whether a point in grid space falls inside it.
type shape func(x, y float64) bool

func main() {
	out := flag.String("out", filepath.Join("internal", "web", "static"), "directory to write into")
	flag.Parse()

	// The favicon is the mark alone, so it reads on whatever colour the browser
	// puts behind a tab. Everything a launcher shows is white on the accent,
	// because there it is a tile on somebody's wallpaper and needs its own
	// ground.
	write(filepath.Join(*out, "icon.svg"), markSVG())
	write(filepath.Join(*out, "favicon.ico"), ico(16, 32, 48))
	write(filepath.Join(*out, "icon-192.png"), encode(draw(192, roundedTile(tileR), accent, white, tileScale)))
	write(filepath.Join(*out, "icon-512.png"), encode(draw(512, roundedTile(tileR), accent, white, tileScale)))
	write(filepath.Join(*out, "icon-maskable-512.png"), encode(draw(512, square(), accent, white, maskableScale)))
	// iOS rounds the corners itself, so this one is handed over square.
	write(filepath.Join(*out, "apple-touch-icon.png"), encode(draw(180, square(), accent, white, tileScale)))
	// Android draws a notification's status-bar badge from its alpha channel
	// alone, so an opaque tile there is a solid white square.
	write(filepath.Join(*out, "notification-badge.png"), encode(draw(96, nothing(), transparent, white, 1)))
}

// draw renders one icon: a background, then the mark laid over it. scale shrinks
// the mark about the centre of the grid without moving the background.
func draw(size int, bg shape, bgColor, fgColor color.NRGBA, scale float64) *image.NRGBA {
	fg := mark()
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	unit := grid / float64(size)

	for py := 0; py < size; py++ {
		for px := 0; px < size; px++ {
			var bgHits, fgHits int
			for sy := 0; sy < samples; sy++ {
				for sx := 0; sx < samples; sx++ {
					x := (float64(px) + (float64(sx)+0.5)/samples) * unit
					y := (float64(py) + (float64(sy)+0.5)/samples) * unit
					if bg(x, y) {
						bgHits++
					}
					if fg(grid/2+(x-grid/2)/scale, grid/2+(y-grid/2)/scale) {
						fgHits++
					}
				}
			}
			total := float64(samples * samples)
			img.SetNRGBA(px, py, over(fgColor, float64(fgHits)/total, bgColor, float64(bgHits)/total))
		}
	}
	return img
}

// over composites fg at coverage fa onto bg at coverage ba, both unpremultiplied.
func over(fg color.NRGBA, fa float64, bg color.NRGBA, ba float64) color.NRGBA {
	fa *= float64(fg.A) / 255
	ba *= float64(bg.A) / 255
	a := fa + ba*(1-fa)
	if a == 0 {
		return transparent
	}
	mix := func(f, b uint8) uint8 {
		v := (float64(f)*fa + float64(b)*ba*(1-fa)) / a
		return uint8(math.Round(math.Min(255, math.Max(0, v))))
	}
	return color.NRGBA{mix(fg.R, bg.R), mix(fg.G, bg.G), mix(fg.B, bg.B), uint8(math.Round(a * 255))}
}

// The tail and the dots as points, shared by the rasteriser and the SVG so the
// two cannot draw different shapes.
var (
	tail = [3][2]float64{{6.2, 20.5}, {5.2, 29.4}, {15.8, 22}}
	dots = [3][2]float64{{12.6, 10.6}, {19.4, 10.6}, {16, 16.6}}
)

func mark() shape {
	bubble := union(roundRect(bubbleX, bubbleY, bubbleW, bubbleH, bubbleR), triangle(tail))
	holes := nothing()
	for _, d := range dots {
		holes = union(holes, diamond(d[0], d[1], dotR))
	}
	return without(bubble, holes)
}

func diamond(cx, cy, r float64) shape {
	return func(x, y float64) bool { return math.Abs(x-cx)+math.Abs(y-cy) <= r }
}

func triangle(p [3][2]float64) shape {
	side := func(a, b [2]float64, x, y float64) float64 {
		return (b[0]-a[0])*(y-a[1]) - (b[1]-a[1])*(x-a[0])
	}
	return func(x, y float64) bool {
		d1, d2, d3 := side(p[0], p[1], x, y), side(p[1], p[2], x, y), side(p[2], p[0], x, y)
		return !((d1 < 0 || d2 < 0 || d3 < 0) && (d1 > 0 || d2 > 0 || d3 > 0))
	}
}

func union(a, b shape) shape {
	return func(x, y float64) bool { return a(x, y) || b(x, y) }
}

func square() shape { return func(x, y float64) bool { return true } }

func roundedTile(r float64) shape { return roundRect(0, 0, grid, grid, r) }

func roundRect(x, y, w, h, r float64) shape {
	return func(px, py float64) bool {
		if px < x || py < y || px > x+w || py > y+h {
			return false
		}
		// Distance to the rectangle the corners are swept around.
		cx := clamp(px, x+r, x+w-r)
		cy := clamp(py, y+r, y+h-r)
		dx, dy := px-cx, py-cy
		return dx*dx+dy*dy <= r*r
	}
}

func without(outer, inner shape) shape {
	return func(x, y float64) bool { return outer(x, y) && !inner(x, y) }
}

func clamp(v, lo, hi float64) float64 { return math.Min(hi, math.Max(lo, v)) }

// markSVG is the favicon browsers prefer, and the one place the mark stays
// resolution-free. It carries both inks so a dark tab strip gets the lighter
// one. The dots are a mask rather than a traced outline, so this file and the
// rasteriser above cannot drift into drawing two different shapes.
func markSVG() []byte {
	var holes, tailPath bytes.Buffer
	for _, d := range dots {
		fmt.Fprintf(&holes, "<path d=\"M%.1f %.1f L%.1f %.1f L%.1f %.1f L%.1f %.1f Z\" fill=\"#000\"/>\n",
			d[0], d[1]-dotR, d[0]+dotR, d[1], d[0], d[1]+dotR, d[0]-dotR, d[1])
	}
	fmt.Fprintf(&tailPath, "M%g %g L%g %g L%g %g Z", tail[0][0], tail[0][1], tail[1][0], tail[1][1], tail[2][0], tail[2][1])
	return []byte(fmt.Sprintf(`<!-- Generated by cmd/icon; run `+"`make icons`"+`. -->
<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %g %g" fill="%s">
<style>@media (prefers-color-scheme: dark) { svg { fill: %s } }</style>
<mask id="dots">
<rect width="%g" height="%g" fill="#fff"/>
%s</mask>
<g mask="url(#dots)">
<rect x="%g" y="%g" width="%g" height="%g" rx="%g"/>
<path d="%s"/>
</g>
</svg>
`,
		grid, grid, hex(ink), hex(inkDark),
		grid, grid, holes.String(),
		bubbleX, bubbleY, bubbleW, bubbleH, bubbleR, tailPath.String()))
}

func hex(c color.NRGBA) string { return fmt.Sprintf("#%02x%02x%02x", c.R, c.G, c.B) }

// ico packs the mark at several sizes into the one file Windows and older
// browsers still ask for by name. Each entry is a PNG, which every browser that
// matters has understood since Vista.
func ico(sizes ...int) []byte {
	var dir, body bytes.Buffer
	offset := 6 + 16*len(sizes)

	for _, size := range sizes {
		frame := encode(draw(size, nothing(), transparent, ink, 1))
		dir.Write([]byte{byte(size), byte(size), 0, 0})
		binary.Write(&dir, binary.LittleEndian, uint16(1))          // colour planes
		binary.Write(&dir, binary.LittleEndian, uint16(32))         // bits per pixel
		binary.Write(&dir, binary.LittleEndian, uint32(len(frame))) // size of this image
		binary.Write(&dir, binary.LittleEndian, uint32(offset))     // where it starts
		body.Write(frame)
		offset += len(frame)
	}

	var out bytes.Buffer
	binary.Write(&out, binary.LittleEndian, uint16(0)) // reserved
	binary.Write(&out, binary.LittleEndian, uint16(1)) // an icon, not a cursor
	binary.Write(&out, binary.LittleEndian, uint16(len(sizes)))
	out.Write(dir.Bytes())
	out.Write(body.Bytes())
	return out.Bytes()
}

func nothing() shape { return func(x, y float64) bool { return false } }

func encode(img image.Image) []byte {
	var buf bytes.Buffer
	enc := png.Encoder{CompressionLevel: png.BestCompression}
	if err := enc.Encode(&buf, img); err != nil {
		log.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}

func write(path string, data []byte) {
	if err := os.WriteFile(path, data, 0o644); err != nil {
		log.Fatalf("write %s: %v", path, err)
	}
	fmt.Printf("%s (%d bytes)\n", path, len(data))
}
