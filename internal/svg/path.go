package svg

import (
	"math"

	"bardraw/internal/geom"
)

// flattener turns path commands (in local coordinates) into polylines in
// document coordinates. Curves are transformed first and flattened afterwards,
// so the tolerance is expressed in document units.
type flattener struct {
	m      geom.Mat
	tol    float64
	out    []geom.Polyline
	cur    []geom.Pt // current subpath (document space)
	start  geom.Pt   // current subpath start (local space)
	hasCur bool
}

func newFlattener(m geom.Mat, tol float64) *flattener {
	if tol <= 0 {
		tol = 0.1
	}
	return &flattener{m: m, tol: tol}
}

func (f *flattener) flush(closed bool) {
	if len(f.cur) >= 2 {
		pts := geom.Dedupe(f.cur)
		if closed && len(pts) > 2 && pts[0].Dist2(pts[len(pts)-1]) < 1e-18 {
			pts = pts[:len(pts)-1]
		}
		if len(pts) >= 2 {
			f.out = append(f.out, geom.Polyline{Pts: pts, Closed: closed && len(pts) > 2})
		}
	}
	f.cur = nil
}

func (f *flattener) moveTo(p geom.Pt) {
	f.flush(false)
	f.start = p
	f.cur = []geom.Pt{f.m.Apply(p)}
	f.hasCur = true
}

// ensure starts a new subpath at the last subpath start after a closepath.
func (f *flattener) ensure() {
	if len(f.cur) == 0 {
		f.cur = []geom.Pt{f.m.Apply(f.start)}
	}
}

func (f *flattener) lineTo(p geom.Pt) {
	f.ensure()
	f.cur = append(f.cur, f.m.Apply(p))
}

func (f *flattener) cubicTo(c1, c2, p geom.Pt) {
	f.ensure()
	p0 := f.cur[len(f.cur)-1]
	p1, p2, p3 := f.m.Apply(c1), f.m.Apply(c2), f.m.Apply(p)
	// Wang's formula: n segments guarantee deviation <= tol
	d1 := p0.Sub(p1.Mul(2)).Add(p2).Len()
	d2 := p1.Sub(p2.Mul(2)).Add(p3).Len()
	n := int(math.Ceil(math.Sqrt(0.75 * math.Max(d1, d2) / f.tol)))
	if n < 1 {
		n = 1
	} else if n > 600 {
		n = 600
	}
	for i := 1; i <= n; i++ {
		t := float64(i) / float64(n)
		mt := 1 - t
		a := mt * mt * mt
		b := 3 * mt * mt * t
		c := 3 * mt * t * t
		d := t * t * t
		f.cur = append(f.cur, geom.Pt{
			X: a*p0.X + b*p1.X + c*p2.X + d*p3.X,
			Y: a*p0.Y + b*p1.Y + c*p2.Y + d*p3.Y,
		})
	}
	f.cur[len(f.cur)-1] = p3
}

func (f *flattener) quadTo(c, p geom.Pt) {
	f.ensure()
	p0 := f.cur[len(f.cur)-1]
	p1, p2 := f.m.Apply(c), f.m.Apply(p)
	d := p0.Sub(p1.Mul(2)).Add(p2).Len()
	n := int(math.Ceil(math.Sqrt(0.25 * d / f.tol)))
	if n < 1 {
		n = 1
	} else if n > 600 {
		n = 600
	}
	for i := 1; i <= n; i++ {
		t := float64(i) / float64(n)
		mt := 1 - t
		f.cur = append(f.cur, geom.Pt{
			X: mt*mt*p0.X + 2*mt*t*p1.X + t*t*p2.X,
			Y: mt*mt*p0.Y + 2*mt*t*p1.Y + t*t*p2.Y,
		})
	}
	f.cur[len(f.cur)-1] = p2
}

// arcTo implements the SVG elliptical arc (endpoint parameterization, spec F.6).
func (f *flattener) arcTo(p0 geom.Pt, rx, ry, phiDeg float64, large, sweep bool, p1 geom.Pt) {
	if p0 == p1 {
		return
	}
	rx, ry = math.Abs(rx), math.Abs(ry)
	if rx < 1e-12 || ry < 1e-12 {
		f.lineTo(p1)
		return
	}
	sinPhi, cosPhi := math.Sincos(phiDeg * math.Pi / 180)
	dx2, dy2 := (p0.X-p1.X)/2, (p0.Y-p1.Y)/2
	x1 := cosPhi*dx2 + sinPhi*dy2
	y1 := -sinPhi*dx2 + cosPhi*dy2
	if lambda := x1*x1/(rx*rx) + y1*y1/(ry*ry); lambda > 1 {
		s := math.Sqrt(lambda)
		rx, ry = rx*s, ry*s
	}
	num := rx*rx*ry*ry - rx*rx*y1*y1 - ry*ry*x1*x1
	den := rx*rx*y1*y1 + ry*ry*x1*x1
	coef := 0.0
	if den > 0 && num > 0 {
		coef = math.Sqrt(num / den)
	}
	if large == sweep {
		coef = -coef
	}
	cxp := coef * rx * y1 / ry
	cyp := -coef * ry * x1 / rx
	cx := cosPhi*cxp - sinPhi*cyp + (p0.X+p1.X)/2
	cy := sinPhi*cxp + cosPhi*cyp + (p0.Y+p1.Y)/2

	angle := func(ux, uy, vx, vy float64) float64 {
		return math.Atan2(ux*vy-uy*vx, ux*vx+uy*vy)
	}
	ux, uy := (x1-cxp)/rx, (y1-cyp)/ry
	vx, vy := (-x1-cxp)/rx, (-y1-cyp)/ry
	theta := angle(1, 0, ux, uy)
	dtheta := angle(ux, uy, vx, vy)
	if !sweep && dtheta > 0 {
		dtheta -= 2 * math.Pi
	} else if sweep && dtheta < 0 {
		dtheta += 2 * math.Pi
	}

	n := int(math.Ceil(math.Abs(dtheta) / (math.Pi / 2)))
	if n < 1 {
		n = 1
	}
	delta := dtheta / float64(n)
	t := 4.0 / 3.0 * math.Tan(delta/4)
	mapPt := func(x, y float64) geom.Pt {
		return geom.Pt{X: cx + rx*cosPhi*x - ry*sinPhi*y, Y: cy + rx*sinPhi*x + ry*cosPhi*y}
	}
	for i := 0; i < n; i++ {
		s1, c1 := math.Sincos(theta)
		s2, c2 := math.Sincos(theta + delta)
		cp1 := mapPt(c1-t*s1, s1+t*c1)
		cp2 := mapPt(c2+t*s2, s2-t*c2)
		end := mapPt(c2, s2)
		if i == n-1 {
			end = p1
		}
		f.cubicTo(cp1, cp2, end)
		theta += delta
	}
}

func (f *flattener) closePath() {
	if len(f.cur) > 0 {
		f.flush(true)
	}
}

func (f *flattener) finish() []geom.Polyline {
	f.flush(false)
	return f.out
}

// ---------------------------------------------------------------------------
// path data scanner

type scanner struct {
	s string
	i int
}

func isWS(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' || c == ','
}

func (sc *scanner) skipWS() {
	for sc.i < len(sc.s) && isWS(sc.s[sc.i]) {
		sc.i++
	}
}

func (sc *scanner) eof() bool { sc.skipWS(); return sc.i >= len(sc.s) }

// command returns the next command letter without consuming it.
func (sc *scanner) command() (byte, bool) {
	sc.skipWS()
	if sc.i >= len(sc.s) {
		return 0, false
	}
	c := sc.s[sc.i]
	if (c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z') && c != 'e' && c != 'E' {
		return c, true
	}
	return 0, false
}

func (sc *scanner) number() (float64, bool) {
	sc.skipWS()
	s := sc.s
	i := sc.i
	start := i
	if i < len(s) && (s[i] == '+' || s[i] == '-') {
		i++
	}
	digits := false
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
		digits = true
	}
	if i < len(s) && s[i] == '.' {
		i++
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
			digits = true
		}
	}
	if !digits {
		return 0, false
	}
	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		j := i + 1
		if j < len(s) && (s[j] == '+' || s[j] == '-') {
			j++
		}
		k := j
		for k < len(s) && s[k] >= '0' && s[k] <= '9' {
			k++
		}
		if k > j {
			i = k
		}
	}
	v := parseFloatFast(s[start:i])
	sc.i = i
	return v, true
}

// flag reads an arc flag, which may be packed without separators ("a1 1 0 01...").
func (sc *scanner) flag() (bool, bool) {
	sc.skipWS()
	if sc.i < len(sc.s) && (sc.s[sc.i] == '0' || sc.s[sc.i] == '1') {
		v := sc.s[sc.i] == '1'
		sc.i++
		return v, true
	}
	return false, false
}

func (sc *scanner) nums(n int) ([]float64, bool) {
	out := make([]float64, n)
	for k := 0; k < n; k++ {
		v, ok := sc.number()
		if !ok {
			return nil, false
		}
		out[k] = v
	}
	return out, true
}

// parsePathData feeds an SVG path "d" string into f. On malformed input it
// stops and keeps everything rendered so far, as the SVG spec requires.
func parsePathData(d string, f *flattener) {
	sc := &scanner{s: d}
	var cmd byte
	cp := geom.Pt{}    // current point (local)
	start := geom.Pt{} // subpath start
	var lastCtrl geom.Pt
	var lastCmd byte
	for !sc.eof() {
		if c, ok := sc.command(); ok {
			cmd = c
			sc.i++
		} else if cmd == 0 || cmd == 'z' || cmd == 'Z' {
			return // numbers without a command, or after closepath
		}
		rel := cmd >= 'a'
		base := geom.Pt{}
		if rel {
			base = cp
		}
		switch cmd {
		case 'M', 'm':
			v, ok := sc.nums(2)
			if !ok {
				return
			}
			p := geom.Pt{X: v[0], Y: v[1]}.Add(base)
			f.moveTo(p)
			cp, start = p, p
			if rel {
				cmd = 'l'
			} else {
				cmd = 'L'
			}
		case 'L', 'l':
			v, ok := sc.nums(2)
			if !ok {
				return
			}
			cp = geom.Pt{X: v[0], Y: v[1]}.Add(base)
			f.lineTo(cp)
		case 'H', 'h':
			v, ok := sc.number()
			if !ok {
				return
			}
			if rel {
				cp.X += v
			} else {
				cp.X = v
			}
			f.lineTo(cp)
		case 'V', 'v':
			v, ok := sc.number()
			if !ok {
				return
			}
			if rel {
				cp.Y += v
			} else {
				cp.Y = v
			}
			f.lineTo(cp)
		case 'C', 'c':
			v, ok := sc.nums(6)
			if !ok {
				return
			}
			c1 := geom.Pt{X: v[0], Y: v[1]}.Add(base)
			c2 := geom.Pt{X: v[2], Y: v[3]}.Add(base)
			p := geom.Pt{X: v[4], Y: v[5]}.Add(base)
			f.cubicTo(c1, c2, p)
			lastCtrl, cp = c2, p
		case 'S', 's':
			v, ok := sc.nums(4)
			if !ok {
				return
			}
			c1 := cp
			if lastCmd == 'C' || lastCmd == 'c' || lastCmd == 'S' || lastCmd == 's' {
				c1 = cp.Mul(2).Sub(lastCtrl)
			}
			c2 := geom.Pt{X: v[0], Y: v[1]}.Add(base)
			p := geom.Pt{X: v[2], Y: v[3]}.Add(base)
			f.cubicTo(c1, c2, p)
			lastCtrl, cp = c2, p
		case 'Q', 'q':
			v, ok := sc.nums(4)
			if !ok {
				return
			}
			c := geom.Pt{X: v[0], Y: v[1]}.Add(base)
			p := geom.Pt{X: v[2], Y: v[3]}.Add(base)
			f.quadTo(c, p)
			lastCtrl, cp = c, p
		case 'T', 't':
			v, ok := sc.nums(2)
			if !ok {
				return
			}
			c := cp
			if lastCmd == 'Q' || lastCmd == 'q' || lastCmd == 'T' || lastCmd == 't' {
				c = cp.Mul(2).Sub(lastCtrl)
			}
			p := geom.Pt{X: v[0], Y: v[1]}.Add(base)
			f.quadTo(c, p)
			lastCtrl, cp = c, p
		case 'A', 'a':
			rx, ok1 := sc.number()
			ry, ok2 := sc.number()
			rot, ok3 := sc.number()
			large, ok4 := sc.flag()
			sweep, ok5 := sc.flag()
			x, ok6 := sc.number()
			y, ok7 := sc.number()
			if !(ok1 && ok2 && ok3 && ok4 && ok5 && ok6 && ok7) {
				return
			}
			p := geom.Pt{X: x, Y: y}.Add(base)
			f.ensure()
			f.arcTo(cp, rx, ry, rot, large, sweep, p)
			cp = p
		case 'Z', 'z':
			f.closePath()
			cp = start
			f.start = start
		default:
			return
		}
		lastCmd = cmd
	}
}
