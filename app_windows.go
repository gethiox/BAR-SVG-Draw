//go:build windows

package main

import (
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	rl "github.com/gen2brain/raylib-go/raylib"

	"bardraw/internal/geom"
	"bardraw/internal/plan"
	"bardraw/internal/winput"
)

type appState int

const (
	stIdle    appState = iota
	stSelect           // waiting for / dragging the box
	stArming           // Space pressed: waiting for all keys to be released + start_delay_ms
	stDrawing          // executor running
	stPaused           // drawing stopped part-way: Space continues, Esc discards
)

type rectI struct{ X, Y, W, H int }

func (r rectI) valid() bool { return r.W >= 8 && r.H >= 8 }

type runInfo struct {
	res      *PlanRes
	box      rectI
	strokes  [][]geom.Pt // absolute screen pixels
	starts   []time.Duration
	from     int
	timing   plan.Timing
	armAt    time.Time
	keysWait bool // still waiting for the user to release keys
	ex       *executor
	file     string
}

// App is the overlay application.
type App struct {
	cfgPath, statePath, baseDir string
	cfg                         Config
	cfgErr                      string
	st                          State
	dryFlag                     bool
	dryOverride                 *bool // set from the tray menu
	svgPath                     string

	hooks   *winput.Hooks
	inj     *winput.Injector
	lib     *Library
	planner *Planner
	timing  plan.Timing

	hwnd       uintptr
	winX, winY int
	font       rl.Font
	fontOK     bool

	state       appState
	dragging    bool     // drawing a new box
	moving      bool     // dragging the placed box around
	mvDX, mvDY  int      // grab offset while moving
	placed      *rectI   // box chosen in select mode (nil: box follows the cursor)
	armedFrom   appState // where a cancelled start returns to
	ax, ay      int
	cx, cy      int
	target      uintptr
	targetTitle string
	showVerts   bool

	run    *runInfo
	resume *runInfo

	toast      string
	toastErr   bool
	toastUntil time.Time
	lastTop    time.Time
	lastKeyUp  time.Time // when the draw key was last released by us
	quit       atomic.Bool
}

// stateVersion of bardraw.state.json (3: quality starts at the configured maximum).
const stateVersion = 3

// keyChainGuard: BAR runs "drawlabel" when the draw key is pressed twice
// within the engine's key-chain timeout (750 ms), so two drawings must not
// press it closer together than this.
const keyChainGuard = 850 * time.Millisecond

func newApp(cfgPath string, dry bool) (*App, error) {
	a := &App{
		cfgPath:   cfgPath,
		statePath: strings.TrimSuffix(cfgPath, filepath.Ext(cfgPath)) + ".state.json",
		baseDir:   filepath.Dir(cfgPath),
		dryFlag:   dry,
		lib:       NewLibrary(),
		planner:   NewPlanner(),
	}
	a.st = loadState(a.statePath)
	hk := a.reloadConfig()
	trayTip := ""
	if a.cfg.TrayIcon {
		trayTip = a.idleTip()
	}
	hooks, err := winput.StartHooks(hk, a.cfg.StopOnShakePx, trayTip)
	if err != nil {
		return nil, fmt.Errorf("installing input hooks failed: %v", err)
	}
	a.hooks = hooks
	a.updateTrayMenu()
	ready := fmt.Sprintf("%s draws, %s quits", a.hotkeyLabel(a.cfg.TriggerHotkey), a.hotkeyLabel(a.cfg.QuitHotkey))
	if a.cfgErr != "" {
		a.notify(a.cfgErr, true)
	} else {
		a.notify("bardraw is running — "+ready, false)
	}
	if a.cfg.StartupNotice {
		hooks.Tray().Notify("bardraw is running", ready+". Right-click the tray icon for the menu.")
	}
	return a, nil
}

// reloadConfig re-reads bardraw.json and returns the parsed hotkeys.
func (a *App) reloadConfig() []winput.Hotkey {
	cfg, err := loadConfig(a.cfgPath)
	a.cfg = cfg
	a.cfgErr = ""
	if err != nil {
		a.cfgErr = "config: " + err.Error()
	}
	var hks []winput.Hotkey
	for _, h := range []struct{ name, spec, def string }{
		{"trigger", cfg.TriggerHotkey, "ctrl+home"},
		{"resume", cfg.ResumeHotkey, "ctrl+shift+home"},
		{"quit", cfg.QuitHotkey, "ctrl+shift+end"},
	} {
		if strings.TrimSpace(h.spec) == "" {
			continue
		}
		hk, err := winput.ParseHotkey(h.name, h.spec)
		if err != nil {
			a.cfgErr = err.Error()
			hk, _ = winput.ParseHotkey(h.name, h.def)
		}
		hks = append(hks, hk)
	}
	keys, err := winput.ParseKeyCombo(cfg.DrawKey)
	if err != nil {
		a.cfgErr = err.Error()
		keys = []winput.KeySpec{{Scan: 0x29}}
	}
	a.inj = winput.NewInjector(keys, strings.EqualFold(cfg.MouseMoveMethod, "setcursorpos"))
	a.timing = cfg.timing()
	if a.st.Version < stateVersion {
		// older versions remembered the last quality; start from the
		// configured default (maximum) once, auto-lowering takes it from there
		a.st.Quality = 0
		a.st.Version = stateVersion
	}
	if a.st.Quality == 0 {
		a.st.Quality = cfg.Quality
	}
	a.st.Quality = plan.ClampLevel(a.st.Quality)
	if a.st.Mode == "" {
		a.st.Mode = cfg.Mode
	}
	svgPath := cfg.SVGPath
	if !filepath.IsAbs(svgPath) {
		svgPath = filepath.Join(a.baseDir, svgPath)
	}
	if !strings.EqualFold(filepath.Ext(svgPath), ".svg") {
		_ = os.MkdirAll(svgPath, 0o755)
	}
	a.svgPath = svgPath
	a.lib.Refresh(svgPath, a.st.File)
	if a.hooks != nil {
		a.hooks.Configure(hks, cfg.StopOnShakePx)
		a.updateTrayMenu()
	}
	return hks
}

func (a *App) dry() bool {
	if a.dryOverride != nil {
		return *a.dryOverride
	}
	return a.dryFlag || a.cfg.DryRun
}

func (a *App) idleTip() string {
	return "bardraw — " + a.hotkeyLabel(a.cfg.TriggerHotkey) + " to draw"
}

func (a *App) updateTrayMenu() {
	if a.hooks == nil {
		return
	}
	hint := fmt.Sprintf("%s draw · %s resume · %s quit", a.hotkeyLabel(a.cfg.TriggerHotkey),
		a.hotkeyLabel(a.cfg.ResumeHotkey), a.hotkeyLabel(a.cfg.QuitHotkey))
	a.hooks.Tray().SetMenu("bardraw is running", hint, a.dry())
}

// updateTray mirrors the state in the tray icon colour and tooltip.
func (a *App) updateTray() {
	t := a.hooks.Tray()
	if t == nil {
		return
	}
	switch a.state {
	case stSelect:
		t.Update(winput.TraySelect, "bardraw — choose a box")
	case stPaused:
		t.Update(winput.TrayIdle, "bardraw — paused, Space continues")
	case stArming, stDrawing:
		frac, _, _, _ := a.progress()
		t.Update(winput.TrayDraw, fmt.Sprintf("bardraw — drawing %d%%", int(frac*20)*5))
	default:
		tip := a.idleTip()
		if a.dry() {
			tip += " (dry run)"
		}
		t.Update(winput.TrayIdle, tip)
	}
}

func (a *App) handleTray(name string) {
	switch name {
	case "quit":
		a.requestQuit()
	case "folder":
		dir := a.svgPath
		if strings.EqualFold(filepath.Ext(dir), ".svg") {
			dir = filepath.Dir(dir)
		}
		winput.OpenPath(dir)
	case "settings":
		winput.OpenPath(a.cfgPath)
	case "dry":
		v := !a.dry()
		a.dryOverride = &v
		a.updateTrayMenu()
		if v {
			a.notify("Dry run on — drawings are only simulated", false)
		} else {
			a.notify("Dry run off — drawings send input to the game", false)
		}
	}
}

func (a *App) hotkeyLabel(spec string) string {
	if hk, err := winput.ParseHotkey("", spec); err == nil {
		return hk.String()
	}
	return spec
}

func (a *App) notify(msg string, isErr bool) {
	a.toast, a.toastErr = msg, isErr
	a.toastUntil = time.Now().Add(time.Duration(2500+40*len(msg)) * time.Millisecond)
}

func (a *App) saveState() {
	if f, _, _ := a.lib.Current(); f != "" {
		a.st.File = filepath.Base(f)
	}
	_ = saveJSON(a.statePath, a.st)
}

func (a *App) requestQuit() { a.quit.Store(true) }

func (a *App) shutdown() {
	if a.run != nil && a.run.ex != nil {
		a.run.ex.abort.Store(true)
		select {
		case <-a.run.ex.done:
		case <-time.After(2 * time.Second):
		}
	}
	if a.hooks != nil {
		a.hooks.SetMode(winput.ModeIdle)
		a.hooks.Stop()
	}
	a.saveState()
}

// ---------------------------------------------------------------------------
// window + main loop

func (a *App) initWindow() {
	vx, vy, vw, vh := winput.VirtualScreen()
	rl.SetTraceLogLevel(rl.LogWarning)
	flags := uint32(rl.FlagWindowUndecorated | rl.FlagWindowTransparent | rl.FlagWindowTopmost |
		rl.FlagWindowMousePassthrough | rl.FlagWindowUnfocused)
	if a.cfg.MSAA {
		flags |= rl.FlagMsaa4xHint
	}
	rl.SetConfigFlags(flags)
	// one pixel short of the full desktop so Windows does not treat the
	// overlay as a full-screen app (which can change how the game is presented)
	rl.InitWindow(int32(vw), int32(vh-1), "bardraw overlay")
	rl.SetWindowPosition(vx, vy)
	rl.SetExitKey(0)
	a.hwnd = uintptr(rl.GetWindowHandle())
	winput.MakeOverlayWindow(a.hwnd)
	a.loadFont()
}

func (a *App) loadFont() {
	runes := make([]rune, 0, 128)
	for r := rune(32); r < 127; r++ {
		runes = append(runes, r)
	}
	runes = append(runes, '·', '°', 'ε', '→', '…', '≈', '×', '–', '—', '≥', '≤')
	for _, f := range []string{`C:\Windows\Fonts\segoeui.ttf`, `C:\Windows\Fonts\arial.ttf`} {
		font := rl.LoadFontEx(f, 72, runes, int32(len(runes)))
		if font.Texture.ID != 0 && font.BaseSize > 0 {
			rl.SetTextureFilter(font.Texture, rl.FilterBilinear)
			a.font, a.fontOK = font, true
			return
		}
	}
}

func (a *App) loop() {
	for !a.quit.Load() && !rl.WindowShouldClose() {
		a.drainEvents()
		a.update()
		a.updateTray()
		if a.state == stIdle && time.Now().After(a.toastUntil) {
			rl.SetTargetFPS(15)
		} else {
			rl.SetTargetFPS(60)
		}
		rl.BeginDrawing()
		rl.ClearBackground(rl.Blank)
		a.draw()
		rl.EndDrawing()
	}
}

func (a *App) drainEvents() {
	for {
		select {
		case ev := <-a.hooks.Events:
			a.handle(ev)
		default:
			return
		}
	}
}

func (a *App) handle(ev winput.Event) {
	if ev.Kind == winput.EvTray {
		a.handleTray(ev.Name)
		return
	}
	if ev.Kind == winput.EvHotkey {
		switch ev.Name {
		case "quit":
			a.requestQuit()
		case "trigger":
			switch a.state {
			case stIdle, stPaused:
				a.enterSelect()
			case stSelect:
				a.toIdle("Cancelled", false)
			}
		case "resume":
			if a.state == stIdle || a.state == stPaused {
				a.startResume()
			}
		}
		return
	}
	switch a.state {
	case stSelect:
		a.handleSelect(ev)
	case stArming:
		if ev.Kind == winput.EvAbort {
			a.cancelArming()
		}
	case stDrawing:
		if ev.Kind == winput.EvAbort && a.run != nil && a.run.ex != nil {
			a.run.ex.abort.Store(true)
		}
	case stPaused:
		if ev.Kind == winput.EvKey {
			switch ev.Name {
			case "start":
				a.startResume()
			case "cancel":
				a.resume = nil
				a.toIdle("Drawing discarded", false)
			}
		}
	}
}

// ui is the HUD scale factor (text and panels).
func (a *App) ui() float32 { return float32(1.5 * a.cfg.HUDScale) }

func (a *App) toIdle(msg string, isErr bool) {
	a.state = stIdle
	a.dragging, a.moving = false, false
	a.hooks.SetMode(winput.ModeIdle)
	if msg != "" {
		a.notify(msg, isErr)
	}
}

func (a *App) enterSelect() {
	a.reloadConfig()
	a.target, a.targetTitle = winput.Foreground()
	a.state = stSelect
	a.dragging, a.moving = false, false
	a.placed = nil
	a.hooks.SetMode(winput.ModeSelect)
	winput.BringToTop(a.hwnd)
	a.toastUntil = time.Time{}
}

// backToSelect returns to box selection keeping the placed box.
func (a *App) backToSelect(msg string) {
	a.state = stSelect
	a.dragging, a.moving = false, false
	a.hooks.SetMode(winput.ModeSelect)
	if msg != "" {
		a.notify(msg, false)
	}
}

func (r rectI) contains(x, y int) bool {
	return x >= r.X && y >= r.Y && x < r.X+r.W && y < r.Y+r.H
}

func (a *App) place(b rectI) {
	a.placed = &b
	a.st.BoxX, a.st.BoxY, a.st.BoxW, a.st.BoxH = b.X, b.Y, b.W, b.H
}

func (a *App) handleSelect(ev winput.Event) {
	switch ev.Kind {
	case winput.EvSelDown:
		if a.placed != nil && a.placed.contains(ev.X, ev.Y) {
			a.moving = true // drag inside the box moves it
			a.mvDX, a.mvDY = ev.X-a.placed.X, ev.Y-a.placed.Y
			return
		}
		a.dragging = true
		a.ax, a.ay = ev.X, ev.Y
	case winput.EvSelUp:
		a.cx, a.cy = ev.X, ev.Y // exact release point
		switch {
		case a.moving:
			a.place(a.currentBox())
		case a.dragging:
			box := a.currentBox()
			a.dragging = false
			if !box.valid() {
				box = a.ghostBox(ev.X, ev.Y) // click: place the box at this size here
			}
			a.place(box)
		}
		a.dragging, a.moving = false, false
		a.saveState()
	case winput.EvCancel: // right click: un-place, or leave
		if a.placed != nil && !a.dragging {
			a.placed = nil
			return
		}
		a.toIdle("Cancelled", false)
	case winput.EvWheel:
		if ev.Alt {
			a.stepBudget(ev.Delta)
			return
		}
		if ev.Ctrl || a.dragging { // while dragging the mouse sets the size
			a.st.Quality = plan.ClampLevel(a.st.Quality + ev.Delta)
		} else {
			a.scaleBox(ev.Delta)
		}
		a.saveState()
	case winput.EvKey:
		switch ev.Name {
		case "cancel":
			a.toIdle("Cancelled", false)
		case "start":
			a.dragging, a.moving = false, false
			box := a.currentBox()
			a.place(box)
			a.saveState()
			a.beginRun(box)
			return
		case "next":
			if ev.Shift {
				a.lib.Step(-1)
			} else {
				a.lib.Step(1)
			}
		case "q+":
			a.st.Quality = plan.ClampLevel(a.st.Quality + 1)
		case "q-":
			a.st.Quality = plan.ClampLevel(a.st.Quality - 1)
		case "rotate":
			if ev.Shift {
				a.st.Rotate = (a.st.Rotate + 3) % 4
			} else {
				a.st.Rotate = (a.st.Rotate + 1) % 4
			}
		case "flip":
			a.st.FlipH = !a.st.FlipH
		case "stretch":
			a.st.Stretch = !a.st.Stretch
		case "mode":
			i := 0
			for k, m := range plan.Modes {
				if m == a.st.Mode {
					i = k
				}
			}
			a.st.Mode = plan.Modes[(i+1)%len(plan.Modes)]
		case "budget":
			v := !a.autoLower()
			a.st.AutoLower = &v
		case "verts":
			a.showVerts = !a.showVerts
		case "repeat":
			if a.st.BoxW >= 8 && a.st.BoxH >= 8 {
				a.dragging, a.moving = false, false
				a.place(rectI{a.st.BoxX, a.st.BoxY, a.st.BoxW, a.st.BoxH})
			} else {
				a.notify("No previous box yet", true)
			}
		}
		a.saveState()
	}
}

// ---------------------------------------------------------------------------
// planning helpers

func (a *App) geometry() (*plan.Geometry, error, bool) {
	f, _, _ := a.lib.Current()
	return a.lib.Geometry(f, a.cfg.prepOptions(a.st.Mode))
}

func (a *App) params(stretch bool) plan.Params {
	return plan.Params{
		Level:  a.st.Quality,
		MaxSeg: a.cfg.MaxSegmentPx,
		Order:  a.cfg.Order,
		Xf:     plan.Transform{Rotate: a.st.Rotate, FlipH: a.st.FlipH, Stretch: stretch || a.st.Stretch},
	}
}

func (a *App) currentBox() rectI {
	switch {
	case a.dragging:
		x0, x1 := min(a.ax, a.cx), max(a.ax, a.cx)
		y0, y1 := min(a.ay, a.cy), max(a.ay, a.cy)
		return rectI{x0, y0, x1 - x0, y1 - y0}
	case a.moving && a.placed != nil:
		return rectI{a.cx - a.mvDX, a.cy - a.mvDY, a.placed.W, a.placed.H}
	case a.placed != nil:
		return *a.placed
	}
	return a.ghostBox(a.cx, a.cy)
}

// scaleBox resizes the placed box (around its centre) or the box that
// follows the cursor by 10 % per wheel notch.
func (a *App) scaleBox(delta int) {
	b := a.currentBox()
	k := math.Pow(1.1, float64(delta))
	l, t, r, bot := winput.MonitorRect(b.X+b.W/2, b.Y+b.H/2)
	w := int(math.Round(math.Max(24, math.Min(float64(r-l), float64(b.W)*k))))
	h := int(math.Round(math.Max(24, math.Min(float64(bot-t), float64(b.H)*k))))
	nb := rectI{b.X + (b.W-w)/2, b.Y + (b.H-h)/2, w, h}
	if a.placed != nil {
		a.place(nb)
		return
	}
	a.st.BoxX, a.st.BoxY, a.st.BoxW, a.st.BoxH = nb.X, nb.Y, nb.W, nb.H
}

// ghostBox is the previous box size centred on (x,y).
func (a *App) ghostBox(x, y int) rectI {
	w, h := a.st.BoxW, a.st.BoxH
	if w < 8 || h < 8 {
		_, t, _, b := winput.MonitorRect(x, y)
		h = (b - t) * 35 / 100
		w = h * 4 / 3
	}
	return rectI{x - w/2, y - h/2, w, h}
}

func (a *App) planReq(g *plan.Geometry, box rectI, stretch bool) PlanReq {
	return PlanReq{G: g, W: box.W, H: box.H, P: a.params(stretch), Budget: a.cfg.budget(), AutoLower: a.autoLower(), Timing: a.timing}
}

// stepBudget changes the time budget by 10 s per wheel notch and stores it
// in bardraw.json (it is the "currently defined" limit everywhere).
func (a *App) stepBudget(delta int) {
	b := a.cfg.TimeBudgetS
	if b <= 0 {
		b = 60
	} else {
		b = math.Round(b/10)*10 + float64(delta)*10
	}
	a.cfg.TimeBudgetS = math.Max(10, math.Min(600, b))
	_ = saveJSON(a.cfgPath, a.cfg)
}

func (a *App) autoLower() bool {
	if a.st.AutoLower != nil {
		return *a.st.AutoLower
	}
	return a.cfg.AutoLowerQuality
}

// beginRun computes the final plan for box and arms drawing.
func (a *App) beginRun(box rectI) {
	g, err, loading := a.geometry()
	switch {
	case err != nil:
		a.notify("Cannot read SVG: "+err.Error(), true)
		return
	case loading || g == nil:
		a.notify("Still processing the SVG, try again in a moment", true)
		return
	}
	res := ComputePlan(a.planReq(g, box, winput.ShiftHeld()))
	if len(res.Plan.Strokes) == 0 {
		a.notify("Nothing to draw at this size/quality", true)
		return
	}
	a.st.BoxX, a.st.BoxY, a.st.BoxW, a.st.BoxH = box.X, box.Y, box.W, box.H
	a.saveState()
	f, _, _ := a.lib.Current()
	r := &runInfo{res: res, box: box, timing: a.timing, file: f}
	r.strokes = res.Plan.Translate(float64(box.X), float64(box.Y))
	r.starts = plan.StrokeStartTimes(r.strokes, r.timing)
	a.arm(r)
}

func (a *App) arm(r *runInfo) {
	a.armedFrom = a.state
	r.armAt = time.Now().Add(a.cfg.startDelay())
	r.keysWait = true
	r.ex = nil
	a.run = r
	a.state = stArming
	a.hooks.SetMode(winput.ModeDraw) // from now on a new key/button press cancels
	winput.BringToTop(a.hwnd)
}

// cancelArming handles input that arrived before drawing started.
func (a *App) cancelArming() {
	switch a.armedFrom {
	case stSelect:
		a.backToSelect("Not started — Space starts drawing")
	case stPaused:
		a.pause("")
	default:
		a.toIdle("Cancelled before drawing started", false)
	}
}

// pause keeps the unfinished drawing; Space continues it.
func (a *App) pause(msg string) {
	if a.resume == nil {
		a.toIdle(msg, false)
		return
	}
	cp := *a.resume
	a.run = &cp
	a.state = stPaused
	a.dragging, a.moving = false, false
	a.hooks.SetMode(winput.ModePaused)
	if msg != "" {
		a.notify(msg, false)
	}
}

func (a *App) startResume() {
	r := a.resume
	if r == nil || r.from >= len(r.strokes) {
		a.notify("Nothing to resume", true)
		return
	}
	if a.state == stIdle {
		a.reloadConfig()
		a.target, a.targetTitle = winput.Foreground()
	}
	cp := *r
	a.arm(&cp)
}

func (a *App) update() {
	a.cx, a.cy = winput.CursorPos()
	p := rl.GetWindowPosition()
	a.winX, a.winY = int(p.X), int(p.Y)
	if a.state != stIdle && time.Since(a.lastTop) > 2*time.Second {
		winput.BringToTop(a.hwnd)
		a.lastTop = time.Now()
	}
	switch a.state {
	case stSelect:
		if g, err, loading := a.geometry(); err == nil && !loading && g != nil {
			if box := a.currentBox(); box.valid() {
				a.planner.Request(a.planReq(g, box, winput.ShiftHeld()))
			}
		}
	case stArming:
		a.updateArming()
	case stDrawing:
		a.updateDrawing()
	}
}

func (a *App) updateArming() {
	r := a.run
	now := time.Now()
	// start only from a clean input state: every real key (Space, Ctrl, …)
	// released, then start_delay_ms of quiet
	if a.hooks.KeysHeld() {
		r.keysWait = true
		r.armAt = now.Add(a.cfg.startDelay())
		return
	}
	r.keysWait = false
	if now.Before(r.armAt) {
		return
	}
	if wait := time.Until(a.lastKeyUp.Add(keyChainGuard)); wait > 0 {
		return // a second draw-key press this soon would open BAR's label prompt
	}
	if !a.dry() {
		fg, title := winput.Foreground()
		if want := strings.TrimSpace(a.cfg.GameWindowTitle); want != "" &&
			!strings.Contains(strings.ToLower(title), strings.ToLower(want)) {
			a.toIdle(fmt.Sprintf("Foreground window %q does not match game_window_title", title), true)
			return
		}
		if a.target != 0 && fg != a.target {
			a.toIdle("The active window changed — drawing cancelled", true)
			return
		}
	}
	var inj *winput.Injector
	if !a.dry() {
		inj = a.inj
	}
	r.ex = startExecutor(r.strokes, r.from, r.timing, inj)
	a.state = stDrawing
}

func (a *App) updateDrawing() {
	r := a.run
	select {
	case res := <-r.ex.done:
		a.lastKeyUp = time.Now()
		total := len(r.strokes)
		switch {
		case errors.Is(res.err, plan.ErrAborted):
			cp := *r
			cp.from = res.next
			cp.ex = nil
			a.resume = &cp
			a.pause(fmt.Sprintf("Paused after %d/%d strokes", res.next, total))
		case res.err != nil:
			a.toIdle("Input error: "+res.err.Error(), true)
		default:
			a.resume = nil
			a.toIdle(fmt.Sprintf("Done: %d strokes in %.1f s", total-r.from, res.elapsed.Seconds()), false)
		}
	default:
	}
}

// progress returns the completed fraction and the remaining time estimate.
func (a *App) progress() (frac float64, left time.Duration, stroke, vertex int) {
	r := a.run
	if r == nil || len(r.starts) == 0 {
		return 0, 0, 0, 0
	}
	n := len(r.strokes)
	if r.ex == nil {
		return 0, r.starts[n] - r.starts[min(r.from, n)], r.from, 0
	}
	stroke, vertex = int(r.ex.stroke.Load()), int(r.ex.vertex.Load())
	stroke = max(r.from, min(stroke, n))
	begin := r.starts[min(r.from, n)]
	total := r.starts[n] - begin
	done := r.starts[stroke] - begin
	if stroke < n {
		done += time.Duration(vertex) * r.timing.VertexInterval
	}
	if total <= 0 {
		return 1, 0, stroke, vertex
	}
	frac = math.Min(1, float64(done)/float64(total))
	return frac, max(0, total-done), stroke, vertex
}
