// Command icongen draws Kith's icons from the brand geometry
// (branding/geometry.json): two offset half-rings, the two machines, passing
// a dot, the task, between them. Run from gui/: go run ./build/icongen
//
// It writes every raster the apps ship: the app icon (PNG, .ico, .icns),
// the tray icons and the web favicon. Sizes of 32 px and below use the
// heavier small cut so the gaps don't close up.
package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"os/exec"
	"path/filepath"
)

type rgba struct{ r, g, b, a float64 }

// over composites src (straight alpha, coverage c) onto dst.
func over(dst rgba, src rgba, c float64) rgba {
	a := src.a * c
	oa := a + dst.a*(1-a)
	if oa == 0 {
		return rgba{}
	}
	return rgba{(src.r*a + dst.r*dst.a*(1-a)) / oa, (src.g*a + dst.g*dst.a*(1-a)) / oa,
		(src.b*a + dst.b*dst.a*(1-a)) / oa, oa}
}

func hex(h uint32) rgba {
	return rgba{float64(h>>16&0xff) / 255, float64(h>>8&0xff) / 255, float64(h&0xff) / 255, 1}
}

// geom is the mark in a 256 box centred at (128,128); see branding/geometry.json.
type geom struct{ dx, dy, R, W, rd, s float64 }

var (
	mainCut  = geom{dx: 34, dy: 20, R: 54, W: 36, rd: 20, s: 0.97}
	smallCut = geom{dx: 40, dy: 16, R: 50, W: 44, rd: 21, s: 0.9}
)

func inCircle(x, y, cx, cy, r float64) bool { return (x-cx)*(x-cx)+(y-cy)*(y-cy) <= r*r }

// inHalf: a half-ring band of centre radius r and width w around (cx,cy),
// the left half (side -1) or right half (side +1), with round ends.
func inHalf(x, y, cx, cy, r, w, side float64) bool {
	h := w / 2
	if (x-cx)*side >= 0 {
		d := math.Hypot(x-cx, y-cy)
		if math.Abs(d-r) <= h {
			return true
		}
	}
	return inCircle(x, y, cx, cy-r, h) || inCircle(x, y, cx, cy+r, h)
}

// part reports which part of the mark covers (u,v) in the 256 box:
// 0 none, 1 a half, 2 the dot.
func (g geom) part(u, v float64) int {
	s := g.s
	if inCircle(u, v, 128, 128, g.rd*s) {
		return 2
	}
	if inHalf(u, v, 128-g.dx*s, 128-g.dy*s, g.R*s, g.W*s, -1) ||
		inHalf(u, v, 128+g.dx*s, 128+g.dy*s, g.R*s, g.W*s, 1) {
		return 1
	}
	return 0
}

type style struct {
	tile   *rgba   // rounded-square background; nil for a bare mark
	inset  float64 // tile inset, fraction of the size
	radius float64 // tile corner radius, fraction of the size
	scale  float64 // mark size relative to the full box
	halves rgba
	dot    rgba
	badge  *rgba // a status dot at the lower right, cut out of what's under it
	small  bool  // force the small cut (icons shown at 16-22 pt)
}

func inRoundRect(x, y, inset, radius float64) bool {
	lo, hi := inset, 1-inset
	if x < lo || x > hi || y < lo || y > hi {
		return false
	}
	cx := math.Max(lo+radius, math.Min(x, hi-radius))
	cy := math.Max(lo+radius, math.Min(y, hi-radius))
	return (x-cx)*(x-cx)+(y-cy)*(y-cy) <= radius*radius
}

func draw(size int, st style) *image.NRGBA {
	g := mainCut
	if size <= 32 || st.small {
		g = smallCut
	}
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	const ss = 5 // supersampling per axis
	const bx, by, br, bgap = 0.80, 0.80, 0.19, 0.07
	for py := 0; py < size; py++ {
		for px := 0; px < size; px++ {
			var acc rgba
			for sy := 0; sy < ss; sy++ {
				for sx := 0; sx < ss; sx++ {
					x := (float64(px) + (float64(sx)+0.5)/ss) / float64(size)
					y := (float64(py) + (float64(sy)+0.5)/ss) / float64(size)
					c := rgba{}
					if st.tile != nil && inRoundRect(x, y, st.inset, st.radius) {
						c = over(c, *st.tile, 1)
					}
					// Mark coordinates: scale about the centre into the 256 box.
					u := 128 + (x-0.5)*256/st.scale
					v := 128 + (y-0.5)*256/st.scale
					switch g.part(u, v) {
					case 1:
						c = over(c, st.halves, 1)
					case 2:
						c = over(c, st.dot, 1)
					}
					if st.badge != nil {
						if inCircle(x, y, bx, by, br) {
							c = *st.badge
						} else if inCircle(x, y, bx, by, br+bgap) {
							c = rgba{}
						}
					}
					acc.r += c.r * c.a
					acc.g += c.g * c.a
					acc.b += c.b * c.a
					acc.a += c.a
				}
			}
			n := float64(ss * ss)
			a := acc.a / n
			var r, gg, b float64
			if acc.a > 0 {
				r, gg, b = acc.r/acc.a, acc.g/acc.a, acc.b/acc.a
			}
			img.SetNRGBA(px, py, color.NRGBA{uint8(r*255 + .5), uint8(gg*255 + .5), uint8(b*255 + .5), uint8(a*255 + .5)})
		}
	}
	return img
}

func encode(img image.Image) []byte {
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		panic(err)
	}
	return b.Bytes()
}

func write(path string, data []byte) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		panic(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		panic(err)
	}
}

// ico packs PNG-compressed images (Vista and later read these).
func ico(imgs map[int][]byte, sizes []int) []byte {
	var b bytes.Buffer
	le := binary.LittleEndian
	binary.Write(&b, le, [3]uint16{0, 1, uint16(len(sizes))})
	off := 6 + 16*len(sizes)
	for _, s := range sizes {
		d := uint8(s)
		if s >= 256 {
			d = 0
		}
		b.Write([]byte{d, d, 0, 0})
		binary.Write(&b, le, [2]uint16{1, 32})
		binary.Write(&b, le, [2]uint32{uint32(len(imgs[s])), uint32(off)})
		off += len(imgs[s])
	}
	for _, s := range sizes {
		b.Write(imgs[s])
	}
	return b.Bytes()
}

// icns builds the macOS icon with iconutil, which writes the small sizes in
// the formats Finder expects. Elsewhere the committed .icns is kept.
func icns(path string, px map[int][]byte) {
	tool, err := exec.LookPath("iconutil")
	if err != nil {
		fmt.Fprintln(os.Stderr, "icongen: iconutil not found; keeping", path)
		return
	}
	dir, err := os.MkdirTemp("", "kith-icon")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(dir)
	set := filepath.Join(dir, "icon.iconset")
	for _, s := range []int{16, 32, 128, 256, 512} {
		write(filepath.Join(set, fmt.Sprintf("icon_%dx%d.png", s, s)), px[s])
		write(filepath.Join(set, fmt.Sprintf("icon_%dx%d@2x.png", s, s)), px[2*s])
	}
	if out, err := exec.Command(tool, "-c", "icns", set, "-o", path).CombinedOutput(); err != nil {
		panic(fmt.Sprintf("iconutil: %v: %s", err, out))
	}
}

func main() {
	teal, white := hex(0x0f8c8c), hex(0xffffff)
	amber, grey := hex(0xe8890c), hex(0x8a9399)
	black := rgba{0, 0, 0, 1}

	// App icon: a teal tile with a white mark. The tile follows the macOS
	// grid (824 of 1024, corner 185); small sizes fill more of the square.
	app := func(size int) []byte {
		st := style{tile: &teal, inset: 100.0 / 1024, radius: 185.0 / 1024, scale: 0.6, halves: white, dot: white}
		if size <= 32 {
			st.inset, st.radius, st.scale = 0.03, 0.2, 0.84
		} else if size <= 64 {
			st.inset, st.radius, st.scale = 0.06, 0.2, 0.7
		}
		return encode(draw(size, st))
	}
	px := map[int][]byte{}
	for _, s := range []int{16, 24, 32, 48, 64, 128, 256, 512, 1024} {
		px[s] = app(s)
	}
	write("build/appicon.png", px[1024])
	write("build/windows/icon.ico", ico(px, []int{16, 24, 32, 48, 64, 256}))
	icns("build/darwin/icons.icns", px)

	// Web favicon: the app tile, legible on light and dark tab strips.
	write("frontend/public/icon.png", px[64])

	// macOS menu bar: monochrome template (the system tints it).
	tmpl := style{scale: 1, halves: black, dot: black, small: true}
	write("icons/tray-template.png", encode(draw(44, tmpl)))
	tmpl.badge = &black
	write("icons/tray-template-pending.png", encode(draw(44, tmpl)))

	// Windows / Linux trays: the app tile, legible on light and dark taskbars.
	tray := style{tile: &teal, inset: 0.03, radius: 0.22, scale: 0.84, halves: white, dot: white, small: true}
	write("icons/tray.png", encode(draw(64, tray)))
	pending := tray
	pending.badge = &amber
	write("icons/tray-pending.png", encode(draw(64, pending)))
	off := tray
	off.tile = &grey
	write("icons/tray-off.png", encode(draw(64, off)))
}
