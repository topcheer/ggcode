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

	toolSucceeded, lastErr := runLinuxToolCandidates(tools, rawPath, opts)

	if !toolSucceeded && lastErr != nil {
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

func runLinuxToolCandidates(tools []string, rawPath string, opts ScreenshotOptions) (bool, error) {
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

		return true, nil
	}
	return false, lastErr
}

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

	if opts.Window == "" && opts.Region == nil && opts.Display >= 1 {
		if region, err := linuxDisplayBounds(opts.Display); err == nil {
			opts.Region = &region
		}
	} else if opts.Window == "" && opts.Region == nil && opts.Display == 0 {

		if region, err := linuxDisplayRegionForFn(0); err == nil {
			opts.Region = &region
		}
	}
	return opts, nil
}

func primaryDisplayIndex(displays []DisplayInfo) int {
	for i, d := range displays {
		if d.IsPrimary {
			return i + 1
		}
	}
	return 1
}

func linuxDisplayRegionFor(_ int) (ScreenshotRegion, error) {
	displays, err := ListDisplays()
	if err != nil {
		return ScreenshotRegion{}, err
	}
	if len(displays) == 0 {
		return ScreenshotRegion{}, fmt.Errorf("no displays reported")
	}
	return linuxDisplayBounds(primaryDisplayIndex(displays))
}

var linuxDisplayRegionForFn = linuxDisplayRegionFor

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

func ListDisplays() ([]DisplayInfo, error) {

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

func ListWindows() ([]WindowInfo, error) {
	if _, err := exec.LookPath("wmctrl"); err != nil {
		return nil, fmt.Errorf("window listing requires wmctrl. Install wmctrl, then try again")
	}
	out, err := exec.Command("wmctrl", "-lx").Output()
	if err != nil {
		return nil, fmt.Errorf("wmctrl failed: %w", err)
	}

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

	_ = opts
	return exec.Command("gnome-screenshot", "-f", outPath)
}
func buildScrotCommand(outPath string, opts ScreenshotOptions) (*exec.Cmd, error) {
	args := []string{"-z"}
	if opts.Region != nil {
		r := opts.Region

		args = append(args, "-a", fmt.Sprintf("%d,%d,%d,%d", r.X, r.Y, r.Width, r.Height))
	}
	if opts.Window != "" {

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

func matchLinuxWindowID(query string) (int, error) {
	windows, err := ListWindows()
	if err != nil {
		return 0, err
	}
	return matchWindowQuery(windows, query)
}

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

var _ = filepath.Join
