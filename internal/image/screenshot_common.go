package image

import (
	"bytes"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	xdraw "golang.org/x/image/draw"
)

// ScreenshotAction specifies what the screenshot tool should do.
type ScreenshotAction string

const (
	ActionCapture      ScreenshotAction = "capture"
	ActionListDisplays ScreenshotAction = "list_displays"
	ActionListWindows  ScreenshotAction = "list_windows"
)

// ScreenshotOptions controls screenshot capture behavior.
type ScreenshotOptions struct {
	Display       int               // 1-based monitor index, 0=primary
	Window        string            // match by title/app name
	Region        *ScreenshotRegion // rectangular area
	Cursor        bool              // include mouse cursor
	DelayMs       int               // delay before capture
	Format        string            // "png" or "jpeg"
	Quality       int               // JPEG quality 1-100
	OutputPath    string            // save finalized image to file
	RawOutputPath string            // save raw (unprocessed) image to file
	MaxWidth      int               // auto-resize max width
}

// ScreenshotRegion defines a rectangular area to capture.
type ScreenshotRegion struct {
	X, Y, Width, Height int
}

// DisplayInfo describes a monitor.
type DisplayInfo struct {
	Index     int    `json:"index"`
	IsPrimary bool   `json:"is_primary"`
	Width     int    `json:"width"`
	Height    int    `json:"height"`
	X         int    `json:"x"`
	Y         int    `json:"y"`
	Name      string `json:"name,omitempty"`
}

// WindowInfo describes a capturable window.
type WindowInfo struct {
	ID     int    `json:"id"`
	Title  string `json:"title"`
	App    string `json:"app"`
	X      int    `json:"x"`
	Y      int    `json:"y"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

// ScreenshotResult is the output of CaptureScreen.
type ScreenshotResult struct {
	Image     Image
	SavedPath string
}

// applyDelay waits for opts.DelayMs if non-zero.
func applyDelay(delayMs int) {
	if delayMs > 0 {
		time.Sleep(time.Duration(delayMs) * time.Millisecond)
	}
}

// finalizeImage reads the raw screenshot file, applies format conversion
// and resize, then returns the final Image.
func finalizeImage(rawPath string, opts ScreenshotOptions) (Image, error) {
	data, err := os.ReadFile(rawPath)
	if err != nil {
		return Image{}, fmt.Errorf("reading screenshot file: %w", err)
	}

	// Save the raw (unprocessed) screenshot if requested.
	if opts.RawOutputPath != "" {
		if werr := os.WriteFile(opts.RawOutputPath, data, 0644); werr != nil {
			return Image{}, fmt.Errorf("writing raw screenshot to %s: %w", opts.RawOutputPath, werr)
		}
	}

	mime := DetectMIME(data)
	if mime == "" {
		mime = MIMEPNG
	}

	img := Image{Data: data, MIME: mime}

	decoded, err := decodeImageData(data)
	if err == nil {
		img.Width = decoded.Bounds().Dx()
		img.Height = decoded.Bounds().Dy()

		maxW := opts.MaxWidth
		if maxW == 0 {
			maxW = 1920
		}

		target := decoded
		if img.Width > maxW {
			target = resizeImage(decoded, maxW)
			img.Width = target.Bounds().Dx()
			img.Height = target.Bounds().Dy()
		}

		// Re-encode if format conversion or resize is needed.
		format := strings.ToLower(opts.Format)
		if format == "" {
			format = "png"
		}

		if format == "jpeg" {
			q := opts.Quality
			if q == 0 {
				q = 85
			}
			buf, err := encodeJPEGBytes(target, q)
			if err == nil {
				img.Data = buf
				img.MIME = MIMEJPEG
			}
		} else if img.Width > maxW || target != decoded {
			// Re-encode PNG after resize.
			buf, err := encodePNGBytes(target)
			if err == nil {
				img.Data = buf
				img.MIME = MIMEPNG
			}
		}
	}

	// When output_path is set, the raw screenshot was written directly to
	// that path. Overwrite it with the finalized (resized/converted) image
	// so the saved file matches what is returned to the caller.
	if opts.OutputPath != "" {
		if werr := os.WriteFile(opts.OutputPath, img.Data, 0644); werr != nil {
			return Image{}, fmt.Errorf("writing finalized screenshot to %s: %w", opts.OutputPath, werr)
		}
	}

	return img, nil
}

func decodeImageData(data []byte) (image.Image, error) {
	img, _, err := image.Decode(bytes.NewReader(data))
	return img, err
}

// resizeImage resizes src so its width does not exceed maxW,
// maintaining aspect ratio using CatmullRom interpolation for high quality.
// This produces significantly sharper text and UI elements than nearest-neighbor.
func resizeImage(src image.Image, maxW int) image.Image {
	bounds := src.Bounds()
	oldW := bounds.Dx()
	oldH := bounds.Dy()
	if oldW <= maxW {
		return src
	}
	newW := maxW
	newH := oldH * newW / oldW
	dst := image.NewRGBA(image.Rect(0, 0, newW, newH))
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), src, bounds, xdraw.Over, nil)
	return dst
}

// createTempScreenshotPath creates a temp or output file path. The error is
// returned to the caller: a broken TMPDIR used to be swallowed, making the
// screenshot tool write "screenshot.png" into the process CWD while cleanup
// removed a non-existent dir and left the stray file behind (#975).
func createTempScreenshotPath(opts ScreenshotOptions) (string, func(), error) {
	if opts.OutputPath != "" {
		return opts.OutputPath, func() {}, nil
	}
	tmpDir, err := os.MkdirTemp("", "ggcode-screenshot-*")
	if err != nil {
		return "", func() {}, fmt.Errorf("creating temp screenshot dir: %w", err)
	}
	ext := "png"
	if strings.ToLower(opts.Format) == "jpeg" {
		ext = "jpg"
	}
	return filepath.Join(tmpDir, "screenshot."+ext), func() { os.RemoveAll(tmpDir) }, nil
}

// parseWmctrlOutput parses `wmctrl -lx` output, whose five columns (window
// ID, desktop, hostname, WM_CLASS, title) are padded with spaces: the desktop
// column is right-aligned (" 0" vs "-1"), and the class column is padded to
// a fixed width, so titles are preceded by runs of spaces. A single-space
// SplitN(line, " ", 5) therefore produced an empty App (double space between
// ID and desktop), mixed class padding into the Title, or dropped lines with
// fewer than 5 chunks entirely. Fields-based column access fixes the columns
// and the title is sliced from the original line so interior spacing survives
// (#975).
func parseWmctrlOutput(out string) []WindowInfo {
	var windows []WindowInfo
	for _, line := range strings.Split(out, "\n") {
		if w, ok := parseWmctrlLine(line); ok {
			windows = append(windows, w)
		}
	}
	return windows
}

func parseWmctrlLine(line string) (WindowInfo, bool) {
	line = strings.TrimSuffix(line, "\r")
	fields := strings.Fields(line)
	if len(fields) < 5 {
		return WindowInfo{}, false
	}
	id, err := strconv.ParseUint(fields[0], 0, 64)
	if err != nil {
		return WindowInfo{}, false
	}
	// WM_CLASS column is "instance.class"; keep the class part (same
	// semantics as before, now reading the correct column).
	clsParts := strings.SplitN(fields[3], ".", 2)
	app := clsParts[len(clsParts)-1]
	// Titles may contain any spacing, so take them from the original line at
	// the offset of the 5th column instead of from the field split; strip the
	// class column's trailing padding.
	title := strings.TrimRight(line[wmctrlTitleOffset(line):], " \t")
	return WindowInfo{ID: int(id), App: app, Title: title}, true
}

// wmctrlTitleOffset returns the byte offset of the 5th column (the title) in
// a wmctrl -lx line, skipping the run of padding spaces before it.
func wmctrlTitleOffset(line string) int {
	const titleColumn = 4 // 0-based index of the title column
	idx := 0
	for i := 0; i < titleColumn; i++ {
		for idx < len(line) && (line[idx] == ' ' || line[idx] == '\t') {
			idx++
		}
		for idx < len(line) && line[idx] != ' ' && line[idx] != '\t' {
			idx++
		}
	}
	for idx < len(line) && (line[idx] == ' ' || line[idx] == '\t') {
		idx++
	}
	return idx
}

// parseWlrrandrOutput parses `wlr-randr` output. Structure: an output name
// header line, then indented property lines, and under an indented "Modes:"
// header a block of deeper-indented mode lines such as
//
//	1920x1080 px, 60.000000 Hz (preferred, current)
//
// The old parser looked for lines starting with "Mode:", which never matches
// ("Modes:" has an "s"), leaving Width/Height at 0 — so linuxDisplayBounds
// always failed with "geometry unknown" and multi-display selection on
// Wayland silently fell back to a full-screen capture (#975). Mode lines are
// now recognized by their shape; the (current) mode wins, falling back to
// (preferred), then the first listed mode.
func parseWlrrandrOutput(out string) []DisplayInfo {
	var displays []DisplayInfo
	idx := 0
	var current DisplayInfo
	var mode wlrModePick
	flush := func() {
		if current.Name == "" {
			return
		}
		idx++
		current.Index = idx
		current.Width = mode.width
		current.Height = mode.height
		displays = append(displays, current)
	}
	for _, rawLine := range strings.Split(out, "\n") {
		// #764: keep the raw line -- indentation IS the structure (indented
		// lines are sub-fields of the display above).
		if strings.TrimSpace(rawLine) == "" {
			continue
		}
		indented := strings.HasPrefix(rawLine, " ") || strings.HasPrefix(rawLine, "\t")
		if !indented {
			flush()
			current = DisplayInfo{Name: strings.TrimSpace(rawLine)}
			mode = wlrModePick{}
			continue
		}
		line := strings.TrimSpace(rawLine)
		if strings.HasPrefix(line, "Position:") {
			// #764 secondary: parse X/Y offset so multi-monitor regions are right.
			for _, f := range strings.Fields(line) {
				if strings.Contains(f, ",") {
					parts := strings.Split(strings.TrimSuffix(f, ","), ",")
					if len(parts) == 2 {
						current.X, _ = strconv.Atoi(parts[0])
						current.Y, _ = strconv.Atoi(parts[1])
					}
				}
			}
			continue
		}
		if w, h, isCurrent, isPreferred, ok := parseWlrrandrModeLine(line); ok {
			mode.offer(w, h, isCurrent, isPreferred)
		}
	}
	flush()
	return displays
}

// parseWlrrandrModeLine recognizes a mode entry like
// "1920x1080 px, 60.000000 Hz (preferred, current)". Requiring a leading
// WxH token followed by a px token keeps "Modes:" headers and lines like
// "Physical size: 340x190 mm" out.
func parseWlrrandrModeLine(line string) (w, h int, isCurrent, isPreferred bool, ok bool) {
	fields := strings.Fields(line)
	if len(fields) < 2 || !strings.HasPrefix(fields[1], "px") {
		return 0, 0, false, false, false
	}
	parts := strings.Split(fields[0], "x")
	if len(parts) != 2 {
		return 0, 0, false, false, false
	}
	var errW, errH error
	w, errW = strconv.Atoi(parts[0])
	h, errH = strconv.Atoi(parts[1])
	if errW != nil || errH != nil || w <= 0 || h <= 0 {
		return 0, 0, false, false, false
	}
	isCurrent = strings.Contains(line, "current")
	isPreferred = strings.Contains(line, "preferred")
	return w, h, isCurrent, isPreferred, true
}

// wlrModePick tracks the best mode seen for one output: (current) wins over
// (preferred), which wins over the first listed mode.
type wlrModePick struct {
	width, height      int
	current, preferred bool
	seen               bool
}

func (p *wlrModePick) offer(w, h int, isCurrent, isPreferred bool) {
	if !p.seen {
		*p = wlrModePick{width: w, height: h, current: isCurrent, preferred: isPreferred, seen: true}
		return
	}
	if p.current {
		return // the active mode always wins
	}
	if isCurrent || (isPreferred && !p.preferred) {
		*p = wlrModePick{width: w, height: h, current: isCurrent, preferred: isPreferred, seen: true}
	}
}

// gnomeScreenshotUnsupportedOpts reports an error when opts needs a
// capability gnome-screenshot does not expose on the command line: it cannot
// capture a region non-interactively, and --display expects a GDK/X11
// connection name (host:D.S like ":0"), not an xrandr/wlr-randr output name,
// so per-output selection is impossible too. Failing explicitly mirrors the
// existing window-capture treatment instead of silently capturing the full
// screen (#975).
func gnomeScreenshotUnsupportedOpts(opts ScreenshotOptions) error {
	if opts.Display > 1 {
		return fmt.Errorf("gnome-screenshot cannot select a display by index; use grim (Wayland) or scrot/import (X11) for per-display capture")
	}
	if opts.Region != nil {
		return fmt.Errorf("gnome-screenshot cannot capture a region non-interactively; use grim -g, scrot -a, or import -crop for region capture")
	}
	return nil
}
