package raster

import (
	"math"
	"math/rand"
	"testing"

	"bardraw/internal/geom"
)

func TestEDTMatchesBruteForce(t *testing.T) {
	rnd := rand.New(rand.NewSource(1))
	w, h := 37, 23
	set := make([]bool, w*h)
	for i := range set {
		set[i] = rnd.Float64() < 0.03
	}
	got := edt(set, w, h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			best := math.Inf(1)
			for j, s := range set {
				if s {
					dx, dy := float64(j%w-x), float64(j/w-y)
					best = math.Min(best, dx*dx+dy*dy)
				}
			}
			if math.Abs(float64(got[y*w+x])-best) > 1e-3 {
				t.Fatalf("(%d,%d) got %v want %v", x, y, got[y*w+x], best)
			}
		}
	}
}

func rect(x0, y0, x1, y1 float64) []geom.Polyline {
	return []geom.Polyline{{Pts: []geom.Pt{{X: x0, Y: y0}, {X: x1, Y: y0}, {X: x1, Y: y1}, {X: x0, Y: y1}}, Closed: true}}
}

// totalLength sums polyline lengths.
func totalLength(ps []geom.Polyline) float64 {
	l := 0.0
	for _, p := range ps {
		l += geom.PathLength(p.Pts)
	}
	return l
}

func TestVisibleHidesCoveredEdges(t *testing.T) {
	// a red square half covered by a blue square: the covered quarter of the
	// red outline must not be drawn
	bounds := geom.Rect{Min: geom.Pt{X: 0, Y: 0}, Max: geom.Pt{X: 150, Y: 150}}
	layers := []Layer{
		{Polys: rect(0, 0, 100, 100), R: 1, A: 1},
		{Polys: rect(50, 50, 150, 150), B: 1, A: 1},
	}
	polys, _ := Visible(layers, bounds, LayeredOptions{Resolution: 600})
	// visible: red outer L (100+100+50+50) + full blue outline (400) = 700;
	// the hidden red edges (x=100 and y=100 inside blue, 100 long) are gone
	got := totalLength(polys)
	if got < 670 || got > 730 {
		t.Fatalf("visible length %.0f, want ~700", got)
	}
	for _, p := range polys {
		for _, q := range p.Pts {
			if q.X > 55 && q.X < 145 && q.Y > 55 && q.Y < 145 {
				t.Fatalf("hidden edge drawn at %v", q)
			}
		}
	}
}

func TestVisibleOutlineRingBecomesOneLine(t *testing.T) {
	// classic vector art: dark outline shape with a lighter fill on top that
	// is a little smaller — the visible ring must become ONE centre-line
	bounds := geom.Rect{Min: geom.Pt{X: 0, Y: 0}, Max: geom.Pt{X: 200, Y: 200}}
	layers := []Layer{
		{Polys: rect(0, 0, 200, 200), R: 0.9, G: 0.8, B: 0.3, A: 1},
		{Polys: rect(4, 4, 196, 196), R: 1, G: 1, B: 0.7, A: 1},
	}
	polys, _ := Visible(layers, bounds, LayeredOptions{Resolution: 800})
	got := totalLength(polys)
	want := 4 * 196.0 // centre-line of the 4 px ring
	if got < want*0.93 || got > want*1.07 {
		t.Fatalf("length %.0f, want ~%.0f (one line, not two)", got, want)
	}
}

func TestVisibleClipAndStroke(t *testing.T) {
	bounds := geom.Rect{Min: geom.Pt{X: 0, Y: 0}, Max: geom.Pt{X: 100, Y: 100}}
	line := []geom.Polyline{{Pts: []geom.Pt{{X: 10, Y: 50}, {X: 90, Y: 50}}}}
	layers := []Layer{
		{Polys: line, StrokeWidth: 2, A: 1, Clips: []ClipSet{{Polys: rect(0, 0, 50, 100)}}},
	}
	polys, _ := Visible(layers, bounds, LayeredOptions{Resolution: 500})
	got := totalLength(polys)
	if got < 36 || got > 44 {
		t.Fatalf("clipped stroke length %.1f, want ~40", got)
	}
	for _, p := range polys {
		for _, q := range p.Pts {
			if q.X > 52 {
				t.Fatalf("clipped part drawn at %v", q)
			}
		}
	}
}
