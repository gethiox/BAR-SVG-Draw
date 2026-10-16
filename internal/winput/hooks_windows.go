package winput

import (
	"errors"
	"math"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"
)

// Mode decides what the hooks do with user input.
type Mode int32

const (
	// ModeIdle: only hotkeys are intercepted.
	ModeIdle Mode = iota
	// ModeSelect: left button / wheel / a few keys drive the box selection and
	// are swallowed so the game never sees them.
	ModeSelect
	// ModeDraw: any real key or button press aborts drawing; real mouse moves
	// are blocked so they cannot add stray lines.
	ModeDraw
	// ModePaused: only Space (continue) and Esc (discard) are intercepted;
	// the mouse and all other keys reach the game.
	ModePaused
)

// EventKind enumerates hook events.
type EventKind int

const (
	EvHotkey  EventKind = iota // Name = hotkey name
	EvSelDown                  // left press at X,Y (select mode)
	EvSelUp                    // left release at X,Y (select mode)
	EvCancel                   // right click / Esc in select mode
	EvWheel                    // wheel (select mode), Delta = ±1 per notch
	EvKey                      // select-mode key, Name = action
	EvAbort                    // real input while drawing
	EvTray                     // tray menu command, Name = folder, settings, dry or quit
)

// Event is delivered on Hooks.Events.
type Event struct {
	Kind  EventKind
	Name  string
	X, Y  int
	Delta int
	Shift bool
	Ctrl  bool
	Alt   bool
}

// Hooks owns the low-level keyboard and mouse hooks.
type Hooks struct {
	Events chan Event

	mode       atomic.Int32
	abortSent  atomic.Bool
	hotkeys    atomic.Pointer[[]Hotkey]
	abortMove  atomic.Uint64 // float64 bits
	resetShake atomic.Bool

	heldMu   sync.Mutex
	heldKeys map[uint32]time.Time // real keys that are down (last down/repeat time)

	// only touched on the hook thread
	threadID  uint32
	swallowUp map[uint32]bool
	btnSwal   map[uintptr]bool
	shake     shakeMeter

	kbHook, msHook uintptr
	tray           *Tray
}

var (
	active     atomic.Pointer[Hooks]
	kbCallback = syscall.NewCallback(kbProc)
	msCallback = syscall.NewCallback(msProc)
)

// StartHooks installs the hooks on a dedicated thread with its own message
// loop. A non-empty trayTip also creates the notification-area icon there.
func StartHooks(hotkeys []Hotkey, abortMovePx float64, trayTip string) (*Hooks, error) {
	h := &Hooks{
		Events:    make(chan Event, 256),
		heldKeys:  map[uint32]time.Time{},
		swallowUp: map[uint32]bool{},
		btnSwal:   map[uintptr]bool{},
	}
	h.Configure(hotkeys, abortMovePx)
	if !active.CompareAndSwap(nil, h) {
		return nil, errors.New("hooks already running")
	}
	ready := make(chan error, 1)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		tid, _, _ := pGetCurrentThreadId.Call()
		h.threadID = uint32(tid)
		hmod, _, _ := pGetModuleHandleW.Call(0)
		var err error
		h.kbHook, _, err = pSetWindowsHookExW.Call(13 /*WH_KEYBOARD_LL*/, kbCallback, hmod, 0)
		if h.kbHook == 0 {
			ready <- err
			return
		}
		h.msHook, _, err = pSetWindowsHookExW.Call(14 /*WH_MOUSE_LL*/, msCallback, hmod, 0)
		if h.msHook == 0 {
			pUnhookWindowsHookEx.Call(h.kbHook)
			ready <- err
			return
		}
		if trayTip != "" {
			h.tray, _ = newTray(h, trayTip) // a missing tray icon is not fatal
		}
		ready <- nil
		var msg struct {
			Hwnd    uintptr
			Message uint32
			WParam  uintptr
			LParam  uintptr
			Time    uint32
			Pt      point
			Private uint32
		}
		for {
			r, _, _ := pGetMessageW.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
			if int32(r) <= 0 {
				break
			}
			pTranslateMessage.Call(uintptr(unsafe.Pointer(&msg)))
			pDispatchMessageW.Call(uintptr(unsafe.Pointer(&msg)))
		}
		pUnhookWindowsHookEx.Call(h.msHook)
		pUnhookWindowsHookEx.Call(h.kbHook)
	}()
	if err := <-ready; err != nil {
		active.Store(nil)
		return nil, err
	}
	return h, nil
}

// Tray returns the tray icon (nil if it could not be created).
func (h *Hooks) Tray() *Tray { return h.tray }

// Stop removes the hooks and the tray icon.
func (h *Hooks) Stop() {
	h.tray.Remove()
	if h.threadID != 0 {
		pPostThreadMessageW.Call(uintptr(h.threadID), 0x0012 /*WM_QUIT*/, 0, 0)
	}
	active.CompareAndSwap(h, nil)
}

// SetMode switches the interception mode.
func (h *Hooks) SetMode(m Mode) {
	h.abortSent.Store(false)
	h.mode.Store(int32(m))
	h.resetShake.Store(true)
}

// KeysHeld reports whether any real key is currently held down. Keys whose
// last down/auto-repeat event is older than 1.2 s are treated as released, so
// a missed key-up can never block forever.
func (h *Hooks) KeysHeld() bool {
	h.heldMu.Lock()
	defer h.heldMu.Unlock()
	for vk, t := range h.heldKeys {
		if time.Since(t) < 1200*time.Millisecond {
			return true
		}
		delete(h.heldKeys, vk)
	}
	return false
}

// keyEvent records a real key transition and reports whether a down event
// is an auto-repeat.
func (h *Hooks) keyEvent(vk uint32, down bool) (repeat bool) {
	h.heldMu.Lock()
	defer h.heldMu.Unlock()
	if !down {
		delete(h.heldKeys, vk)
		return false
	}
	_, repeat = h.heldKeys[vk]
	h.heldKeys[vk] = time.Now()
	return repeat
}

// Configure replaces the hotkeys and the mouse-shake stop distance
// (0 = mouse movement never stops drawing).
func (h *Hooks) Configure(hotkeys []Hotkey, abortMovePx float64) {
	hk := append([]Hotkey(nil), hotkeys...)
	h.hotkeys.Store(&hk)
	h.abortMove.Store(math.Float64bits(abortMovePx))
}

// Mode returns the current mode.
func (h *Hooks) Mode() Mode { return Mode(h.mode.Load()) }

func (h *Hooks) emit(e Event) {
	select {
	case h.Events <- e:
	default: // never block the hook thread
	}
}

func (h *Hooks) abort() {
	if h.abortSent.CompareAndSwap(false, true) {
		h.emit(Event{Kind: EvAbort})
	}
}

func (h *Hooks) modsMatch(hk Hotkey) bool {
	return keyDown(0x11) == hk.Ctrl && keyDown(0x10) == hk.Shift && keyDown(0x12) == hk.Alt &&
		(keyDown(0x5B) || keyDown(0x5C)) == hk.Win
}

// selection-mode keys
func selectAction(vk uint32) string {
	switch vk {
	case 0x1B: // Esc
		return "cancel"
	case 0x09: // Tab
		return "next"
	case 0x21, 0x6B, 0xBB: // PageUp, numpad +, =/+
		return "q+"
	case 0x22, 0x6D, 0xBD: // PageDown, numpad -, -
		return "q-"
	case 'R':
		return "rotate"
	case 'F':
		return "flip"
	case 'M':
		return "mode"
	case 'V':
		return "verts"
	case 'B':
		return "budget"
	case 'S':
		return "stretch"
	case 0x0D: // Enter
		return "repeat"
	case 0x20: // Space
		return "start"
	}
	return ""
}

type kbdll struct {
	VkCode      uint32
	ScanCode    uint32
	Flags       uint32
	Time        uint32
	DwExtraInfo uintptr
}

type msll struct {
	Pt          point
	MouseData   uint32
	Flags       uint32
	Time        uint32
	DwExtraInfo uintptr
}

func callNext(nCode, wParam, lParam uintptr) uintptr {
	r, _, _ := pCallNextHookEx.Call(0, nCode, wParam, lParam)
	return r
}

func kbProc(nCode, wParam, lParam uintptr) uintptr {
	h := active.Load()
	if int32(nCode) < 0 || h == nil {
		return callNext(nCode, wParam, lParam)
	}
	k := *(**kbdll)(unsafe.Pointer(&lParam)) // lParam points to a KBDLLHOOKSTRUCT
	if k.DwExtraInfo == Magic {
		return callNext(nCode, wParam, lParam)
	}
	vk := k.VkCode
	down := wParam == 0x0100 || wParam == 0x0104 // WM_KEYDOWN / WM_SYSKEYDOWN
	repeat := h.keyEvent(vk, down)
	if !down {
		if h.swallowUp[vk] {
			delete(h.swallowUp, vk)
			return 1
		}
		return callNext(nCode, wParam, lParam)
	}
	mode := Mode(h.mode.Load())

	if mode == ModeDraw { // any real key press stops drawing (hotkeys included)
		if !repeat {
			h.abort()
		}
		h.swallowUp[vk] = true
		return 1
	}
	for _, hk := range *h.hotkeys.Load() {
		if hk.VK == vk && h.modsMatch(hk) {
			if !repeat {
				h.emit(Event{Kind: EvHotkey, Name: hk.Name})
			}
			h.swallowUp[vk] = true
			return 1
		}
	}
	switch mode {
	case ModePaused:
		if vk == 0x20 || vk == 0x1B {
			if !repeat {
				act := "start"
				if vk == 0x1B {
					act = "cancel"
				}
				h.emit(Event{Kind: EvKey, Name: act})
			}
			h.swallowUp[vk] = true
			return 1
		}
	case ModeSelect:
		if act := selectAction(vk); act != "" {
			if !repeat || act == "q+" || act == "q-" {
				h.emit(Event{Kind: EvKey, Name: act, Shift: keyDown(0x10)})
			}
			h.swallowUp[vk] = true
			return 1
		}
	}
	return callNext(nCode, wParam, lParam)
}

func msProc(nCode, wParam, lParam uintptr) uintptr {
	h := active.Load()
	if int32(nCode) < 0 || h == nil {
		return callNext(nCode, wParam, lParam)
	}
	m := *(**msll)(unsafe.Pointer(&lParam)) // lParam points to an MSLLHOOKSTRUCT
	if m.DwExtraInfo == Magic {
		return callNext(nCode, wParam, lParam)
	}
	const (
		wmMove   = 0x0200
		wmLDown  = 0x0201
		wmLUp    = 0x0202
		wmRDown  = 0x0204
		wmRUp    = 0x0205
		wmMDown  = 0x0207
		wmMUp    = 0x0208
		wmWheel  = 0x020A
		wmXDown  = 0x020B
		wmXUp    = 0x020C
		wmHWheel = 0x020E
	)
	x, y := int(m.Pt.X), int(m.Pt.Y)
	switch Mode(h.mode.Load()) {
	case ModeSelect:
		switch wParam {
		case wmLDown:
			h.emit(Event{Kind: EvSelDown, X: x, Y: y, Shift: keyDown(0x10)})
			h.btnSwal[wmLUp] = true
			return 1
		case wmLUp:
			// only releases of presses we swallowed; a drag that started in
			// the game before the hotkey must still finish in the game
			if h.btnSwal[wmLUp] {
				delete(h.btnSwal, wmLUp)
				h.emit(Event{Kind: EvSelUp, X: x, Y: y, Shift: keyDown(0x10)})
				return 1
			}
		case wmRDown:
			h.emit(Event{Kind: EvCancel})
			h.btnSwal[wmRUp] = true
			return 1
		case wmRUp:
			if h.btnSwal[wmRUp] {
				delete(h.btnSwal, wmRUp)
				return 1
			}
		case wmWheel:
			d := int(int16(m.MouseData >> 16))
			step := 1
			if d < 0 {
				step = -1
			}
			h.emit(Event{Kind: EvWheel, Delta: step, Shift: keyDown(0x10), Ctrl: keyDown(0x11), Alt: keyDown(0x12)})
			return 1
		}
	case ModeDraw:
		switch wParam {
		case wmMove:
			h.measureShake(m.Pt)
			return 1 // never let the real mouse move the pen
		case wmLDown, wmRDown, wmMDown, wmXDown, wmWheel, wmHWheel:
			h.abort()
			switch wParam {
			case wmRDown:
				h.btnSwal[wmRUp] = true
			case wmMDown:
				h.btnSwal[wmMUp] = true
			case wmXDown:
				h.btnSwal[wmXUp] = true
			}
			return 1
		case wmLUp:
			delete(h.btnSwal, wmLUp)
			return 1 // the left button belongs to the pen while drawing
		case wmRUp, wmMUp, wmXUp:
			if h.btnSwal[wParam] {
				delete(h.btnSwal, wParam)
				return 1
			}
		}
	default:
		// release events for buttons swallowed in another mode
		if h.btnSwal[wParam] {
			delete(h.btnSwal, wParam)
			return 1
		}
	}
	return callNext(nCode, wParam, lParam)
}

// measureShake feeds a real mouse-move event into the shake meter.
func (h *Hooks) measureShake(p point) {
	if h.resetShake.Swap(false) {
		h.shake = shakeMeter{}
	}
	if h.shake.add(float64(p.X), float64(p.Y), time.Now(), math.Float64frombits(h.abortMove.Load())) {
		h.abort()
	}
}
