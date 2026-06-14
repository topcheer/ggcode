//go:build linux

package image

import (
	"strings"
	"testing"
)

func TestParseClipboardTypesDeduplicatesWhitespace(t *testing.T) {
	got := parseClipboardTypes("text/plain\nimage/png\timage/png\nimage/jpeg\n")
	want := []string{"text/plain", "image/png", "image/jpeg"}
	if len(got) != len(want) {
		t.Fatalf("parseClipboardTypes length = %d, want %d: %#v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("parseClipboardTypes[%d] = %q, want %q (all: %#v)", i, got[i], want[i], got)
		}
	}
}

func TestLinuxClipboardToolsMissingErrorWayland(t *testing.T) {
	t.Setenv("XDG_SESSION_TYPE", "wayland")
	err := linuxClipboardToolsMissingError()
	if err == nil {
		t.Fatal("expected error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "Wayland") || !strings.Contains(msg, "wl-clipboard") {
		t.Fatalf("unexpected Wayland missing-tool message: %q", msg)
	}
}

func TestLinuxClipboardToolsMissingErrorX11(t *testing.T) {
	t.Setenv("XDG_SESSION_TYPE", "x11")
	err := linuxClipboardToolsMissingError()
	if err == nil {
		t.Fatal("expected error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "X11") || !strings.Contains(msg, "xclip") {
		t.Fatalf("unexpected X11 missing-tool message: %q", msg)
	}
}

func TestLinuxClipboardImageUnavailableErrorIncludesAvailableTypes(t *testing.T) {
	msg := linuxClipboardImageUnavailableError(false, false).Error()
	if !strings.Contains(msg, "PNG") || !strings.Contains(msg, "JPEG") || !strings.Contains(msg, "WebP") {
		t.Fatalf("expected supported image formats in message, got %q", msg)
	}
}
