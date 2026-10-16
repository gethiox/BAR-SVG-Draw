package plan

import (
	"math"
	"sort"

	"bardraw/internal/geom"
)

// Level is one step of the quality/speed slider. All values are in screen pixels.
type Level struct {
	Eps    float64 // max deviation from the true curve (Douglas–Peucker tolerance)
	MinLen float64 // strokes shorter than this are skipped
	Join   float64 // strokes whose ends are closer than this are drawn as one
}

// Levels: 1 = fastest/roughest … 7 = slowest/finest. In-game lines are 3 px
// wide, so deviations around 1 px are practically invisible.
var Levels = []Level{
	{Eps: 4.0, MinLen: 14, Join: 4},
	{Eps: 3.0, MinLen: 10, Join: 3},
	{Eps: 2.2, MinLen: 7, Join: 2.5},
	{Eps: 1.6, MinLen: 5, Join: 2},
	{Eps: 1.1, MinLen: 3.5, Join: 1.5},
	{Eps: 0.75, MinLen: 2.5, Join: 1},
	{Eps: 0.5, MinLen: 1.5, Join: 0.75},
}

// ClampLevel keeps a 1-based level in range.
func ClampLevel(l int) int { return max(1, min(len(Levels), l)) }

// Transform options applied when fitting the drawing into the box.
type Transform struct {
	Rotate  int  // quarter turns clockwise
	FlipH   bool // mirror horizontally
	Stretch bool // fill the box ignoring aspect ratio
}

// Params for building a plan.
type Params struct {
	Level  int     // 1..len(Levels)
	MaxSeg float64 // split segments longer than this (px); 0 = unlimited
	Order  string  // nearest | longest | document
	Xf     Transform
}

// Plan is the list of pen strokes in integer pixels relative to the box origin.
type Plan struct {
	Strokes  [][]geom.Pt
	Fit      geom.Rect // area covered by the drawing (relative to box origin)
	W, H     float64   // box size the plan was made for
	Level    int
	Vertices int // total pen positions
	Segments int // total line segments the game will create
	Length   float64
	Dropped  int // strokes skipped for being too short
}

// FitMatrix maps document space into a w×h box at the origin.
func FitMatrix(bounds geom.Rect, w, h float64, xf Transform) geom.Mat {
	bw, bh := bounds.W(), bounds.H()
	if bw < 1e-9 && bh < 1e-9 {
		bw, bh = 1, 1
	} else if bw < 1e-9 {
		bw = bh
	} else if bh < 1e-9 {
		bh = bw
	}
	rot := ((xf.Rotate % 4) + 4) % 4
	ew, eh := bw, bh
	if rot%2 == 1 {
		ew, eh = bh, bw
	}
	sx, sy := w/ew, h/eh
	if !xf.Stretch {
		s := math.Min(sx, sy)
		sx, sy = s, s
	}
	c := bounds.Center()
	m := geom.Translate(w/2, h/2).Mul(geom.Scale(sx, sy)).Mul(geom.Rotate(float64(rot) * math.Pi / 2))
	if xf.FlipH {
		m = m.Mul(geom.Scale(-1, 1))
	}
	return m.Mul(geom.Translate(-c.X, -c.Y))
}

type stroke struct {
	pts    []geom.Pt
	closed bool
}

// Build creates a plan for a w×h pixel box.
func Build(g *Geometry, w, h float64, p Params) *Plan {
	lvl := Levels[ClampLevel(p.Level)-1]
	pl := &Plan{W: w, H: h, Level: ClampLevel(p.Level)}
	if g == nil || w < 1 || h < 1 {
		return pl
	}
	m := FitMatrix(g.Bounds, w, h, p.Xf)
	pl.Fit = geom.EmptyRect()
	for _, c := range []geom.Pt{g.Bounds.Min, g.Bounds.Max, {X: g.Bounds.Min.X, Y: g.Bounds.Max.Y}, {X: g.Bounds.Max.X, Y: g.Bounds.Min.Y}} {
		pl.Fit.Extend(m.Apply(c))
	}

	var ss []stroke
	buf := make([]geom.Pt, 0, 256)
	for _, s := range g.Strokes {
		if len(s.Pts) < 2 {
			continue
		}
		buf = buf[:0]
		for _, q := range s.Pts {
			buf = append(buf, m.Apply(q))
		}
		var simp []geom.Pt
		if s.Closed {
			simp = geom.SimplifyClosed(buf, lvl.Eps)
		} else {
			simp = geom.SimplifyRDP(buf, lvl.Eps)
		}
		simp = geom.Subdivide(simp, p.MaxSeg)
		for i := range simp {
			simp[i] = simp[i].Round()
		}
		simp = geom.Dedupe(simp)
		if len(simp) < 2 {
			pl.Dropped++
			continue
		}
		if geom.PathLength(simp) < lvl.MinLen {
			pl.Dropped++
			continue
		}
		ss = append(ss, stroke{pts: simp, closed: len(simp) > 2 && simp[0] == simp[len(simp)-1]})
	}
	ss = joinStrokes(ss, lvl.Join)
	ss = orderStrokes(ss, p.Order)
	for _, s := range ss {
		pl.Strokes = append(pl.Strokes, s.pts)
		pl.Vertices += len(s.pts)
		pl.Segments += len(s.pts) - 1
		pl.Length += geom.PathLength(s.pts)
	}
	return pl
}

// joinStrokes merges open strokes whose endpoints lie within d pixels, which
// saves a pen-up/pen-down cycle per joint.
func joinStrokes(ss []stroke, d float64) []stroke {
	if d <= 0 || len(ss) < 2 {
		return ss
	}
	cell := math.Max(d, 1)
	type key struct{ x, y int }
	type ref struct{ i, end int }
	grid := map[key][]ref{}
	k := func(p geom.Pt) key { return key{int(math.Floor(p.X / cell)), int(math.Floor(p.Y / cell))} }
	for i, s := range ss {
		if s.closed {
			continue
		}
		grid[k(s.pts[0])] = append(grid[k(s.pts[0])], ref{i, 0})
		e := s.pts[len(s.pts)-1]
		grid[k(e)] = append(grid[k(e)], ref{i, 1})
	}
	used := make([]bool, len(ss))
	d2 := d * d
	find := func(p geom.Pt) (int, int) {
		c := k(p)
		best, bestEnd, bestD := -1, 0, math.Inf(1)
		for dx := -1; dx <= 1; dx++ {
			for dy := -1; dy <= 1; dy++ {
				for _, r := range grid[key{c.x + dx, c.y + dy}] {
					if used[r.i] {
						continue
					}
					q := ss[r.i].pts[0]
					if r.end == 1 {
						q = ss[r.i].pts[len(ss[r.i].pts)-1]
					}
					if dd := p.Dist2(q); dd <= d2 && dd < bestD {
						best, bestEnd, bestD = r.i, r.end, dd
					}
				}
			}
		}
		return best, bestEnd
	}
	extend := func(pts []geom.Pt) []geom.Pt {
		for {
			j, end := find(pts[len(pts)-1])
			if j < 0 {
				return pts
			}
			used[j] = true
			add := ss[j].pts
			if end == 1 {
				add = reverse(add)
			}
			if add[0] == pts[len(pts)-1] {
				add = add[1:]
			}
			pts = append(pts, add...)
		}
	}
	// longest first, so the main lines absorb the small bits
	idx := make([]int, len(ss))
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool {
		return geom.PathLength(ss[idx[a]].pts) > geom.PathLength(ss[idx[b]].pts)
	})
	var out []stroke
	for _, i := range idx {
		if used[i] {
			continue
		}
		used[i] = true
		if ss[i].closed {
			out = append(out, ss[i])
			continue
		}
		pts := append([]geom.Pt(nil), ss[i].pts...)
		pts = extend(pts)
		pts = reverse(extend(reverse(pts)))
		out = append(out, stroke{pts: pts, closed: len(pts) > 2 && pts[0] == pts[len(pts)-1]})
	}
	return out
}

func reverse(p []geom.Pt) []geom.Pt {
	out := make([]geom.Pt, len(p))
	for i := range p {
		out[len(p)-1-i] = p[i]
	}
	return out
}

// orderStrokes decides drawing order and direction. Travel between strokes is
// free (the cursor teleports), so ordering is about how the drawing grows.
func orderStrokes(ss []stroke, order string) []stroke {
	switch order {
	case "document":
		return ss
	case "longest":
		sort.SliceStable(ss, func(a, b int) bool {
			return geom.PathLength(ss[a].pts) > geom.PathLength(ss[b].pts)
		})
		return ss
	}
	// nearest neighbour from the top-left corner, like a human would draw
	out := make([]stroke, 0, len(ss))
	used := make([]bool, len(ss))
	pen := geom.Pt{}
	for range ss {
		best, bestD, bestRev, bestRot := -1, math.Inf(1), false, 0
		for i, s := range ss {
			if used[i] {
				continue
			}
			if s.closed {
				for k := 0; k < len(s.pts)-1; k++ {
					if d := pen.Dist2(s.pts[k]); d < bestD {
						best, bestD, bestRev, bestRot = i, d, false, k
					}
				}
				continue
			}
			if d := pen.Dist2(s.pts[0]); d < bestD {
				best, bestD, bestRev, bestRot = i, d, false, 0
			}
			if d := pen.Dist2(s.pts[len(s.pts)-1]); d < bestD {
				best, bestD, bestRev, bestRot = i, d, true, 0
			}
		}
		used[best] = true
		s := ss[best]
		if s.closed && bestRot > 0 {
			ring := s.pts[:len(s.pts)-1]
			rot := append(append([]geom.Pt(nil), ring[bestRot:]...), ring[:bestRot]...)
			s.pts = append(rot, rot[0])
		} else if bestRev {
			s.pts = reverse(s.pts)
		}
		out = append(out, s)
		pen = s.pts[len(s.pts)-1]
	}
	return out
}

// Translate returns the strokes moved by (dx,dy) (absolute screen pixels).
func (p *Plan) Translate(dx, dy float64) [][]geom.Pt {
	out := make([][]geom.Pt, len(p.Strokes))
	for i, s := range p.Strokes {
		o := make([]geom.Pt, len(s))
		for k, q := range s {
			o[k] = geom.Pt{X: q.X + dx, Y: q.Y + dy}
		}
		out[i] = o
	}
	return out
}
