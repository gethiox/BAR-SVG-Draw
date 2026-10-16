# Disclaimer

Entirety of this project has been purely AI generated 

# BAR-Draw — draw SVG images on the map in Beyond All Reason

A transparent, click-through overlay (Go + raylib) that turns any SVG into map
drawings by emulating what a player does: hold the draw key (`` ` ``/`~`) and
drag with the left mouse button.

```
Ctrl+Home  →  place / reshape the box (live preview)  →  Space  →  release all keys  →  250 ms  →  drawing
```

Space (or any key press or mouse click) pauses it immediately and safely —
button and draw key are always released. Mouse movement is ignored while
drawing. While paused the game is fully usable; Space continues, Esc discards. A tray icon shows that bardraw is running —
amber when idle, cyan while you choose a box, red while it draws — and its menu
opens the drawings folder or the settings, toggles dry run and quits.

## Build (Windows)

Needs Go 1.22+. No C compiler: in cgo-free mode raylib-go embeds `raylib.dll`.

```bat
set CGO_ENABLED=0
go build -trimpath -ldflags "-s -w -H=windowsgui" -o bardraw.exe .
```

(or just run `build.bat`). Drop `-H=windowsgui` to keep a console window for
log output. The first start creates `bardraw.json` (settings, each one
documented in its `_help` section) and uses the `drawings` folder next to the
exe — put your `.svg` files there.

Run BAR in **borderless / windowed** mode; nothing can be drawn on top of
exclusive full-screen.

## Using it

| While selecting | |
|---|---|
| **Space** | **start drawing** (in the placed box, or at the cursor if none is placed) |
| Left drag | new box (the drawing is fitted inside, aspect kept) |
| Left drag inside the box | move it |
| Left click | place the box at the cursor with its current size |
| Wheel | resize the box by 10 % per notch |
| Enter | put the box back where the previous drawing was |
| Right click | un-place the box (it follows the cursor again); again to cancel |
| Ctrl+Wheel, PgUp/PgDn, +/− | quality 1 (fast) … 7 (exact); default 7 |
| Alt+Wheel | time limit ±10 s (default 60 s) |
| B | auto-lower quality on/off (off = keep quality, plan line turns red when over the limit) |
| Tab / Shift+Tab | next / previous SVG in the folder |
| M | mode: auto → layered → outline → centerline |
| R / Shift+R | rotate 90° |
| F | mirror |
| S or hold Shift | stretch to fill the box |
| V | show vertices |
| Esc, Ctrl+Home | cancel |

The preview shows two layers: thin white = the exact SVG, amber 3 px = what the
map lines will look like at the chosen quality (map lines are 3 px wide). The
panel shows strokes, vertices and the ETA. Arrow keys still reach the game, so
you can move the camera while choosing.

Drawing waits until every key (Space, Ctrl, …) is released and then
`start_delay_ms`, so the game sees a clean input state.

Other hotkeys: **Ctrl+Shift+Home** continues a paused drawing even after you
pressed Esc, **Ctrl+Shift+End** quits. All hotkeys are configurable.

## How the timing works (from the game source)

Checked against RecoilEngine `rts/Game/InMapDraw.cpp` and BAR
`luaui/Widgets/gui_chat.lua` (current BAR handles map drawing in Lua there),
`common/configs/keybind_defaults.json` and the server's `GameServer.cpp`:

* With the draw key held, a left press anchors the pen and each mouse move adds
  a line from the anchor to the cursor, **but only if ≥ 50 ms passed since the
  previous line (or the press)**. The check happens when the game processes
  input, once per rendered frame — so a vertex is only safe if it is held for
  50 ms + one frame. bardraw therefore teleports the cursor from vertex to
  vertex every `50 + 1.2·frame + 3` ms (73 ms at 60 fps, 61 ms at 144 fps — set
  `game_fps`).
* A vertex costs the same time whatever the segment length, so long straight
  lines are nearly free and curves are what cost time. The quality slider is a
  Douglas–Peucker tolerance in screen pixels: straight runs collapse to their
  endpoints, curves keep as many vertices as the tolerance needs.
* BAR binds `drawlabel` to the key chain `` sc_`,sc_` ``: pressing the draw key
  twice within the engine's `KeyChainTimeout` (750 ms) opens the label prompt.
  bardraw therefore presses the key once and holds it for the whole drawing,
  and keeps at least 850 ms between two drawings.
* Two left presses within 300 ms also open the label prompt, so strokes start
  at least `300 + 1.2·frame + 10` ms apart (330 ms at 60 fps). Long strokes
  take longer than that anyway; short ones pay the difference, which is why
  touching strokes are merged.
* The server drops map-draw messages that arrive < 50 ms apart 25 times in a
  row — never reached at these rates.
* **BAR erases map marks ~60 s after they were drawn** (the "Auto mapmark
  eraser" widget, on by default, per viewer). Hence the default
  `time_budget_s: 60` with quality 7: if the ETA is longer, quality is lowered
  only until it fits (the panel says so). Turn auto-lowering off with B to keep
  your quality; the plan line then turns red when it is over the limit. The optional "Map Draw Blocker" widget (off by default)
  mutes players who draw too much line length per second.
* Map lines are straight in 3D between the ground heights of their endpoints:
  on hilly maps a long segment can vanish into a hill. `max_segment_px` (e.g.
  60) splits long segments at the cost of time.

Strokes whose ends touch are merged (saves a pen-up/pen-down), tiny strokes
below the quality's minimum length are skipped, and strokes are ordered
nearest-first so the drawing grows like a human would draw it.

## Layered colour art: only visible lines

Most coloured SVGs (Inkscape/Illustrator art, vectors of cartoon characters)
are stacks of overlapping objects: the outline is often a darker shape
*behind* the fill, shading sits on top, eyes are clipped into place.
Drawing every object's border would show everything that is hidden — an
x-ray. **layered** mode (picked automatically for multi-colour or clipped art)
instead renders the picture exactly as it looks, clip paths included, and
draws only what is visible:

* thin visible regions that are darker than their surroundings (outlines,
  strands, lashes) become **one** line along their middle;
* boundaries between visible coloured areas (pupil/iris, highlights, shading)
  are drawn where the colours differ by at least `edge_contrast`;
* nothing covered by an object in front is drawn.

Tuning: `edge_contrast` (default 0.08) — raise it to skip subtle shading,
lower it for more detail; `line_width_percent` (default 2) — how narrow a
region must be to count as a line; `layered_resolution` (default 1400).
Gradients are treated as their average colour.

## Outline vs centre-line

Many SVGs (traced images, coloring pages, logos) draw every line as a thin
*filled* shape. Drawing its outline gives two parallel lines per stroke and
twice the vertices. **centerline** mode rasterises the filled ink, thins it to a
one-pixel skeleton, prunes spurs, smooths it and chains it back into long
strokes. `auto` picks centre-lines when the ink is thin relative to the picture.
For the included test image this means 444 instead of 764 vertices at quality 4
in a 600×500 box (≈ 35 s at 60 fps).

Stroked SVG paths (`stroke=…`, no fill) are always drawn as they are.

## Planning without the game

```bat
go run ./cmd/svgplan -svg drawings\Fluttershy.svg -w 600 -h 500 -o preview.png
```

prints strokes, vertices and ETA for all 7 quality levels and writes a preview
(grey = SVG, blue = in-game estimate, `-v` marks vertices). Runs on any OS.

To try the overlay without BAR: set `"dry_run": true` (or start with `-dry`) to
watch the simulated progress, or open MS Paint, pick the pencil and draw — Paint
ignores the `` ` `` key and connects the drag points just like the game.

## Settings worth knowing

| key | default | |
|---|---|---|
| `draw_key` | `sc:0x29` | scan code of the key left of 1; BAR's Grid and Legacy keybind profiles bind `sc_`` ` to drawinmap. Legacy 60%: `sc:0x10` (Q). Grid 60%: `sc:0x39+sc:0x10` (Space as meta + Q). |
| `game_fps` | 60 | sets the safe time per vertex |
| `vertex_interval_ms` | 0 (auto) | override if corners get skipped (raise) or you want to experiment (lower) |
| `time_budget_s` | 60 | drawing time limit; 0 = none. Alt+Wheel while selecting |
| `auto_lower_quality` | true | lower quality until the ETA fits; false = only warn (red plan line). B while selecting |
| `quality` | 7 | starting quality; auto-lowering steps down only as far as needed |
| `mode` | auto | `outline`, `centerline` |
| `game_window_title` | "" | e.g. `"Beyond All Reason"` to refuse drawing into other windows |
| `stop_on_mouse_shake_px` | 0 | 0 = mouse movement never stops drawing (real moves are blocked anyway). e.g. 400 = shaking the mouse for about a second stops it; bumps don't count |
| `hud_scale` | 1 | size of the overlay text and panels |
| `tray_icon`, `startup_notification` | true | tray icon and the "bardraw is running" notification |

The config is re-read on every Ctrl+Home, so edits apply without restarting
(tray menu → *Edit settings*). Files from older versions are updated in place:
new settings are added, retired ones removed, your values kept.

## Good to know

* Keep the box off the minimap and screen edges: over the minimap BAR maps the
  cursor to minimap positions, and near an edge the camera may edge-scroll mid
  drawing (the panel warns about edges). Points over sky/void are not drawn.
* While drawing, the overlay only shows what is still to come; finished parts
  are already visible in the game.
* The low-level keyboard hook and `SendInput` can make antivirus heuristics
  nervous about any freshly built exe; that is what they are for here.
* Text elements are skipped — convert text to paths in your editor
  (Inkscape: *Path → Object to Path*). Images, masks and clip paths are ignored.
* If the overlay shows black instead of transparent, set `"msaa": false` and
  restart; if it stays black, the GPU driver does not offer transparent OpenGL
  windows (updating it usually helps).

## Layout

```
main_windows.go, app_windows.go   overlay state machine and main loop
draw_windows.go                   preview, HUD, progress rendering
exec_windows.go                   drawing executor (precise timing, abort)
config.go, library.go             settings, SVG folder, async planner
internal/svg      SVG parser (paths, shapes, transforms, CSS, use) → polylines
internal/raster   scanline fill, Zhang–Suen thinning, skeleton tracing
internal/plan     fitting, simplification, joining, ordering, timing model
internal/winput   Windows hooks, hotkeys, SendInput, tray icon
cmd/svgplan       headless planner / PNG preview
```
