//go:build linux

package image

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// CaptureScreen captures a screenshot on Linux by auto-detecting an
// available screenshot tool.
func CaptureScreen(opts ScreenshotOptions) (ScreenshotResult, error) {
	tools := candidateLinuxScreenshotTools()
	if len(tools) == 0 {
		return ScreenshotResult{}, linuxScreenshotToolsMissingError()
	}

	applyDelay(opts.DelayMs)

	rawPath, cleanup, err := createTempScreenshotPath(opts)
	if err != nil {
		return ScreenshotResult{}, err
	}
	defer cleanup()

	// #1571-A: try every installed candidate - a session-mismatched tool
	// (grim on X11, scrot-only on Wayland) no longer aborts the capture;
	// the next usable tool takes over. Normalize options per tool: translate
	// unsupported modes (window, multi-display) into what the tool can do,
	// or skip that tool (#555, #975).
	var lastErr error
	for _, tool := range tools {
		toolOpts, err := prepareLinuxCaptureOpts(tool, opts)
		if err != nil {
			lastErr = err
			continue
		}

		var cmd *exec.Cmd
		switch tool {
		case "grim":
			cmd = buildGrimCommand(rawPath, toolOpts)
		case "gnome-screenshot":
			cmd = buildGnomeScreenshotCommand(rawPath, toolOpts)
		case "scrot":
			scrotCmd, err := buildScrotCommand(rawPath, toolOpts)
			if err != nil {
				lastErr = err
				continue
			}
			cmd = scrotCmd
		case "import":
			cmd = buildImportCommand(rawPath, toolOpts)
		default:
			lastErr = fmt.Errorf("unsupported screenshot tool: %s", tool)
			continue
		}

		out, err := cmd.CombinedOutput()
		if err != nil {
			lastErr = fmt.Errorf("%s failed: %w\n%s", tool, err, strings.TrimSpace(string(out)))
			continue
		}
		// Found a working tool - finalize below re-reads the file.
		break
	}
	if lastErr != nil && !fileExists(rawPath) {
		return ScreenshotResult{}, lastErr
	}

	img, err := finalizeImage(rawPath, opts)
	if err != nil {
		return ScreenshotResult{}, err
	}

	result := ScreenshotResult{Image: img}
	if opts.OutputPath != "" {
		result.SavedPath = opts.OutputPath
	}
	return result, nil
}

// prepareLinuxCaptureOpts normalizes opts for the detected tool:
//
//   - Window capture (#555): grim resolves the window geometry via hyprctl
//     into a Region; tools that cannot target a window by title fail with an
//     explicit, actionable error instead of returning the wrong image.
//   - gnome-screenshot limits (#975): no CLI region capture and no per-output
//     selection, so Display>1/Region fail explicitly (same treatment).
//   - Best-effort display selection (#555): most tools cannot select an
//     output by index, so translate the 1-based display index into a region
//     covering that output (geometry from xrandr/wlr-randr). Region and
//     Window take precedence; resolution failure falls back to the default
//     output.
func prepareLinuxCaptureOpts(tool string, opts ScreenshotOptions) (ScreenshotOptions, error) {
	if opts.Window != "" {
		switch tool {
		case "grim":
			region, err := resolveLinuxWindowRegion(opts.Window)
			if err != nil {
				return opts, fmt.Errorf(
					"window capture with grim: %w (grim window capture requires hyprctl; alternatives: scrot or imagemagick import on X11)", err)
			}
			opts.Window = ""
			opts.Region = &region
		case "gnome-screenshot":
			return opts, fmt.Errorf(
				"gnome-screenshot cannot capture a specific window by title; use grim with hyprctl, scrot, or imagemagick (import) for window capture")
		}
	}

	if tool == "gnome-screenshot" {
		if err := gnomeScreenshotUnsupportedOpts(opts); err != nil {
			return opts, err
		}
	}

	if opts.Window == "" && opts.Region == nil && opts.Display > 1 {
		if region, err := linuxDisplayBounds(opts.Display); err == nil {
			opts.Region = &region
		}
	}
	return opts, nil
}

func detectLinuxScreenshotTool() string {
	if tools := candidateLinuxScreenshotTools(); len(tools) > 0 {
		return tools[0]
	}
	return ""
}

// candidateLinuxScreenshotTools returns installed screenshot tools ordered
// for the current session type (#1571-A): on X11, an incidental grim
// (pulled in by distro deps) was tried first and its guaranteed failure
// (no wlroots connection) aborted the whole capture even though scrot/
// import were perfectly usable; the mirror on Wayland with only scrot
// installed silently grabbed the XWayland virtual screen. Preferred set
// first, then everything else as fallback.
func candidateLinuxScreenshotTools() []string {
	installed := func(names ...string) []string {
		var out []string
		for _, n := range names {
			if _, err := exec.LookPath(n); err == nil {
				out = append(out, n)
			}
		}
		return out
	}
	sessionType := strings.ToLower(strings.TrimSpace(os.Getenv("XDG_SESSION_TYPE")))
	switch sessionType {
	case "x11":
		return append(installed("scrot", "import", "gnome-screenshot"), installed("grim")...)
	case "wayland":
		return append(installed("grim", "gnome-screenshot"), installed("scrot", "import")...)
	default:
		return installed("grim", "gnome-screenshot", "scrot", "import")
	}
}

func linuxScreenshotToolsMissingError() error {
	sessionType := strings.ToLower(strings.TrimSpace(os.Getenv("XDG_SESSION_TYPE")))
	switch sessionType {
	case "wayland":
		return fmt.Errorf("screenshot on Wayland requires grim or gnome-screenshot. Install one of them, then try again")
	case "x11":
		return fmt.Errorf("screenshot on X11 requires scrot, gnome-screenshot, or imagemagick (import). Install one of them, then try again")
	default:
		return fmt.Errorf("screenshot on Linux requires a tool such as grim (Wayland), gnome-screenshot, scrot, or imagemagick (import). Install one of them, then try again")
	}
}

// ListDisplays returns display information on Linux using xrandr or wlr-randr.
func ListDisplays() ([]DisplayInfo, error) {
	// #1571-B: on Wayland, xrandr (XWayland) reports the single virtual
	// screen's PHYSICAL geometry while grim -g wants wlroots LOGICAL
	// coordinates - the geometry handed to grim was wrong for any
	// multi-display setup (the #1258 wlr-randr class, resurrected by the
	// xrandr-first ordering). Wayland sessions try wlr-randr first.
	if strings.EqualFold(strings.TrimSpace(os.Getenv("XDG_SESSION_TYPE")), "wayland") {
		if _, err := exec.LookPath("wlr-randr"); err == nil {
			return listDisplaysWlrrandr()
		}
	}
	if _, err := exec.LookPath("xrandr"); err == nil {
		return listDisplaysXrandr()
	}
	if _, err := exec.LookPath("wlr-randr"); err == nil {
		return listDisplaysWlrrandr()
	}
	return nil, fmt.Errorf("no display info tool found (install xrandr for X11 or wlr-randr for Wayland)")
}

func listDisplaysXrandr() ([]DisplayInfo, error) {
	out, err := exec.Command("xrandr", "--query").Output()
	if err != nil {
		return nil, fmt.Errorf("xrandr failed: %w", err)
	}
	var displays []DisplayInfo
	idx := 0
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, " connected") {
			idx++
			displays = append(displays, parseXrandrLine(line, idx))
		}
	}
	return displays, nil
}

func parseXrandrLine(line string, index int) DisplayInfo {
	di := DisplayInfo{Index: index}
	fields := strings.Fields(line)
	for i, f := range fields {
		if f == "primary" {
			di.IsPrimary = true
		}
		if strings.Contains(f, "x") && strings.Contains(f, "+") {
			parts := strings.Split(f, "+")
			if len(parts) >= 3 {
				res := strings.Split(parts[0], "x")
				if len(res) == 2 {
					di.Width, _ = strconv.Atoi(res[0])
					di.Height, _ = strconv.Atoi(res[1])
				}
				di.X, _ = strconv.Atoi(parts[1])
				di.Y, _ = strconv.Atoi(parts[2])
			}
		}
		if i == 0 {
			di.Name = f
		}
	}
	return di
}

func listDisplaysWlrrandr() ([]DisplayInfo, error) {
	out, err := exec.Command("wlr-randr").Output()
	if err != nil {
		return nil, fmt.Errorf("wlr-randr failed: %w", err)
	}
	return parseWlrrandrOutput(string(out)), nil
}

// ListWindows returns capturable windows on Linux using wmctrl.
func ListWindows() ([]WindowInfo, error) {
	if _, err := exec.LookPath("wmctrl"); err != nil {
		return nil, fmt.Errorf("window listing requires wmctrl. Install wmctrl, then try again")
	}
	out, err := exec.Command("wmctrl", "-lx").Output()
	if err != nil {
		return nil, fmt.Errorf("wmctrl failed: %w", err)
	}
	// #975: parse the padded fixed-width columns with strings.Fields, not a
	// single-space SplitN (which broke App/Title alignment and dropped lines).
	return parseWmctrlOutput(string(out)), nil
}

func buildGrimCommand(outPath string, opts ScreenshotOptions) *exec.Cmd {
	args := []string{}
	if opts.Region != nil {
		r := opts.Region
		args = append(args, "-g", fmt.Sprintf("%d,%d,%dx%d", r.X, r.Y, r.Width, r.Height))
	}
	args = append(args, outPath)
	return exec.Command("grim", args...)
}

func buildGnomeScreenshotCommand(outPath string, opts ScreenshotOptions) *exec.Cmd {
	// #975: no --display= is passed anymore. The flag is a GDK/X11 connection
	// name (host:D.S like ":0"), not an xrandr output name — passing "HDMI-A-1"
	// made gnome-screenshot fail with "cannot open display". Display/Region
	// requests are rejected earlier by gnomeScreenshotUnsupportedOpts.
	_ = opts
	return exec.Command("gnome-screenshot", "-f", outPath)
}
func buildScrotCommand(outPath string, opts ScreenshotOptions) (*exec.Cmd, error) {
	args := []string{"-z"}
	if opts.Region != nil {
		r := opts.Region
		// #762: scrot -a takes X,Y,W,H (width/height), not bottom-right
		// coordinates. Passing X+W/Y+H overshot every region and fell off
		// multi-monitor edges; X=Y=0 masked it in the full-screen test path.
		args = append(args, "-a", fmt.Sprintf("%d,%d,%d,%d", r.X, r.Y, r.Width, r.Height))
	}
	if opts.Window != "" {
		// #555: resolve the title to a concrete window ID via wmctrl.
		// #1259: a resolution failure used to silently append "-u" (the
		// CURRENTLY FOCUSED window) — the agent asked for "Firefox", got a
		// screenshot of the terminal, and reasoned from the wrong image with
		// a success status. Fail explicitly like the grim path instead; there
		// is no "focused window" request semantic that could justify -u.
		wid, err := matchLinuxWindowID(opts.Window)
		if err != nil {
			return nil, fmt.Errorf(
				"window capture with scrot: %w (scrot window capture requires wmctrl; alternatives: grim with hyprctl, or imagemagick import)", err)
		}
		args = append(args, "-i", fmt.Sprintf("0x%x", wid))
	}
	args = append(args, outPath)
	return exec.Command("scrot", args...), nil
}

func buildImportCommand(outPath string, opts ScreenshotOptions) *exec.Cmd {
	if opts.Window != "" {
		// #555: a raw title query almost never matches X11 WM_NAME, so resolve
		// to a concrete window ID first (exact title match preferred); only fall
		// back to the raw query if resolution is unavailable.
		if wid, err := matchLinuxWindowID(opts.Window); err == nil {
			return exec.Command("import", "-window", fmt.Sprintf("0x%x", wid), outPath)
		}
		return exec.Command("import", "-window", opts.Window, outPath)
	}
	args := []string{"-window", "root"}
	if opts.Region != nil {
		r := opts.Region
		args = append(args, "-crop", fmt.Sprintf("%dx%d+%d+%d", r.Width, r.Height, r.X, r.Y))
	}
	args = append(args, outPath)
	return exec.Command("import", args...)
}

// matchLinuxWindowID resolves a title/app query to an X11 window ID via
// wmctrl. Matching semantics (exact-first) live in matchWindowQuery (#555).
func matchLinuxWindowID(query string) (int, error) {
	windows, err := ListWindows()
	if err != nil {
		return 0, err
	}
	return matchWindowQuery(windows, query)
}

// resolveLinuxWindowRegion returns the geometry of the window matching query
// using hyprctl (Hyperland compositor). grim has no window mode, so the
// geometry is applied via -g. Other compositors are not guessed at: callers
// surface an explicit error rather than silently capturing the full screen.
func resolveLinuxWindowRegion(query string) (ScreenshotRegion, error) {
	if _, err := exec.LookPath("hyprctl"); err != nil {
		return ScreenshotRegion{}, fmt.Errorf("hyprctl not found")
	}
	out, err := exec.Command("hyprctl", "-j", "clients").Output()
	if err != nil {
		return ScreenshotRegion{}, fmt.Errorf("hyprctl failed: %w", err)
	}
	var clients []struct {
		Title    string `json:"title"`
		Class    string `json:"class"`
		Position struct {
			X int `json:"x"`
			Y int `json:"y"`
		} `json:"position"`
		Size struct {
			Width  int `json:"width"`
			Height int `json:"height"`
		} `json:"size"`
	}
	if err := json.Unmarshal(out, &clients); err != nil {
		return ScreenshotRegion{}, fmt.Errorf("parsing hyprctl clients: %w", err)
	}

	// Exact match (title, then class) before substring match, mirroring
	// matchLinuxWindowID's preference ordering.
	q := strings.ToLower(query)
	found := -1
	for _, pass := range []struct{ exact bool }{{true}, {false}} {
		for i, c := range clients {
			title, class := strings.ToLower(c.Title), strings.ToLower(c.Class)
			var match bool
			if pass.exact {
				match = title == q || class == q
			} else {
				match = strings.Contains(title, q) || strings.Contains(class, q)
			}
			if match {
				found = i
				break
			}
		}
		if found >= 0 {
			break
		}
	}
	if found < 0 {
		return ScreenshotRegion{}, fmt.Errorf("no hyprland window matching %q", query)
	}
	c := clients[found]
	return ScreenshotRegion{X: c.Position.X, Y: c.Position.Y, Width: c.Size.Width, Height: c.Size.Height}, nil
}

// linuxDisplayBounds resolves a 1-based display index to its on-screen
// geometry using xrandr (X11) or wlr-randr (Wayland) via ListDisplays.
func linuxDisplayBounds(index int) (ScreenshotRegion, error) {
	displays, err := ListDisplays()
	if err != nil {
		return ScreenshotRegion{}, err
	}
	if index < 1 || index > len(displays) {
		return ScreenshotRegion{}, fmt.Errorf("display %d not found (%d displays)", index, len(displays))
	}
	d := displays[index-1]
	if d.Width <= 0 || d.Height <= 0 {
		return ScreenshotRegion{}, fmt.Errorf("display %d geometry unknown", index)
	}
	return ScreenshotRegion{X: d.X, Y: d.Y, Width: d.Width, Height: d.Height}, nil
}

// Guard against unused import warnings on some build paths.
var _ = filepath.Join

// fileExists reports whether the given path exists (#1571-A capture loop).
func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
