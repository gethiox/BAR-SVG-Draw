package plan

import (
	"errors"
	"math"
	"time"

	"bardraw/internal/geom"
)

// Game facts this timing model is built on (Recoil engine, BAR gui_chat.lua
// and BAR's keybind_defaults.json):
//
//   - While the draw key is held, a left press anchors the pen and every mouse
//     move emits a line from the anchor to the cursor, but only if ≥ 50 ms
//     passed since the previous line or the press. The check runs when the
//     game processes input (once per rendered frame), so a vertex is only
//     guaranteed to land if it is held for 50 ms + one frame.
//   - Each vertex costs the same time no matter how long the segment is, so
//     long straight lines are cheap and curves are what cost time.
//   - Two left presses within 300 ms open the label prompt, so consecutive
//     strokes need a press gap of 300 ms + one frame.
//   - Pressing the draw key twice within the key-chain timeout (750 ms) runs
//     "drawlabel" (bound to the chain sc_`,sc_`), so the key is pressed once
//     and held for the whole drawing.

// Timing parameters of the input schedule.
type Timing struct {
	VertexInterval time.Duration // between consecutive pen positions (and press → first move)
	Settle         time.Duration // small pause so events of different kinds are processed in order
	PressGap       time.Duration // minimum time between two left presses
}

func frameMs(fps float64) float64 {
	if fps <= 0 {
		fps = 60
	}
	return 1000.0 / fps
}

// AutoVertexInterval returns a safe vertex interval for the game's frame rate.
func AutoVertexInterval(fps float64) time.Duration {
	return time.Duration((50 + 1.2*frameMs(fps) + 3) * float64(time.Millisecond))
}

// AutoPressGap returns a safe gap between left presses for the frame rate.
func AutoPressGap(fps float64) time.Duration {
	return time.Duration((300 + 1.2*frameMs(fps) + 10) * float64(time.Millisecond))
}

// NewTiming builds the timing. vertexMs <= 0 and pressGapMs < 0 select
// automatic values derived from the game's frame rate.
func NewTiming(fps, vertexMs, settleMs, pressGapMs float64) Timing {
	t := Timing{
		VertexInterval: AutoVertexInterval(fps),
		Settle:         time.Duration(math.Max(0, settleMs) * float64(time.Millisecond)),
		PressGap:       AutoPressGap(fps),
	}
	if vertexMs > 0 {
		t.VertexInterval = time.Duration(vertexMs * float64(time.Millisecond))
	}
	if pressGapMs >= 0 {
		t.PressGap = time.Duration(pressGapMs * float64(time.Millisecond))
	}
	return t
}

// Driver performs (or simulates) the input actions.
type Driver interface {
	Now() time.Duration
	Wait(until time.Duration) error // abortable sleep
	Pause(d time.Duration)          // non-abortable sleep (cleanup only)
	Move(p geom.Pt) error
	Button(down bool) error
	Key(down bool) error
	Progress(stroke, vertex int)
}

// ErrAborted is returned by Driver.Wait when the user interrupted drawing.
var ErrAborted = errors.New("aborted")

// Run draws strokes[from:] and returns the index of the first stroke that was
// not completed (len(strokes) when everything was drawn). The mouse button
// and draw key are always released before returning.
func Run(strokes [][]geom.Pt, from int, t Timing, d Driver) (next int, err error) {
	keyDown, btnDown := false, false
	next = from
	defer func() {
		if btnDown {
			_ = d.Button(false)
			d.Pause(t.Settle + 5*time.Millisecond)
		}
		if keyDown {
			_ = d.Key(false)
		}
	}()
	after := func(dur time.Duration) error { return d.Wait(d.Now() + dur) }

	// the draw key goes down once and stays down for the whole drawing
	if err = d.Key(true); err != nil {
		return
	}
	keyDown = true
	if err = after(t.Settle + 30*time.Millisecond); err != nil {
		return
	}
	lastPress := time.Duration(math.MinInt64 / 2)
	lastVertex := lastPress
	for si := from; si < len(strokes); si++ {
		s := strokes[si]
		if len(s) < 2 {
			next = si + 1
			continue
		}
		if err = d.Move(s[0]); err != nil {
			return
		}
		if err = after(t.Settle); err != nil {
			return
		}
		if err = d.Wait(max(d.Now(), lastPress+t.PressGap)); err != nil {
			return
		}
		if err = d.Button(true); err != nil {
			return
		}
		btnDown = true
		lastPress = d.Now()
		lastVertex = lastPress
		d.Progress(si, 0)
		for vi := 1; vi < len(s); vi++ {
			if err = d.Wait(lastVertex + t.VertexInterval); err != nil {
				return
			}
			if err = d.Move(s[vi]); err != nil {
				return
			}
			lastVertex = d.Now()
			d.Progress(si, vi)
		}
		// let the game handle the last move while the button is still down
		if err = after(t.Settle); err != nil {
			return
		}
		if err = d.Button(false); err != nil {
			return
		}
		btnDown = false
		next = si + 1
	}
	d.Progress(len(strokes), 0)
	return len(strokes), nil
}

// simDriver runs the schedule on a virtual clock.
type simDriver struct{ now time.Duration }

func (s *simDriver) Now() time.Duration { return s.now }
func (s *simDriver) Wait(u time.Duration) error {
	if u > s.now {
		s.now = u
	}
	return nil
}
func (s *simDriver) Pause(d time.Duration) { s.now += d }
func (s *simDriver) Move(geom.Pt) error    { return nil }
func (s *simDriver) Button(bool) error     { return nil }
func (s *simDriver) Key(bool) error        { return nil }
func (s *simDriver) Progress(int, int)     {}

// Estimate returns the ideal drawing time of strokes[from:].
func Estimate(strokes [][]geom.Pt, from int, t Timing) time.Duration {
	d := &simDriver{}
	_, _ = Run(strokes, from, t, d)
	return d.now
}

// StrokeStartTimes returns the simulated start time of every stroke plus the
// total as last element (len = len(strokes)+1). Used for progress/ETA display.
func StrokeStartTimes(strokes [][]geom.Pt, t Timing) []time.Duration {
	d := &recDriver{starts: make([]time.Duration, 0, len(strokes)+1)}
	_, _ = Run(strokes, 0, t, d)
	out := d.starts
	if len(out) > len(strokes) {
		out = out[:len(strokes)]
	}
	for len(out) < len(strokes) {
		out = append(out, d.now)
	}
	return append(out, d.now)
}

type recDriver struct {
	simDriver
	starts []time.Duration
}

func (r *recDriver) Progress(stroke, vertex int) {
	if vertex == 0 {
		for len(r.starts) <= stroke {
			r.starts = append(r.starts, r.now)
		}
	}
}
