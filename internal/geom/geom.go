// Package geom holds the small 2D toolkit shared by the SVG parser, the
// centerline extractor and the stroke planner.
package geom

import "math"

// Pt is a 2D point / vector.
type Pt struct{ X, Y float64 }

func (a Pt) Add(b Pt) Pt        { return Pt{a.X + b.X, a.Y + b.Y} }
func (a Pt) Sub(b Pt) Pt        { return Pt{a.X - b.X, a.Y - b.Y} }
func (a Pt) Mul(s float64) Pt   { return Pt{a.X * s, a.Y * s} }
func (a Pt) Dot(b Pt) float64   { return a.X*b.X + a.Y*b.Y }
func (a Pt) Cross(b Pt) float64 { return a.X*b.Y - a.Y*b.X }
func (a Pt) Len() float64       { return math.Hypot(a.X, a.Y) }
func (a Pt) Dist(b Pt) float64  { return math.Hypot(a.X-b.X, a.Y-b.Y) }
func (a Pt) Dist2(b Pt) float64 { dx, dy := a.X-b.X, a.Y-b.Y; return dx*dx + dy*dy }
func (a Pt) Lerp(b Pt, t float64) Pt {
	return Pt{a.X + (b.X-a.X)*t, a.Y + (b.Y-a.Y)*t}
}
func (a Pt) Round() Pt { return Pt{math.Round(a.X), math.Round(a.Y)} }

// Polyline is an ordered list of points. Closed polylines are stored without
// repeating the first point at the end.
type Polyline struct {
	Pts    []Pt
	Closed bool
}

// Length returns the arc length (including the closing segment if closed).
func (p Polyline) Length() float64 {
	l := PathLength(p.Pts)
	if p.Closed && len(p.Pts) > 1 {
		l += p.Pts[len(p.Pts)-1].Dist(p.Pts[0])
	}
	return l
}

// Open returns the points as an open path; closed polylines get their first
// point appended so that drawing them visits the closing segment.
func (p Polyline) Open() []Pt {
	if !p.Closed || len(p.Pts) < 2 {
		return p.Pts
	}
	out := make([]Pt, len(p.Pts)+1)
	copy(out, p.Pts)
	out[len(p.Pts)] = p.Pts[0]
	return out
}

// PathLength is the arc length of an open point sequence.
func PathLength(pts []Pt) float64 {
	l := 0.0
	for i := 1; i < len(pts); i++ {
		l += pts[i].Dist(pts[i-1])
	}
	return l
}

// Rect is an axis-aligned rectangle.
type Rect struct{ Min, Max Pt }

// EmptyRect returns a rectangle that any Extend call will replace.
func EmptyRect() Rect {
	inf := math.Inf(1)
	return Rect{Pt{inf, inf}, Pt{-inf, -inf}}
}

func RectXYWH(x, y, w, h float64) Rect { return Rect{Pt{x, y}, Pt{x + w, y + h}} }

func (r Rect) Empty() bool { return !(r.Max.X >= r.Min.X && r.Max.Y >= r.Min.Y) }
func (r Rect) W() float64  { return r.Max.X - r.Min.X }
func (r Rect) H() float64  { return r.Max.Y - r.Min.Y }
func (r Rect) Center() Pt  { return Pt{(r.Min.X + r.Max.X) / 2, (r.Min.Y + r.Max.Y) / 2} }
func (r *Rect) Extend(p Pt) {
	r.Min.X = math.Min(r.Min.X, p.X)
	r.Min.Y = math.Min(r.Min.Y, p.Y)
	r.Max.X = math.Max(r.Max.X, p.X)
	r.Max.Y = math.Max(r.Max.Y, p.Y)
}
func (r *Rect) Union(o Rect) {
	if o.Empty() {
		return
	}
	r.Extend(o.Min)
	r.Extend(o.Max)
}
func (r Rect) Contains(p Pt) bool {
	return p.X >= r.Min.X && p.X <= r.Max.X && p.Y >= r.Min.Y && p.Y <= r.Max.Y
}

// Bounds of a set of point slices.
func Bounds(polys []Polyline) Rect {
	r := EmptyRect()
	for _, pl := range polys {
		for _, p := range pl.Pts {
			r.Extend(p)
		}
	}
	return r
}

// Mat is a 2D affine transform in SVG order:
//
//	x' = A*x + C*y + E
//	y' = B*x + D*y + F
type Mat struct{ A, B, C, D, E, F float64 }

func Identity() Mat              { return Mat{1, 0, 0, 1, 0, 0} }
func Translate(x, y float64) Mat { return Mat{1, 0, 0, 1, x, y} }
func Scale(sx, sy float64) Mat   { return Mat{sx, 0, 0, sy, 0, 0} }
func Rotate(rad float64) Mat     { s, c := math.Sincos(rad); return Mat{c, s, -s, c, 0, 0} }
func SkewX(rad float64) Mat      { return Mat{1, 0, math.Tan(rad), 1, 0, 0} }
func SkewY(rad float64) Mat      { return Mat{1, math.Tan(rad), 0, 1, 0, 0} }

// Mul returns m*n, i.e. the transform that applies n first and then m.
func (m Mat) Mul(n Mat) Mat {
	return Mat{
		A: m.A*n.A + m.C*n.B,
		B: m.B*n.A + m.D*n.B,
		C: m.A*n.C + m.C*n.D,
		D: m.B*n.C + m.D*n.D,
		E: m.A*n.E + m.C*n.F + m.E,
		F: m.B*n.E + m.D*n.F + m.F,
	}
}

func (m Mat) Apply(p Pt) Pt {
	return Pt{m.A*p.X + m.C*p.Y + m.E, m.B*p.X + m.D*p.Y + m.F}
}

// ScaleFactor is the geometric mean scale of the transform (used for stroke widths).
func (m Mat) ScaleFactor() float64 { return math.Sqrt(math.Abs(m.A*m.D - m.B*m.C)) }

// ---------------------------------------------------------------------------
// Simplification

// SimplifyRDP runs Ramer–Douglas–Peucker on an open point sequence, keeping
// both endpoints. eps is the maximum allowed perpendicular deviation.
func SimplifyRDP(pts []Pt, eps float64) []Pt {
	n := len(pts)
	if n <= 2 || eps <= 0 {
		return append([]Pt(nil), pts...)
	}
	keep := make([]bool, n)
	keep[0], keep[n-1] = true, true
	type span struct{ a, b int }
	stack := []span{{0, n - 1}}
	eps2 := eps * eps
	for len(stack) > 0 {
		s := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if s.b-s.a < 2 {
			continue
		}
		a, b := pts[s.a], pts[s.b]
		ab := b.Sub(a)
		l2 := ab.Dot(ab)
		best, bestD := -1, -1.0
		for i := s.a + 1; i < s.b; i++ {
			var d float64
			if l2 < 1e-18 {
				d = pts[i].Dist2(a)
			} else {
				c := ab.Cross(pts[i].Sub(a))
				d = c * c / l2
				// points beyond the segment ends count by endpoint distance
				t := pts[i].Sub(a).Dot(ab) / l2
				if t < 0 {
					d = math.Max(d, pts[i].Dist2(a))
				} else if t > 1 {
					d = math.Max(d, pts[i].Dist2(b))
				}
			}
			if d > bestD {
				best, bestD = i, d
			}
		}
		if bestD > eps2 {
			keep[best] = true
			stack = append(stack, span{s.a, best}, span{best, s.b})
		}
	}
	out := make([]Pt, 0, n/4+2)
	for i, k := range keep {
		if k {
			out = append(out, pts[i])
		}
	}
	return out
}

// SimplifyClosed simplifies a closed ring (given without the repeated first
// point) and returns an open path that starts and ends at the same point.
func SimplifyClosed(pts []Pt, eps float64) []Pt {
	n := len(pts)
	if n < 3 {
		pl := Polyline{Pts: pts, Closed: true}
		return pl.Open()
	}
	// split at the vertex farthest from the first one so that RDP sees two
	// well-conditioned halves
	far, farD := 0, -1.0
	for i := 1; i < n; i++ {
		if d := pts[i].Dist2(pts[0]); d > farD {
			far, farD = i, d
		}
	}
	a := SimplifyRDP(pts[:far+1], eps)
	tail := make([]Pt, 0, n-far+1)
	tail = append(tail, pts[far:]...)
	tail = append(tail, pts[0])
	b := SimplifyRDP(tail, eps)
	out := append(a, b[1:]...)
	return out
}

// Dedupe removes consecutive duplicates (exact equality).
func Dedupe(pts []Pt) []Pt {
	if len(pts) < 2 {
		return pts
	}
	out := pts[:1]
	for _, p := range pts[1:] {
		if p != out[len(out)-1] {
			out = append(out, p)
		}
	}
	return out
}

// Subdivide inserts points so that no segment is longer than maxLen.
func Subdivide(pts []Pt, maxLen float64) []Pt {
	if maxLen <= 0 || len(pts) < 2 {
		return pts
	}
	out := []Pt{pts[0]}
	for i := 1; i < len(pts); i++ {
		a, b := pts[i-1], pts[i]
		d := a.Dist(b)
		if n := int(math.Ceil(d / maxLen)); n > 1 {
			for k := 1; k < n; k++ {
				out = append(out, a.Lerp(b, float64(k)/float64(n)))
			}
		}
		out = append(out, b)
	}
	return out
}
