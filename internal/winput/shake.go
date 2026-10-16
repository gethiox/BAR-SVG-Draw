package winput

import (
	"math"
	"time"
)

// shakeMeter decides whether real mouse movement during drawing is a
// deliberate "stop" gesture. Event positions are the cursor (which bardraw
// keeps teleporting) plus the real motion, so one event can look like a huge
// jump: each event counts at most 8 px, and the total leaks away at 150 px/s.
// Only sustained, deliberate movement reaches the limit.
type shakeMeter struct {
	total float64
	last  time.Time
	x, y  float64
	has   bool
}

// add records an event and reports whether the limit was exceeded.
// limit <= 0 disables the meter.
func (m *shakeMeter) add(x, y float64, now time.Time, limit float64) bool {
	if limit <= 0 {
		return false
	}
	if !m.last.IsZero() {
		m.total = math.Max(0, m.total-150*now.Sub(m.last).Seconds())
	}
	m.last = now
	if m.has {
		m.total += math.Min(math.Hypot(x-m.x, y-m.y), 8)
	}
	m.x, m.y, m.has = x, y, true
	return m.total > limit
}
