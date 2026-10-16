package winput

import (
	"testing"
	"time"
)

func TestShakeMeter(t *testing.T) {
	t0 := time.Unix(0, 0)
	// a bump: a few events, some of them "teleport-sized" — must not stop
	var m shakeMeter
	for i, p := range [][2]float64{{0, 0}, {500, 300}, {2, 1}, {900, 40}, {3, 2}} {
		if m.add(p[0], p[1], t0.Add(time.Duration(i)*8*time.Millisecond), 400) {
			t.Fatalf("bump stopped drawing at event %d", i)
		}
	}
	// an occasional nudge every second over a long drawing must not add up
	m = shakeMeter{}
	for i := 0; i < 300; i++ {
		if m.add(float64(i%2)*30, 0, t0.Add(time.Duration(i)*time.Second), 400) {
			t.Fatalf("slow nudges stopped drawing after %d s", i)
		}
	}
	// shaking for about a second (125 Hz mouse, 20 px per event) stops it
	m = shakeMeter{}
	stopped := -1
	for i := 0; i < 250; i++ {
		x := float64((i % 2) * 20)
		if m.add(x, 0, t0.Add(time.Duration(i)*8*time.Millisecond), 400) {
			stopped = i
			break
		}
	}
	if stopped < 0 || stopped > 160 {
		t.Fatalf("shaking stopped at event %d", stopped)
	}
	// disabled
	m = shakeMeter{}
	for i := 0; i < 1000; i++ {
		if m.add(float64(i%2)*50, 0, t0.Add(time.Duration(i)*time.Millisecond), 0) {
			t.Fatal("disabled meter fired")
		}
	}
}
