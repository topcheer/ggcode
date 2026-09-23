package image

import (
	"testing"
)

func TestParseWmctrlLineRejectsBadID(t *testing.T) {
	// The ID column must be numeric; anything else drops the line.
	if _, ok := parseWmctrlLine("notanid 0 host class Title text"); ok {
		t.Fatal("non-numeric window ID must be rejected")
	}
}

func TestParseWlrrandrModeLineShapes(t *testing.T) {
	tests := []struct {
		line string
		ok   bool
	}{
		{"1920px", false},                    // no WxH split
		{"abcd px", false},                   // non-numeric width
		{"1920x0 px", false},                 // zero height
		{"0x1080 px, 60 Hz", false},          // zero width
		{"1920x1080 px, 60.000000 Hz", true}, // plain mode
		{"1920x1080 px, 60.000000 Hz (current)", true},
	}
	for _, tt := range tests {
		w, h, isCur, isPref, ok := parseWlrrandrModeLine(tt.line)
		if ok != tt.ok {
			t.Errorf("parseWlrrandrModeLine(%q) ok=%v, want %v", tt.line, ok, tt.ok)
			continue
		}
		if ok && (w != 1920 || h != 1080) {
			t.Errorf("parseWlrrandrModeLine(%q) = %dx%d, want 1920x1080", tt.line, w, h)
		}
		_ = isCur
		_ = isPref
	}
	// Explicit current/preferred flags on the valid line.
	_, _, isCur, isPref, ok := parseWlrrandrModeLine("3840x2160 px, 144.000 Hz (preferred, current)")
	if !ok || !isCur || !isPref {
		t.Fatalf("current+preferred flags lost: ok=%v cur=%v pref=%v", ok, isCur, isPref)
	}
}

// wlrModePick precedence: first seen wins initially, (current) always wins,
// (preferred) beats a plain first mode, and an already-current mode is final.
func TestWlrModePickPrecedence(t *testing.T) {
	var p wlrModePick
	p.offer(640, 480, false, false) // first seen
	if p.width != 640 || p.height != 480 || !p.seen {
		t.Fatalf("first mode lost: %+v", p)
	}
	p.offer(800, 600, false, false) // plain mode must not replace
	if p.width != 640 || p.height != 480 {
		t.Fatalf("plain mode replaced first: %+v", p)
	}
	p.offer(1024, 768, false, true) // preferred takes over
	if p.width != 1024 || p.height != 768 || !p.preferred {
		t.Fatalf("preferred takeover failed: %+v", p)
	}
	p.offer(1280, 1024, false, true) // preferred already set: keep
	if p.width != 1024 || p.height != 768 {
		t.Fatalf("second preferred replaced first: %+v", p)
	}
	p.offer(1920, 1080, true, false) // current takes over
	if p.width != 1920 || p.height != 1080 || !p.current {
		t.Fatalf("current takeover failed: %+v", p)
	}
	p.offer(2560, 1440, true, true) // current is final
	if p.width != 1920 || p.height != 1080 {
		t.Fatalf("current was replaced: %+v", p)
	}
}
