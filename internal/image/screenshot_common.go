package image

import (
	"bytes"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	xdraw "golang.org/x/image/draw"
)

type ScreenshotAction string

const (
	ActionCapture      ScreenshotAction = "capture"
	ActionListDisplays ScreenshotAction = "list_displays"
	ActionListWindows  ScreenshotAction = "list_windows"
)

type ScreenshotOptions struct {
	Display       int
	Window        string
	Region        *ScreenshotRegion
	Cursor        bool
	DelayMs       int
	Format        string
	Quality       int
	OutputPath    string
	RawOutputPath string
	MaxWidth      int
}

type ScreenshotRegion struct {
	X, Y, Width, Height int
}

type DisplayInfo struct {
	Index     int    `json:"index"`
	IsPrimary bool   `json:"is_primary"`
	Width     int    `json:"width"`
	Height    int    `json:"height"`
	X         int    `json:"x"`
	Y         int    `json:"y"`
	Name      string `json:"name,omitempty"`
}

type WindowInfo struct {
	ID     int    `json:"id"`
	Title  string `json:"title"`
	App    string `json:"app"`
	X      int    `json:"x"`
	Y      int    `json:"y"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

type ScreenshotResult struct {
	Image     Image
	SavedPath string
}

func applyDelay(delayMs int) {
	if delayMs > 0 {
		time.Sleep(time.Duration(delayMs) * time.Millisecond)
	}
}

func finalizeImage(rawPath string, opts ScreenshotOptions) (Image, error) {
	data, err := os.ReadFile(rawPath)
	if err != nil {
		return Image{}, fmt.Errorf("reading screenshot file: %w", err)
	}

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
	if err != nil {

		return Image{}, fmt.Errorf("decoding screenshot bytes: %w", err)
	}
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

	format := strings.ToLower(opts.Format)
	if format == "" {
		format = "png"
	}

	if format == "jpeg" {
		q := opts.Quality
		if q == 0 {
			q = 85
		}
		buf, jerr := encodeJPEGBytes(target, q)
		if jerr != nil {

			return Image{}, fmt.Errorf("encoding jpeg screenshot: %w", jerr)
		}
		img.Data = buf
		img.MIME = MIMEJPEG
	} else if img.Width > maxW || target != decoded {

		buf, perr := encodePNGBytes(target)
		if perr != nil {

			return Image{}, fmt.Errorf("encoding png screenshot: %w", perr)
		}
		img.Data = buf
		img.MIME = MIMEPNG
	}

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

func resizeImage(src image.Image, maxW int) image.Image {
	bounds := src.Bounds()
	oldW := bounds.Dx()
	oldH := bounds.Dy()
	if maxW < 1 {
		maxW = 1
	}
	if oldW <= maxW {
		return src
	}
	newW := maxW
	newH := oldH * newW / oldW

	if newH < 1 {
		newH = 1
	}
	dst := image.NewRGBA(image.Rect(0, 0, newW, newH))
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), src, bounds, xdraw.Over, nil)
	return dst
}

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

	clsParts := strings.SplitN(fields[3], ".", 2)
	app := clsParts[len(clsParts)-1]

	title := strings.TrimRight(line[wmctrlTitleOffset(line):], " \t")
	return WindowInfo{ID: int(id), App: app, Title: title}, true
}

func wmctrlTitleOffset(line string) int {
	const titleColumn = 4
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

func parseWlrrandrOutput(out string) []DisplayInfo {
	var displays []DisplayInfo
	idx := 0
	var current DisplayInfo
	var mode wlrModePick
	scale := 1.0
	flush := func() {
		if current.Name == "" {
			return
		}
		idx++
		current.Index = idx

		if scale > 0 && scale != 1 {
			current.Width = int(math.Round(float64(mode.width) / scale))
			current.Height = int(math.Round(float64(mode.height) / scale))
		} else {
			current.Width = mode.width
			current.Height = mode.height
		}
		scale = 1.0
		displays = append(displays, current)
	}
	for _, rawLine := range strings.Split(out, "\n") {

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

		if strings.HasPrefix(line, "Scale:") {
			if v, err := strconv.ParseFloat(strings.TrimSpace(strings.TrimPrefix(line, "Scale:")), 64); err == nil && v > 0 {
				scale = v
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
		return
	}
	if isCurrent || (isPreferred && !p.preferred) {
		*p = wlrModePick{width: w, height: h, current: isCurrent, preferred: isPreferred, seen: true}
	}
}

func gnomeScreenshotUnsupportedOpts(opts ScreenshotOptions) error {

	if opts.Display >= 1 {
		return fmt.Errorf("gnome-screenshot cannot select a display by index; use grim (Wayland) or scrot/import (X11) for per-display capture")
	}
	if opts.Region != nil {
		return fmt.Errorf("gnome-screenshot cannot capture a region non-interactively; use grim -g, scrot -a, or import -crop for region capture")
	}
	return nil
}
