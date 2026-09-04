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

	rawPath, cleanup, err := createTempScreenshotPath(opts)
	if err != nil {
		return ScreenshotResult{}, err
	}
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
		// #1570-D: screencapture -D expects a CGDirectDisplayID, but
		// opts.Display is OUR 1-based DisplayInfo.Index (the main display's
		// CGDirectDisplayID happens to be 1, so single-screen setups worked
		// by coincidence; a second screen's ID is a large 32-bit number and
		// -D 2 fails). Capture by the display's on-screen rectangle instead.
		if r, err := macDisplayRegion(opts.Display); err == nil {
			args = append(args, "-R",
				fmt.Sprintf("%d,%d,%d,%d", r.X, r.Y, r.Width, r.Height))
		} else {
			args = append(args, "-D", strconv.Itoa(opts.Display))
		}
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
	return parseSPDisplaysJSON(out)
}

// macDisplayEntry is one flattened display unit from SPDisplaysDataType JSON.
type macDisplayEntry struct {
	Name   string
	Width  int
	Height int
	Main   bool // spdisplays_main == spdisplays_yes
	X, Y   int
}

// parseSPDisplaysJSON turns system_profiler SPDisplaysDataType JSON into
// DisplayInfo entries. Displays are flattened across GPU entries and
// renumbered 1..N over OUTPUT units (#555): the old code used the GPU-entry
// index, so a single GPU driving two monitors reported both as Index:1
// IsPrimary:true. IsPrimary prefers the entry with spdisplays_main=yes (real
// primary) and falls back to the first display. X/Y come from
// _spdisplays_display-vsa when present (multi-monitor negative origins);
// older macOS versions without that field leave them at 0.
func parseSPDisplaysJSON(out []byte) ([]DisplayInfo, error) {
	var data struct {
		SPDisplaysDataType []struct {
			SpdisplaysNdrvs []struct {
				SPDisplaysResolution string `json:"_spdisplays_resolution"`
				Name                 string `json:"_name"`
				Main                 string `json:"spdisplays_main"`
				DisplayVsa           struct {
					OffsetX int `json:"OffsetX"`
					OffsetY int `json:"OffsetY"`
				} `json:"_spdisplays_display-vsa"`
			} `json:"spdisplays_ndrvs"`
		} `json:"SPDisplaysDataType"`
	}
	if err := json.Unmarshal(out, &data); err != nil {
		return nil, fmt.Errorf("parsing display info: %w", err)
	}

	var flat []macDisplayEntry
	for _, hw := range data.SPDisplaysDataType {
		for _, disp := range hw.SpdisplaysNdrvs {
			w, h := parseMacResolution(disp.SPDisplaysResolution)
			flat = append(flat, macDisplayEntry{
				Name:   disp.Name,
				Width:  w,
				Height: h,
				Main:   disp.Main == "spdisplays_yes",
				X:      disp.DisplayVsa.OffsetX,
				Y:      disp.DisplayVsa.OffsetY,
			})
		}
	}

	// Pick the primary: the spdisplays_main entry when present, else the first.
	primary := 0
	for i, d := range flat {
		if d.Main {
			primary = i
			break
		}
	}

	var displays []DisplayInfo
	for i, d := range flat {
		displays = append(displays, DisplayInfo{
			Index:     i + 1, // renumber over output units, not GPU units
			IsPrimary: i == primary,
			Width:     d.Width,
			Height:    d.Height,
			X:         d.X,
			Y:         d.Y,
			Name:      d.Name,
		})
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
// Exact (case-insensitive) title/app matches are preferred over substring
// matches so short queries do not land on unrelated windows (#555).
func findMacWindowID(query string) (string, error) {
	windows, err := ListWindows()
	if err != nil {
		return "", err
	}
	id, err := matchWindowQuery(windows, query)
	if err != nil {
		return "", err
	}
	return strconv.Itoa(id), nil
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

// macDisplayRegion resolves OUR 1-based display index to its on-screen
// rectangle (#1570-D) - used to route display capture through -R because
// screencapture -D expects CGDirectDisplayIDs, not indexes.
func macDisplayRegion(index int) (ScreenshotRegion, error) {
	displays, err := ListDisplays()
	if err != nil {
		return ScreenshotRegion{}, err
	}
	for _, d := range displays {
		if d.Index == index {
			return ScreenshotRegion{X: d.X, Y: d.Y, Width: d.Width, Height: d.Height}, nil
		}
	}
	return ScreenshotRegion{}, fmt.Errorf("display %d not found", index)
}
