package winput

import "testing"

func TestParseHotkey(t *testing.T) {
	h, err := ParseHotkey("trigger", "Ctrl+Home")
	if err != nil || h.VK != 0x24 || !h.Ctrl || h.Shift || h.Alt {
		t.Fatalf("%+v %v", h, err)
	}
	if h.String() != "Ctrl+Home" {
		t.Fatalf("label %q", h.String())
	}
	h, err = ParseHotkey("quit", "ctrl+shift+end")
	if err != nil || h.VK != 0x23 || !h.Ctrl || !h.Shift {
		t.Fatalf("%+v %v", h, err)
	}
	if h, err = ParseHotkey("x", "f8"); err != nil || h.VK != 0x77 {
		t.Fatalf("%+v %v", h, err)
	}
	if _, err = ParseHotkey("x", "ctrl+nope"); err == nil {
		t.Fatal("expected error")
	}
}

func TestParseKeyCombo(t *testing.T) {
	k, err := ParseKeyCombo("")
	if err != nil || len(k) != 1 || k[0].Scan != 0x29 {
		t.Fatalf("%+v %v", k, err)
	}
	k, err = ParseKeyCombo("sc:0x39+sc:0x10")
	if err != nil || len(k) != 2 || k[0].Scan != 0x39 || k[1].Scan != 0x10 {
		t.Fatalf("%+v %v", k, err)
	}
	k, err = ParseKeyCombo("sc:0xe01d")
	if err != nil || k[0].Scan != 0x1d || !k[0].Extended {
		t.Fatalf("%+v %v", k, err)
	}
	k, err = ParseKeyCombo("grave")
	if err != nil || k[0].Scan != 0x29 {
		t.Fatalf("%+v %v", k, err)
	}
}
