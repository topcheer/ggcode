//go:build darwin

package image

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Only a TRULY empty/missing clipboard image maps to Unavailable; TCC denial
// and exec failures must NOT be swallowed (#1807 case 2).
func TestIsNoClipboardImageError(t *testing.T) {
	if isNoClipboardImageError(nil) {
		t.Fatal("nil error must not classify as no-image")
	}
	cases := []struct {
		msg  string
		want bool
	}{
		{"The clipboard is Missing Value", true},
		{"Can't get clipboard as «class PNGf»", true},
		{"clipboard doesn't contain the data", true},
		{"Can't make some data into the expected Type Of", true},
		{"Not authorized to send Apple events (-1743)", false},
		{"execution failed", false},
	}
	for _, tt := range cases {
		if got := isNoClipboardImageError(errors.New(tt.msg)); got != tt.want {
			t.Errorf("isNoClipboardImageError(%q) = %v, want %v", tt.msg, got, tt.want)
		}
	}
}

// sips failing on a non-TIFF payload must surface as a wrapped,
// output-carrying error (not silently succeed). Note: sips exits 0 for a
// missing source file, so the failure is provoked with garbage bytes.
func TestConvertClipboardTIFFToPNGErrorPath(t *testing.T) {
	if !commandAvailable("sips") {
		t.Skip("sips unavailable")
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "bad.tiff")
	if err := os.WriteFile(src, []byte("sa-140: not a tiff payload"), 0644); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "out.png")
	err := convertClipboardTIFFToPNG(src, dst)
	if err == nil {
		t.Skip("sips accepted the payload; error branch not reachable in this environment")
	}
	if !strings.Contains(err.Error(), "converting clipboard image") {
		t.Fatalf("error must carry the conversion prefix: %v", err)
	}
}

func TestParseSPDisplaysJSONInvalid(t *testing.T) {
	if _, err := parseSPDisplaysJSON([]byte("{ not json")); err == nil || !strings.Contains(err.Error(), "parsing display info") {
		t.Fatalf("expected parse error, got %v", err)
	}
}

func TestParseSPDisplaysJSONEmptyDefaults(t *testing.T) {
	// No display units at all: callers still get a usable primary display.
	displays, err := parseSPDisplaysJSON([]byte(`{"SPDisplaysDataType": []}`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(displays) != 1 || displays[0].Index != 1 || !displays[0].IsPrimary {
		t.Fatalf("expected single default primary display, got %+v", displays)
	}
}

func TestParseMacResolutionFallbacks(t *testing.T) {
	tests := []struct {
		res  string
		w, h int
	}{
		{"3840x2160", 3840, 2160},
		{"default", 0, 0},  // virtual/no-mode display
		{"1080p x5", 0, 0}, // only one numeric token
		{"", 0, 0},
	}
	for _, tt := range tests {
		w, h := parseMacResolution(tt.res)
		if w != tt.w || h != tt.h {
			t.Errorf("parseMacResolution(%q) = %dx%d, want %dx%d", tt.res, w, h, tt.w, tt.h)
		}
	}
}

// macDisplayRegion maps OUR 1-based index to the on-screen rectangle
// (#1570-D); unknown indexes must fail explicitly.
func TestMacDisplayRegionLookup(t *testing.T) {
	if !commandAvailable("system_profiler") {
		t.Skip("system_profiler unavailable")
	}
	if _, err := ListDisplays(); err != nil {
		t.Skipf("system_profiler failed: %v", err)
	}
	if r, err := macDisplayRegion(1); err != nil {
		t.Fatalf("primary display lookup failed: %v", err)
	} else if r.Width <= 0 || r.Height <= 0 {
		t.Fatalf("primary display has non-positive geometry: %+v", r)
	}
	if _, err := macDisplayRegion(9999); err == nil || !strings.Contains(err.Error(), "display 9999 not found") {
		t.Fatalf("expected not-found error, got %v", err)
	}
}
