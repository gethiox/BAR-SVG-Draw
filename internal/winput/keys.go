// Package winput wraps the Windows APIs the overlay needs: low-level
// keyboard/mouse hooks (hotkeys, box selection, interrupt detection),
// SendInput injection and a few window/monitor helpers.
//
// Only keys.go is platform independent (so hotkey parsing can be tested
// anywhere); everything else is in *_windows.go files.
package winput

import (
	"fmt"
	"strconv"
	"strings"
)

// Hotkey is a key with an exact modifier combination.
type Hotkey struct {
	Name                  string
	VK                    uint32
	Ctrl, Shift, Alt, Win bool
}

func (h Hotkey) String() string {
	var parts []string
	if h.Ctrl {
		parts = append(parts, "Ctrl")
	}
	if h.Shift {
		parts = append(parts, "Shift")
	}
	if h.Alt {
		parts = append(parts, "Alt")
	}
	if h.Win {
		parts = append(parts, "Win")
	}
	parts = append(parts, keyLabel(h.VK))
	return strings.Join(parts, "+")
}

var vkNames = map[string]uint32{
	"home": 0x24, "end": 0x23, "insert": 0x2D, "ins": 0x2D, "delete": 0x2E, "del": 0x2E,
	"pageup": 0x21, "pgup": 0x21, "pagedown": 0x22, "pgdn": 0x22,
	"up": 0x26, "down": 0x28, "left": 0x25, "right": 0x27,
	"space": 0x20, "enter": 0x0D, "return": 0x0D, "tab": 0x09, "escape": 0x1B, "esc": 0x1B,
	"backspace": 0x08, "pause": 0x13, "scrolllock": 0x91, "capslock": 0x14, "printscreen": 0x2C,
	"grave": 0xC0, "`": 0xC0, "tilde": 0xC0, "~": 0xC0, "minus": 0xBD, "-": 0xBD,
	"equals": 0xBB, "=": 0xBB, "backslash": 0xDC, "\\": 0xDC, "semicolon": 0xBA,
	"comma": 0xBC, "period": 0xBE, "slash": 0xBF, "apostrophe": 0xDE,
	"lbracket": 0xDB, "rbracket": 0xDD,
}

func init() {
	for c := 'a'; c <= 'z'; c++ {
		vkNames[string(c)] = uint32(c - 'a' + 'A')
	}
	for c := '0'; c <= '9'; c++ {
		vkNames[string(c)] = uint32(c)
		vkNames["numpad"+string(c)] = uint32(0x60 + c - '0')
	}
	for i := 1; i <= 24; i++ {
		vkNames["f"+strconv.Itoa(i)] = uint32(0x70 + i - 1)
	}
}

func keyLabel(vk uint32) string {
	best := ""
	for name, v := range vkNames {
		if v != vk || len(name) > 9 {
			continue
		}
		if best == "" || len(name) > len(best) || (len(name) == len(best) && name < best) {
			best = name
		}
	}
	if best == "" {
		return fmt.Sprintf("VK_%02X", vk)
	}
	if len(best) == 1 {
		return strings.ToUpper(best)
	}
	return strings.ToUpper(best[:1]) + best[1:]
}

// ParseHotkey parses strings like "ctrl+home", "ctrl+shift+end", "f8", "alt+d".
func ParseHotkey(name, s string) (Hotkey, error) {
	h := Hotkey{Name: name}
	parts := strings.Split(strings.ToLower(strings.ReplaceAll(s, " ", "")), "+")
	// allow "ctrl++" style for the plus key? not needed; keep it simple
	for i, p := range parts {
		last := i == len(parts)-1
		switch p {
		case "ctrl", "control":
			h.Ctrl = true
		case "shift":
			h.Shift = true
		case "alt":
			h.Alt = true
		case "win", "super", "meta":
			h.Win = true
		default:
			if !last {
				return h, fmt.Errorf("hotkey %q: unknown modifier %q", s, p)
			}
			if strings.HasPrefix(p, "vk:") {
				v, err := strconv.ParseUint(strings.TrimPrefix(p, "vk:"), 0, 32)
				if err != nil {
					return h, fmt.Errorf("hotkey %q: %v", s, err)
				}
				h.VK = uint32(v)
				continue
			}
			vk, ok := vkNames[p]
			if !ok {
				return h, fmt.Errorf("hotkey %q: unknown key %q", s, p)
			}
			h.VK = vk
		}
	}
	if h.VK == 0 {
		return h, fmt.Errorf("hotkey %q: no key", s)
	}
	return h, nil
}

// KeySpec identifies the key that is injected to hold BAR's "drawinmap" action.
type KeySpec struct {
	Scan     uint16 // hardware scan code (preferred: layout independent)
	Extended bool
	VK       uint16 // used when Scan == 0
}

// ParseKeyCombo parses one or more key specs joined by "+", pressed in order
// and released in reverse, e.g. "sc:0x39+sc:0x10" = hold Space, then Q (BAR's
// "Grid (60% keyboard)" profile binds drawinmap to meta+Q with Space as meta).
func ParseKeyCombo(s string) ([]KeySpec, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return []KeySpec{{Scan: 0x29}}, nil
	}
	var out []KeySpec
	for _, part := range strings.Split(s, "+") {
		k, err := ParseKeySpec(part)
		if err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, nil
}

// ParseKeySpec accepts "sc:0x29" (scan code, the key left of 1 — `/~ on US,
// default), "sc:0xe01d" (extended), "vk:0xC0", or a key name ("grave", "f13").
func ParseKeySpec(s string) (KeySpec, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	switch {
	case s == "":
		return KeySpec{Scan: 0x29}, nil
	case strings.HasPrefix(s, "sc:"):
		v, err := strconv.ParseUint(strings.TrimPrefix(s, "sc:"), 0, 32)
		if err != nil {
			return KeySpec{}, fmt.Errorf("draw_key %q: %v", s, err)
		}
		k := KeySpec{Scan: uint16(v & 0xFF)}
		if v>>8 == 0xE0 {
			k.Extended = true
		}
		return k, nil
	case strings.HasPrefix(s, "vk:"):
		v, err := strconv.ParseUint(strings.TrimPrefix(s, "vk:"), 0, 32)
		if err != nil {
			return KeySpec{}, fmt.Errorf("draw_key %q: %v", s, err)
		}
		return KeySpec{VK: uint16(v)}, nil
	}
	if vk, ok := vkNames[s]; ok {
		if vk == 0xC0 {
			return KeySpec{Scan: 0x29}, nil
		}
		return KeySpec{VK: uint16(vk)}, nil
	}
	return KeySpec{}, fmt.Errorf("draw_key %q: unknown key", s)
}
