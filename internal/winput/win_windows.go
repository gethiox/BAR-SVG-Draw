package winput

import (
	"math"
	"syscall"
	"unsafe"
)

var (
	user32   = syscall.NewLazyDLL("user32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")
	winmm    = syscall.NewLazyDLL("winmm.dll")

	pSetWindowsHookExW   = user32.NewProc("SetWindowsHookExW")
	pUnhookWindowsHookEx = user32.NewProc("UnhookWindowsHookEx")
	pCallNextHookEx      = user32.NewProc("CallNextHookEx")
	pGetMessageW         = user32.NewProc("GetMessageW")
	pPostThreadMessageW  = user32.NewProc("PostThreadMessageW")
	pSendInput           = user32.NewProc("SendInput")
	pGetCursorPos        = user32.NewProc("GetCursorPos")
	pSetCursorPos        = user32.NewProc("SetCursorPos")
	pGetSystemMetrics    = user32.NewProc("GetSystemMetrics")
	pGetAsyncKeyState    = user32.NewProc("GetAsyncKeyState")
	pGetForegroundWindow = user32.NewProc("GetForegroundWindow")
	pGetWindowTextW      = user32.NewProc("GetWindowTextW")
	pMonitorFromPoint    = user32.NewProc("MonitorFromPoint")
	pGetMonitorInfoW     = user32.NewProc("GetMonitorInfoW")
	pGetWindowLongPtrW   = user32.NewProc("GetWindowLongPtrW")
	pSetWindowLongPtrW   = user32.NewProc("SetWindowLongPtrW")
	pSetWindowPos        = user32.NewProc("SetWindowPos")
	pShowWindow          = user32.NewProc("ShowWindow")
	pIsWindow            = user32.NewProc("IsWindow")
	pMapVirtualKeyW      = user32.NewProc("MapVirtualKeyW")
	pSetDpiCtx           = user32.NewProc("SetProcessDpiAwarenessContext")
	pMessageBoxW         = user32.NewProc("MessageBoxW")
	pSetProcessDPIAware  = user32.NewProc("SetProcessDPIAware")

	pGetCurrentThreadId = kernel32.NewProc("GetCurrentThreadId")
	pGetModuleHandleW   = kernel32.NewProc("GetModuleHandleW")

	pTimeBeginPeriod = winmm.NewProc("timeBeginPeriod")
	pTimeEndPeriod   = winmm.NewProc("timeEndPeriod")
)

// Magic is written into dwExtraInfo of every injected event so the hooks can
// tell our input apart from the user's (and from other injectors). It must
// not look like a pen/touch signature (0xFF5157xx), which SDL filters out.
const Magic = 0x0BA4D0A7

type point struct{ X, Y int32 }
type rect struct{ Left, Top, Right, Bottom int32 }

// EnableDPIAwareness makes all coordinates physical pixels. Call first.
func EnableDPIAwareness() {
	if pSetDpiCtx.Find() == nil {
		// DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2 = -4
		if r, _, _ := pSetDpiCtx.Call(^uintptr(3)); r != 0 {
			return
		}
	}
	if pSetProcessDPIAware.Find() == nil {
		pSetProcessDPIAware.Call()
	}
}

// HighResTimer requests 1 ms scheduler granularity (helps sleep precision).
func HighResTimer(on bool) {
	if pTimeBeginPeriod.Find() != nil {
		return
	}
	if on {
		pTimeBeginPeriod.Call(1)
	} else {
		pTimeEndPeriod.Call(1)
	}
}

// CursorPos returns the cursor position in screen pixels.
func CursorPos() (int, int) {
	var p point
	pGetCursorPos.Call(uintptr(unsafe.Pointer(&p)))
	return int(p.X), int(p.Y)
}

// VirtualScreen returns the bounding box of all monitors.
func VirtualScreen() (x, y, w, h int) {
	m := func(i uintptr) int { r, _, _ := pGetSystemMetrics.Call(i); return int(int32(r)) }
	return m(76), m(77), m(78), m(79)
}

// MonitorRect returns the rectangle of the monitor nearest to (x,y).
func MonitorRect(x, y int) (l, t, r, b int) {
	pt := uintptr(uint32(int32(x))) | uintptr(uint32(int32(y)))<<32
	hmon, _, _ := pMonitorFromPoint.Call(pt, 2 /*MONITOR_DEFAULTTONEAREST*/)
	var mi struct {
		Size    uint32
		Monitor rect
		Work    rect
		Flags   uint32
	}
	mi.Size = uint32(unsafe.Sizeof(mi))
	if ok, _, _ := pGetMonitorInfoW.Call(hmon, uintptr(unsafe.Pointer(&mi))); ok == 0 {
		vx, vy, vw, vh := VirtualScreen()
		return vx, vy, vx + vw, vy + vh
	}
	return int(mi.Monitor.Left), int(mi.Monitor.Top), int(mi.Monitor.Right), int(mi.Monitor.Bottom)
}

// Foreground returns the active window and its title.
func Foreground() (uintptr, string) {
	h, _, _ := pGetForegroundWindow.Call()
	if h == 0 {
		return 0, ""
	}
	buf := make([]uint16, 512)
	n, _, _ := pGetWindowTextW.Call(h, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	return h, syscall.UTF16ToString(buf[:n])
}

// IsWindow reports whether the handle still refers to a window.
func IsWindow(h uintptr) bool {
	r, _, _ := pIsWindow.Call(h)
	return r != 0
}

func keyDown(vk uintptr) bool {
	r, _, _ := pGetAsyncKeyState.Call(vk)
	return r&0x8000 != 0
}

// ShiftHeld reports whether a Shift key is physically held.
func ShiftHeld() bool { return keyDown(0x10) }

// ModifiersHeld reports whether Ctrl, Shift, Alt or Win is held.
func ModifiersHeld() bool {
	return keyDown(0x10) || keyDown(0x11) || keyDown(0x12) || keyDown(0x5B) || keyDown(0x5C)
}

// MakeOverlayWindow hides the window from the taskbar/Alt-Tab, stops it from
// ever being activated and puts it on top of other topmost windows.
func MakeOverlayWindow(hwnd uintptr) {
	if hwnd == 0 {
		return
	}
	const gwlExStyle = ^uintptr(19) // -20
	style, _, _ := pGetWindowLongPtrW.Call(hwnd, gwlExStyle)
	style |= 0x00000080 /*WS_EX_TOOLWINDOW*/ | 0x08000000 /*WS_EX_NOACTIVATE*/ | 0x00000008 /*WS_EX_TOPMOST*/
	style &^= 0x00040000                                                                    /*WS_EX_APPWINDOW*/
	pShowWindow.Call(hwnd, 0 /*SW_HIDE*/)
	pSetWindowLongPtrW.Call(hwnd, gwlExStyle, style)
	pShowWindow.Call(hwnd, 4 /*SW_SHOWNOACTIVATE*/)
	BringToTop(hwnd)
}

// BringToTop re-asserts the topmost z-order without activating the window.
func BringToTop(hwnd uintptr) {
	if hwnd == 0 {
		return
	}
	const swpFlags = 0x0001 /*NOSIZE*/ | 0x0002 /*NOMOVE*/ | 0x0010 /*NOACTIVATE*/ | 0x0040 /*SHOWWINDOW*/
	pSetWindowPos.Call(hwnd, ^uintptr(0) /*HWND_TOPMOST*/, 0, 0, 0, 0, swpFlags)
}

// ---------------------------------------------------------------------------
// input injection

type mouseInput struct {
	Dx, Dy      int32
	MouseData   uint32
	DwFlags     uint32
	Time        uint32
	DwExtraInfo uintptr
}

type keybdInput struct {
	WVk         uint16
	WScan       uint16
	DwFlags     uint32
	Time        uint32
	DwExtraInfo uintptr
}

// input mirrors INPUT; the union is sized by its largest member (MOUSEINPUT).
type input struct {
	Type uint32
	Mi   mouseInput
}

const (
	inputMouse    = 0
	inputKeyboard = 1

	mouseMove        = 0x0001
	mouseLeftDown    = 0x0002
	mouseLeftUp      = 0x0004
	mouseNoCoalesce  = 0x2000
	mouseVirtualDesk = 0x4000
	mouseAbsolute    = 0x8000

	keyExtended = 0x0001
	keyUp       = 0x0002
	keyScanCode = 0x0008
)

func sendInputs(in []input) error {
	if len(in) == 0 {
		return nil
	}
	n, _, err := pSendInput.Call(uintptr(len(in)), uintptr(unsafe.Pointer(&in[0])), unsafe.Sizeof(in[0]))
	if int(n) != len(in) {
		return err
	}
	return nil
}

// Injector sends synthetic mouse/keyboard input.
type Injector struct {
	DrawKeys       []KeySpec
	UseSetCursor   bool // move with SetCursorPos instead of SendInput
	vx, vy, vw, vh int
}

// NewInjector prepares an injector for the current monitor layout.
func NewInjector(drawKeys []KeySpec, useSetCursor bool) *Injector {
	in := &Injector{DrawKeys: append([]KeySpec(nil), drawKeys...), UseSetCursor: useSetCursor}
	in.vx, in.vy, in.vw, in.vh = VirtualScreen()
	for i, k := range in.DrawKeys {
		if k.Scan == 0 && k.VK != 0 {
			sc, _, _ := pMapVirtualKeyW.Call(uintptr(k.VK), 0 /*MAPVK_VK_TO_VSC*/)
			in.DrawKeys[i].Scan = uint16(sc)
		}
	}
	return in
}

// Move puts the cursor on (x,y) and makes Windows emit one mouse-move event.
func (in *Injector) Move(x, y int) error {
	if in.UseSetCursor || in.vw <= 0 || in.vh <= 0 {
		r, _, err := pSetCursorPos.Call(uintptr(int32(x)), uintptr(int32(y)))
		if r == 0 {
			return err
		}
		return nil
	}
	// normalised virtual-desktop coordinates; +0.4 keeps both truncating and
	// rounding implementations on the intended pixel
	nx := int32(math.Floor((float64(x-in.vx) + 0.4) * 65536 / float64(in.vw)))
	ny := int32(math.Floor((float64(y-in.vy) + 0.4) * 65536 / float64(in.vh)))
	ev := []input{{Type: inputMouse, Mi: mouseInput{Dx: nx, Dy: ny,
		DwFlags: mouseMove | mouseAbsolute | mouseVirtualDesk | mouseNoCoalesce, DwExtraInfo: Magic}}}
	if err := sendInputs(ev); err != nil {
		return err
	}
	if cx, cy := CursorPos(); cx != x || cy != y {
		pSetCursorPos.Call(uintptr(int32(x)), uintptr(int32(y))) // correct rounding quirks
	}
	return nil
}

// Button presses or releases the left mouse button at the current position.
func (in *Injector) Button(down bool) error {
	f := uint32(mouseLeftUp)
	if down {
		f = mouseLeftDown
	}
	return sendInputs([]input{{Type: inputMouse, Mi: mouseInput{DwFlags: f, DwExtraInfo: Magic}}})
}

// Key presses (in order) or releases (in reverse order) the draw key combo.
func (in *Injector) Key(down bool) error {
	evs := make([]input, 0, len(in.DrawKeys))
	for i := range in.DrawKeys {
		k := in.DrawKeys[i]
		if !down {
			k = in.DrawKeys[len(in.DrawKeys)-1-i]
		}
		var ev input
		ev.Type = inputKeyboard
		ki := (*keybdInput)(unsafe.Pointer(&ev.Mi))
		ki.DwExtraInfo = Magic
		if k.Scan != 0 {
			ki.WScan = k.Scan
			ki.DwFlags = keyScanCode
			if k.Extended {
				ki.DwFlags |= keyExtended
			}
		} else {
			ki.WVk = k.VK
		}
		if !down {
			ki.DwFlags |= keyUp
		}
		evs = append(evs, ev)
	}
	return sendInputs(evs)
}

// MessageBox shows a modal message (used for fatal start-up errors).
func MessageBox(title, text string) {
	t, _ := syscall.UTF16PtrFromString(title)
	m, _ := syscall.UTF16PtrFromString(text)
	pMessageBoxW.Call(0, uintptr(unsafe.Pointer(m)), uintptr(unsafe.Pointer(t)), 0x10 /*MB_ICONERROR*/)
}
