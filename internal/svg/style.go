package svg

import (
	"math"
	"sort"
	"strconv"
	"strings"
)

func parseFloatFast(s string) float64 {
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return v
}

// Paint is a resolved fill or stroke paint. A zero Paint (Set == false) means "none".
type Paint struct {
	Set     bool
	R, G, B float64 // 0..1
	A       float64 // effective opacity (paint alpha * *-opacity * group opacity)
}

// Luma is the relative luminance of the paint colour (0 = black, 1 = white).
func (p Paint) Luma() float64 { return 0.2126*p.R + 0.7152*p.G + 0.0722*p.B }

// Visible reports whether the paint would put anything on the canvas.
func (p Paint) Visible() bool { return p.Set && p.A > 0.02 }

// ---------------------------------------------------------------------------
// colours

var namedColors = map[string][3]uint8{
	"black": {0, 0, 0}, "white": {255, 255, 255}, "red": {255, 0, 0}, "lime": {0, 255, 0},
	"green": {0, 128, 0}, "blue": {0, 0, 255}, "yellow": {255, 255, 0}, "cyan": {0, 255, 255},
	"aqua": {0, 255, 255}, "magenta": {255, 0, 255}, "fuchsia": {255, 0, 255}, "gray": {128, 128, 128},
	"grey": {128, 128, 128}, "silver": {192, 192, 192}, "maroon": {128, 0, 0}, "olive": {128, 128, 0},
	"purple": {128, 0, 128}, "teal": {0, 128, 128}, "navy": {0, 0, 128}, "orange": {255, 165, 0},
	"pink": {255, 192, 203}, "brown": {165, 42, 42}, "gold": {255, 215, 0}, "beige": {245, 245, 220},
	"ivory": {255, 255, 240}, "snow": {255, 250, 250}, "whitesmoke": {245, 245, 245},
	"ghostwhite": {248, 248, 255}, "floralwhite": {255, 250, 240}, "linen": {250, 240, 230},
	"lightgray": {211, 211, 211}, "lightgrey": {211, 211, 211}, "gainsboro": {220, 220, 220},
	"darkgray": {169, 169, 169}, "darkgrey": {169, 169, 169}, "dimgray": {105, 105, 105},
	"dimgrey": {105, 105, 105}, "lightblue": {173, 216, 230}, "skyblue": {135, 206, 235},
	"darkblue": {0, 0, 139}, "darkgreen": {0, 100, 0}, "darkred": {139, 0, 0},
	"lightgreen": {144, 238, 144}, "lightyellow": {255, 255, 224}, "tan": {210, 180, 140},
	"khaki": {240, 230, 140}, "salmon": {250, 128, 114}, "coral": {255, 127, 80},
	"tomato": {255, 99, 71}, "orangered": {255, 69, 0}, "crimson": {220, 20, 60},
	"indigo": {75, 0, 130}, "violet": {238, 130, 238}, "plum": {221, 160, 221},
	"chocolate": {210, 105, 30}, "sienna": {160, 82, 45}, "steelblue": {70, 130, 180},
	"royalblue": {65, 105, 225}, "slategray": {112, 128, 144}, "darkslategray": {47, 79, 79},
	"forestgreen": {34, 139, 34}, "seagreen": {46, 139, 87}, "turquoise": {64, 224, 208},
	"lavender": {230, 230, 250}, "mintcream": {245, 255, 250}, "azure": {240, 255, 255},
	"aliceblue": {240, 248, 255}, "honeydew": {240, 255, 240}, "seashell": {255, 245, 238},
	"oldlace": {253, 245, 230}, "cornsilk": {255, 248, 220}, "lemonchiffon": {255, 250, 205},
	"wheat": {245, 222, 179}, "peachpuff": {255, 218, 185}, "mistyrose": {255, 228, 225},
	"lavenderblush": {255, 240, 245}, "antiquewhite": {250, 235, 215}, "papayawhip": {255, 239, 213},
	"blanchedalmond": {255, 235, 205}, "bisque": {255, 228, 196}, "moccasin": {255, 228, 181},
	"navajowhite": {255, 222, 173},
}

// parseColor parses a CSS colour. ok=false means unknown syntax.
func parseColor(s string) (r, g, b, a float64, ok bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	a = 1
	switch {
	case strings.HasPrefix(s, "#"):
		h := s[1:]
		hex := func(x string) float64 {
			v, err := strconv.ParseUint(x, 16, 8)
			if err != nil {
				return 0
			}
			return float64(v) / 255
		}
		switch len(h) {
		case 3, 4:
			r, g, b = hex(h[0:1]+h[0:1]), hex(h[1:2]+h[1:2]), hex(h[2:3]+h[2:3])
			if len(h) == 4 {
				a = hex(h[3:4] + h[3:4])
			}
			return r, g, b, a, true
		case 6, 8:
			r, g, b = hex(h[0:2]), hex(h[2:4]), hex(h[4:6])
			if len(h) == 8 {
				a = hex(h[6:8])
			}
			return r, g, b, a, true
		}
		return 0, 0, 0, 1, false
	case strings.HasPrefix(s, "rgb"), strings.HasPrefix(s, "hsl"):
		open, close := strings.IndexByte(s, '('), strings.LastIndexByte(s, ')')
		if open < 0 || close < open {
			return 0, 0, 0, 1, false
		}
		fields := strings.FieldsFunc(s[open+1:close], func(c rune) bool { return c == ',' || c == ' ' || c == '/' })
		if len(fields) < 3 {
			return 0, 0, 0, 1, false
		}
		comp := func(f string, scale float64) float64 {
			if strings.HasSuffix(f, "%") {
				return parseFloatFast(strings.TrimSuffix(f, "%")) / 100
			}
			return parseFloatFast(strings.TrimSuffix(f, "deg")) / scale
		}
		if len(fields) >= 4 {
			a = clamp01(comp(fields[3], 1))
		}
		if strings.HasPrefix(s, "rgb") {
			return clamp01(comp(fields[0], 255)), clamp01(comp(fields[1], 255)), clamp01(comp(fields[2], 255)), a, true
		}
		h := comp(fields[0], 360)
		sat, l := clamp01(comp(fields[1], 100)), clamp01(comp(fields[2], 100))
		r, g, b = hslToRGB(h-math.Floor(h), sat, l)
		return r, g, b, a, true
	}
	if c, found := namedColors[s]; found {
		return float64(c[0]) / 255, float64(c[1]) / 255, float64(c[2]) / 255, 1, true
	}
	return 0, 0, 0, 1, false
}

func clamp01(v float64) float64 { return math.Max(0, math.Min(1, v)) }

func hslToRGB(h, s, l float64) (float64, float64, float64) {
	if s == 0 {
		return l, l, l
	}
	q := l * (1 + s)
	if l >= 0.5 {
		q = l + s - l*s
	}
	p := 2*l - q
	f := func(t float64) float64 {
		t -= math.Floor(t)
		switch {
		case t < 1.0/6:
			return p + (q-p)*6*t
		case t < 0.5:
			return q
		case t < 2.0/3:
			return p + (q-p)*(2.0/3-t)*6
		}
		return p
	}
	return f(h + 1.0/3), f(h), f(h - 1.0/3)
}

// ---------------------------------------------------------------------------
// minimal CSS (enough for Illustrator / Inkscape / Figma exports)

type compound struct {
	tag     string
	id      string
	classes []string
}

type cssRule struct {
	sel   compound
	spec  int
	order int
	decls [][2]string
}

func parseDecls(s string) [][2]string {
	var out [][2]string
	for _, d := range strings.Split(s, ";") {
		k, v, ok := strings.Cut(d, ":")
		if !ok {
			continue
		}
		k = strings.ToLower(strings.TrimSpace(k))
		v = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(v), "!important"))
		if k != "" {
			out = append(out, [2]string{k, strings.TrimSpace(v)})
		}
	}
	return out
}

func stripComments(s string) string {
	for {
		i := strings.Index(s, "/*")
		if i < 0 {
			return s
		}
		j := strings.Index(s[i+2:], "*/")
		if j < 0 {
			return s[:i]
		}
		s = s[:i] + " " + s[i+2+j+2:]
	}
}

func parseCSS(text string, rules []cssRule) []cssRule {
	s := stripComments(text)
	s = strings.ReplaceAll(s, "<![CDATA[", "")
	s = strings.ReplaceAll(s, "]]>", "")
	for {
		open := strings.IndexByte(s, '{')
		if open < 0 {
			return rules
		}
		head := strings.TrimSpace(s[:open])
		// find matching close brace
		depth, end := 0, -1
		for i := open; i < len(s); i++ {
			if s[i] == '{' {
				depth++
			} else if s[i] == '}' {
				depth--
				if depth == 0 {
					end = i
					break
				}
			}
		}
		if end < 0 {
			return rules
		}
		body := s[open+1 : end]
		s = s[end+1:]
		if strings.HasPrefix(head, "@") {
			if strings.HasPrefix(head, "@media") || strings.HasPrefix(head, "@supports") {
				rules = parseCSS(body, rules)
			}
			continue
		}
		decls := parseDecls(body)
		for _, sel := range strings.Split(head, ",") {
			sel = strings.TrimSpace(sel)
			if sel == "" || strings.ContainsAny(sel, ":[") {
				continue
			}
			// only the right-most compound is matched (descendant parts ignored)
			parts := strings.FieldsFunc(sel, func(c rune) bool { return c == ' ' || c == '>' || c == '+' || c == '~' })
			if len(parts) == 0 {
				continue
			}
			c, spec := parseCompound(parts[len(parts)-1])
			rules = append(rules, cssRule{sel: c, spec: spec, order: len(rules), decls: decls})
		}
	}
}

func parseCompound(s string) (compound, int) {
	var c compound
	spec := 0
	i := 0
	for i < len(s) && s[i] != '.' && s[i] != '#' {
		i++
	}
	c.tag = s[:i]
	if c.tag != "" && c.tag != "*" {
		spec++
	}
	for i < len(s) {
		kind := s[i]
		j := i + 1
		for j < len(s) && s[j] != '.' && s[j] != '#' {
			j++
		}
		name := s[i+1 : j]
		if kind == '.' {
			c.classes = append(c.classes, name)
			spec += 10
		} else {
			c.id = name
			spec += 100
		}
		i = j
	}
	return c, spec
}

func (c compound) matches(n *node) bool {
	if c.tag != "" && c.tag != "*" && c.tag != n.tag {
		return false
	}
	if c.id != "" && n.attrs["id"] != c.id {
		return false
	}
	if len(c.classes) > 0 {
		have := strings.Fields(n.attrs["class"])
		for _, want := range c.classes {
			found := false
			for _, h := range have {
				if h == want {
					found = true
					break
				}
			}
			if !found {
				return false
			}
		}
	}
	return true
}

var presentationAttrs = []string{
	"fill", "stroke", "stroke-width", "fill-rule", "fill-opacity", "stroke-opacity",
	"opacity", "display", "visibility", "color", "stop-color", "stop-opacity",
	"clip-path", "clip-rule",
}

// props collects the cascaded property values of an element:
// presentation attributes < CSS rules (by specificity) < style attribute.
func (p *parser) props(n *node) map[string]string {
	out := map[string]string{}
	for _, k := range presentationAttrs {
		if v, ok := n.attrs[k]; ok {
			out[k] = strings.TrimSpace(v)
		}
	}
	if len(p.css) > 0 {
		var matched []*cssRule
		for i := range p.css {
			if p.css[i].sel.matches(n) {
				matched = append(matched, &p.css[i])
			}
		}
		sort.SliceStable(matched, func(a, b int) bool {
			if matched[a].spec != matched[b].spec {
				return matched[a].spec < matched[b].spec
			}
			return matched[a].order < matched[b].order
		})
		for _, r := range matched {
			for _, d := range r.decls {
				out[d[0]] = d[1]
			}
		}
	}
	if st, ok := n.attrs["style"]; ok {
		for _, d := range parseDecls(st) {
			out[d[0]] = d[1]
		}
	}
	return out
}

// computed style (inherited properties only, plus accumulated group opacity)
type style struct {
	fill, stroke  string
	fillRule      string
	strokeWidth   float64
	fillOpacity   float64
	strokeOpacity float64
	visibility    string
	color         string
	groupAlpha    float64
}

func defaultStyle() style {
	return style{fill: "black", stroke: "none", fillRule: "nonzero", strokeWidth: 1,
		fillOpacity: 1, strokeOpacity: 1, visibility: "visible", color: "black", groupAlpha: 1}
}

func parseOpacity(v string) (float64, bool) {
	v = strings.TrimSpace(v)
	if v == "" || v == "inherit" {
		return 0, false
	}
	if strings.HasSuffix(v, "%") {
		return clamp01(parseFloatFast(strings.TrimSuffix(v, "%")) / 100), true
	}
	return clamp01(parseFloatFast(v)), true
}

func (p *parser) inherit(parent style, pr map[string]string, vp viewport) style {
	s := parent
	if v, ok := pr["fill"]; ok && v != "inherit" {
		s.fill = v
	}
	if v, ok := pr["stroke"]; ok && v != "inherit" {
		s.stroke = v
	}
	if v, ok := pr["fill-rule"]; ok && v != "inherit" {
		s.fillRule = v
	}
	if v, ok := pr["stroke-width"]; ok && v != "inherit" {
		if w, ok := parseLength(v, vp.diag()); ok {
			s.strokeWidth = w
		}
	}
	if v, ok := parseOpacity(pr["fill-opacity"]); ok {
		s.fillOpacity = v
	}
	if v, ok := parseOpacity(pr["stroke-opacity"]); ok {
		s.strokeOpacity = v
	}
	if v, ok := pr["visibility"]; ok && v != "inherit" {
		s.visibility = v
	}
	if v, ok := pr["color"]; ok && v != "inherit" {
		s.color = v
	}
	if v, ok := parseOpacity(pr["opacity"]); ok {
		s.groupAlpha *= v
	}
	return s
}

// resolvePaint converts a fill/stroke value into a Paint.
func (p *parser) resolvePaint(v string, st style, opacity float64) Paint {
	v = strings.TrimSpace(v)
	low := strings.ToLower(v)
	if low == "" || low == "none" || low == "transparent" {
		return Paint{}
	}
	if low == "currentcolor" {
		v, low = st.color, strings.ToLower(st.color)
	}
	if strings.HasPrefix(low, "url(") {
		end := strings.IndexByte(v, ')')
		ref := ""
		fallback := ""
		if end > 0 {
			ref = strings.Trim(strings.TrimSpace(v[4:end]), `'"`)
			fallback = strings.TrimSpace(v[end+1:])
		}
		if r, g, b, a, ok := p.gradientColor(strings.TrimPrefix(ref, "#")); ok {
			return Paint{Set: true, R: r, G: g, B: b, A: a * opacity * st.groupAlpha}
		}
		if fallback != "" {
			return p.resolvePaint(fallback, st, opacity)
		}
		// unknown paint server (pattern…): treat as mid grey ink
		return Paint{Set: true, R: .5, G: .5, B: .5, A: opacity * st.groupAlpha}
	}
	r, g, b, a, ok := parseColor(v)
	if !ok {
		r, g, b, a = 0, 0, 0, 1 // unknown colour name: assume ink
	}
	return Paint{Set: true, R: r, G: g, B: b, A: a * opacity * st.groupAlpha}
}

// gradientColor averages the stop colours of a gradient (following href chains).
func (p *parser) gradientColor(id string) (r, g, b, a float64, ok bool) {
	n := p.ids[id]
	for depth := 0; n != nil && depth < 8; depth++ {
		if n.tag != "linearGradient" && n.tag != "radialGradient" {
			return 0, 0, 0, 0, false
		}
		count := 0.0
		for _, c := range n.children {
			if c.tag != "stop" {
				continue
			}
			pr := p.props(c)
			col := pr["stop-color"]
			if col == "" {
				col = "black"
			}
			cr, cg, cb, ca, cok := parseColor(col)
			if !cok {
				continue
			}
			if op, has := parseOpacity(pr["stop-opacity"]); has {
				ca *= op
			}
			r, g, b, a = r+cr, g+cg, b+cb, a+ca
			count++
		}
		if count > 0 {
			return r / count, g / count, b / count, a / count, true
		}
		href := n.attrs["href"]
		if !strings.HasPrefix(href, "#") {
			return 0, 0, 0, 0, false
		}
		n = p.ids[href[1:]]
	}
	return 0, 0, 0, 0, false
}

// ---------------------------------------------------------------------------
// lengths

type viewport struct{ w, h float64 }

func (v viewport) diag() float64 { return math.Sqrt(v.w*v.w+v.h*v.h) / math.Sqrt2 }

// parseLength parses an SVG length; percentages are relative to ref.
func parseLength(s string, ref float64) (float64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	unit := 1.0
	for _, u := range []struct {
		suf string
		k   float64
	}{{"px", 1}, {"pt", 4.0 / 3}, {"pc", 16}, {"mm", 96 / 25.4}, {"cm", 96 / 2.54}, {"in", 96}, {"em", 16}, {"ex", 8}, {"%", ref / 100}} {
		if strings.HasSuffix(s, u.suf) {
			s = strings.TrimSpace(strings.TrimSuffix(s, u.suf))
			unit = u.k
			break
		}
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, false
	}
	return v * unit, true
}

func numberList(s string) []float64 {
	sc := &scanner{s: s}
	var out []float64
	for {
		v, ok := sc.number()
		if !ok {
			return out
		}
		out = append(out, v)
	}
}
