package winput

import (
	"sync"
	"syscall"
	"unsafe"
)

var (
	gdi32   = syscall.NewLazyDLL("gdi32.dll")
	shell32 = syscall.NewLazyDLL("shell32.dll")

	pRegisterClassExW       = user32.NewProc("RegisterClassExW")
	pCreateWindowExW        = user32.NewProc("CreateWindowExW")
	pDefWindowProcW         = user32.NewProc("DefWindowProcW")
	pCreatePopupMenu        = user32.NewProc("CreatePopupMenu")
	pAppendMenuW            = user32.NewProc("AppendMenuW")
	pTrackPopupMenu         = user32.NewProc("TrackPopupMenu")
	pDestroyMenu            = user32.NewProc("DestroyMenu")
	pSetForegroundWindow    = user32.NewProc("SetForegroundWindow")
	pPostMessageW           = user32.NewProc("PostMessageW")
	pRegisterWindowMessageW = user32.NewProc("RegisterWindowMessageW")
	pCreateIconIndirect     = user32.NewProc("CreateIconIndirect")
	pTranslateMessage       = user32.NewProc("TranslateMessage")
	pDispatchMessageW       = user32.NewProc("DispatchMessageW")
	pCreateDIBSection       = gdi32.NewProc("CreateDIBSection")
	pCreateBitmap           = gdi32.NewProc("CreateBitmap")
	pDeleteObject           = gdi32.NewProc("DeleteObject")
	pShellNotifyIconW       = shell32.NewProc("Shell_NotifyIconW")
	pShellExecuteW          = shell32.NewProc("ShellExecuteW")
)

// TrayState selects the tray icon colour.
type TrayState int

const (
	TrayIdle   TrayState = iota // amber
	TraySelect                  // cyan: waiting for a box
	TrayDraw                    // red: sending input
)

type notifyIconData struct {
	CbSize           uint32
	HWnd             uintptr
	UID              uint32
	UFlags           uint32
	UCallbackMessage uint32
	HIcon            uintptr
	SzTip            [128]uint16
	DwState          uint32
	DwStateMask      uint32
	SzInfo           [256]uint16
	UVersion         uint32
	SzInfoTitle      [64]uint16
	DwInfoFlags      uint32
	GuidItem         [16]byte
	HBalloonIcon     uintptr
}

const (
	trayCallbackMsg = 0x8000 + 1 // WM_APP+1
	nifMessage      = 0x01
	nifIcon         = 0x02
	nifTip          = 0x04
	nifInfo         = 0x10
	nimAdd          = 0
	nimModify       = 1
	nimDelete       = 2
)

// Tray is the notification-area icon with its menu.
type Tray struct {
	h              *Hooks
	hwnd           uintptr
	icons          [3]uintptr
	taskbarCreated uintptr

	mu    sync.Mutex
	state TrayState
	tip   string
	title string
	hint  string
	dry   bool
	added bool
}

var (
	activeTray   *Tray
	trayCallback = syscall.NewCallback(trayProc)
)

func utf16Into(dst []uint16, s string) {
	u, _ := syscall.UTF16FromString(s)
	if len(u) > len(dst) {
		u = u[:len(dst)]
		u[len(u)-1] = 0
	}
	copy(dst, u)
}

// newTray must run on the hook thread (it owns the window).
func newTray(h *Hooks, tip string) (*Tray, error) {
	t := &Tray{h: h, tip: tip, title: "bardraw is running"}
	activeTray = t
	hinst, _, _ := pGetModuleHandleW.Call(0)
	className, _ := syscall.UTF16PtrFromString("bardrawTrayWindow")
	wc := struct {
		CbSize        uint32
		Style         uint32
		LpfnWndProc   uintptr
		CbClsExtra    int32
		CbWndExtra    int32
		HInstance     uintptr
		HIcon         uintptr
		HCursor       uintptr
		HbrBackground uintptr
		LpszMenuName  *uint16
		LpszClassName *uint16
		HIconSm       uintptr
	}{LpfnWndProc: trayCallback, HInstance: hinst, LpszClassName: className}
	wc.CbSize = uint32(unsafe.Sizeof(wc))
	pRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))
	title, _ := syscall.UTF16PtrFromString("bardraw")
	hwnd, _, err := pCreateWindowExW.Call(0, uintptr(unsafe.Pointer(className)), uintptr(unsafe.Pointer(title)),
		0, 0, 0, 0, 0, 0, 0, hinst, 0)
	if hwnd == 0 {
		return nil, err
	}
	t.hwnd = hwnd
	tc, _ := syscall.UTF16PtrFromString("TaskbarCreated")
	t.taskbarCreated, _, _ = pRegisterWindowMessageW.Call(uintptr(unsafe.Pointer(tc)))
	size := int(getMetric(49)) // SM_CXSMICON
	if size < 16 {
		size = 16
	}
	size *= 2 // render larger, the shell scales down nicely
	t.icons[TrayIdle] = makeIcon(size, [3]uint8{255, 190, 40}, [3]uint8{26, 29, 36})
	t.icons[TraySelect] = makeIcon(size, [3]uint8{40, 200, 230}, [3]uint8{16, 30, 40})
	t.icons[TrayDraw] = makeIcon(size, [3]uint8{229, 72, 77}, [3]uint8{255, 255, 255})
	t.apply(nimAdd, "", "")
	return t, nil
}

func getMetric(i uintptr) int32 {
	r, _, _ := pGetSystemMetrics.Call(i)
	return int32(r)
}

func (t *Tray) apply(op uintptr, infoTitle, info string) {
	t.mu.Lock()
	nid := notifyIconData{HWnd: t.hwnd, UID: 1, UFlags: nifMessage | nifIcon | nifTip, UCallbackMessage: trayCallbackMsg}
	nid.CbSize = uint32(unsafe.Sizeof(nid))
	nid.HIcon = t.icons[t.state]
	utf16Into(nid.SzTip[:], t.tip)
	if info != "" {
		nid.UFlags |= nifInfo
		utf16Into(nid.SzInfo[:], info)
		utf16Into(nid.SzInfoTitle[:], infoTitle)
		nid.DwInfoFlags = 0x01 | 0x10 // NIIF_INFO | NIIF_NOSOUND
	}
	t.mu.Unlock()
	r, _, _ := pShellNotifyIconW.Call(op, uintptr(unsafe.Pointer(&nid)))
	if op == nimAdd && r != 0 {
		t.added = true
	}
}

// Update changes the icon colour and tooltip (only when they changed).
func (t *Tray) Update(state TrayState, tip string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	changed := t.state != state || t.tip != tip
	t.state, t.tip = state, tip
	t.mu.Unlock()
	if changed {
		t.apply(nimModify, "", "")
	}
}

// SetMenu sets the informational lines and the dry-run check mark.
func (t *Tray) SetMenu(title, hint string, dry bool) {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.title, t.hint, t.dry = title, hint, dry
	t.mu.Unlock()
}

// Notify shows a Windows notification from the tray icon.
func (t *Tray) Notify(title, text string) {
	if t != nil {
		t.apply(nimModify, title, text)
	}
}

// Remove deletes the tray icon.
func (t *Tray) Remove() {
	if t == nil {
		return
	}
	nid := notifyIconData{HWnd: t.hwnd, UID: 1}
	nid.CbSize = uint32(unsafe.Sizeof(nid))
	pShellNotifyIconW.Call(nimDelete, uintptr(unsafe.Pointer(&nid)))
}

func trayProc(hwnd, msg, wParam, lParam uintptr) uintptr {
	t := activeTray
	if t != nil && msg == trayCallbackMsg {
		switch lParam & 0xFFFF {
		case 0x0202, 0x0205, 0x007B: // WM_LBUTTONUP, WM_RBUTTONUP, WM_CONTEXTMENU
			t.showMenu()
		}
		return 0
	}
	if t != nil && t.taskbarCreated != 0 && msg == t.taskbarCreated {
		t.apply(nimAdd, "", "") // Explorer restarted
		return 0
	}
	r, _, _ := pDefWindowProcW.Call(hwnd, msg, wParam, lParam)
	return r
}

func (t *Tray) showMenu() {
	const (
		mfString    = 0x0000
		mfGrayed    = 0x0001
		mfChecked   = 0x0008
		mfSeparator = 0x0800
	)
	t.mu.Lock()
	title, hint, dry := t.title, t.hint, t.dry
	t.mu.Unlock()
	m, _, _ := pCreatePopupMenu.Call()
	if m == 0 {
		return
	}
	defer pDestroyMenu.Call(m)
	add := func(flags, id uintptr, text string) {
		var p uintptr
		if text != "" {
			s, _ := syscall.UTF16PtrFromString(text)
			p = uintptr(unsafe.Pointer(s))
		}
		pAppendMenuW.Call(m, flags, id, p)
	}
	add(mfString|mfGrayed, 1, title)
	if hint != "" {
		add(mfString|mfGrayed, 2, hint)
	}
	add(mfSeparator, 0, "")
	add(mfString, 11, "Open drawings folder")
	add(mfString, 12, "Edit settings")
	dryFlags := uintptr(mfString)
	if dry {
		dryFlags |= mfChecked
	}
	add(dryFlags, 13, "Dry run (simulate, send no input)")
	add(mfSeparator, 0, "")
	add(mfString, 15, "Quit bardraw")

	x, y := CursorPos()
	pSetForegroundWindow.Call(t.hwnd)
	cmd, _, _ := pTrackPopupMenu.Call(m, 0x0100|0x0080|0x0002, // TPM_RETURNCMD|TPM_NONOTIFY|TPM_RIGHTBUTTON
		uintptr(int32(x)), uintptr(int32(y)), 0, t.hwnd, 0)
	pPostMessageW.Call(t.hwnd, 0, 0, 0) // WM_NULL, required after TrackPopupMenu
	names := map[uintptr]string{11: "folder", 12: "settings", 13: "dry", 15: "quit"}
	if name, ok := names[cmd]; ok {
		t.h.emit(Event{Kind: EvTray, Name: name})
	}
}

// makeIcon turns the rendered icon pixels into an HICON.
func makeIcon(size int, bg, ink [3]uint8) uintptr {
	pix := IconPixels(size, bg, ink)
	bmi := struct {
		Size          uint32
		Width, Height int32
		Planes, Bits  uint16
		Compression   uint32
		SizeImage     uint32
		XPels, YPels  int32
		ClrUsed       uint32
		ClrImportant  uint32
	}{Width: int32(size), Height: -int32(size), Planes: 1, Bits: 32}
	bmi.Size = uint32(unsafe.Sizeof(bmi))
	var bits unsafe.Pointer
	color, _, _ := pCreateDIBSection.Call(0, uintptr(unsafe.Pointer(&bmi)), 0, uintptr(unsafe.Pointer(&bits)), 0, 0)
	if color == 0 || bits == nil {
		return 0
	}
	copy(unsafe.Slice((*byte)(bits), len(pix)), pix)
	maskBytes := make([]byte, ((size+15)/16)*2*size) // all zero: alpha decides
	mask, _, _ := pCreateBitmap.Call(uintptr(size), uintptr(size), 1, 1, uintptr(unsafe.Pointer(&maskBytes[0])))
	ii := struct {
		FIcon    int32
		XHotspot uint32
		YHotspot uint32
		HbmMask  uintptr
		HbmColor uintptr
	}{FIcon: 1, HbmMask: mask, HbmColor: color}
	icon, _, _ := pCreateIconIndirect.Call(uintptr(unsafe.Pointer(&ii)))
	pDeleteObject.Call(color)
	pDeleteObject.Call(mask)
	return icon
}

// OpenPath opens a folder or file with its default application
// (files without an association open in Notepad).
func OpenPath(path string) {
	verb, _ := syscall.UTF16PtrFromString("open")
	p, _ := syscall.UTF16PtrFromString(path)
	r, _, _ := pShellExecuteW.Call(0, uintptr(unsafe.Pointer(verb)), uintptr(unsafe.Pointer(p)), 0, 0, 1)
	if r <= 32 {
		np, _ := syscall.UTF16PtrFromString("notepad.exe")
		pShellExecuteW.Call(0, uintptr(unsafe.Pointer(verb)), uintptr(unsafe.Pointer(np)), uintptr(unsafe.Pointer(p)), 0, 1)
	}
}
