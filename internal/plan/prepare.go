// Package plan turns an SVG into pen strokes for a target box, and models
// how long Beyond All Reason needs to draw them.
package plan

import (
	"fmt"
	"math"

	"bardraw/internal/geom"
	"bardraw/internal/raster"
	"bardraw/internal/svg"
)

// Modes for filled shapes.
const (
	ModeAuto       = "auto"       // pick per drawing
	ModeOutline    = "outline"    // draw the boundary of every filled shape
	ModeCenterline = "centerline" // draw the centre line of filled ink (line art)
	ModeLayered    = "layered"    // only what is visible: occluded edges are hidden
)

// Modes lists the cycle order used by the UI.
var Modes = []string{ModeAuto, ModeLayered, ModeOutline, ModeCenterline}

// PrepOptions control how an SVG becomes drawable geometry.
type PrepOptions struct {
	Mode           string
	FitViewBox     bool    // fit the declared canvas instead of the visible content
	SkipWhiteFills bool    // ignore near-white filled shapes without stroke (backgrounds, highlights)
	CenterlineRes  int     // raster long side for centre-line mode, 0 = auto
	LayeredRes     int     // raster long side for layered mode, 0 = auto
	LineWidth      float64 // layered: regions thinner than this fraction of the size are lines (0 = default)
	EdgeContrast   float64 // layered: minimum colour difference of a drawn boundary (0 = default)
}

// Geometry is resolution-independent drawable geometry in document space.
type Geometry struct {
	Strokes []geom.Polyline // what the pen will follow
	Ref     []geom.Polyline // all visible outlines (high-quality preview)
	Bounds  geom.Rect       // rectangle that is fitted into the user's box
	Mode    string          // effective mode (outline or centerline)
	Note    string          // human-readable info
	Skipped map[string]int
	Points  int // total points in Strokes
}

// Prepare converts a parsed document into Geometry.
func Prepare(doc *svg.Doc, o PrepOptions) *Geometry {
	auto := o.Mode != ModeOutline && o.Mode != ModeCenterline && o.Mode != ModeLayered
	if o.Mode == ModeLayered || (auto && isLayeredArt(doc)) {
		return prepareLayered(doc, o, auto)
	}
	return prepareFlat(doc, o)
}

// isLayeredArt reports whether the drawing is made of overlapping objects
// in several colours (or uses clipping), where hidden edges matter.
func isLayeredArt(doc *svg.Doc) bool {
	var colors [][3]float64
	for _, s := range doc.Shapes {
		if len(s.Clips) > 0 && (s.Fill.Visible() || s.Stroke.Visible()) {
			return true
		}
		for _, p := range []svg.Paint{s.Fill, s.Stroke} {
			if !p.Visible() || p.Luma() > 0.92 {
				continue // white is paper, not a colour of its own
			}
			c := [3]float64{p.R, p.G, p.B}
			found := false
			for _, k := range colors {
				if math.Abs(k[0]-c[0])+math.Abs(k[1]-c[1])+math.Abs(k[2]-c[2]) < 0.15 {
					found = true
					break
				}
			}
			if !found {
				colors = append(colors, c)
				if len(colors) >= 2 {
					return true
				}
			}
		}
	}
	return false
}

// prepareLayered composites the drawing and keeps only visible lines.
func prepareLayered(doc *svg.Doc, o PrepOptions, auto bool) *Geometry {
	g := &Geometry{Skipped: doc.Skipped, Mode: ModeLayered}
	var layers []raster.Layer
	bounds := geom.EmptyRect()
	toClips := func(cs []svg.Clip) []raster.ClipSet {
		out := make([]raster.ClipSet, len(cs))
		for i, c := range cs {
			out[i] = raster.ClipSet{Polys: c.Polys, EvenOdd: c.EvenOdd}
		}
		return out
	}
	for _, s := range doc.Shapes {
		clips := toClips(s.Clips)
		if s.Fill.Visible() {
			layers = append(layers, raster.Layer{Polys: s.Polys, EvenOdd: s.EvenOdd,
				R: s.Fill.R, G: s.Fill.G, B: s.Fill.B, A: s.Fill.A, Clips: clips})
		}
		if s.Stroke.Visible() {
			layers = append(layers, raster.Layer{Polys: s.Polys, StrokeWidth: s.StrokeWidth,
				R: s.Stroke.R, G: s.Stroke.G, B: s.Stroke.B, A: s.Stroke.A, Clips: clips})
		}
		if s.Fill.Visible() || s.Stroke.Visible() {
			b := geom.Bounds(s.Polys)
			if s.Stroke.Visible() {
				b.Min = b.Min.Sub(geom.Pt{X: s.StrokeWidth / 2, Y: s.StrokeWidth / 2})
				b.Max = b.Max.Add(geom.Pt{X: s.StrokeWidth / 2, Y: s.StrokeWidth / 2})
			}
			if len(s.Clips) > 0 { // clipped objects only count inside their clip
				cb := geom.Bounds(s.Clips[0].Polys)
				b = geom.Rect{Min: geom.Pt{X: math.Max(b.Min.X, cb.Min.X), Y: math.Max(b.Min.Y, cb.Min.Y)},
					Max: geom.Pt{X: math.Min(b.Max.X, cb.Max.X), Y: math.Min(b.Max.Y, cb.Max.Y)}}
			}
			bounds.Union(b)
		}
	}
	if bounds.Empty() {
		g.Bounds = doc.ViewBox
		g.Note = "nothing drawable"
		return g
	}
	g.Bounds = bounds
	if o.FitViewBox && !doc.ViewBox.Empty() {
		g.Bounds = doc.ViewBox
	}
	strokes, info := raster.Visible(layers, bounds, raster.LayeredOptions{
		Resolution: o.LayeredRes, LineWidth: o.LineWidth, EdgeContrast: o.EdgeContrast})
	g.Strokes = strokes
	g.Ref = strokes // the preview shows visible lines only, no x-ray outlines
	for _, s := range g.Strokes {
		g.Points += len(s.Pts)
	}
	g.Note = fmt.Sprintf("visible lines only, %d colours", info.Colors-1)
	return g
}

// prepareFlat handles single-colour drawings (outline / centre-line).
func prepareFlat(doc *svg.Doc, o PrepOptions) *Geometry {
	g := &Geometry{Skipped: doc.Skipped}
	var fills []raster.Fill
	var outline []geom.Polyline
	darkFills := 0
	bounds := geom.EmptyRect()
	for _, s := range doc.Shapes {
		strokeVis := s.Stroke.Visible()
		fillVis := s.Fill.Visible()
		if !strokeVis && !fillVis {
			continue
		}
		white := fillVis && s.Fill.Luma() > 0.9
		if fillVis {
			// visible paint counts as opaque: a faint grey line is still a line to draw
			fills = append(fills, raster.Fill{Polys: s.Polys, EvenOdd: s.EvenOdd, Luma: s.Fill.Luma(), Alpha: 1})
			if s.Fill.Luma() < 0.6 {
				darkFills++
			}
		}
		if white && !strokeVis && o.SkipWhiteFills {
			continue
		}
		b := geom.Bounds(s.Polys)
		bounds.Union(b)
		g.Ref = append(g.Ref, s.Polys...)
		if strokeVis {
			g.Strokes = append(g.Strokes, s.Polys...)
		} else {
			for _, pl := range s.Polys {
				if len(pl.Pts) >= 2 {
					outline = append(outline, geom.Polyline{Pts: pl.Pts, Closed: len(pl.Pts) > 2})
				}
			}
		}
	}
	if bounds.Empty() {
		g.Bounds = doc.ViewBox
		g.Mode = ModeOutline
		g.Note = "nothing drawable"
		return g
	}
	g.Bounds = bounds
	if o.FitViewBox && !doc.ViewBox.Empty() {
		g.Bounds = doc.ViewBox
	}

	mode := o.Mode
	auto := mode != ModeOutline && mode != ModeCenterline
	if auto {
		mode = ModeOutline
		if darkFills > 0 && len(outline) > 0 {
			info := raster.Analyze(fills, bounds)
			// thin ink relative to the picture and not mostly solid → line art
			if info.ThickFrac > 0 && info.ThickFrac < 0.04 && info.InkFraction < 0.3 {
				mode = ModeCenterline
			}
			g.Note = fmt.Sprintf("ink %.1f%% thick, %.0f%% coverage", info.ThickFrac*100, info.InkFraction*100)
		}
	}
	if mode == ModeCenterline && len(fills) > 0 {
		cl, info := raster.Centerline(fills, bounds, o.CenterlineRes)
		g.Strokes = append(g.Strokes, cl...)
		g.Note = fmt.Sprintf("centre-lines from %dpx raster, ink %.1f%% thick", info.Resolution, info.ThickFrac*100)
	} else {
		mode = ModeOutline
		g.Strokes = append(g.Strokes, outline...)
	}
	g.Mode = mode
	for _, s := range g.Strokes {
		g.Points += len(s.Pts)
	}
	return g
}
