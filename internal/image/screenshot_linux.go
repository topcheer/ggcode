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
	tool := detectLinuxScreenshotTool()
	if tool == "" {
		return ScreenshotResult{}, linuxScreenshotToolsMissingError()
	}

	applyDelay(opts.DelayMs)

	rawPath, cleanup := createTempScreenshotPath(opts)
	defer cleanup()

	// #555: window capture support varies by tool. Previously grim and
	// gnome-screenshot silently captured the FULL screen when opts.Window was
	// set. Now grim resolves the window geometry via hyprctl, and tools that
	// cannot target a window by title fail with an explicit, actionable error
	// instead of returning the wrong image.
	if opts.Window != "" {
		switch tool {
		case "grim":
			region, err := resolveLinuxWindowRegion(opts.Window)
			if err != nil {
				return ScreenshotResult{}, fmt.Errorf(
					"window capture with grim: %w (grim window capture requires hyprctl; alternatives: scrot or imagemagick import on X11)", err)
			}
			opts.Window = ""
			opts.Region = &region
		case "gnome-screenshot":
			return ScreenshotResult{}, fmt.Errorf(
				"gnome-screenshot cannot capture a specific window by title; use grim with hyprctl, scrot, or imagemagick (import) for window capture")
		}
	}

	// #555: best-effort display selection. Most tools below cannot select an
	// output by index, so translate the 1-based display index into a region
	// covering that output (geometry from xrandr/wlr-randr). Region and Window
	// take precedence. Resolution failure falls back to the default output.
	if opts.Window == "" && opts.Region == nil && opts.Display > 1 {
		if region, err := linuxDisplayBounds(opts.Display); err == nil {
			opts.Region = &region
		}
	}

	var cmd *exec.Cmd
	switch tool {
	case "grim":
		cmd = buildGrimCommand(rawPath, opts)
	case "gnome-screenshot":
		cmd = buildGnomeScreenshotCommand(rawPath, opts)
	case "scrot":
		cmd = buildScrotCommand(rawPath, opts)
	case "import":
		cmd = buildImportCommand(rawPath, opts)
	default:
		return ScreenshotResult{}, fmt.Errorf("unsupported screenshot tool: %s", tool)
	}

	if out, err := cmd.CombinedOutput(); err != nil {
		return ScreenshotResult{}, fmt.Errorf("%s failed: %w\n%s", tool, err, strings.TrimSpace(string(out)))
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

func detectLinuxScreenshotTool() string {
	for _, tool := range []string{"grim", "gnome-screenshot", "scrot", "import"} {
		if _, err := exec.LookPath(tool); err == nil {
			return tool
		}
	}
	return ""
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
	var displays []DisplayInfo
	idx := 0
	var current DisplayInfo
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") {
			if current.Name != "" {
				idx++
				current.Index = idx
				displays = append(displays, current)
			}
			current = DisplayInfo{Name: line}
			continue
		}
		if strings.HasPrefix(line, "Mode:") {
			fields := strings.Fields(line)
			for _, f := range fields {
				if strings.Contains(f, "x") {
					parts := strings.Split(f, "x")
					if len(parts) == 2 {
						current.Width, _ = strconv.Atoi(parts[0])
						current.Height, _ = strconv.Atoi(parts[1])
					}
				}
			}
		}
	}
	if current.Name != "" {
		idx++
		current.Index = idx
		displays = append(displays, current)
	}
	return displays, nil
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
	var windows []WindowInfo
	for _, line := range strings.Split(string(out), "\n") {
		if len(line) < 10 {
			continue
		}
		fields := strings.SplitN(line, " ", 5)
		if len(fields) < 5 {
			continue
		}
		id, _ := strconv.ParseInt(fields[0], 0, 64)
		clsParts := strings.SplitN(fields[2], ".", 2)
		app := fields[2]
		if len(clsParts) > 0 {
			app = clsParts[len(clsParts)-1]
		}
		title := fields[4]
		windows = append(windows, WindowInfo{
			ID:    int(id),
			App:   app,
			Title: title,
		})
	}
	return windows, nil
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
	args := []string{"-f", outPath}
	// #555: pass the requested display through when its name can be resolved.
	if opts.Display > 1 {
		if name, err := linuxDisplayName(opts.Display); err == nil {
			args = append([]string{"--display=" + name}, args...)
		}
	}
	return exec.Command("gnome-screenshot", args...)
}

func buildScrotCommand(outPath string, opts ScreenshotOptions) *exec.Cmd {
	args := []string{"-z"}
	if opts.Region != nil {
		r := opts.Region
		args = append(args, "-a", fmt.Sprintf("%d,%d,%d,%d", r.X, r.Y, r.X+r.Width, r.Y+r.Height))
	}
	if opts.Window != "" {
		// #555: prefer the resolved window ID (exact title match first via
		// wmctrl) over "-u" (focused window), which may not be the requested one.
		if wid, err := matchLinuxWindowID(opts.Window); err == nil {
			args = append(args, "-i", fmt.Sprintf("0x%x", wid))
		} else {
			args = append(args, "-u") // focused window fallback
		}
	}
	args = append(args, outPath)
	return exec.Command("scrot", args...)
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

// linuxDisplayName resolves a 1-based display index to its output name
// (e.g. "HDMI-A-1") for tools like gnome-screenshot --display=NAME.
func linuxDisplayName(index int) (string, error) {
	displays, err := ListDisplays()
	if err != nil {
		return "", err
	}
	if index < 1 || index > len(displays) {
		return "", fmt.Errorf("display %d not found (%d displays)", index, len(displays))
	}
	if displays[index-1].Name == "" {
		return "", fmt.Errorf("display %d name unknown", index)
	}
	return displays[index-1].Name, nil
}

// Guard against unused import warnings on some build paths.
var _ = filepath.Join
