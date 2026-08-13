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

func TestBuildGrimCommandRegionFormat(t *testing.T) {
	// grim -g expects "<x>,<y>,<width>x<height>"
	cmd := buildGrimCommand("/tmp/shot.png", ScreenshotOptions{
		Region: &ScreenshotRegion{X: 100, Y: 200, Width: 800, Height: 600},
	})
	args := cmd.Args
	// find the -g flag value
	var gVal string
	for i, a := range args {
		if a == "-g" && i+1 < len(args) {
			gVal = args[i+1]
			break
		}
	}
	if gVal == "" {
		t.Fatal("expected -g flag in grim command")
	}
	want := "100,200,800x600"
	if gVal != want {
		t.Fatalf("grim -g value = %q, want %q", gVal, want)
	}
}
