package raster

import (
	"math"
	"sort"

	"bardraw/internal/geom"
)

// Layer is one painted object of a layered drawing, in painting order.
type Layer struct {
	Polys       []geom.Polyline
	EvenOdd     bool
	R, G, B, A  float64   // 0..1
	StrokeWidth float64   // > 0: the polylines are stroked with this width instead of filled
	Clips       []ClipSet // visible only inside all of these
}

// ClipSet is a clip region (document coordinates).
type ClipSet struct {
	Polys   []geom.Polyline
	EvenOdd bool
}

// LayeredOptions tune visible-line extraction.
type LayeredOptions struct {
	Resolution   int     // raster long side (0 = 1400)
	LineWidth    float64 // visible regions narrower than this fraction of the drawing size are lines (0 = 0.02)
	EdgeContrast float64 // minimum colour difference (0..1) for a boundary to be drawn (0 = 0.08)
}

// LayeredInfo reports what the extraction found.
type LayeredInfo struct {
	Resolution int
	Colors     int // distinct visible colours after merging near-identical ones
	LineFrac   float64
	EdgeFrac   float64
}

// Visible renders the layers as they would look and returns only what can
// be seen: centre-lines of thin visible regions (line art drawn as filled
// shapes or strokes) and the boundaries between visible areas of different
// colour. Anything covered by objects in front is never drawn.
func Visible(layers []Layer, bounds geom.Rect, o LayeredOptions) ([]geom.Polyline, LayeredInfo) {
	if o.Resolution <= 0 {
		o.Resolution = 1400
	}
	if o.LineWidth <= 0 {
		o.LineWidth = 0.02
	}
	if o.EdgeContrast <= 0 {
		o.EdgeContrast = 0.08
	}
	c := NewCanvas(bounds, o.Resolution)
	w, h := c.W, c.H
	n := w * h

	// 1. composite colours (no anti-aliasing: every pixel gets one colour)
	rgb := make([]float32, 3*n)
	covered := make([]bool, n)
	for i := range rgb {
		rgb[i] = 1
	}
	clipCache := map[*geom.Polyline][]bool{}
	for li := range layers {
		l := &layers[li]
		a := float32(clamp01(l.A))
		if a < 0.01 {
			continue
		}
		var clip []uint8
		if len(l.Clips) > 0 {
			clip = make([]uint8, n)
			for _, cs := range l.Clips {
				if len(cs.Polys) == 0 {
					continue
				}
				m, ok := clipCache[&cs.Polys[0]]
				if !ok {
					m = make([]bool, n)
					c.Spans(cs.Polys, cs.EvenOdd, func(y, xa, xb int) {
						for x := xa; x <= xb; x++ {
							m[y*w+x] = true
						}
					})
					clipCache[&cs.Polys[0]] = m
				}
				for i, in := range m {
					if in {
						clip[i]++
					}
				}
			}
		}
		need := uint8(len(l.Clips))
		r, g, b := float32(l.R), float32(l.G), float32(l.B)
		paint := func(y, xa, xb int) {
			for x := xa; x <= xb; x++ {
				i := y*w + x
				if clip != nil && clip[i] < need {
					continue
				}
				covered[i] = true
				if a >= 0.999 {
					rgb[3*i], rgb[3*i+1], rgb[3*i+2] = r, g, b
				} else {
					rgb[3*i] += (r - rgb[3*i]) * a
					rgb[3*i+1] += (g - rgb[3*i+1]) * a
					rgb[3*i+2] += (b - rgb[3*i+2]) * a
				}
			}
		}
		if l.StrokeWidth > 0 {
			strokePolys := strokeOutline(l.Polys, l.StrokeWidth, c)
			spans(w, h, strokePolys, func(p geom.Pt) geom.Pt { return p }, false, paint)
		} else {
			c.Spans(l.Polys, l.EvenOdd, paint)
		}
	}

	// 2. label pixels by colour, merging near-identical colours
	label, reps := labelColors(rgb, covered, n)
	info := LayeredInfo{Resolution: o.Resolution, Colors: len(reps)}
	const bgLabel = 0 // uncovered pixels
	contrast := func(a, b int32) float64 {
		ra, rb := reps[a], reps[b]
		dr, dg, db := ra[0]-rb[0], ra[1]-rb[1], ra[2]-rb[2]
		return math.Sqrt((2*dr*dr + 4*dg*dg + 3*db*db) / 9)
	}

	// 3. thick vs thin: morphological opening with a disc of radius T
	long := float64(max(w, h) - 2*pad)
	T := math.Max(2, o.LineWidth*long/2)
	boundary := make([]bool, n)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			i := y*w + x
			l := label[i]
			if (x+1 < w && label[i+1] != l) || (y+1 < h && label[i+w] != l) ||
				(x > 0 && label[i-1] != l) || (y > 0 && label[i-w] != l) {
				boundary[i] = true
			}
		}
	}
	dB := edt(boundary, w, h) // squared distance to a colour change
	core := make([]bool, n)
	for i, d := range dB {
		core[i] = float64(d) > T*T
	}
	dC := edt(core, w, h)
	limit := float32((T + 1) * (T + 1))
	thick := make([]bool, n)
	for i, d := range dC {
		thick[i] = d <= limit
	}
	// the background colour (what surrounds the drawing) never forms lines
	bgColor := dominantBorderLabel(label, w, h)

	// 4. decide per connected region whether it is a line: mostly thin and
	// not lighter than its surroundings (outlines are darker than the fills
	// they border; slivers of fill and highlights are not)
	comp, ncomp := components(label, w, h)
	type stat struct {
		px, thin int
		luma     float64
		nbLuma   float64
		nbCount  int
		lbl      int32
	}
	st := make([]stat, ncomp)
	luma := func(l int32) float64 { r := reps[l]; return 0.2126*r[0] + 0.7152*r[1] + 0.0722*r[2] }
	for i := 0; i < n; i++ {
		c := &st[comp[i]]
		c.px++
		c.lbl = label[i]
		if !thick[i] {
			c.thin++
		}
		x, y := i%w, i/w
		for _, j := range [4]int{i + 1, i - 1, i + w, i - w} {
			if (j == i+1 && x+1 >= w) || (j == i-1 && x == 0) || (j == i+w && y+1 >= h) || (j == i-w && y == 0) {
				continue
			}
			if comp[j] != comp[i] {
				c.nbLuma += luma(label[j])
				c.nbCount++
			}
		}
	}
	isLine := make([]bool, ncomp)
	for k := range st {
		c := &st[k]
		if c.px == 0 || c.lbl == bgLabel || c.lbl == bgColor || c.nbCount == 0 {
			continue
		}
		thinRatio := float64(c.thin) / float64(c.px)
		darker := luma(c.lbl) <= c.nbLuma/float64(c.nbCount)+0.02
		isLine[k] = thinRatio > 0.6 && darker
	}

	// 5. masks: ink = line regions, edges = boundaries between other regions
	m := &Mask{W: w, H: h, B: make([]uint8, n)}
	ink := func(i int) bool { return isLine[comp[i]] }
	inkPx, edgePx := 0, 0
	for y := 1; y < h-1; y++ {
		for x := 1; x < w-1; x++ {
			i := y*w + x
			if ink(i) {
				m.B[i] = 1
				inkPx++
				continue
			}
			l := label[i]
			for _, j := range [2]int{i + 1, i + w} {
				lj := label[j]
				if lj == l || ink(j) || contrast(l, lj) < o.EdgeContrast {
					continue
				}
				// one pixel wide line on a deterministic side
				k := i
				if lj > l {
					k = j
				}
				if m.B[k] == 0 {
					m.B[k] = 1
					edgePx++
				}
			}
		}
	}
	info.LineFrac = float64(inkPx) / float64(n)
	info.EdgeFrac = float64(edgePx) / float64(n)
	for x := 0; x < w; x++ {
		m.B[x], m.B[(h-1)*w+x] = 0, 0
	}
	for y := 0; y < h; y++ {
		m.B[y*w], m.B[y*w+w-1] = 0, 0
	}

	if DebugDir != "" {
		dumpDebug(DebugDir, w, h, rgb, label, thick, m)
	}
	m.Thin()
	polys := m.Trace(math.Max(4, T))
	for i := range polys {
		for k, p := range polys[i].Pts {
			polys[i].Pts[k] = c.ToDoc(p)
		}
	}
	return polys, info
}

// components labels 4-connected regions of equal label.
func components(label []int32, w, h int) ([]int32, int) {
	comp := make([]int32, len(label))
	for i := range comp {
		comp[i] = -1
	}
	next := int32(0)
	stack := make([]int, 0, 1024)
	for s := range label {
		if comp[s] >= 0 {
			continue
		}
		comp[s] = next
		stack = append(stack[:0], s)
		l := label[s]
		for len(stack) > 0 {
			i := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			x, y := i%w, i/w
			if x+1 < w && comp[i+1] < 0 && label[i+1] == l {
				comp[i+1] = next
				stack = append(stack, i+1)
			}
			if x > 0 && comp[i-1] < 0 && label[i-1] == l {
				comp[i-1] = next
				stack = append(stack, i-1)
			}
			if y+1 < h && comp[i+w] < 0 && label[i+w] == l {
				comp[i+w] = next
				stack = append(stack, i+w)
			}
			if y > 0 && comp[i-w] < 0 && label[i-w] == l {
				comp[i-w] = next
				stack = append(stack, i-w)
			}
		}
		next++
	}
	return comp, int(next)
}

// labelColors assigns each pixel a colour cluster id; 0 = not covered.
func labelColors(rgb []float32, covered []bool, n int) ([]int32, [][3]float64) {
	key := func(i int) uint32 {
		q := func(v float32) uint32 { return uint32(math.Round(float64(clamp01(float64(v)) * 255))) }
		return q(rgb[3*i])<<16 | q(rgb[3*i+1])<<8 | q(rgb[3*i+2])
	}
	counts := map[uint32]int{}
	for i := 0; i < n; i++ {
		if covered[i] {
			counts[key(i)]++
		}
	}
	keys := make([]uint32, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	// most common first, so clusters are represented by their main colour
	sort.Slice(keys, func(a, b int) bool {
		if counts[keys[a]] != counts[keys[b]] {
			return counts[keys[a]] > counts[keys[b]]
		}
		return keys[a] < keys[b]
	})
	col := func(k uint32) [3]float64 {
		return [3]float64{float64(k>>16&255) / 255, float64(k>>8&255) / 255, float64(k&255) / 255}
	}
	reps := [][3]float64{{1, 1, 1}} // label 0: uncovered (paper)
	assign := map[uint32]int32{}
	const merge = 0.035
	for _, k := range keys {
		c := col(k)
		best := int32(-1)
		if len(reps) < 4096 {
			for li := 1; li < len(reps); li++ {
				r := reps[li]
				if math.Abs(r[0]-c[0]) < merge && math.Abs(r[1]-c[1]) < merge && math.Abs(r[2]-c[2]) < merge {
					best = int32(li)
					break
				}
			}
		}
		if best < 0 {
			reps = append(reps, c)
			best = int32(len(reps) - 1)
		}
		assign[k] = best
	}
	label := make([]int32, n)
	for i := 0; i < n; i++ {
		if covered[i] {
			label[i] = assign[key(i)]
		}
	}
	return label, reps
}

// dominantBorderLabel is the label most common along the image border.
func dominantBorderLabel(label []int32, w, h int) int32 {
	cnt := map[int32]int{}
	for x := 0; x < w; x++ {
		cnt[label[x]]++
		cnt[label[(h-1)*w+x]]++
	}
	for y := 0; y < h; y++ {
		cnt[label[y*w]]++
		cnt[label[y*w+w-1]]++
	}
	best, bc := int32(0), -1
	for l, c := range cnt {
		if c > bc || (c == bc && l < best) {
			best, bc = l, c
		}
	}
	return best
}

// strokeOutline converts stroked polylines into consistently oriented
// polygons (segment quads + round joins) in raster coordinates.
func strokeOutline(polys []geom.Polyline, width float64, c *Canvas) []geom.Polyline {
	r := width * c.Scale() / 2
	if r < 0.5 {
		r = 0.5
	}
	var out []geom.Polyline
	orient := func(pts []geom.Pt) []geom.Pt {
		a := 0.0
		for i := range pts {
			p, q := pts[i], pts[(i+1)%len(pts)]
			a += p.X*q.Y - q.X*p.Y
		}
		if a < 0 {
			for i, j := 0, len(pts)-1; i < j; i, j = i+1, j-1 {
				pts[i], pts[j] = pts[j], pts[i]
			}
		}
		return pts
	}
	disc := func(p geom.Pt) {
		const k = 12
		pts := make([]geom.Pt, k)
		for i := 0; i < k; i++ {
			s, co := math.Sincos(2 * math.Pi * float64(i) / k)
			pts[i] = geom.Pt{X: p.X + r*co, Y: p.Y + r*s}
		}
		out = append(out, geom.Polyline{Pts: orient(pts), Closed: true})
	}
	for _, pl := range polys {
		pts := make([]geom.Pt, 0, len(pl.Pts)+1)
		for _, p := range pl.Open() {
			pts = append(pts, c.ToRaster(p))
		}
		for i, p := range pts {
			disc(p)
			if i == 0 {
				continue
			}
			a := pts[i-1]
			d := p.Sub(a)
			l := d.Len()
			if l < 1e-9 {
				continue
			}
			nx, ny := -d.Y/l*r, d.X/l*r
			q := []geom.Pt{{X: a.X + nx, Y: a.Y + ny}, {X: p.X + nx, Y: p.Y + ny}, {X: p.X - nx, Y: p.Y - ny}, {X: a.X - nx, Y: a.Y - ny}}
			out = append(out, geom.Polyline{Pts: orient(q), Closed: true})
		}
	}
	return out
}

// edt returns the squared Euclidean distance of every pixel to the nearest
// set pixel (Felzenszwalb & Huttenlocher). With no set pixels all distances
// are large.
func edt(set []bool, w, h int) []float32 {
	const inf = 1e20
	f := make([]float64, max(w, h))
	d := make([]float64, max(w, h))
	v := make([]int, max(w, h))
	z := make([]float64, max(w, h)+1)
	out := make([]float32, w*h)
	tmp := make([]float64, w*h)
	for i, s := range set {
		if s {
			tmp[i] = 0
		} else {
			tmp[i] = inf
		}
	}
	dt1 := func(n int) {
		sect := func(q, p int) float64 {
			return ((f[q] + float64(q*q)) - (f[p] + float64(p*p))) / float64(2*q-2*p)
		}
		k := 0
		v[0] = 0
		z[0], z[1] = -inf, inf
		for q := 1; q < n; q++ {
			s := sect(q, v[k])
			for s <= z[k] {
				k--
				s = sect(q, v[k])
			}
			k++
			v[k] = q
			z[k], z[k+1] = s, inf
		}
		k = 0
		for q := 0; q < n; q++ {
			for z[k+1] < float64(q) {
				k++
			}
			dq := float64(q - v[k])
			d[q] = dq*dq + f[v[k]]
		}
	}
	for x := 0; x < w; x++ { // columns
		for y := 0; y < h; y++ {
			f[y] = tmp[y*w+x]
		}
		dt1(h)
		for y := 0; y < h; y++ {
			tmp[y*w+x] = d[y]
		}
	}
	for y := 0; y < h; y++ { // rows
		copy(f[:w], tmp[y*w:(y+1)*w])
		dt1(w)
		for x := 0; x < w; x++ {
			out[y*w+x] = float32(math.Min(d[x], inf))
		}
	}
	return out
}
