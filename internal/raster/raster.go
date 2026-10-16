// Package raster converts filled artwork into single centre-lines: the fills
// are rasterised, thinned to a one-pixel skeleton and traced back into
// polylines. This turns "outlined" line art (potrace output, coloring pages,
// logos made of thin shapes) into one stroke per line instead of two outlines.
package raster

import (
	"math"
	"sort"

	"bardraw/internal/geom"
)

// Fill is one filled shape to paint, in document coordinates.
type Fill struct {
	Polys   []geom.Polyline
	EvenOdd bool
	Luma    float64 // 0 black .. 1 white
	Alpha   float64
}

// Canvas is a greyscale raster (255 = paper, 0 = ink) with a document mapping.
type Canvas struct {
	W, H  int
	Pix   []uint8
	scale float64
	off   geom.Pt // document point that maps to raster (pad, pad)
	pad   float64
}

const pad = 3

// NewCanvas creates a canvas covering bounds with the long side ≈ res pixels.
func NewCanvas(bounds geom.Rect, res int) *Canvas {
	bw, bh := math.Max(bounds.W(), 1e-9), math.Max(bounds.H(), 1e-9)
	s := float64(res-2*pad) / math.Max(bw, bh)
	w := int(math.Ceil(bw*s)) + 2*pad
	h := int(math.Ceil(bh*s)) + 2*pad
	c := &Canvas{W: w, H: h, Pix: make([]uint8, w*h), scale: s, off: bounds.Min, pad: pad}
	for i := range c.Pix {
		c.Pix[i] = 255
	}
	return c
}

// ToRaster maps a document point into raster pixel coordinates.
func (c *Canvas) ToRaster(p geom.Pt) geom.Pt {
	return geom.Pt{X: (p.X-c.off.X)*c.scale + c.pad, Y: (p.Y-c.off.Y)*c.scale + c.pad}
}

// ToDoc maps a raster coordinate back into document space.
func (c *Canvas) ToDoc(p geom.Pt) geom.Pt {
	return geom.Pt{X: (p.X-c.pad)/c.scale + c.off.X, Y: (p.Y-c.pad)/c.scale + c.off.Y}
}

// Scale returns raster pixels per document unit.
func (c *Canvas) Scale() float64 { return c.scale }

type edge struct {
	x0, y0, x1, y1 float64
	dir            int
}

// Paint fills a shape with scanline conversion (sampling at pixel centres).
func (c *Canvas) Paint(f Fill) {
	lum := uint8(math.Round(clamp01(f.Luma) * 255))
	alpha := clamp01(f.Alpha)
	c.Spans(f.Polys, f.EvenOdd, func(y, xa, xb int) {
		row := c.Pix[y*c.W : (y+1)*c.W]
		for x := xa; x <= xb; x++ {
			if alpha >= 0.999 {
				row[x] = lum
			} else {
				row[x] = uint8(float64(row[x])*(1-alpha) + float64(lum)*alpha + 0.5)
			}
		}
	})
}

// Spans calls fn for every horizontal run of pixels [xa, xb] in row y whose
// centre lies inside the polygons (subpaths are implicitly closed).
func (c *Canvas) Spans(polys []geom.Polyline, evenOdd bool, fn func(y, xa, xb int)) {
	spans(c.W, c.H, polys, c.ToRaster, evenOdd, fn)
}

func spans(w, h int, polys []geom.Polyline, xf func(geom.Pt) geom.Pt, evenOdd bool, fn func(y, xa, xb int)) {
	var edges []edge
	minY, maxY := math.Inf(1), math.Inf(-1)
	for _, pl := range polys {
		n := len(pl.Pts)
		if n < 2 {
			continue
		}
		for i := 0; i < n; i++ {
			a := xf(pl.Pts[i])
			b := xf(pl.Pts[(i+1)%n])
			if a.Y == b.Y {
				continue
			}
			e := edge{a.X, a.Y, b.X, b.Y, 1}
			if a.Y > b.Y {
				e = edge{b.X, b.Y, a.X, a.Y, -1}
			}
			edges = append(edges, e)
			minY = math.Min(minY, e.y0)
			maxY = math.Max(maxY, e.y1)
		}
	}
	if len(edges) == 0 {
		return
	}
	sort.Slice(edges, func(i, j int) bool { return edges[i].y0 < edges[j].y0 })
	y0 := int(math.Max(0, math.Floor(minY)))
	y1 := int(math.Min(float64(h-1), math.Ceil(maxY)))
	type cross struct {
		x   float64
		dir int
	}
	var active []int
	var xs []cross
	next := 0
	for y := y0; y <= y1; y++ {
		yc := float64(y) + 0.5
		for next < len(edges) && edges[next].y0 <= yc {
			active = append(active, next)
			next++
		}
		xs = xs[:0]
		k := 0
		for _, ei := range active {
			e := &edges[ei]
			if e.y1 <= yc {
				continue // finished, drop
			}
			active[k] = ei
			k++
			if e.y0 <= yc {
				x := e.x0 + (yc-e.y0)*(e.x1-e.x0)/(e.y1-e.y0)
				xs = append(xs, cross{x, e.dir})
			}
		}
		active = active[:k]
		if len(xs) < 2 {
			continue
		}
		sort.Slice(xs, func(i, j int) bool { return xs[i].x < xs[j].x })
		wind := 0
		for i := 0; i < len(xs)-1; i++ {
			if evenOdd {
				wind ^= 1
			} else {
				wind += xs[i].dir
			}
			if wind == 0 {
				continue
			}
			xa := max(0, int(math.Ceil(xs[i].x-0.5)))
			xb := min(w-1, int(math.Ceil(xs[i+1].x-0.5))-1)
			if xa <= xb {
				fn(y, xa, xb)
			}
		}
	}
}

func clamp01(v float64) float64 { return math.Max(0, math.Min(1, v)) }

// Mask is a binary ink image (1 = ink).
type Mask struct {
	W, H int
	B    []uint8
}

// Stats describe the ink: Thickness is the mean stroke width in raster pixels
// (2·area / perimeter), InkFraction the share of inked pixels.
type Stats struct {
	InkPixels   int
	Thickness   float64
	InkFraction float64
}

// Threshold converts the canvas to a binary mask and measures it.
func (c *Canvas) Threshold() (*Mask, Stats) {
	m := &Mask{W: c.W, H: c.H, B: make([]uint8, len(c.Pix))}
	ink := 0
	for i, v := range c.Pix {
		if v < 128 {
			m.B[i] = 1
			ink++
		}
	}
	// keep the 1px frame empty so neighbourhood lookups never leave the image
	for x := 0; x < m.W; x++ {
		m.B[x], m.B[(m.H-1)*m.W+x] = 0, 0
	}
	for y := 0; y < m.H; y++ {
		m.B[y*m.W], m.B[y*m.W+m.W-1] = 0, 0
	}
	boundary := 0
	for y := 0; y < m.H-1; y++ {
		for x := 0; x < m.W-1; x++ {
			i := y*m.W + x
			if m.B[i] != m.B[i+1] {
				boundary++
			}
			if m.B[i] != m.B[i+m.W] {
				boundary++
			}
		}
	}
	st := Stats{InkPixels: ink, InkFraction: float64(ink) / float64(len(c.Pix))}
	if boundary > 0 {
		st.Thickness = 2 * float64(ink) / float64(boundary)
	}
	return m, st
}

// ---------------------------------------------------------------------------
// thinning

// neighbour offsets in ring order N, NE, E, SE, S, SW, W, NW
var ndx = [8]int{0, 1, 1, 1, 0, -1, -1, -1}
var ndy = [8]int{-1, -1, 0, 1, 1, 1, 0, -1}

// compTable[mask] = number of 8-connected components among the set neighbours.
var compTable [256]uint8

func init() {
	for mask := 0; mask < 256; mask++ {
		seen := 0
		comps := 0
		for i := 0; i < 8; i++ {
			if mask&(1<<i) == 0 || seen&(1<<i) != 0 {
				continue
			}
			comps++
			stack := []int{i}
			seen |= 1 << i
			for len(stack) > 0 {
				a := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				for b := 0; b < 8; b++ {
					if mask&(1<<b) == 0 || seen&(1<<b) != 0 {
						continue
					}
					if absInt(ndx[a]-ndx[b]) <= 1 && absInt(ndy[a]-ndy[b]) <= 1 {
						seen |= 1 << b
						stack = append(stack, b)
					}
				}
			}
		}
		compTable[mask] = uint8(comps)
	}
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func (m *Mask) nmask(i int) int {
	w := m.W
	b := m.B
	v := 0
	if b[i-w] != 0 {
		v |= 1
	}
	if b[i-w+1] != 0 {
		v |= 2
	}
	if b[i+1] != 0 {
		v |= 4
	}
	if b[i+w+1] != 0 {
		v |= 8
	}
	if b[i+w] != 0 {
		v |= 16
	}
	if b[i+w-1] != 0 {
		v |= 32
	}
	if b[i-1] != 0 {
		v |= 64
	}
	if b[i-w-1] != 0 {
		v |= 128
	}
	return v
}

func popcount8(v int) int {
	n := 0
	for ; v != 0; v &= v - 1 {
		n++
	}
	return n
}

// Thin reduces the mask to a one-pixel wide 8-connected skeleton
// (Zhang–Suen followed by removal of redundant staircase pixels).
func (m *Mask) Thin() {
	var pts []int
	for i, v := range m.B {
		if v != 0 {
			pts = append(pts, i)
		}
	}
	del := make([]int, 0, 1024)
	for {
		changed := false
		for pass := 0; pass < 2; pass++ {
			del = del[:0]
			for _, i := range pts {
				if m.B[i] == 0 {
					continue
				}
				nm := m.nmask(i)
				bc := popcount8(nm)
				if bc < 2 || bc > 6 {
					continue
				}
				// number of 0->1 transitions in the ring P2..P9,P2
				a := 0
				for k := 0; k < 8; k++ {
					if nm&(1<<k) == 0 && nm&(1<<((k+1)&7)) != 0 {
						a++
					}
				}
				if a != 1 {
					continue
				}
				p2, p4, p6, p8 := nm&1 != 0, nm&4 != 0, nm&16 != 0, nm&64 != 0
				if pass == 0 {
					if (p2 && p4 && p6) || (p4 && p6 && p8) {
						continue
					}
				} else {
					if (p2 && p4 && p8) || (p2 && p6 && p8) {
						continue
					}
				}
				del = append(del, i)
			}
			for _, i := range del {
				m.B[i] = 0
			}
			if len(del) > 0 {
				changed = true
			}
		}
		// compact the work list
		k := 0
		for _, i := range pts {
			if m.B[i] != 0 {
				pts[k] = i
				k++
			}
		}
		pts = pts[:k]
		if !changed {
			break
		}
	}
	// staircase cleanup: drop pixels whose neighbours stay connected without them
	for iter := 0; iter < 3; iter++ {
		removed := 0
		for _, i := range pts {
			if m.B[i] == 0 {
				continue
			}
			nm := m.nmask(i)
			if popcount8(nm) >= 2 && compTable[nm] == 1 {
				m.B[i] = 0
				removed++
			}
		}
		if removed == 0 {
			break
		}
	}
}
