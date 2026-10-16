package svg

import (
	"math"
	"strings"
	"testing"

	"bardraw/internal/geom"
)

func near(a, b, eps float64) bool { return math.Abs(a-b) <= eps }

func parse(t *testing.T, s string) *Doc {
	t.Helper()
	d, err := Parse(strings.NewReader(s))
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestPathCommandsAndNumbers(t *testing.T) {
	// packed numbers, implicit lineto after moveto, relative commands, H/V, Z
	d := parse(t, `<svg viewBox="0 0 100 100"><path d="M10-10.5.5 0l5,5h10v-5zm1e1 0L1 2 3 4"/></svg>`)
	if len(d.Shapes) != 1 {
		t.Fatalf("shapes=%d", len(d.Shapes))
	}
	polys := d.Shapes[0].Polys
	if len(polys) != 2 {
		t.Fatalf("subpaths=%d %+v", len(polys), polys)
	}
	first := polys[0]
	want := []geom.Pt{{10, -10.5}, {0.5, 0}, {5.5, 5}, {15.5, 5}, {15.5, 0}}
	if !first.Closed || len(first.Pts) != len(want) {
		t.Fatalf("first=%+v", first)
	}
	for i, p := range want {
		if !near(first.Pts[i].X, p.X, 1e-9) || !near(first.Pts[i].Y, p.Y, 1e-9) {
			t.Fatalf("pt %d = %v want %v", i, first.Pts[i], p)
		}
	}
	// after z the current point is the subpath start (10,-10.5); m10 0 -> (20,-10.5)
	if p := polys[1].Pts[0]; !near(p.X, 20, 1e-9) || !near(p.Y, -10.5, 1e-9) {
		t.Fatalf("second start %v", p)
	}
}

func TestArcFlagsPackedAndCircle(t *testing.T) {
	d := parse(t, `<svg viewBox="0 0 100 100">
		<path d="M0 50a50 50 0 1150 50" fill="none" stroke="black"/>
		<circle cx="50" cy="50" r="40"/>
	</svg>`)
	if len(d.Shapes) != 2 {
		t.Fatalf("shapes=%d", len(d.Shapes))
	}
	end := d.Shapes[0].Polys[0].Pts
	last := end[len(end)-1]
	if !near(last.X, 50, 1e-6) || !near(last.Y, 100, 1e-6) {
		t.Fatalf("arc end %v", last)
	}
	for _, p := range d.Shapes[1].Polys[0].Pts {
		if r := p.Dist(geom.Pt{X: 50, Y: 50}); !near(r, 40, 0.05) {
			t.Fatalf("circle point radius %v", r)
		}
	}
	if !d.Shapes[1].Polys[0].Closed {
		t.Fatal("circle not closed")
	}
}

func TestTransformsUseAndCSS(t *testing.T) {
	d := parse(t, `<svg xmlns:xlink="http://www.w3.org/1999/xlink" viewBox="0 0 200 200">
		<style>.ink{fill:none;stroke:#000} #hide{display:none} rect.bg{fill:white}</style>
		<defs><rect id="r" width="10" height="10"/></defs>
		<g transform="translate(100,0) scale(2)">
			<use xlink:href="#r" x="5" y="5" class="ink"/>
		</g>
		<rect class="bg" width="200" height="200"/>
		<rect id="hide" width="5" height="5"/>
		<text>hello</text>
	</svg>`)
	if len(d.Shapes) != 2 {
		t.Fatalf("shapes=%d", len(d.Shapes))
	}
	u := d.Shapes[0]
	if u.Fill.Set || !u.Stroke.Visible() {
		t.Fatalf("use paint fill=%+v stroke=%+v", u.Fill, u.Stroke)
	}
	b := geom.Bounds(u.Polys)
	if !near(b.Min.X, 110, 1e-9) || !near(b.Min.Y, 10, 1e-9) || !near(b.W(), 20, 1e-9) {
		t.Fatalf("use bounds %+v", b)
	}
	if !near(u.StrokeWidth, 2, 1e-9) {
		t.Fatalf("stroke width %v", u.StrokeWidth)
	}
	if bg := d.Shapes[1]; bg.Fill.Luma() < 0.99 {
		t.Fatalf("bg fill %+v", bg.Fill)
	}
	if d.Skipped["text"] != 1 {
		t.Fatalf("skipped %+v", d.Skipped)
	}
}

func TestPotraceStyleFile(t *testing.T) {
	// structure of the attached coloring page: y-flipped group, relative cubics
	d := parse(t, `<svg viewBox="0 0 120 100"><g transform="translate(0,100) scale(0.1,-0.1)" fill="#000000" stroke="none">
	<path d="M100 100 c0 -50 50 -50 50 0 l100 0 0 100 -100 0z m20 20 l10 0 0 10z"/></g></svg>`)
	if len(d.Shapes) != 1 || len(d.Shapes[0].Polys) != 2 {
		t.Fatalf("%+v", d.Shapes)
	}
	b := d.ContentBounds()
	if !near(b.Min.X, 10, 1e-9) || !near(b.Max.Y, 93.75, 1e-9) || !near(b.Min.Y, 80, 1e-9) {
		t.Fatalf("bounds %+v", b)
	}
	if d.Shapes[0].Fill.Luma() != 0 || d.Shapes[0].Stroke.Set {
		t.Fatalf("paint %+v", d.Shapes[0])
	}
}

func TestColors(t *testing.T) {
	for _, c := range []struct {
		in   string
		luma float64
	}{{"#fff", 1}, {"#000000", 0}, {"rgb(255,255,255)", 1}, {"white", 1}, {"hsl(0, 0%, 100%)", 1}, {"rgb(0% 0% 0%)", 0}} {
		r, g, b, _, ok := parseColor(c.in)
		if !ok {
			t.Fatalf("%s not parsed", c.in)
		}
		if l := 0.2126*r + 0.7152*g + 0.0722*b; !near(l, c.luma, 1e-6) {
			t.Fatalf("%s luma %v", c.in, l)
		}
	}
}

func TestRDP(t *testing.T) {
	var pts []geom.Pt
	for i := 0; i <= 100; i++ {
		pts = append(pts, geom.Pt{X: float64(i), Y: 0.01 * math.Sin(float64(i))})
	}
	s := geom.SimplifyRDP(pts, 0.5)
	if len(s) != 2 {
		t.Fatalf("straight line simplified to %d points", len(s))
	}
	ring := []geom.Pt{{0, 0}, {10, 0}, {10, 10}, {0, 10}}
	c := geom.SimplifyClosed(ring, 0.1)
	if len(c) != 5 || c[0] != c[4] {
		t.Fatalf("closed %v", c)
	}
}

func TestClipPath(t *testing.T) {
	d := parse(t, `<svg viewBox="0 0 100 100">
		<defs><clipPath id="c"><rect x="0" y="0" width="50" height="100"/></clipPath>
		<clipPath id="empty"></clipPath></defs>
		<g clip-path="url(#c)" transform="translate(10,0)"><circle cx="50" cy="50" r="20"/></g>
		<rect width="10" height="10" style="clip-path:url(#empty)"/>
		<rect width="10" height="10" clip-path="url(#missing)"/>
	</svg>`)
	if len(d.Shapes) != 2 {
		t.Fatalf("shapes=%d (empty clip must hide, missing clip must be ignored)", len(d.Shapes))
	}
	cs := d.Shapes[0].Clips
	if len(cs) != 1 {
		t.Fatalf("clips=%d", len(cs))
	}
	// the clip is in the user space of the referencing group: shifted by 10
	b := geom.Bounds(cs[0].Polys)
	if !near(b.Min.X, 10, 1e-9) || !near(b.Max.X, 60, 1e-9) {
		t.Fatalf("clip bounds %+v", b)
	}
	if len(d.Shapes[1].Clips) != 0 {
		t.Fatal("unresolvable clip must be ignored")
	}
}
