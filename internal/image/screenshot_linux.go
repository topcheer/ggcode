//go:build linux

package image

import (
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
		args = append(args, "-g", fmt.Sprintf("%d,%d,%d,%d", r.Width, r.Height, r.X, r.Y))
	}
	args = append(args, outPath)
	return exec.Command("grim", args...)
}

func buildGnomeScreenshotCommand(outPath string, opts ScreenshotOptions) *exec.Cmd {
	return exec.Command("gnome-screenshot", "-f", outPath)
}

func buildScrotCommand(outPath string, opts ScreenshotOptions) *exec.Cmd {
	args := []string{"-z"}
	if opts.Region != nil {
		r := opts.Region
		args = append(args, "-a", fmt.Sprintf("%d,%d,%d,%d", r.X, r.Y, r.X+r.Width, r.Y+r.Height))
	}
	if opts.Window != "" {
		args = append(args, "-u") // focused window only
	}
	args = append(args, outPath)
	return exec.Command("scrot", args...)
}

func buildImportCommand(outPath string, opts ScreenshotOptions) *exec.Cmd {
	if opts.Window != "" {
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

// Guard against unused import warnings on some build paths.
var _ = filepath.Join
