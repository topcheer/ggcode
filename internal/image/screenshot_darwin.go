//go:build darwin

package image

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// CaptureScreen captures a screenshot on macOS using screencapture.
func CaptureScreen(opts ScreenshotOptions) (ScreenshotResult, error) {
	applyDelay(opts.DelayMs)

	rawPath, cleanup := createTempScreenshotPath(opts)
	defer cleanup()

	args := []string{"-x"} // silent
	if opts.Cursor {
		args = append(args, "-C") // include cursor
	}

	// Window capture takes precedence.
	if opts.Window != "" {
		windowID, err := findMacWindowID(opts.Window)
		if err != nil {
			return ScreenshotResult{}, fmt.Errorf("finding window: %w", err)
		}
		args = append(args, "-l", windowID)
	} else if opts.Region != nil {
		r := opts.Region
		args = append(args, "-R",
			fmt.Sprintf("%d,%d,%d,%d", r.X, r.Y, r.Width, r.Height))
	} else if opts.Display > 0 {
		args = append(args, "-D", strconv.Itoa(opts.Display))
	}

	args = append(args, rawPath)

	cmd := exec.Command("screencapture", args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return ScreenshotResult{}, fmt.Errorf("screencapture failed: %w\n%s",
			err, strings.TrimSpace(string(out)))
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

// ListDisplays returns information about available displays on macOS.
func ListDisplays() ([]DisplayInfo, error) {
	out, err := exec.Command("system_profiler", "SPDisplaysDataType", "-json").Output()
	if err != nil {
		return nil, fmt.Errorf("system_profiler failed: %w", err)
	}

	var data struct {
		SPDisplaysDataType []struct {
			SpdisplaysNdrvs []struct {
				SPDisplaysResolution string `json:"_spdisplays_resolution"`
				Name                 string `json:"_name"`
			} `json:"spdisplays_ndrvs"`
		} `json:"SPDisplaysDataType"`
	}
	if err := json.Unmarshal(out, &data); err != nil {
		return nil, fmt.Errorf("parsing display info: %w", err)
	}

	var displays []DisplayInfo
	for i, hw := range data.SPDisplaysDataType {
		for _, disp := range hw.SpdisplaysNdrvs {
			w, h := parseMacResolution(disp.SPDisplaysResolution)
			displays = append(displays, DisplayInfo{
				Index:     i + 1,
				IsPrimary: i == 0,
				Width:     w,
				Height:    h,
				Name:      disp.Name,
			})
		}
	}
	if len(displays) == 0 {
		displays = []DisplayInfo{{Index: 1, IsPrimary: true}}
	}
	return displays, nil
}

// ListWindows returns capturable windows on macOS.
// Uses CGWindowListCopyWindowInfo via Swift to get CGWindowIDs, which are
// the IDs that screencapture -l expects. AppleScript's "id of window" returns
// Accessibility API IDs which are in a completely different number space.
func ListWindows() ([]WindowInfo, error) {
	// Swift snippet that uses Core Graphics to list on-screen windows.
	// Each line: CGWindowID\tOwnerName\tWindowName\tX\tY\tW\tH
	swiftCode := `
import Cocoa
let windows = CGWindowListCopyWindowInfo([.optionOnScreenOnly, .excludeDesktopElements], kCGNullWindowID) as! [[String: Any]]
for w in windows {
    let wid = (w["kCGWindowNumber"] as? Int) ?? 0
    let owner = (w["kCGWindowOwnerName"] as? String) ?? ""
    let title = (w["kCGWindowName"] as? String) ?? ""
    let layer = (w["kCGWindowLayer"] as? Int) ?? 0
    if layer != 0 { continue }
    if owner.isEmpty { continue }
    guard let bounds = w["kCGWindowBounds"] as? [String: CGFloat] else { continue }
    let x = Int(bounds["X"] ?? 0)
    let y = Int(bounds["Y"] ?? 0)
    let width = Int(bounds["Width"] ?? 0)
    let height = Int(bounds["Height"] ?? 0)
    if width < 10 || height < 10 { continue }
    print("\(wid)\t\(owner)\t\(title)\t\(x)\t\(y)\t\(width)\t\(height)")
}
`
	cmd := exec.Command("swift", "-e", swiftCode)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("listing windows via Core Graphics: %w", err)
	}

	var windows []WindowInfo
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) < 7 {
			continue
		}
		id, _ := strconv.Atoi(fields[0])
		x, _ := strconv.Atoi(fields[3])
		y, _ := strconv.Atoi(fields[4])
		w, _ := strconv.Atoi(fields[5])
		h, _ := strconv.Atoi(fields[6])
		windows = append(windows, WindowInfo{
			ID:     id,
			App:    fields[1],
			Title:  fields[2],
			X:      x,
			Y:      y,
			Width:  w,
			Height: h,
		})
	}
	return windows, nil
}

// findMacWindowID finds the window ID matching the given query string.
// Matches by title substring or app name (case-insensitive).
func findMacWindowID(query string) (string, error) {
	windows, err := ListWindows()
	if err != nil {
		return "", err
	}
	q := strings.ToLower(query)
	for _, w := range windows {
		if strings.Contains(strings.ToLower(w.Title), q) ||
			strings.Contains(strings.ToLower(w.App), q) {
			return strconv.Itoa(w.ID), nil
		}
	}
	return "", fmt.Errorf("no window found matching %q", query)
}

func parseMacResolution(res string) (int, int) {
	parts := strings.FieldsFunc(res, func(r rune) bool {
		return r == ' ' || r == 'x' || r == 'X'
	})
	var nums []int
	for _, p := range parts {
		if n, err := strconv.Atoi(p); err == nil {
			nums = append(nums, n)
		}
	}
	if len(nums) >= 2 {
		return nums[0], nums[1]
	}
	return 0, 0
}
