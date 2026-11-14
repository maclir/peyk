// Command icon draws this project's mark at every size a browser or an app
// launcher asks for.
//
// The mark is a placeholder and is meant to be replaced: a mold, an outline
// around the space the product goes. What is not a placeholder is the shape of
// this file. A launcher needs PNGs — iOS will not take an SVG for a home screen
// icon — and generating them here means building the app never requires a
// rasteriser, a design tool, or a binary nobody can diff.
//
// Every coordinate is a multiple of four on a 32-unit grid, so at 16 pixels
// each edge still lands on a whole one and the mark is drawn, not blurred.
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

	markX = 4.0 // the mold: a rounded square with the middle taken out
	markW = 24.0
	markR = 8.0
	holeR = 6.0

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
	ink         = color.NRGBA{0x20, 0x21, 0x24, 0xff} // the page's only colour
	inkDark     = color.NRGBA{0xe8, 0xea, 0xed, 0xff} // its inverse, for a dark tab strip
	white       = color.NRGBA{0xff, 0xff, 0xff, 0xff}
	transparent = color.NRGBA{}
)

// A shape answers whether a point in grid space falls inside it.
type shape func(x, y float64) bool

func main() {
	out := flag.String("out", filepath.Join("internal", "web", "static"), "directory to write into")
	flag.Parse()

	// The favicon is the mark alone, so it reads on whatever colour the browser
	// puts behind a tab. Everything a launcher shows is white on ink, because
	// there it is a tile on somebody's wallpaper and needs its own ground.
	write(filepath.Join(*out, "icon.svg"), markSVG())
	write(filepath.Join(*out, "favicon.ico"), ico(16, 32, 48))
	write(filepath.Join(*out, "icon-192.png"), encode(draw(192, roundedTile(tileR), ink, white, tileScale)))
	write(filepath.Join(*out, "icon-512.png"), encode(draw(512, roundedTile(tileR), ink, white, tileScale)))
	write(filepath.Join(*out, "icon-maskable-512.png"), encode(draw(512, square(), ink, white, maskableScale)))
	// iOS rounds the corners itself, so this one is handed over square.
	write(filepath.Join(*out, "apple-touch-icon.png"), encode(draw(180, square(), ink, white, tileScale)))
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

func mark() shape {
	return without(roundRect(markX, markX, markW, markW, markR), circle(grid/2, grid/2, holeR))
}

func circle(cx, cy, r float64) shape {
	return func(x, y float64) bool {
		dx, dy := x-cx, y-cy
		return dx*dx+dy*dy <= r*r
	}
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
// one. The hole is a mask rather than a traced outline, so this file and the
// rasteriser above cannot drift into drawing two different shapes.
func markSVG() []byte {
	return []byte(fmt.Sprintf(`<!-- Generated by cmd/icon; run `+"`make icons`"+`. A mold: replace it with your own mark. -->
<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %g %g" fill="%s">
<style>@media (prefers-color-scheme: dark) { svg { fill: %s } }</style>
<mask id="hole">
<rect width="%g" height="%g" fill="#fff"/>
<circle cx="%g" cy="%g" r="%g" fill="#000"/>
</mask>
<rect x="%g" y="%g" width="%g" height="%g" rx="%g" mask="url(#hole)"/>
</svg>
`,
		grid, grid, hex(ink), hex(inkDark),
		grid, grid,
		grid/2, grid/2, holeR,
		markX, markX, markW, markW, markR))
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
