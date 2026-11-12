package desktop

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"math"
)

// The menu bar icon is Exhale's mark, the two arcs of site/favicon.svg,
// drawn here so there are no image files to keep in step. On a Mac it is a
// template image: black on transparent, which macOS tints for the menu bar.
// Elsewhere it is the app icon's gradient tile. While the computer is kept
// awake, a dot rises above the arcs.

// arc is a stroke along a circle, between two angles (radians, y down).
type arc struct {
	cx, cy, r, from, to, width, alpha float64
}

// The arcs of favicon.svg (a 32 unit square): circles through each Bézier's
// ends and its highest point.
var arcs = []arc{
	{cx: 16, cy: 22.86, r: 9.36, width: 2.6, alpha: 1},
	{cx: 16, cy: 26.43, r: 6.68, width: 2.2, alpha: 0.6},
}

func init() {
	// The arcs end at y 18 and 22, on both sides of their centre.
	for i, end := range []float64{18, 22} {
		a := &arcs[i]
		half := math.Asin((a.cy - end) / a.r)
		a.from, a.to = math.Pi+half, 2*math.Pi-half
	}
}

// IconPNG draws the icon: size pixels square, as a template image (black
// on transparent) or on the gradient tile, with the dot when awake.
func IconPNG(size int, template, awake bool) []byte {
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	// The template shows the arcs alone, larger: a 24 unit window on the
	// 32 unit drawing. The tile shows the whole drawing.
	view, x0, y0 := 32.0, 0.0, 0.0
	if template {
		view, x0, y0 = 24, 4, 5
	}
	scale := view / float64(size)
	const ss = 4 // samples per pixel side
	for py := range size {
		for px := range size {
			var r, g, b, a float64
			for sy := range ss {
				for sx := range ss {
					x := x0 + (float64(px)+(float64(sx)+0.5)/ss)*scale
					y := y0 + (float64(py)+(float64(sy)+0.5)/ss)*scale
					cr, cg, cb, ca := sample(x, y, template, awake)
					r, g, b, a = r+cr, g+cg, b+cb, a+ca
				}
			}
			if a == 0 {
				continue
			}
			n := float64(ss * ss)
			img.SetNRGBA(px, py, color.NRGBA{R: byte(r / a * 255), G: byte(g / a * 255), B: byte(b / a * 255), A: byte(a / n * 255)})
		}
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}

// sample is the colour at a point of the 32 unit drawing, premultiplied by
// its alpha.
func sample(x, y float64, template, awake bool) (r, g, b, a float64) {
	const ink = 0.04 // #0a0a0a
	if !template {
		if !inRoundedSquare(x, y, 32, 9) {
			return 0, 0, 0, 0
		}
		r, g, b = gradient(x, y)
		a = 1
	}
	over := func(alpha float64) {
		r, g, b = ink*alpha+r*(1-alpha), ink*alpha+g*(1-alpha), ink*alpha+b*(1-alpha)
		a = alpha + a*(1-alpha)
	}
	for _, s := range arcs {
		if s.distance(x, y) <= s.width/2 {
			over(s.alpha)
		}
	}
	if awake {
		cy := 8.5
		if !template {
			cy = 9.5
		}
		if math.Hypot(x-16, y-cy) <= 2.3 {
			over(1)
		}
	}
	return r, g, b, a
}

// distance is from a point to the arc, round caps included.
func (s arc) distance(x, y float64) float64 {
	t := math.Atan2(y-s.cy, x-s.cx)
	if t < 0 {
		t += 2 * math.Pi
	}
	if t >= s.from && t <= s.to {
		return math.Abs(math.Hypot(x-s.cx, y-s.cy) - s.r)
	}
	ax, ay := s.cx+s.r*math.Cos(s.from), s.cy+s.r*math.Sin(s.from)
	bx, by := s.cx+s.r*math.Cos(s.to), s.cy+s.r*math.Sin(s.to)
	return math.Min(math.Hypot(x-ax, y-ay), math.Hypot(x-bx, y-by))
}

func inRoundedSquare(x, y, size, radius float64) bool {
	if x < 0 || y < 0 || x > size || y > size {
		return false
	}
	cx := math.Max(radius-x, math.Max(x-(size-radius), 0))
	cy := math.Max(radius-y, math.Max(y-(size-radius), 0))
	return cx*cx+cy*cy <= radius*radius
}

// gradient is the tile's: #c4a5f5 → #93c5f5 → #7ee0c8 along (1, 0.6).
func gradient(x, y float64) (r, g, b float64) {
	t := (x + y*0.6) / 32 / 1.36 // projected on the direction, over its squared length
	stops := [][3]float64{{0xc4, 0xa5, 0xf5}, {0x93, 0xc5, 0xf5}, {0x7e, 0xe0, 0xc8}}
	i, f := 0, t*2
	if f >= 1 {
		i, f = 1, f-1
	}
	f = math.Min(math.Max(f, 0), 1)
	mix := func(k int) float64 { return (stops[i][k]*(1-f) + stops[i+1][k]*f) / 255 }
	return mix(0), mix(1), mix(2)
}
