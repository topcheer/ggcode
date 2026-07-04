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
func ListWindows() ([]WindowInfo, error) {
	script := `
tell application "System Events"
  set output to ""
  repeat with p in (every process whose background only is false)
    set pname to name of p
    try
      repeat with w in windows of p
        set wname to name of w
        try
          set wid to id of w
        on error
          set wid to 0
        end try
        try
          set wpos to position of w
          set wsize to size of w
          set wx to item 1 of wpos
          set wy to item 2 of wpos
          set ww to item 1 of wsize
          set wh to item 2 of wsize
          set output to output & pname & "\t" & wname & "\t" & wid & "\t" & wx & "\t" & wy & "\t" & ww & "\t" & wh & "\n"
        end try
      end repeat
    end try
  end repeat
  return output
end tell
`
	cmd := exec.Command("osascript", "-e", script)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("listing windows (accessibility permission may be required): %w", err)
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
		id, _ := strconv.Atoi(fields[2])
		x, _ := strconv.Atoi(fields[3])
		y, _ := strconv.Atoi(fields[4])
		w, _ := strconv.Atoi(fields[5])
		h, _ := strconv.Atoi(fields[6])
		windows = append(windows, WindowInfo{
			ID:     id,
			App:    fields[0],
			Title:  fields[1],
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
