package image

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- #975 issue 1: wmctrl -lx padded-column parsing ---

const wmctrlLxOutput = "0x03c00007  0 hostname Navigator.firefox   Mozilla Firefox\n" +
	"0x04a0000b -1 hostname kitty                   kitty - main.go\n" +
	"0x05200003  0 hostname gnome-calculator.Gnome-calculator Calculator\n" +
	"0x0600000a  0 N/A\n" +
	"\n"

func TestParseWmctrlOutputPaddedColumns(t *testing.T) {
	windows := parseWmctrlOutput(wmctrlLxOutput)
	if len(windows) != 3 {
		t.Fatalf("got %d windows, want 3 (malformed lines dropped): %+v", len(windows), windows)
	}

	// Right-aligned desktop column " 0" (double space after the ID) used to
	// shift every field: App empty, Title misaligned.
	firefox := windows[0]
	if firefox.ID != 0x03c00007 {
		t.Errorf("firefox ID = %#x, want 0x03c00007", firefox.ID)
	}
	if firefox.App != "firefox" {
		t.Errorf("firefox App = %q, want \"firefox\" (double-space desktop padding)", firefox.App)
	}
	if firefox.Title != "Mozilla Firefox" {
		t.Errorf("firefox Title = %q, want \"Mozilla Firefox\"", firefox.Title)
	}

	// Sticky window on desktop -1.
	kitty := windows[1]
	if kitty.ID != 0x04a0000b {
		t.Errorf("kitty ID = %#x, want 0x04a0000b", kitty.ID)
	}
	if kitty.App != "kitty" {
		t.Errorf("kitty App = %q, want \"kitty\"", kitty.App)
	}
	if kitty.Title != "kitty - main.go" {
		t.Errorf("kitty Title = %q, want \"kitty - main.go\" (class padding must not leak in)", kitty.Title)
	}

	// Class with multiple dots keeps the last segment; long class padding
	// still yields a clean title.
	calc := windows[2]
	if calc.App != "Gnome-calculator" {
		t.Errorf("calc App = %q, want \"Gnome-calculator\"", calc.App)
	}
	if calc.Title != "Calculator" {
		t.Errorf("calc Title = %q, want \"Calculator\"", calc.Title)
	}
}

func TestParseWmctrlLineSingleSpaceLine(t *testing.T) {
	// Single-space-separated lines (hand-crafted / other wmctrl versions)
	// must still parse: Fields handles any run of spaces.
	w, ok := parseWmctrlLine("0x01a00001 0 host Term.term Title with  double  spaces")
	if !ok {
		t.Fatal("single-space line not parsed")
	}
	if w.App != "term" || w.Title != "Title with  double  spaces" {
		t.Errorf("App = %q, Title = %q; want term / preserved spacing", w.App, w.Title)
	}
}

// --- #975 issue 2: wlr-randr Modes: block parsing ---

const wlrrandrOutput = "eDP-1\n" +
	"  Position: 0,0\n" +
	"  Modes:\n" +
	"    1920x1080 px, 60.000000 Hz (preferred, current)\n" +
	"    1920x1080 px, 120.000000 Hz\n" +
	"DP-1\n" +
	"  Position: 1920,0\n" +
	"  Modes:\n" +
	"    2560x1440 px, 59.951000 Hz (current)\n" +
	"    3840x2160 px, 60.000000 Hz (preferred)\n" +
	"HDMI-A-1\n" +
	"  Physical size: 340x190 mm\n" +
	"  Modes:\n" +
	"    1280x720 px, 60.000000 Hz (preferred)\n" +
	"    1024x768 px, 60.000000 Hz\n"

func TestParseWlrrandrOutputModesBlock(t *testing.T) {
	displays := parseWlrrandrOutput(wlrrandrOutput)
	if len(displays) != 3 {
		t.Fatalf("got %d displays, want 3: %+v", len(displays), displays)
	}

	edp := displays[0]
	if edp.Name != "eDP-1" || edp.Index != 1 {
		t.Errorf("display 0 = %+v, want eDP-1 index 1", edp)
	}
	if edp.Width != 1920 || edp.Height != 1080 {
		t.Errorf("eDP-1 = %dx%d, want 1920x1080 (current mode preferred over first listed)", edp.Width, edp.Height)
	}
	if edp.X != 0 || edp.Y != 0 {
		t.Errorf("eDP-1 position = %d,%d, want 0,0", edp.X, edp.Y)
	}

	dp := displays[1]
	// 2560x1440 is (current); (preferred) 3840x2160 must NOT override it.
	if dp.Width != 2560 || dp.Height != 1440 {
		t.Errorf("DP-1 = %dx%d, want 2560x1440 ((current) beats (preferred))", dp.Width, dp.Height)
	}
	if dp.X != 1920 || dp.Y != 0 {
		t.Errorf("DP-1 position = %d,%d, want 1920,0", dp.X, dp.Y)
	}

	hdmi := displays[2]
	// No (current) anywhere: fall back to (preferred).
	if hdmi.Width != 1280 || hdmi.Height != 720 {
		t.Errorf("HDMI-A-1 = %dx%d, want 1280x720 (preferred fallback)", hdmi.Width, hdmi.Height)
	}
}

func TestParseWlrrandrModeLineRejectsNonModes(t *testing.T) {
	for _, line := range []string{
		"Modes:",
		"  Physical size: 340x190 mm",
		"Position: 1920,0",
		"Transform: normal",
		"1920x1080 px", // ok actually - covered below
	} {
		if line == "1920x1080 px" {
			continue
		}
		if _, _, _, _, ok := parseWlrrandrModeLine(line); ok {
			t.Errorf("parseWlrrandrModeLine(%q) accepted a non-mode line", line)
		}
	}
	if _, _, isCurrent, isPreferred, ok := parseWlrrandrModeLine("1920x1080 px, 60.000000 Hz (preferred, current)"); !ok || !isCurrent || !isPreferred {
		t.Errorf("parseWlrrandrModeLine failed on a real mode line (ok=%v current=%v preferred=%v)", ok, isCurrent, isPreferred)
	}
}

// --- #975 issue 3: gnome-screenshot unsupported options ---

func TestGnomeScreenshotUnsupportedOpts(t *testing.T) {
	if err := gnomeScreenshotUnsupportedOpts(ScreenshotOptions{}); err != nil {
		t.Errorf("plain full-screen capture must be allowed, got %v", err)
	}
	if err := gnomeScreenshotUnsupportedOpts(ScreenshotOptions{Display: 1}); err != nil {
		t.Errorf("Display=1 (primary) must be allowed, got %v", err)
	}
	if err := gnomeScreenshotUnsupportedOpts(ScreenshotOptions{Display: 2}); err == nil {
		t.Error("Display>1 must fail explicitly (cannot select output by index)")
	} else if !strings.Contains(err.Error(), "gnome-screenshot") {
		t.Errorf("Display>1 error should name the tool, got: %v", err)
	}
	region := &ScreenshotRegion{X: 0, Y: 0, Width: 100, Height: 100}
	if err := gnomeScreenshotUnsupportedOpts(ScreenshotOptions{Region: region}); err == nil {
		t.Error("Region must fail explicitly (no non-interactive region capture)")
	}
}

// --- #975附带: MkdirTemp error propagation ---

func TestCreateTempScreenshotPathMkdirTempError(t *testing.T) {
	// Point TMPDIR at a path that cannot host temp dirs. On darwin/linux,
	// os.MkdirTemp("/dev/null/sandbox", ...) fails because /dev/null is not
	// a directory. On windows use an invalid drive path instead.
	badDir := "/dev/null/ggcode-test"
	if filepath.Separator == '\\' {
		badDir = `Z:\definitely\not\a\dir`
	}
	t.Setenv("TMPDIR", badDir)

	rawPath, cleanup, err := createTempScreenshotPath(ScreenshotOptions{})
	if err == nil {
		cleanup()
		t.Fatalf("expected MkdirTemp failure with TMPDIR=%q, got path %q", badDir, rawPath)
	}
	if rawPath != "" {
		t.Errorf("rawPath = %q on error, want \"\" (old code returned a path under an empty dir)", rawPath)
	}
	cleanup() // must be safe to call (no-op)
}

func TestCreateTempScreenshotPathOutputPathShortCircuit(t *testing.T) {
	// OutputPath must bypass MkdirTemp entirely: even with a broken TMPDIR
	// the provided path is used and cleanup is a no-op.
	badDir := "/dev/null/ggcode-test"
	if filepath.Separator == '\\' {
		badDir = `Z:\definitely\not\a\dir`
	}
	t.Setenv("TMPDIR", badDir)

	rawPath, cleanup, err := createTempScreenshotPath(ScreenshotOptions{OutputPath: "/tmp/out.png"})
	if err != nil {
		t.Fatalf("OutputPath path should not touch TMPDIR, got %v", err)
	}
	if rawPath != "/tmp/out.png" {
		t.Errorf("rawPath = %q, want /tmp/out.png", rawPath)
	}
	cleanup()
}

func TestCreateTempScreenshotPathSuccess(t *testing.T) {
	rawPath, cleanup, err := createTempScreenshotPath(ScreenshotOptions{Format: "jpeg"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer cleanup()
	if !strings.HasSuffix(rawPath, ".jpg") {
		t.Errorf("jpeg path = %q, want .jpg suffix", rawPath)
	}
	if _, statErr := os.Stat(rawPath); statErr != nil {
		// Only the dir must exist; the screenshot tool creates the file.
		if _, dirErr := os.Stat(filepath.Dir(rawPath)); dirErr != nil {
			t.Errorf("temp dir missing: %v", dirErr)
		}
	}
	cleanup()
	if _, statErr := os.Stat(filepath.Dir(rawPath)); !os.IsNotExist(statErr) {
		t.Errorf("cleanup left temp dir %q behind", filepath.Dir(rawPath))
	}
}
