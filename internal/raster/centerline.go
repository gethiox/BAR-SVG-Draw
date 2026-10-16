package raster

import (
	"math"

	"bardraw/internal/geom"
)

// Info reports what the centre-line extraction did.
type Info struct {
	Resolution  int     // raster long side in pixels
	Thickness   float64 // mean ink thickness in document units
	ThickFrac   float64 // thickness relative to the drawing size
	InkFraction float64
}

func render(fills []Fill, bounds geom.Rect, res int) (*Canvas, *Mask, Stats) {
	c := NewCanvas(bounds, res)
	for _, f := range fills {
		c.Paint(f)
	}
	m, st := c.Threshold()
	return c, m, st
}

// Analyze measures the ink of the fills without tracing (cheap).
func Analyze(fills []Fill, bounds geom.Rect) Info {
	c, _, st := render(fills, bounds, 800)
	long := float64(max(c.W, c.H) - 2*pad)
	return Info{Resolution: 800, Thickness: st.Thickness / c.Scale(), ThickFrac: st.Thickness / long, InkFraction: st.InkFraction}
}

// Centerline returns the skeleton strokes of the painted fills in document
// coordinates. res <= 0 picks a resolution so that typical strokes are
// 3–16 raster pixels thick.
func Centerline(fills []Fill, bounds geom.Rect, res int) ([]geom.Polyline, Info) {
	auto := res <= 0
	if auto {
		res = 1200
	}
	c, m, st := render(fills, bounds, res)
	if auto && st.InkPixels > 0 && st.Thickness > 0 {
		newRes := res
		if st.Thickness < 3 {
			newRes = min(3200, int(float64(res)*4/math.Max(st.Thickness, 0.5)))
		} else if st.Thickness > 16 {
			newRes = max(500, int(float64(res)*10/st.Thickness))
		}
		if newRes != res {
			res = newRes
			c, m, st = render(fills, bounds, res)
		}
	}
	info := Info{Resolution: res, InkFraction: st.InkFraction}
	if c.Scale() > 0 {
		info.Thickness = st.Thickness / c.Scale()
		info.ThickFrac = st.Thickness / float64(res-2*pad)
	}
	m.Thin()
	polys := m.Trace(1.2*st.Thickness + 2)
	for i := range polys {
		for k, p := range polys[i].Pts {
			polys[i].Pts[k] = c.ToDoc(p)
		}
	}
	return polys, info
}
