package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"time"

	"bardraw/internal/plan"
)

// Config is stored as bardraw.json next to the executable. Unknown or missing
// fields fall back to defaults; the file is (re)read every time the trigger
// hotkey is pressed, so edits apply without restarting.
type Config struct {
	SVGPath          string  `json:"svg_path"`
	TriggerHotkey    string  `json:"trigger_hotkey"`
	ResumeHotkey     string  `json:"resume_hotkey"`
	QuitHotkey       string  `json:"quit_hotkey"`
	DrawKey          string  `json:"draw_key"`
	GameFPS          float64 `json:"game_fps"`
	VertexIntervalMs float64 `json:"vertex_interval_ms"`
	SettleMs         float64 `json:"settle_ms"`
	PressGapMs       float64 `json:"press_gap_ms"`
	StartDelayMs     float64 `json:"start_delay_ms"`
	Quality          int     `json:"quality"`
	TimeBudgetS      float64 `json:"time_budget_s"`
	AutoLowerQuality bool    `json:"auto_lower_quality"`
	ConfigVersion    int     `json:"config_version"`
	Mode             string  `json:"mode"`
	Fit              string  `json:"fit"`
	Order            string  `json:"order"`
	MaxSegmentPx     float64 `json:"max_segment_px"`
	SkipWhiteFills   bool    `json:"skip_white_fills"`
	CenterlineRes    int     `json:"centerline_resolution"`
	LayeredRes       int     `json:"layered_resolution"`
	LineWidthPct     float64 `json:"line_width_percent"`
	EdgeContrast     float64 `json:"edge_contrast"`
	PreviewLinePx    float64 `json:"preview_line_px"`
	StopOnShakePx    float64 `json:"stop_on_mouse_shake_px"`
	GameWindowTitle  string  `json:"game_window_title"`
	MouseMoveMethod  string  `json:"mouse_move_method"`
	EdgeWarnPercent  float64 `json:"edge_warning_percent"`
	DryRun           bool    `json:"dry_run"`
	MSAA             bool    `json:"msaa"`
	HUDScale         float64 `json:"hud_scale"`
	TrayIcon         bool    `json:"tray_icon"`
	StartupNotice    bool    `json:"startup_notification"`

	Help map[string]string `json:"_help,omitempty"`
}

func defaultConfig() Config {
	return Config{
		SVGPath:          "drawings",
		TriggerHotkey:    "ctrl+home",
		ResumeHotkey:     "ctrl+shift+home",
		QuitHotkey:       "ctrl+shift+end",
		DrawKey:          "sc:0x29",
		GameFPS:          60,
		VertexIntervalMs: 0,
		SettleMs:         12,
		PressGapMs:       -1,
		StartDelayMs:     250,
		Quality:          7,
		TimeBudgetS:      60,
		AutoLowerQuality: true,
		ConfigVersion:    configVersion,
		Mode:             plan.ModeAuto,
		Fit:              "content",
		Order:            "nearest",
		MaxSegmentPx:     0,
		SkipWhiteFills:   true,
		CenterlineRes:    0,
		LayeredRes:       1400,
		LineWidthPct:     2,
		EdgeContrast:     0.08,
		PreviewLinePx:    3,
		StopOnShakePx:    0,
		GameWindowTitle:  "",
		MouseMoveMethod:  "sendinput",
		EdgeWarnPercent:  2.5,
		DryRun:           false,
		MSAA:             true,
		HUDScale:         1,
		TrayIcon:         true,
		StartupNotice:    true,
		Help:             configHelp,
	}
}

var configHelp = map[string]string{
	"svg_path":               "SVG file or folder of SVGs (relative to this file). Tab / Shift+Tab cycles files while selecting.",
	"trigger_hotkey":         "Starts box selection. Modifiers: ctrl, shift, alt, win. Keys: a-z, 0-9, f1-f24, home, end, insert, delete, pageup, pagedown, …",
	"resume_hotkey":          "Continues an interrupted drawing at the same place.",
	"quit_hotkey":            "Closes the overlay.",
	"draw_key":               "Key held for the whole drawing (BAR action drawinmap). sc:0x29 = scan code of the key left of 1, which BAR's Grid and Legacy profiles bind (sc_`). Legacy 60%: sc:0x10 (Q). Grid 60%: sc:0x39+sc:0x10 (Space as meta + Q). Also vk:0xC0 or key names. It is pressed only once per drawing: pressing it twice within 750 ms opens BAR's label prompt.",
	"game_fps":               "Your in-game FPS. The game accepts one vertex per 50 ms checked once per frame, so the safe vertex interval is ~50 ms + 1.2 frames.",
	"vertex_interval_ms":     "Override the time per vertex (0 = derive from game_fps). Too low = corners get skipped.",
	"settle_ms":              "Pause between key/button events so the game processes them in order.",
	"press_gap_ms":           "Minimum time between left presses (-1 = auto: 300 ms + 1.2 frames + 10 ms). Two presses within 300 ms open BAR's label prompt.",
	"start_delay_ms":         "After Space, drawing waits until every key is released, then this long.",
	"quality":                "1 (fast, rough) … 7 (slow, exact). Default 7: with auto_lower_quality the planner steps down only as far as the time budget requires. Ctrl+wheel / PageUp / PageDown while selecting.",
	"time_budget_s":          "Drawing time limit in seconds (0 = none). BAR erases map marks ~60 s after they are drawn by default, so a longer drawing loses its start before it ends. The plan line turns red when the ETA exceeds it. Alt+wheel changes it in 10 s steps while selecting.",
	"auto_lower_quality":     "true: lower the quality automatically until the ETA fits time_budget_s. false: keep your quality and only warn (red plan line). Toggle with B while selecting.",
	"config_version":         "Internal: used to update old settings files. Do not edit.",
	"mode":                   "auto | layered | outline | centerline. layered: colour art made of overlapping objects — only visible lines are drawn (nothing hidden behind other objects), outlines become single lines. centerline: one-colour line art (coloring pages, potrace output) as single lines. outline: the border of every filled shape (x-ray). auto picks layered for multi-colour or clipped art, otherwise centerline/outline.",
	"layered_resolution":     "Raster size used to work out what is visible in layered mode (higher = more exact, slower).",
	"line_width_percent":     "layered: visible regions narrower than this percentage of the drawing size, and darker than their surroundings, are lines and drawn once along their middle.",
	"edge_contrast":          "layered: minimum colour difference (0..1) for a boundary between two coloured areas to be drawn. Raise it (e.g. 0.15) to skip subtle shading edges, lower it for more detail.",
	"fit":                    "content: fit the visible drawing into the box; viewbox: fit the SVG canvas (keeps its margins).",
	"order":                  "nearest (draws like a human, neighbouring strokes in sequence) | longest (main shapes first) | document.",
	"max_segment_px":         "Split long straight segments (0 = off). Map lines are straight in 3D between endpoint heights, so on hilly maps long segments can disappear into hills; ~60 helps.",
	"skip_white_fills":       "Ignore white/near-white filled shapes without stroke (backgrounds, highlights).",
	"centerline_resolution":  "Raster size for centerline mode (0 = auto).",
	"preview_line_px":        "Line width of the in-game estimate (map lines are 3 px).",
	"stop_on_mouse_shake_px": "0 = mouse movement never stops drawing (real moves are blocked while drawing, so they cannot spoil it). Set e.g. 400 to stop by shaking the mouse: sustained movement counts, small bumps do not.",
	"game_window_title":      "If set, drawing only starts when the foreground window title contains this text.",
	"mouse_move_method":      "sendinput (default) | setcursorpos",
	"edge_warning_percent":   "Warn when the box is closer than this to a screen edge (edge scrolling would move the camera mid-drawing).",
	"dry_run":                "Simulate drawing on the overlay without sending any input (for testing).",
	"msaa":                   "Anti-aliased overlay. Set false if the overlay is black instead of transparent (needs a restart).",
	"hud_scale":              "Size of the overlay text and panels (1 = default).",
	"tray_icon":              "Show the notification-area icon (menu: open drawings folder, edit settings, dry run, quit). Needs a restart.",
	"startup_notification":   "Show a Windows notification when bardraw starts.",
}

// State remembers UI choices between runs (bardraw.state.json).
type State struct {
	Version int    `json:"version"`
	File    string `json:"file"`
	Quality int    `json:"quality"`
	Mode    string `json:"mode"`
	// AutoLower overrides auto_lower_quality when set (B while selecting)
	AutoLower *bool `json:"auto_lower,omitempty"`
	Rotate    int   `json:"rotate"`
	FlipH     bool  `json:"flip_h"`
	Stretch   bool  `json:"stretch"`
	BoxX      int   `json:"box_x"`
	BoxY      int   `json:"box_y"`
	BoxW      int   `json:"box_w"`
	BoxH      int   `json:"box_h"`
}

func loadConfig(path string) (Config, error) {
	cfg := defaultConfig()
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return cfg, saveJSON(path, cfg)
	}
	if err != nil {
		return cfg, err
	}
	cfg.ConfigVersion = 0 // files without the field predate versioning
	cfg.Help = nil        // never let the file's _help merge into configHelp
	if err := json.Unmarshal(data, &cfg); err != nil {
		return defaultConfig(), fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	cfg.Help = configHelp
	cfg.Quality = plan.ClampLevel(cfg.Quality)
	if cfg.GameFPS <= 0 {
		cfg.GameFPS = 60
	}
	if cfg.PreviewLinePx <= 0 {
		cfg.PreviewLinePx = 3
	}
	if cfg.HUDScale <= 0 {
		cfg.HUDScale = 1
	}
	if cfg.ConfigVersion < 2 {
		// v1 shipped a 50 s budget; BAR's eraser allows 60 s
		if cfg.TimeBudgetS == 50 {
			cfg.TimeBudgetS = 60
		}
	}
	if cfg.ConfigVersion < 3 {
		// the old default quality 4 becomes "maximum, lowered by the budget"
		if cfg.Quality == 4 {
			cfg.Quality = 7
		}
	}
	cfg.ConfigVersion = configVersion
	// keep the file in step with this version: add new settings (with their
	// help text) and drop retired ones, preserving the user's values
	var have map[string]json.RawMessage
	if json.Unmarshal(data, &have) == nil {
		want, _ := json.Marshal(cfg)
		var wantKeys map[string]json.RawMessage
		_ = json.Unmarshal(want, &wantKeys)
		var oldHelp map[string]string
		_ = json.Unmarshal(have["_help"], &oldHelp)
		stale := len(have) != len(wantKeys) || !maps.Equal(oldHelp, configHelp) || string(have["time_budget_s"]) != string(wantKeys["time_budget_s"]) || string(have["quality"]) != string(wantKeys["quality"]) || string(have["config_version"]) != string(wantKeys["config_version"])
		for k := range wantKeys {
			if _, ok := have[k]; !ok {
				stale = true
			}
		}
		if stale {
			_ = saveJSON(path, cfg)
		}
	}
	return cfg, nil
}

const configVersion = 3

func loadState(path string) State {
	var st State
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &st)
	}
	return st
}

func saveJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func (c Config) timing() plan.Timing {
	return plan.NewTiming(c.GameFPS, c.VertexIntervalMs, c.SettleMs, c.PressGapMs)
}

func (c Config) startDelay() time.Duration {
	return time.Duration(max(0, c.StartDelayMs) * float64(time.Millisecond))
}

func (c Config) budget() time.Duration {
	return time.Duration(max(0, c.TimeBudgetS) * float64(time.Second))
}

func (c Config) prepOptions(mode string) plan.PrepOptions {
	return plan.PrepOptions{Mode: mode, FitViewBox: c.Fit == "viewbox", SkipWhiteFills: c.SkipWhiteFills, CenterlineRes: c.CenterlineRes,
		LayeredRes: c.LayeredRes, LineWidth: c.LineWidthPct / 100, EdgeContrast: c.EdgeContrast}
}
