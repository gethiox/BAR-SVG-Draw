//go:build windows

package main

import (
	"fmt"
	"runtime"
	"sync/atomic"
	"time"

	"bardraw/internal/geom"
	"bardraw/internal/plan"
	"bardraw/internal/winput"
)

type execResult struct {
	next    int
	err     error
	elapsed time.Duration
}

// executor runs plan.Run on its own OS thread with precise timing.
type executor struct {
	abort  atomic.Bool
	stroke atomic.Int64
	vertex atomic.Int64
	start  time.Time
	done   chan execResult
}

// startExecutor draws strokes[from:]. inj == nil means dry run.
func startExecutor(strokes [][]geom.Pt, from int, t plan.Timing, inj *winput.Injector) *executor {
	ex := &executor{start: time.Now(), done: make(chan execResult, 1)}
	ex.stroke.Store(int64(from))
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		d := &liveDriver{ex: ex, inj: inj, t0: time.Now()}
		next, err := from, error(nil)
		func() {
			defer func() {
				if p := recover(); p != nil {
					err = fmt.Errorf("internal error: %v", p)
					if inj != nil { // never leave the button or key stuck
						_ = inj.Button(false)
						_ = inj.Key(false)
					}
				}
			}()
			next, err = plan.Run(strokes, from, t, d)
		}()
		ex.done <- execResult{next: next, err: err, elapsed: time.Since(ex.start)}
	}()
	return ex
}

type liveDriver struct {
	ex  *executor
	inj *winput.Injector
	t0  time.Time
}

func (d *liveDriver) Now() time.Duration { return time.Since(d.t0) }

// Wait sleeps until the deadline with ~0.1 ms precision (coarse sleeps, then
// a short spin) and returns plan.ErrAborted as soon as the user interrupts.
func (d *liveDriver) Wait(until time.Duration) error {
	for {
		if d.ex.abort.Load() {
			return plan.ErrAborted
		}
		rem := until - d.Now()
		if rem <= 0 {
			return nil
		}
		if rem > 3*time.Millisecond {
			time.Sleep(min(rem-2*time.Millisecond, 5*time.Millisecond))
		} else {
			runtime.Gosched()
		}
	}
}

func (d *liveDriver) Pause(dur time.Duration) { time.Sleep(dur) }

func (d *liveDriver) Move(p geom.Pt) error {
	if d.inj == nil {
		return nil
	}
	return d.inj.Move(int(p.X), int(p.Y))
}

func (d *liveDriver) Button(down bool) error {
	if d.inj == nil {
		return nil
	}
	return d.inj.Button(down)
}

func (d *liveDriver) Key(down bool) error {
	if d.inj == nil {
		return nil
	}
	return d.inj.Key(down)
}

func (d *liveDriver) Progress(stroke, vertex int) {
	d.ex.stroke.Store(int64(stroke))
	d.ex.vertex.Store(int64(vertex))
}
