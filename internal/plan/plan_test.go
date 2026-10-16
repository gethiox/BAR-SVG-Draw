package plan

import (
	"strings"
	"testing"
	"time"

	"bardraw/internal/geom"
	"bardraw/internal/svg"
)

type recorder struct {
	simDriver
	log []string
}

func (r *recorder) Move(p geom.Pt) error {
	r.log = append(r.log, "move")
	return nil
}
func (r *recorder) Button(d bool) error {
	if d {
		r.log = append(r.log, "down")
	} else {
		r.log = append(r.log, "up")
	}
	return nil
}
func (r *recorder) Key(d bool) error {
	if d {
		r.log = append(r.log, "key+")
	} else {
		r.log = append(r.log, "key-")
	}
	return nil
}

func TestRunScheduleHoldsKey(t *testing.T) {
	tm := Timing{VertexInterval: 70 * time.Millisecond, Settle: 10 * time.Millisecond, PressGap: 300 * time.Millisecond}
	strokes := [][]geom.Pt{{{X: 0, Y: 0}, {X: 10, Y: 0}, {X: 10, Y: 10}}, {{X: 50, Y: 50}, {X: 60, Y: 60}}}
	r := &recorder{}
	next, err := Run(strokes, 0, tm, r)
	if err != nil || next != 2 {
		t.Fatal(next, err)
	}
	// the draw key must be pressed exactly once (a second press within the
	// key-chain timeout opens BAR's label prompt)
	want := "key+ move down move move up move down move up key-"
	if got := strings.Join(r.log, " "); got != want {
		t.Fatalf("got %q", got)
	}
	// key 0 → +40 settle; stroke 1 presses at 50, vertices 120 & 190, up at 200;
	// stroke 2 moves at 200, may press only at 50+300 = 350, vertex 420, up 430
	if exp := 430 * time.Millisecond; r.now != exp {
		t.Fatalf("time %v want %v", r.now, exp)
	}
	if e := Estimate(strokes, 0, tm); e != 430*time.Millisecond {
		t.Fatalf("estimate %v", e)
	}
}

func TestAutoTiming(t *testing.T) {
	tm := NewTiming(60, 0, 10, -1)
	if tm.VertexInterval < 70*time.Millisecond || tm.VertexInterval > 75*time.Millisecond {
		t.Fatalf("vertex %v", tm.VertexInterval)
	}
	if tm.PressGap < 320*time.Millisecond || tm.PressGap > 340*time.Millisecond {
		t.Fatalf("press gap %v", tm.PressGap)
	}
	strokes := [][]geom.Pt{{{X: 0, Y: 0}, {X: 1, Y: 0}}, {{X: 5, Y: 5}, {X: 6, Y: 6}}}
	starts := StrokeStartTimes(strokes, tm)
	if len(starts) != 3 || starts[1]-starts[0] < tm.PressGap {
		t.Fatalf("starts %v", starts)
	}
}

func TestBuildFitsBox(t *testing.T) {
	doc, err := svg.Parse(strings.NewReader(`<svg viewBox="0 0 200 100">
		<rect x="0" y="0" width="200" height="100" fill="white"/>
		<path d="M10 10 L190 10 L190 90" fill="none" stroke="black"/>
		<circle cx="100" cy="50" r="20" fill="none" stroke="black"/>
		<path d="M190 90 L10 90" fill="none" stroke="black"/></svg>`))
	if err != nil {
		t.Fatal(err)
	}
	g := Prepare(doc, PrepOptions{Mode: ModeAuto, SkipWhiteFills: true})
	if g.Mode != ModeOutline {
		t.Fatalf("mode %s", g.Mode)
	}
	p := Build(g, 400, 400, Params{Level: 4})
	for _, s := range p.Strokes {
		for _, q := range s {
			if q.X < 0 || q.X > 400 || q.Y < 100 || q.Y > 300 {
				t.Fatalf("point %v outside fitted area", q)
			}
		}
	}
	// the two open paths share an endpoint and must be joined into one stroke
	if len(p.Strokes) != 2 {
		t.Fatalf("strokes %d: %v", len(p.Strokes), p.Strokes)
	}
	// straight lines need only their corners
	for _, s := range p.Strokes {
		if s[0] != s[len(s)-1] && len(s) != 4 {
			t.Fatalf("open stroke has %d vertices: %v", len(s), s)
		}
	}
	rot := Build(g, 400, 400, Params{Level: 4, Xf: Transform{Rotate: 1}})
	if r := rot.Fit; r.W() > 201 || r.H() < 399 {
		t.Fatalf("rotated fit %+v", r)
	}
}

func TestCenterlineSingleStroke(t *testing.T) {
	// a thick filled "L" drawn as outline shape must become one centre-line stroke
	doc, err := svg.Parse(strings.NewReader(`<svg viewBox="0 0 1000 1000">
		<path d="M100 100 H120 V880 H900 V900 H100 Z"/></svg>`))
	if err != nil {
		t.Fatal(err)
	}
	g := Prepare(doc, PrepOptions{Mode: ModeAuto})
	if g.Mode != ModeCenterline {
		t.Fatalf("mode %s (%s)", g.Mode, g.Note)
	}
	p := Build(g, 500, 500, Params{Level: 4})
	if len(p.Strokes) != 1 || len(p.Strokes[0]) > 5 {
		t.Fatalf("strokes %v", p.Strokes)
	}
}
