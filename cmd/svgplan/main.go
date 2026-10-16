// Command svgplan plans an SVG drawing without the game: it prints vertex
// counts and the estimated drawing time for every quality level and can
// render a PNG preview of what the in-game result will look like.
//
//	svgplan -svg drawing.svg -w 600 -h 450 -q 4 -o preview.png
package main

import (
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"time"

	"bardraw/internal/geom"
	"bardraw/internal/plan"
	"bardraw/internal/svg"
)

func main() {
	file := flag.String("svg", "", "SVG file")
	w := flag.Float64("w", 600, "box width in screen pixels")
	h := flag.Float64("h", 450, "box height in screen pixels")
	q := flag.Int("q", 4, "quality level 1..7")
	mode := flag.String("mode", "auto", "auto | outline | centerline")
	fps := flag.Float64("fps", 60, "game frame rate (sets the safe vertex interval)")
	order := flag.String("order", "nearest", "nearest | longest | document")
	maxSeg := flag.Float64("maxseg", 0, "split segments longer than this many px (0 = off)")
	out := flag.String("o", "", "write a PNG preview here")
	verts := flag.Bool("v", false, "mark vertices in the preview")
	flag.Parse()
	if *file == "" {
		flag.Usage()
		os.Exit(2)
	}

	t0 := time.Now()
	doc, err := svg.ParseFile(*file)
	if err != nil {
		fmt.Fprintln(os.Stderr, "parse:", err)
		os.Exit(1)
	}
	tParse := time.Since(t0)
	t0 = time.Now()
	g := plan.Prepare(doc, plan.PrepOptions{Mode: *mode, SkipWhiteFills: true})
	tPrep := time.Since(t0)
	fmt.Printf("%s: %d shapes, skipped %v\n", *file, len(doc.Shapes), doc.Skipped)
	fmt.Printf("mode %s — %s\n", g.Mode, g.Note)
	fmt.Printf("geometry: %d polylines, %d points (parse %v, prepare %v)\n", len(g.Strokes), g.Points, tParse.Round(time.Millisecond), tPrep.Round(time.Millisecond))

	tm := plan.NewTiming(*fps, 0, 12, -1)
	fmt.Printf("timing: vertex %v, settle %v, press gap %v\n", tm.VertexInterval, tm.Settle, tm.PressGap)
	fmt.Printf("box %.0fx%.0f px\n  lvl   eps  strokes  vertices  dropped     ETA\n", *w, *h)
	var chosen *plan.Plan
	for l := 1; l <= len(plan.Levels); l++ {
		t0 = time.Now()
		p := plan.Build(g, *w, *h, plan.Params{Level: l, Order: *order, MaxSeg: *maxSeg})
		dt := time.Since(t0)
		eta := plan.Estimate(p.Strokes, 0, tm)
		mark := " "
		if l == *q {
			mark, chosen = "*", p
		}
		fmt.Printf("%s %2d  %4.2f  %7d  %8d  %7d  %6.1fs   (plan %v)\n", mark, l, plan.Levels[l-1].Eps, len(p.Strokes), p.Vertices, p.Dropped, eta.Seconds(), dt.Round(time.Microsecond*100))
	}
	if *out != "" && chosen != nil {
		if err := writePreview(*out, g, chosen, *w, *h, *verts); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Println("preview:", *out)
	}
}

func writePreview(path string, g *plan.Geometry, p *plan.Plan, w, h float64, verts bool) error {
	const margin = 20
	img := image.NewRGBA(image.Rect(0, 0, int(w)+2*margin, int(h)+2*margin))
	for i := range img.Pix {
		img.Pix[i] = 255
	}
	off := geom.Pt{X: margin, Y: margin}
	// box frame
	frame := color.RGBA{200, 200, 220, 255}
	corners := []geom.Pt{{X: 0, Y: 0}, {X: w, Y: 0}, {X: w, Y: h}, {X: 0, Y: h}, {X: 0, Y: 0}}
	for i := 1; i < len(corners); i++ {
		line(img, corners[i-1].Add(off), corners[i].Add(off), 0.5, frame)
	}
	// high quality reference outlines
	m := plan.FitMatrix(g.Bounds, w, h, plan.Transform{})
	ref := color.RGBA{185, 185, 185, 255}
	for _, pl := range g.Ref {
		pts := pl.Open()
		for i := 1; i < len(pts); i++ {
			line(img, m.Apply(pts[i-1]).Add(off), m.Apply(pts[i]).Add(off), 0.5, ref)
		}
	}
	// in-game estimate: 3 px lines
	ink := color.RGBA{20, 70, 200, 255}
	for _, s := range p.Strokes {
		for i := 1; i < len(s); i++ {
			line(img, s[i-1].Add(off), s[i].Add(off), 1.5, ink)
		}
	}
	if verts {
		red := color.RGBA{220, 30, 30, 255}
		for _, s := range p.Strokes {
			for _, q := range s {
				disc(img, q.Add(off), 1.6, red)
			}
		}
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return png.Encode(f, img)
}

func disc(img *image.RGBA, c geom.Pt, r float64, col color.RGBA) {
	for y := int(math.Floor(c.Y - r)); y <= int(math.Ceil(c.Y+r)); y++ {
		for x := int(math.Floor(c.X - r)); x <= int(math.Ceil(c.X+r)); x++ {
			dx, dy := float64(x)+0.5-c.X, float64(y)+0.5-c.Y
			if dx*dx+dy*dy <= r*r && image.Pt(x, y).In(img.Rect) {
				img.SetRGBA(x, y, col)
			}
		}
	}
}

func line(img *image.RGBA, a, b geom.Pt, r float64, col color.RGBA) {
	n := int(math.Ceil(a.Dist(b)*2)) + 1
	for i := 0; i <= n; i++ {
		disc(img, a.Lerp(b, float64(i)/float64(n)), r, col)
	}
}
