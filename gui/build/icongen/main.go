// Command icongen draws Kith's icons: two linked nodes (two machines) on a
// rounded square. Run from gui/: go run ./build/icongen
package main

import (
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
)

type rgba struct{ r, g, b, a float64 }

func mix(a, b rgba, t float64) rgba {
	return rgba{a.r + (b.r-a.r)*t, a.g + (b.g-a.g)*t, a.b + (b.b-a.b)*t, a.a + (b.a-a.a)*t}
}

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

// Shapes in unit coordinates (0..1).
func inRoundRect(x, y, inset, radius float64) bool {
	lo, hi := inset, 1-inset
	if x < lo || x > hi || y < lo || y > hi {
		return false
	}
	cx := math.Max(lo+radius, math.Min(x, hi-radius))
	cy := math.Max(lo+radius, math.Min(y, hi-radius))
	return (x-cx)*(x-cx)+(y-cy)*(y-cy) <= radius*radius
}

func inCircle(x, y, cx, cy, r float64) bool { return (x-cx)*(x-cx)+(y-cy)*(y-cy) <= r*r }

func inCapsule(x, y, ax, ay, bx, by, r float64) bool {
	dx, dy := bx-ax, by-ay
	t := ((x-ax)*dx + (y-ay)*dy) / (dx*dx + dy*dy)
	t = math.Max(0, math.Min(1, t))
	px, py := ax+t*dx, ay+t*dy
	return (x-px)*(x-px)+(y-py)*(y-py) <= r*r
}

// mark: two nodes joined by a link, scaled into a box [o, o+s].
func inMark(x, y, o, s float64) bool {
	u, v := (x-o)/s, (y-o)/s
	return inCircle(u, v, 0.27, 0.70, 0.17) || inCircle(u, v, 0.73, 0.30, 0.17) ||
		inCapsule(u, v, 0.27, 0.70, 0.73, 0.30, 0.075)
}

type style struct {
	bg       bool // rounded-square background with gradient
	top, bot rgba // gradient
	fg       rgba // mark color
	dot      *rgba
	markO    float64
	markS    float64
}

func draw(size int, st style) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	const ss = 4 // supersampling
	for py := 0; py < size; py++ {
		for px := 0; px < size; px++ {
			var acc rgba
			n := 0.0
			for sy := 0; sy < ss; sy++ {
				for sx := 0; sx < ss; sx++ {
					x := (float64(px) + (float64(sx)+0.5)/ss) / float64(size)
					y := (float64(py) + (float64(sy)+0.5)/ss) / float64(size)
					c := rgba{}
					if st.bg && inRoundRect(x, y, 0.06, 0.2) {
						c = over(c, mix(st.top, st.bot, y), 1)
					}
					if inMark(x, y, st.markO, st.markS) {
						c = over(c, st.fg, 1)
					}
					if st.dot != nil && inCircle(x, y, 0.80, 0.80, 0.18) {
						c = over(c, *st.dot, 1)
					}
					acc.r += c.r * c.a
					acc.g += c.g * c.a
					acc.b += c.b * c.a
					acc.a += c.a
					n++
				}
			}
			a := acc.a / n
			var r, g, b float64
			if acc.a > 0 {
				r, g, b = acc.r/acc.a, acc.g/acc.a, acc.b/acc.a
			}
			img.SetNRGBA(px, py, color.NRGBA{uint8(r*255 + .5), uint8(g*255 + .5), uint8(b*255 + .5), uint8(a*255 + .5)})
		}
	}
	return img
}

func save(path string, img image.Image) {
	os.MkdirAll(filepath.Dir(path), 0o755)
	f, err := os.Create(path)
	if err != nil {
		panic(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		panic(err)
	}
}

func hex(h uint32) rgba {
	return rgba{float64(h>>16&0xff) / 255, float64(h>>8&0xff) / 255, float64(h&0xff) / 255, 1}
}

func main() {
	teal, indigo, white := hex(0x14b8a6), hex(0x4338ca), hex(0xffffff)
	amber, grey := hex(0xf59e0b), hex(0x9ca3af)
	app := style{bg: true, top: teal, bot: indigo, fg: white, markO: 0.2, markS: 0.6}
	save("build/appicon.png", draw(1024, app))
	save("frontend/public/icon.png", draw(256, app))
	// macOS menu bar: monochrome template (the system tints it).
	tmpl := style{fg: rgba{0, 0, 0, 1}, markO: 0.04, markS: 0.92}
	save("icons/tray-template.png", draw(44, tmpl))
	pendingTmpl := tmpl
	black := rgba{0, 0, 0, 1}
	pendingTmpl.dot = &black
	save("icons/tray-template-pending.png", draw(44, pendingTmpl))
	// Windows / Linux trays: full color.
	tray := style{bg: true, top: teal, bot: indigo, fg: white, markO: 0.16, markS: 0.68}
	save("icons/tray.png", draw(64, tray))
	pending := tray
	pending.dot = &amber
	save("icons/tray-pending.png", draw(64, pending))
	off := style{bg: true, top: grey, bot: hex(0x6b7280), fg: white, markO: 0.16, markS: 0.68}
	save("icons/tray-off.png", draw(64, off))
}
