package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"bardraw/internal/plan"
	"bardraw/internal/svg"
)

// Library lists the SVG files and prepares their geometry in the background
// (centre-line extraction can take a moment for large drawings).
type Library struct {
	mu    sync.Mutex
	path  string // file or folder
	files []string
	idx   int
	cache map[string]*geoEntry
	docs  map[string]*docEntry
}

type docEntry struct {
	mtime time.Time
	doc   *svg.Doc
	err   error
}

type geoEntry struct {
	g       *plan.Geometry
	err     error
	loading bool
}

func NewLibrary() *Library {
	return &Library{cache: map[string]*geoEntry{}, docs: map[string]*docEntry{}}
}

// Refresh rescans the folder; preferred selects a file by base name.
func (l *Library) Refresh(path, preferred string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.path = path
	cur := ""
	if l.idx < len(l.files) {
		cur = l.files[l.idx]
	}
	l.files = nil
	if fi, err := os.Stat(path); err == nil && !fi.IsDir() {
		l.files = []string{path}
	} else if err == nil {
		entries, _ := os.ReadDir(path)
		for _, e := range entries {
			if !e.IsDir() && strings.EqualFold(filepath.Ext(e.Name()), ".svg") {
				l.files = append(l.files, filepath.Join(path, e.Name()))
			}
		}
		sort.Slice(l.files, func(a, b int) bool { return strings.ToLower(l.files[a]) < strings.ToLower(l.files[b]) })
	}
	l.idx = 0
	for i, f := range l.files {
		if (cur != "" && f == cur) || (cur == "" && preferred != "" && filepath.Base(f) == preferred) {
			l.idx = i
		}
	}
	// drop cached geometry of files that changed on disk
	for _, f := range l.files {
		fi, err := os.Stat(f)
		if d, ok := l.docs[f]; ok && (err != nil || !fi.ModTime().Equal(d.mtime)) {
			delete(l.docs, f)
			for k := range l.cache {
				if strings.HasPrefix(k, f+"|") {
					delete(l.cache, k)
				}
			}
		}
	}
}

// Current returns the selected file, its position and the file count.
func (l *Library) Current() (string, int, int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.files) == 0 {
		return "", 0, 0
	}
	return l.files[l.idx], l.idx, len(l.files)
}

// Step moves the selection by d files.
func (l *Library) Step(d int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if n := len(l.files); n > 0 {
		l.idx = ((l.idx+d)%n + n) % n
	}
}

// Geometry returns the prepared geometry or starts preparing it.
// loading is true while the background job runs.
func (l *Library) Geometry(file string, o plan.PrepOptions) (g *plan.Geometry, err error, loading bool) {
	if file == "" {
		return nil, nil, false
	}
	key := fmt.Sprintf("%s|%+v", file, o) // every option affects the geometry
	l.mu.Lock()
	e, ok := l.cache[key]
	if !ok {
		e = &geoEntry{loading: true}
		l.cache[key] = e
		go l.prepare(file, key, o)
	}
	g, err, loading = e.g, e.err, e.loading
	l.mu.Unlock()
	return
}

func (l *Library) prepare(file, key string, o plan.PrepOptions) {
	l.mu.Lock()
	d := l.docs[file]
	l.mu.Unlock()
	if d == nil {
		d = &docEntry{}
		if fi, err := os.Stat(file); err == nil {
			d.mtime = fi.ModTime()
		}
		d.doc, d.err = svg.ParseFile(file)
		l.mu.Lock()
		l.docs[file] = d
		l.mu.Unlock()
	}
	var g *plan.Geometry
	if d.err == nil {
		g = plan.Prepare(d.doc, o)
	}
	l.mu.Lock()
	if e := l.cache[key]; e != nil {
		e.g, e.err, e.loading = g, d.err, false
	}
	l.mu.Unlock()
}

func boolStr(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var b []byte
	for v > 0 {
		b = append([]byte{byte('0' + v%10)}, b...)
		v /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}

// ---------------------------------------------------------------------------
// planner

// PlanReq describes a plan computation.
type PlanReq struct {
	G         *plan.Geometry
	W, H      int
	P         plan.Params
	Budget    time.Duration
	AutoLower bool // lower quality until the ETA fits Budget
	Timing    plan.Timing
}

// PlanRes is a computed plan with its ETA.
type PlanRes struct {
	Req          PlanReq
	Plan         *plan.Plan
	ETA          time.Duration
	AutoReduced  bool // quality was lowered to fit the time budget
	RequestedLvl int
}

// OverBudget reports whether the plan takes longer than the time budget.
func (r *PlanRes) OverBudget() bool { return r.Req.Budget > 0 && r.ETA > r.Req.Budget }

// ComputePlan builds a plan, lowering the quality level while the ETA exceeds
// the budget.
func ComputePlan(r PlanReq) *PlanRes {
	res := &PlanRes{Req: r, RequestedLvl: r.P.Level}
	p := r.P
	for {
		pl := plan.Build(r.G, float64(r.W), float64(r.H), p)
		eta := plan.Estimate(pl.Strokes, 0, r.Timing)
		res.Plan, res.ETA = pl, eta
		if !r.AutoLower || r.Budget <= 0 || eta <= r.Budget || p.Level <= 1 {
			break
		}
		p.Level--
		res.AutoReduced = true
	}
	return res
}

// Planner computes plans on a worker goroutine; newer requests replace older
// pending ones so the preview stays responsive while dragging.
type Planner struct {
	mu      sync.Mutex
	pending *PlanReq
	latest  *PlanRes
	wake    chan struct{}
}

func NewPlanner() *Planner {
	p := &Planner{wake: make(chan struct{}, 1)}
	go p.loop()
	return p
}

func (p *Planner) loop() {
	for range p.wake {
		for {
			p.mu.Lock()
			r := p.pending
			p.pending = nil
			p.mu.Unlock()
			if r == nil {
				break
			}
			res := ComputePlan(*r)
			p.mu.Lock()
			p.latest = res
			p.mu.Unlock()
		}
	}
}

// Request asks for a plan unless the latest result already matches.
func (p *Planner) Request(r PlanReq) {
	p.mu.Lock()
	if p.latest != nil && p.latest.Req == r {
		p.mu.Unlock()
		return
	}
	if p.pending != nil && *p.pending == r {
		p.mu.Unlock()
		return
	}
	p.pending = &r
	p.mu.Unlock()
	select {
	case p.wake <- struct{}{}:
	default:
	}
}

// Latest returns the newest finished plan (may be for an older request).
func (p *Planner) Latest() *PlanRes {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.latest
}
