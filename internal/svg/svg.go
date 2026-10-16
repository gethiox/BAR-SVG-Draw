// Package svg is a small, dependency-free SVG reader that flattens the
// drawable geometry of a document into polylines.
//
// Supported: path (all commands), rect (incl. rounded), circle, ellipse,
// line, polyline, polygon, g, nested svg, use/symbol, transforms, CSS in
// <style> (simple selectors), style attributes, presentation attributes,
// inheritance, opacity, display/visibility, gradients (averaged colour).
// Ignored: text (convert to paths first), images, clip paths, masks, filters,
// markers, patterns.
package svg

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"strings"

	"bardraw/internal/geom"
)

// Shape is one drawable element after flattening.
type Shape struct {
	Polys       []geom.Polyline // document (root user) coordinates
	Fill        Paint
	Stroke      Paint
	StrokeWidth float64 // document units
	EvenOdd     bool
	Tag, ID     string
	Clips       []Clip // the shape is only visible inside every one of these
}

// Clip is the geometry of a resolved clip-path (document coordinates).
type Clip struct {
	Polys   []geom.Polyline
	EvenOdd bool
}

// Doc is a parsed SVG document.
type Doc struct {
	ViewBox geom.Rect // root viewBox (or 0 0 width height); may be empty
	Shapes  []Shape
	Skipped map[string]int // elements that cannot be drawn (text, image…)
}

// ContentBounds returns the bounds of all shapes with a visible paint.
func (d *Doc) ContentBounds() geom.Rect {
	r := geom.EmptyRect()
	for _, s := range d.Shapes {
		if s.Fill.Visible() || s.Stroke.Visible() {
			r.Union(geom.Bounds(s.Polys))
		}
	}
	return r
}

// ParseFile reads and parses an SVG file.
func ParseFile(path string) (*Doc, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return Parse(f)
}

// Parse parses an SVG document. Curves are flattened with a tolerance of
// 1/4000 of the drawing size, which is far below a screen pixel for any
// realistic on-screen size.
func Parse(r io.Reader) (*Doc, error) {
	root, err := parseDOM(r)
	if err != nil {
		return nil, err
	}
	var svgRoot *node
	for _, c := range root.children {
		if c.tag == "svg" {
			svgRoot = c
			break
		}
	}
	if svgRoot == nil {
		return nil, errors.New("no <svg> element found")
	}
	p := &parser{ids: map[string]*node{}}
	p.index(svgRoot)

	doc := &Doc{ViewBox: geom.EmptyRect()}
	vp := viewport{w: 100, h: 100}
	if vb := numberList(svgRoot.attrs["viewBox"]); len(vb) == 4 && vb[2] > 0 && vb[3] > 0 {
		doc.ViewBox = geom.RectXYWH(vb[0], vb[1], vb[2], vb[3])
		vp = viewport{vb[2], vb[3]}
	} else {
		w, okw := parseLength(svgRoot.attrs["width"], 0)
		h, okh := parseLength(svgRoot.attrs["height"], 0)
		if okw && okh && w > 0 && h > 0 {
			doc.ViewBox = geom.RectXYWH(0, 0, w, h)
			vp = viewport{w, h}
		}
	}

	size := math.Max(vp.w, vp.h)
	p.tol = size / 4000
	p.walkRoot(svgRoot, vp, doc)

	// If the actual content is much smaller than the declared canvas, refine.
	if b := doc.ContentBounds(); !b.Empty() {
		cs := math.Max(b.W(), b.H())
		if cs > 0 && cs/4000 < p.tol*0.5 {
			p.tol = cs / 4000
			doc.Shapes = nil
			doc.Skipped = nil
			p.walkRoot(svgRoot, vp, doc)
		}
	}
	return doc, nil
}

// ---------------------------------------------------------------------------
// DOM

type node struct {
	tag      string
	attrs    map[string]string
	children []*node
	text     strings.Builder
}

func parseDOM(r io.Reader) (*node, error) {
	dec := xml.NewDecoder(r)
	dec.Strict = false
	dec.AutoClose = xml.HTMLAutoClose
	dec.Entity = xml.HTMLEntity
	dec.CharsetReader = func(_ string, in io.Reader) (io.Reader, error) { return in, nil }
	root := &node{tag: "#root", attrs: map[string]string{}}
	stack := []*node{root}
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			if len(root.children) > 0 {
				break // keep what was read so far
			}
			return nil, fmt.Errorf("xml: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			n := &node{tag: t.Name.Local, attrs: make(map[string]string, len(t.Attr))}
			for _, a := range t.Attr {
				if a.Name.Space == "xmlns" || a.Name.Local == "xmlns" {
					continue
				}
				// xlink:href and href both end up as "href"; plain href wins
				if _, exists := n.attrs[a.Name.Local]; exists && a.Name.Space != "" {
					continue
				}
				n.attrs[a.Name.Local] = a.Value
			}
			top := stack[len(stack)-1]
			top.children = append(top.children, n)
			stack = append(stack, n)
		case xml.EndElement:
			if len(stack) > 1 {
				stack = stack[:len(stack)-1]
			}
		case xml.CharData:
			if top := stack[len(stack)-1]; top.tag == "style" {
				top.text.Write(t)
			}
		}
	}
	return root, nil
}

// ---------------------------------------------------------------------------
// traversal

type parser struct {
	ids    map[string]*node
	css    []cssRule
	tol    float64
	doc    *Doc
	budget int // guards against exponential <use> recursion
}

func (p *parser) index(n *node) {
	if id := n.attrs["id"]; id != "" {
		if _, dup := p.ids[id]; !dup {
			p.ids[id] = n
		}
	}
	if n.tag == "style" {
		p.css = parseCSS(n.text.String(), p.css)
	}
	for _, c := range n.children {
		p.index(c)
	}
}

// elements whose content is never rendered directly
var nonRendered = map[string]bool{
	"defs": true, "symbol": true, "clipPath": true, "mask": true, "pattern": true,
	"marker": true, "linearGradient": true, "radialGradient": true, "filter": true,
	"metadata": true, "title": true, "desc": true, "style": true, "script": true,
	"foreignObject": true, "namedview": true, "font": true, "font-face": true,
	"glyph": true, "cursor": true, "view": true,
}

type walkCtx struct {
	m     geom.Mat
	st    style
	vp    viewport
	depth int
	clips []Clip
}

func (p *parser) walkRoot(root *node, vp viewport, doc *Doc) {
	p.doc = doc
	p.budget = 500000
	doc.Skipped = map[string]int{}
	pr := p.props(root)
	if pr["display"] == "none" {
		return
	}
	ctx := walkCtx{m: parseTransform(root.attrs["transform"]), st: p.inherit(defaultStyle(), pr, vp), vp: vp}
	for _, c := range root.children {
		p.walk(c, ctx)
	}
}

func (p *parser) walk(n *node, ctx walkCtx) {
	if ctx.depth > 64 || p.budget <= 0 {
		return
	}
	p.budget--
	if nonRendered[n.tag] {
		return
	}
	switch n.tag {
	case "text", "tspan", "textPath", "flowRoot":
		p.doc.Skipped["text"]++
		return
	case "image":
		p.doc.Skipped["image"]++
		return
	}
	pr := p.props(n)
	if strings.TrimSpace(pr["display"]) == "none" {
		return
	}
	c := walkCtx{
		m:     ctx.m.Mul(parseTransform(n.attrs["transform"])),
		st:    p.inherit(ctx.st, pr, ctx.vp),
		vp:    ctx.vp,
		depth: ctx.depth + 1,
		clips: ctx.clips,
	}
	if cp := strings.TrimSpace(pr["clip-path"]); strings.HasPrefix(cp, "url(") {
		clip, ok := p.resolveClip(cp, c)
		if !ok {
			return // clip-path that clips everything away (empty clipPath)
		}
		if clip != nil {
			c.clips = append(append([]Clip(nil), ctx.clips...), *clip)
		}
	}

	switch n.tag {
	case "svg":
		x, _ := parseLength(n.attrs["x"], ctx.vp.w)
		y, _ := parseLength(n.attrs["y"], ctx.vp.h)
		w, okw := parseLength(n.attrs["width"], ctx.vp.w)
		h, okh := parseLength(n.attrs["height"], ctx.vp.h)
		if !okw {
			w = ctx.vp.w
		}
		if !okh {
			h = ctx.vp.h
		}
		c.m = c.m.Mul(geom.Translate(x, y))
		if vb := numberList(n.attrs["viewBox"]); len(vb) == 4 && vb[2] > 0 && vb[3] > 0 {
			c.m = c.m.Mul(viewBoxTransform(vb, w, h, n.attrs["preserveAspectRatio"]))
			c.vp = viewport{vb[2], vb[3]}
		} else {
			c.vp = viewport{w, h}
		}
		p.walkChildren(n, c)
	case "use":
		href := strings.TrimSpace(n.attrs["href"])
		target := p.ids[strings.TrimPrefix(href, "#")]
		if !strings.HasPrefix(href, "#") || target == nil {
			return
		}
		x, _ := parseLength(n.attrs["x"], ctx.vp.w)
		y, _ := parseLength(n.attrs["y"], ctx.vp.h)
		c.m = c.m.Mul(geom.Translate(x, y))
		if target.tag == "symbol" || target.tag == "svg" {
			w, okw := parseLength(n.attrs["width"], ctx.vp.w)
			h, okh := parseLength(n.attrs["height"], ctx.vp.h)
			if vb := numberList(target.attrs["viewBox"]); len(vb) == 4 && vb[2] > 0 && vb[3] > 0 {
				if !okw {
					w = vb[2]
				}
				if !okh {
					h = vb[3]
				}
				c.m = c.m.Mul(viewBoxTransform(vb, w, h, target.attrs["preserveAspectRatio"]))
				c.vp = viewport{vb[2], vb[3]}
			}
			tpr := p.props(target)
			if strings.TrimSpace(tpr["display"]) == "none" {
				return
			}
			c.st = p.inherit(c.st, tpr, c.vp)
			c.m = c.m.Mul(parseTransform(target.attrs["transform"]))
			p.walkChildren(target, c)
			return
		}
		if nonRendered[target.tag] {
			// <use> may reference an element inside <defs>; render it directly
			return
		}
		p.walk(target, walkCtx{m: c.m, st: c.st, vp: c.vp, depth: c.depth, clips: c.clips})
	case "path":
		f := newFlattener(c.m, p.tol)
		parsePathData(n.attrs["d"], f)
		p.addShape(n, c, f.finish(), true)
	case "rect":
		x, _ := parseLength(n.attrs["x"], c.vp.w)
		y, _ := parseLength(n.attrs["y"], c.vp.h)
		w, _ := parseLength(n.attrs["width"], c.vp.w)
		h, _ := parseLength(n.attrs["height"], c.vp.h)
		if w <= 0 || h <= 0 {
			return
		}
		rx, okx := parseLength(n.attrs["rx"], c.vp.w)
		ry, oky := parseLength(n.attrs["ry"], c.vp.h)
		if okx && !oky {
			ry = rx
		} else if oky && !okx {
			rx = ry
		}
		rx, ry = math.Min(math.Max(rx, 0), w/2), math.Min(math.Max(ry, 0), h/2)
		f := newFlattener(c.m, p.tol)
		if rx > 0 && ry > 0 {
			f.moveTo(geom.Pt{X: x + rx, Y: y})
			f.lineTo(geom.Pt{X: x + w - rx, Y: y})
			f.arcTo(geom.Pt{X: x + w - rx, Y: y}, rx, ry, 0, false, true, geom.Pt{X: x + w, Y: y + ry})
			f.lineTo(geom.Pt{X: x + w, Y: y + h - ry})
			f.arcTo(geom.Pt{X: x + w, Y: y + h - ry}, rx, ry, 0, false, true, geom.Pt{X: x + w - rx, Y: y + h})
			f.lineTo(geom.Pt{X: x + rx, Y: y + h})
			f.arcTo(geom.Pt{X: x + rx, Y: y + h}, rx, ry, 0, false, true, geom.Pt{X: x, Y: y + h - ry})
			f.lineTo(geom.Pt{X: x, Y: y + ry})
			f.arcTo(geom.Pt{X: x, Y: y + ry}, rx, ry, 0, false, true, geom.Pt{X: x + rx, Y: y})
		} else {
			f.moveTo(geom.Pt{X: x, Y: y})
			f.lineTo(geom.Pt{X: x + w, Y: y})
			f.lineTo(geom.Pt{X: x + w, Y: y + h})
			f.lineTo(geom.Pt{X: x, Y: y + h})
		}
		f.closePath()
		p.addShape(n, c, f.finish(), true)
	case "circle", "ellipse":
		cx, _ := parseLength(n.attrs["cx"], c.vp.w)
		cy, _ := parseLength(n.attrs["cy"], c.vp.h)
		var rx, ry float64
		if n.tag == "circle" {
			rx, _ = parseLength(n.attrs["r"], c.vp.diag())
			ry = rx
		} else {
			rx, _ = parseLength(n.attrs["rx"], c.vp.w)
			ry, _ = parseLength(n.attrs["ry"], c.vp.h)
		}
		if rx <= 0 || ry <= 0 {
			return
		}
		f := newFlattener(c.m, p.tol)
		a := geom.Pt{X: cx + rx, Y: cy}
		b := geom.Pt{X: cx - rx, Y: cy}
		f.moveTo(a)
		f.arcTo(a, rx, ry, 0, false, true, b)
		f.arcTo(b, rx, ry, 0, false, true, a)
		f.closePath()
		p.addShape(n, c, f.finish(), true)
	case "line":
		x1, _ := parseLength(n.attrs["x1"], c.vp.w)
		y1, _ := parseLength(n.attrs["y1"], c.vp.h)
		x2, _ := parseLength(n.attrs["x2"], c.vp.w)
		y2, _ := parseLength(n.attrs["y2"], c.vp.h)
		f := newFlattener(c.m, p.tol)
		f.moveTo(geom.Pt{X: x1, Y: y1})
		f.lineTo(geom.Pt{X: x2, Y: y2})
		p.addShape(n, c, f.finish(), false)
	case "polyline", "polygon":
		v := numberList(n.attrs["points"])
		if len(v) < 4 {
			return
		}
		f := newFlattener(c.m, p.tol)
		f.moveTo(geom.Pt{X: v[0], Y: v[1]})
		for i := 2; i+1 < len(v); i += 2 {
			f.lineTo(geom.Pt{X: v[i], Y: v[i+1]})
		}
		if n.tag == "polygon" {
			f.closePath()
		}
		p.addShape(n, c, f.finish(), true)
	default: // g, a, switch and unknown containers
		p.walkChildren(n, c)
	}
}

func (p *parser) walkChildren(n *node, c walkCtx) {
	for _, ch := range n.children {
		p.walk(ch, c)
	}
}

func (p *parser) addShape(n *node, c walkCtx, polys []geom.Polyline, fillable bool) {
	if len(polys) == 0 {
		return
	}
	if v := strings.TrimSpace(c.st.visibility); v == "hidden" || v == "collapse" {
		return
	}
	s := Shape{Polys: polys, Tag: n.tag, ID: n.attrs["id"], EvenOdd: strings.TrimSpace(c.st.fillRule) == "evenodd", Clips: c.clips}
	if fillable {
		s.Fill = p.resolvePaint(c.st.fill, c.st, c.st.fillOpacity)
	}
	s.Stroke = p.resolvePaint(c.st.stroke, c.st, c.st.strokeOpacity)
	s.StrokeWidth = c.st.strokeWidth * c.m.ScaleFactor()
	if s.StrokeWidth <= 0 {
		s.Stroke = Paint{}
	}
	p.doc.Shapes = append(p.doc.Shapes, s)
}

// resolveClip flattens the clipPath referenced by a clip-path value in the
// coordinate system of the referencing element. It returns (nil, true) when
// the reference cannot be resolved (the spec says to ignore it then) and
// (nil, false) when the clip path is empty, i.e. hides the element.
func (p *parser) resolveClip(val string, c walkCtx) (*Clip, bool) {
	end := strings.IndexByte(val, ')')
	if end < 0 {
		return nil, true
	}
	id := strings.TrimPrefix(strings.Trim(strings.TrimSpace(val[4:end]), `'"`), "#")
	n := p.ids[id]
	if n == nil || n.tag != "clipPath" || c.depth > 32 {
		return nil, true
	}
	m := c.m.Mul(parseTransform(n.attrs["transform"]))
	if n.attrs["clipPathUnits"] == "objectBoundingBox" {
		return nil, true // rare in practice; ignoring is the safe fallback
	}
	saved := p.doc
	tmp := &Doc{Skipped: map[string]int{}}
	p.doc = tmp
	ctx := walkCtx{m: m, st: defaultStyle(), vp: c.vp, depth: c.depth + 1}
	for _, ch := range n.children {
		p.walk(ch, ctx)
	}
	p.doc = saved
	clip := &Clip{}
	for _, s := range tmp.Shapes {
		clip.Polys = append(clip.Polys, s.Polys...)
		clip.EvenOdd = clip.EvenOdd || s.EvenOdd
	}
	if len(clip.Polys) == 0 {
		return nil, false
	}
	return clip, true
}

// ---------------------------------------------------------------------------
// transforms

func parseTransform(s string) geom.Mat {
	m := geom.Identity()
	s = strings.TrimSpace(s)
	for s != "" {
		open := strings.IndexByte(s, '(')
		close := strings.IndexByte(s, ')')
		if open < 0 || close < open {
			break
		}
		name := strings.Trim(strings.TrimSpace(s[:open]), ", ")
		args := numberList(s[open+1 : close])
		s = strings.TrimSpace(s[close+1:])
		var t geom.Mat
		arg := func(i int, def float64) float64 {
			if i < len(args) {
				return args[i]
			}
			return def
		}
		switch name {
		case "matrix":
			if len(args) != 6 {
				continue
			}
			t = geom.Mat{A: args[0], B: args[1], C: args[2], D: args[3], E: args[4], F: args[5]}
		case "translate":
			t = geom.Translate(arg(0, 0), arg(1, 0))
		case "scale":
			sx := arg(0, 1)
			t = geom.Scale(sx, arg(1, sx))
		case "rotate":
			a := arg(0, 0) * math.Pi / 180
			cx, cy := arg(1, 0), arg(2, 0)
			t = geom.Translate(cx, cy).Mul(geom.Rotate(a)).Mul(geom.Translate(-cx, -cy))
		case "skewX":
			t = geom.SkewX(arg(0, 0) * math.Pi / 180)
		case "skewY":
			t = geom.SkewY(arg(0, 0) * math.Pi / 180)
		default:
			continue
		}
		m = m.Mul(t)
	}
	return m
}

// viewBoxTransform maps a viewBox into a w×h viewport honouring preserveAspectRatio.
func viewBoxTransform(vb []float64, w, h float64, par string) geom.Mat {
	sx, sy := w/vb[2], h/vb[3]
	fields := strings.Fields(par)
	align := "xMidYMid"
	slice := false
	if len(fields) > 0 {
		align = fields[0]
		if align == "defer" && len(fields) > 1 {
			fields = fields[1:]
			align = fields[0]
		}
		slice = len(fields) > 1 && fields[1] == "slice"
	}
	if align == "none" {
		return geom.Scale(sx, sy).Mul(geom.Translate(-vb[0], -vb[1]))
	}
	s := math.Min(sx, sy)
	if slice {
		s = math.Max(sx, sy)
	}
	tx, ty := 0.0, 0.0
	free := w - vb[2]*s
	freeY := h - vb[3]*s
	switch {
	case strings.Contains(align, "xMid"):
		tx = free / 2
	case strings.Contains(align, "xMax"):
		tx = free
	}
	switch {
	case strings.Contains(align, "YMid"):
		ty = freeY / 2
	case strings.Contains(align, "YMax"):
		ty = freeY
	}
	return geom.Translate(tx, ty).Mul(geom.Scale(s, s)).Mul(geom.Translate(-vb[0], -vb[1]))
}
