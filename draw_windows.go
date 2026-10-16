//go:build windows

package main

import (
	"fmt"
	"image/color"
	"path/filepath"
	"sort"
	"strings"
	"time"

	rl "github.com/gen2brain/raylib-go/raylib"

	"bardraw/internal/geom"
	"bardraw/internal/plan"
	"bardraw/internal/winput"
)

var (
	colPanel    = color.RGBA{12, 14, 20, 205}
	colPanelEdg = color.RGBA{255, 190, 40, 90}
	colText     = color.RGBA{236, 239, 245, 255}
	colDim      = color.RGBA{158, 166, 180, 255}
	colAccent   = color.RGBA{255, 190, 40, 255} // in-game estimate
	colRef      = color.RGBA{255, 255, 255, 150}
	colVert     = color.RGBA{0, 225, 255, 255}
	colWarn     = color.RGBA{255, 168, 64, 255}
	colErr      = color.RGBA{255, 96, 96, 255}
	colOK       = color.RGBA{120, 220, 140, 255}
	colRemain   = color.RGBA{255, 255, 255, 70}
)

type hudLine struct {
	text string
	col  color.RGBA
	size float32
}

func (a *App) ov(p geom.Pt) rl.Vector2 {
	return rl.Vector2{X: float32(p.X) - float32(a.winX), Y: float32(p.Y) - float32(a.winY)}
}

func (a *App) measure(s string, size float32) float32 {
	if a.fontOK {
		return rl.MeasureTextEx(a.font, s, size, 0.5).X
	}
	return float32(rl.MeasureText(s, int32(size)))
}

func (a *App) text(s string, x, y, size float32, c color.RGBA) {
	if a.fontOK {
		rl.DrawTextEx(a.font, s, rl.Vector2{X: x, Y: y}, size, 0.5, c)
		return
	}
	rl.DrawText(s, int32(x), int32(y), int32(size), c)
}

// panel draws a HUD box at the top centre of the monitor containing (sx,sy);
// if it would cover avoid (screen coordinates) it moves to the bottom.
func (a *App) panel(sx, sy int, lines []hudLine, progress float64, avoid *rectI) {
	if len(lines) == 0 {
		return
	}
	l, t, r, b := winput.MonitorRect(sx, sy)
	u := a.ui()
	pad := 14 * u
	margin := 36 * u
	w := float32(0)
	h := float32(0)
	for _, ln := range lines {
		w = max(w, a.measure(ln.text, ln.size*u))
		h += ln.size * u * 1.32
	}
	if progress >= 0 {
		h += 14 * u
	}
	w += 2 * pad
	h += 2*pad - 4
	cx := float32(l+r)/2 - float32(a.winX)
	x := cx - w/2
	y := float32(t-a.winY) + margin
	if avoid != nil {
		ax0, ay0 := float32(avoid.X-a.winX), float32(avoid.Y-a.winY)
		ax1, ay1 := ax0+float32(avoid.W), ay0+float32(avoid.H)
		if ax0 < x+w && ax1 > x && ay0 < y+h && ay1 > y {
			y = float32(b-a.winY) - margin - h
		}
	}
	rec := rl.Rectangle{X: x, Y: y, Width: w, Height: h}
	rl.DrawRectangleRounded(rec, 0.12, 8, colPanel)
	rl.DrawRectangleRoundedLinesEx(rec, 0.12, 8, 1, colPanelEdg)
	ty := y + pad
	for _, ln := range lines {
		a.text(ln.text, x+pad, ty, ln.size*u, ln.col)
		ty += ln.size * u * 1.32
	}
	if progress >= 0 {
		bar := rl.Rectangle{X: x + pad, Y: ty + 2*u, Width: w - 2*pad, Height: 6 * u}
		rl.DrawRectangleRounded(bar, 1, 4, color.RGBA{255, 255, 255, 40})
		bar.Width *= float32(progress)
		if bar.Width > 1 {
			rl.DrawRectangleRounded(bar, 1, 4, colAccent)
		}
	}
}

func (a *App) draw() {
	switch a.state {
	case stSelect:
		a.drawSelect()
	case stArming, stDrawing, stPaused:
		a.drawRun()
	case stIdle:
		if time.Now().Before(a.toastUntil) {
			col := colText
			if a.toastErr {
				col = colErr
			}
			a.panel(a.cx, a.cy, []hudLine{{a.toast, col, 19}}, -1, nil)
		}
	}
}

// strokePolyline draws a thick polyline with round joins.
func (a *App) strokePolyline(pts []geom.Pt, xf func(geom.Pt) geom.Pt, thick float32, c color.RGBA) {
	if len(pts) == 0 {
		return
	}
	prev := a.ov(xf(pts[0]))
	rl.DrawCircleV(prev, thick/2, c)
	for _, p := range pts[1:] {
		cur := a.ov(xf(p))
		rl.DrawLineEx(prev, cur, thick, c)
		rl.DrawCircleV(cur, thick/2, c)
		prev = cur
	}
}

var vbuf []rl.Vector2

func (a *App) thinPolyline(pts []geom.Pt, closed bool, xf func(geom.Pt) geom.Pt, c color.RGBA) {
	vbuf = vbuf[:0]
	for _, p := range pts {
		vbuf = append(vbuf, a.ov(xf(p)))
	}
	if closed && len(pts) > 2 {
		vbuf = append(vbuf, vbuf[0])
	}
	if len(vbuf) >= 2 {
		rl.DrawLineStrip(vbuf, c)
	}
}

func (a *App) drawSelect() {
	box := a.currentBox()
	g, gerr, loading := a.geometry()
	shift := winput.ShiftHeld()

	// box frame with corner brackets
	frame := color.RGBA{255, 255, 255, 110}
	if a.dragging || a.moving || a.placed != nil {
		frame = color.RGBA{255, 255, 255, 230}
	}
	o := a.ov(geom.Pt{X: float64(box.X), Y: float64(box.Y)})
	rec := rl.Rectangle{X: o.X, Y: o.Y, Width: float32(box.W), Height: float32(box.H)}
	rl.DrawRectangleLinesEx(rec, 1, color.RGBA{255, 255, 255, 50})
	cl := min(float32(18), min(rec.Width, rec.Height)/3)
	for _, c := range [][2]float32{{0, 0}, {1, 0}, {0, 1}, {1, 1}} {
		px, py := rec.X+c[0]*rec.Width, rec.Y+c[1]*rec.Height
		dx, dy := cl*(1-2*c[0]), cl*(1-2*c[1])
		rl.DrawLineEx(rl.Vector2{X: px, Y: py}, rl.Vector2{X: px + dx, Y: py}, 2, frame)
		rl.DrawLineEx(rl.Vector2{X: px, Y: py}, rl.Vector2{X: px, Y: py + dy}, 2, frame)
	}

	var res *PlanRes
	if g != nil && box.valid() {
		res = a.planner.Latest()
		if res != nil && res.Req.G != g {
			res = nil
		}
		if res != nil {
			sx := float64(box.W) / float64(res.Req.W)
			sy := float64(box.H) / float64(res.Req.H)
			xf := func(p geom.Pt) geom.Pt {
				return geom.Pt{X: float64(box.X) + p.X*sx, Y: float64(box.Y) + p.Y*sy}
			}
			th := float32(a.cfg.PreviewLinePx)
			for _, s := range res.Plan.Strokes {
				a.strokePolyline(s, xf, th, colAccent)
			}
			if a.showVerts {
				for _, s := range res.Plan.Strokes {
					for _, p := range s {
						rl.DrawCircleV(a.ov(xf(p)), 2.4, colVert)
					}
				}
			}
		}
		// high-quality reference on top
		m := plan.FitMatrix(g.Bounds, float64(box.W), float64(box.H), a.params(shift).Xf)
		m = geom.Translate(float64(box.X), float64(box.Y)).Mul(m)
		budget := 400000
		for _, pl := range g.Ref {
			if budget -= len(pl.Pts); budget < 0 {
				break
			}
			a.thinPolyline(pl.Pts, pl.Closed, m.Apply, colRef)
		}
	}

	// HUD
	file, idx, n := a.lib.Current()
	title := "BAR SVG Draw"
	if a.dry() {
		title += "  ·  DRY RUN (no input is sent)"
	}
	lines := []hudLine{
		{title, colAccent, 22},
		{a.selectHint(), colAccent, 18},
		{"Drag = new box  ·  drag inside = move  ·  click = place here  ·  Wheel = resize  ·  Enter = last box  ·  RMB = unplace", colDim, 16},
	}
	if n == 0 {
		lines = append(lines, hudLine{"No .svg files in " + a.cfg.SVGPath + " — put some there", colErr, 19})
	} else {
		lines = append(lines, hudLine{fmt.Sprintf("File:  %s   (%d/%d, Tab / Shift+Tab)", filepath.Base(file), idx+1, n), colText, 19})
	}
	modeLine := "Mode:  " + a.st.Mode
	if g != nil {
		modeLine = "Mode:  " + g.Mode
		if a.st.Mode == plan.ModeAuto {
			modeLine += " (auto)"
		}
		if g.Note != "" {
			modeLine += "  ·  " + g.Note
		}
	}
	lines = append(lines, hudLine{modeLine + "   [M]", colText, 19})
	lvl := plan.Levels[a.st.Quality-1]
	lines = append(lines, hudLine{fmt.Sprintf("Quality:  %d/%d   (ε %.2g px, Ctrl+wheel or PgUp/PgDn)  ·  %d ms per vertex", a.st.Quality, len(plan.Levels), lvl.Eps, a.timing.VertexInterval.Milliseconds()), colText, 19})
	budget := "Time limit:  none   (Alt+wheel sets one)"
	if a.cfg.TimeBudgetS > 0 {
		auto := "auto-lower quality ON"
		if !a.autoLower() {
			auto = "auto-lower OFF, warn only"
		}
		budget = fmt.Sprintf("Time limit:  %.0f s   (Alt+wheel ±10 s)  ·  %s [B]", a.cfg.TimeBudgetS, auto)
	}
	lines = append(lines, hudLine{budget, colText, 19})
	switch {
	case gerr != nil:
		lines = append(lines, hudLine{"SVG error: " + gerr.Error(), colErr, 19})
	case loading:
		lines = append(lines, hudLine{"Processing SVG…", colWarn, 19})
	case res != nil:
		planCol := colOK
		if res.OverBudget() {
			planCol = colErr
		}
		lines = append(lines, hudLine{fmt.Sprintf("Plan:  %d strokes  ·  %d vertices  ·  ETA %.1f s   (box %d×%d)",
			len(res.Plan.Strokes), res.Plan.Vertices, res.ETA.Seconds(), box.W, box.H), planCol, 19})
		switch {
		case res.AutoReduced:
			lines = append(lines, hudLine{fmt.Sprintf("Quality lowered to %d to fit the %.0f s time limit", res.Plan.Level, a.cfg.TimeBudgetS), colWarn, 17})
		case res.OverBudget():
			lines = append(lines, hudLine{fmt.Sprintf("Over the %.0f s time limit — BAR erases map marks ~60 s after drawing, the start will vanish before the end", a.cfg.TimeBudgetS), colErr, 17})
		}
	}
	xfs := []string{fmt.Sprintf("Rotate %d° [R]", a.st.Rotate*90)}
	if a.st.FlipH {
		xfs = append(xfs, "Flipped [F]")
	} else {
		xfs = append(xfs, "Flip [F]")
	}
	if a.st.Stretch || shift {
		xfs = append(xfs, "Stretch ON [S / hold Shift]")
	} else {
		xfs = append(xfs, "Stretch [S / hold Shift]")
	}
	xfs = append(xfs, "Vertices [V]", "Cancel [Esc]")
	lines = append(lines, hudLine{strings.Join(xfs, "  ·  "), colDim, 16})
	for _, w := range a.selectWarnings(box, g) {
		lines = append(lines, hudLine{w, colWarn, 17})
	}
	if a.cfgErr != "" {
		lines = append(lines, hudLine{a.cfgErr, colErr, 17})
	}
	if a.toast != "" && time.Now().Before(a.toastUntil) {
		col := colText
		if a.toastErr {
			col = colErr
		}
		lines = append(lines, hudLine{a.toast, col, 17})
	}
	a.panel(a.cx, a.cy, lines, -1, &box)
}

func (a *App) selectWarnings(box rectI, g *plan.Geometry) []string {
	var out []string
	cx, cy := box.X+box.W/2, box.Y+box.H/2
	l, t, r, b := winput.MonitorRect(cx, cy)
	mx := int(float64(r-l) * a.cfg.EdgeWarnPercent / 100)
	my := int(float64(b-t) * a.cfg.EdgeWarnPercent / 100)
	if a.cfg.EdgeWarnPercent > 0 && (box.X < l+mx || box.Y < t+my || box.X+box.W > r-mx || box.Y+box.H > b-my) {
		out = append(out, "Box is close to the screen edge — edge scrolling may move the camera while drawing")
	}
	if a.targetTitle != "" {
		out = append(out, "Target window: "+a.targetTitle)
	}
	if g != nil && len(g.Skipped) > 0 {
		var parts []string
		for k, v := range g.Skipped {
			parts = append(parts, fmt.Sprintf("%d %s", v, k))
		}
		sort.Strings(parts)
		out = append(out, "Ignored: "+strings.Join(parts, ", ")+" (convert text to paths)")
	}
	return out
}

func (a *App) drawRun() {
	r := a.run
	if r == nil {
		return
	}
	frac, left, s, v := a.progress()
	id := func(p geom.Pt) geom.Pt { return p }
	// what is still to come; finished strokes are visible in the game itself
	for i := s; i < len(r.strokes); i++ {
		pts := r.strokes[i]
		if i == s && a.state == stDrawing {
			if v < len(pts) {
				a.strokePolyline(pts[max(0, v-1):], id, 2, colAccent)
			}
			continue
		}
		a.thinPolyline(pts, false, id, colRemain)
	}
	if a.state == stDrawing && s < len(r.strokes) && v < len(r.strokes[s]) {
		c := a.ov(r.strokes[s][v])
		rl.DrawRing(c, 5, 7, 0, 360, 24, colVert)
	}

	cx := r.box.X + r.box.W/2
	cy := r.box.Y + r.box.H/2
	var lines []hudLine
	prog := -1.0
	switch a.state {
	case stArming:
		if r.keysWait {
			lines = append(lines, hudLine{"Release all keys to start drawing", colAccent, 21})
		} else {
			wait := max(time.Until(r.armAt), time.Until(a.lastKeyUp.Add(keyChainGuard)))
			lines = append(lines, hudLine{fmt.Sprintf("Starting in %.2f s", max(0, wait.Seconds())), colAccent, 21})
		}
		lines = append(lines, hudLine{fmt.Sprintf("%d strokes · %d vertices · ETA %.1f s", len(r.strokes)-r.from, countVerts(r.strokes[r.from:]), left.Seconds()), colText, 18})
		lines = append(lines, hudLine{"Any key or mouse button cancels", colDim, 16})
	case stDrawing:
		title := fmt.Sprintf("Drawing  %.0f%%  ·  %.1f s left  ·  stroke %d/%d", frac*100, left.Seconds(), min(s+1, len(r.strokes)), len(r.strokes))
		if a.dry() {
			title += "  ·  DRY RUN"
		}
		lines = append(lines, hudLine{title, colAccent, 21})
		hint := "Space (or any key / mouse button) pauses  ·  mouse movement is ignored"
		if a.cfg.StopOnShakePx > 0 {
			hint = "Space (or any key / mouse button / shaking the mouse) pauses"
		}
		lines = append(lines, hudLine{hint, colDim, 16})
		prog = frac
	case stPaused:
		lines = append(lines, hudLine{fmt.Sprintf("Paused  ·  %d/%d strokes done  ·  %.1f s left", s, len(r.strokes), left.Seconds()), colAccent, 21})
		lines = append(lines, hudLine{"Space continues  ·  Esc discards  ·  the game is usable meanwhile", colText, 18})
		lines = append(lines, hudLine{"Don't move the camera, or the rest will land in the wrong place", colWarn, 16})
		prog = float64(s) / float64(max(1, len(r.strokes)))
	}
	a.panel(cx, cy, lines, prog, &r.box)
}

func countVerts(ss [][]geom.Pt) int {
	n := 0
	for _, s := range ss {
		n += len(s)
	}
	return n
}

func (a *App) selectHint() string {
	if a.placed == nil {
		return "Place the box, then press Space to draw (Space now draws at the cursor)"
	}
	return "Space = start drawing"
}
