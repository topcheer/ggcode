//go:build darwin

package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// iosBackend implements mobileBackend using xcrun simctl on macOS.
// Only compiled on darwin platforms.
type iosBackend struct {
	xcrunPath  string
	cachedUDID string
}

func newIOSBackend() *iosBackend {
	path, err := exec.LookPath("xcrun")
	if err != nil {
		return nil
	}
	return &iosBackend{xcrunPath: path}
}

func (b *iosBackend) defaultDevice() string {
	if b.cachedUDID != "" {
		return b.cachedUDID
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, _, err := runCommand(ctx, 10*time.Second, b.xcrunPath, "simctl", "list", "devices", "available", "--json")
	if err != nil {
		return ""
	}
	var list struct {
		Devices map[string][]struct {
			UDID  string `json:"udid"`
			State string `json:"state"`
			Name  string `json:"name"`
		} `json:"devices"`
	}
	if err := json.Unmarshal([]byte(out), &list); err != nil {
		return ""
	}
	// Find first booted device
	for _, devs := range list.Devices {
		for _, d := range devs {
			if d.State == "Booted" {
				b.cachedUDID = d.UDID
				return d.UDID
			}
		}
	}
	return ""
}

func (b *iosBackend) devices(ctx context.Context) (Result, error) {
	out, stderr, err := runCommand(ctx, 10*time.Second, b.xcrunPath, "simctl", "list", "devices", "available", "--json")
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("simctl list failed: %v\n%s", err, stderr)}, nil
	}

	var list struct {
		Devices map[string][]struct {
			UDID  string `json:"udid"`
			State string `json:"state"`
			Name  string `json:"name"`
		} `json:"devices"`
	}
	if err := json.Unmarshal([]byte(out), &list); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("failed to parse devices: %v", err)}, nil
	}

	var sb strings.Builder
	sb.WriteString("Available iOS Simulators:\n")
	bootedCount := 0
	totalCount := 0

	// Sort runtimes for consistent output
	runtimes := make([]string, 0, len(list.Devices))
	for rt := range list.Devices {
		runtimes = append(runtimes, rt)
	}
	sort.Strings(runtimes)

	for _, runtime := range runtimes {
		devs := list.Devices[runtime]
		// Filter to only show devices (not clones)
		var realDevs []struct {
			UDID  string `json:"udid"`
			State string `json:"state"`
			Name  string `json:"name"`
		}
		for _, d := range devs {
			if d.Name != "" {
				realDevs = append(realDevs, d)
			}
		}
		if len(realDevs) == 0 {
			continue
		}
		shortRT := runtime
		if idx := strings.LastIndex(runtime, "."); idx >= 0 {
			shortRT = runtime[idx+1:]
		}
		sb.WriteString(fmt.Sprintf("\n  %s:\n", shortRT))
		for _, d := range realDevs {
			totalCount++
			state := d.State
			if state == "Booted" {
				bootedCount++
				state = "Booted"
			}
			sb.WriteString(fmt.Sprintf("    %s [%s] %s\n", d.Name, state, d.UDID))
		}
	}
	sb.WriteString(fmt.Sprintf("\n%d device(s), %d booted.\n", totalCount, bootedCount))
	if totalCount == 0 {
		sb.WriteString("No simulators installed. Run: xcrun simctl create ...")
	}
	return Result{Content: sb.String()}, nil
}

func (b *iosBackend) boot(ctx context.Context, device string) (Result, error) {
	_, stderr, err := runCommand(ctx, 60*time.Second, b.xcrunPath, "simctl", "boot", device)
	if err != nil {
		// Already booted is not an error
		if strings.Contains(stderr, "already booted") || strings.Contains(stderr, "Unable to boot device in current state") {
			return Result{Content: fmt.Sprintf("Device %s is already booted.", device)}, nil
		}
		return Result{IsError: true, Content: fmt.Sprintf("boot failed: %v\n%s", err, stderr)}, nil
	}
	// Open Simulator app
	exec.Command("open", "-a", "Simulator").Start()
	return Result{Content: fmt.Sprintf("Booted simulator %s.", device)}, nil
}

func (b *iosBackend) install(ctx context.Context, device, appPath string) (Result, error) {
	_, stderr, err := runCommand(ctx, 60*time.Second, b.xcrunPath, "simctl", "install", device, appPath)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("install failed: %v\n%s", err, stderr)}, nil
	}
	return Result{Content: fmt.Sprintf("Installed %s on %s.", appPath, device)}, nil
}

func (b *iosBackend) uninstall(ctx context.Context, device, pkg string) (Result, error) {
	_, stderr, err := runCommand(ctx, 30*time.Second, b.xcrunPath, "simctl", "uninstall", device, pkg)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("uninstall failed: %v\n%s", err, stderr)}, nil
	}
	return Result{Content: fmt.Sprintf("Uninstalled %s from %s.", pkg, device)}, nil
}

func (b *iosBackend) launch(ctx context.Context, device, pkg string) (Result, error) {
	out, stderr, err := runCommand(ctx, 15*time.Second, b.xcrunPath, "simctl", "launch", device, pkg)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("launch failed: %v\n%s", err, stderr)}, nil
	}
	return Result{Content: fmt.Sprintf("Launched %s.\n%s", pkg, out)}, nil
}

func (b *iosBackend) close(ctx context.Context, device, pkg string) (Result, error) {
	_, stderr, err := runCommand(ctx, 10*time.Second, b.xcrunPath, "simctl", "terminate", device, pkg)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("terminate failed: %v\n%s", err, stderr)}, nil
	}
	return Result{Content: fmt.Sprintf("Terminated %s.", pkg)}, nil
}

func (b *iosBackend) snapshot(ctx context.Context, device string) (Result, error) {
	// iOS Simulator doesn't have a direct "accessibility tree dump" via simctl.
	// We use the screenshot + describe approach. For a text-based snapshot,
	// we try to get the app's UI via the accessibility audit.
	//
	// simctl accessibility <udid> enable  (enables accessibility)
	// Then we can query the tree via private API or AppleScript.
	// For now, we provide device info and suggest using screenshot.
	devInfo := fmt.Sprintf("Platform: iOS\nDevice: %s\n", device)

	// Try to get frontmost app
	out, _, err := runCommand(ctx, 10*time.Second, b.xcrunPath, "simctl", "spawn", device, "launchctl", "list")
	if err == nil && out != "" {
		// Parse for any app-like process
		for _, line := range strings.Split(out, "\n") {
			if strings.Contains(line, "Application") || strings.Contains(line, "UIKitApplication") {
				devInfo += "Running apps detected.\n"
				break
			}
		}
	}

	devInfo += "\nNote: iOS UI tree snapshot requires additional setup.\n" +
		"Use action=\"screenshot\" to visually inspect the UI.\n" +
		"Or install the app's accessibility audit via: xcrun simctl accessibility enable"

	return Result{Content: devInfo}, nil
}

func (b *iosBackend) screenshot(ctx context.Context, device, format string, quality int, headless bool) (Result, error) {
	tmpFile := filepath.Join(os.TempDir(), fmt.Sprintf("ggcode_ios_screenshot_%d.png", time.Now().UnixNano()))
	defer os.Remove(tmpFile)

	_, stderr, err := runCommand(ctx, 15*time.Second, b.xcrunPath, "simctl", "io", device, "screenshot", tmpFile)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("screenshot failed: %v\n%s", err, stderr)}, nil
	}

	data, err := os.ReadFile(tmpFile)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("failed to read screenshot: %v", err)}, nil
	}
	if len(data) == 0 {
		return Result{IsError: true, Content: "screenshot captured 0 bytes"}, nil
	}

	if headless {
		return Result{Content: fmt.Sprintf("Screenshot captured (%d bytes, PNG).", len(data))}, nil
	}

	return imageResult(data, format, quality,
		fmt.Sprintf("Screenshot captured (%d bytes, %s). The image is included for visual analysis.", len(data), strings.ToUpper(format))), nil
}

func (b *iosBackend) tap(ctx context.Context, device, ref string, x, y int) (Result, error) {
	// Use AppleScript to send a click to the Simulator window at the given coordinates.
	// This works because Simulator renders at a 1:1 pixel ratio on macOS.
	script := fmt.Sprintf(`
tell application "Simulator" to activate
delay 0.2
tell application "System Events"
    click at {%d, %d}
end tell`, x, y)
	_, stderr, err := runCommand(ctx, 10*time.Second, "osascript", "-e", script)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("tap failed: %v\n%s\nNote: Grant Accessibility permission to Terminal/ggcode in System Preferences > Security & Privacy > Privacy > Accessibility", err, stderr)}, nil
	}
	return Result{Content: fmt.Sprintf("Tapped at (%d, %d).", x, y)}, nil
}

func (b *iosBackend) typeText(ctx context.Context, device, ref, text string, x, y int) (Result, error) {
	if x > 0 || y > 0 {
		b.tap(ctx, device, "", x, y)
		time.Sleep(300 * time.Millisecond)
	}
	// Use AppleScript to type into the Simulator
	// Escape double quotes in text
	escapedText := strings.ReplaceAll(text, "\"", "\\\"")
	script := fmt.Sprintf(`
tell application "Simulator" to activate
delay 0.2
tell application "System Events"
    keystroke "%s"
end tell`, escapedText)
	_, stderr, err := runCommand(ctx, 10*time.Second, "osascript", "-e", script)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("type failed: %v\n%s", err, stderr)}, nil
	}
	return Result{Content: fmt.Sprintf("Typed: %q", text)}, nil
}

func (b *iosBackend) swipe(ctx context.Context, device, ref string, x, y, endX, endY int) (Result, error) {
	// Use AppleScript CGEvent-based mouse drag
	steps := 5
	scriptParts := []string{
		`tell application "Simulator" to activate`,
		`delay 0.2`,
	}
	// Build mouse down → move → mouse up sequence
	scriptParts = append(scriptParts, fmt.Sprintf(
		`tell application "System Events" to click at {%d, %d}`, x, y))
	for i := 1; i <= steps; i++ {
		interX := x + (endX-x)*i/steps
		interY := y + (endY-y)*i/steps
		scriptParts = append(scriptParts, fmt.Sprintf(
			`tell application "System Events" to click at {%d, %d}`, interX, interY))
	}
	script := strings.Join(scriptParts, "\n")
	_, stderr, err := runCommand(ctx, 15*time.Second, "osascript", "-e", script)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("swipe failed: %v\n%s", err, stderr)}, nil
	}
	return Result{Content: fmt.Sprintf("Swiped from (%d,%d) to (%d,%d).", x, y, endX, endY)}, nil
}

func (b *iosBackend) press(ctx context.Context, device, key string) (Result, error) {
	var script string
	switch strings.ToLower(key) {
	case "home":
		script = `tell application "Simulator" to activate
delay 0.1
tell application "System Events" to keystroke (ASCII character 28) using {shift down, command down}`
	case "back":
		// iOS doesn't have a back button; use the app-specific back gesture
		return Result{IsError: true, Content: "iOS does not have a hardware back button. Use swipe from left edge."}, nil
	case "enter":
		script = `tell application "Simulator" to activate
delay 0.1
tell application "System Events" to keystroke return`
	case "escape":
		script = `tell application "Simulator" to activate
delay 0.1
tell application "System Events" to key code 53`
	default:
		script = fmt.Sprintf(`tell application "Simulator" to activate
delay 0.1
tell application "System Events" to keystroke "%s"`, key)
	}
	_, stderr, err := runCommand(ctx, 10*time.Second, "osascript", "-e", script)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("press %s failed: %v\n%s", key, err, stderr)}, nil
	}
	return Result{Content: fmt.Sprintf("Pressed %s.", key)}, nil
}

func (b *iosBackend) logs(ctx context.Context, device, pkg string, lines int) (Result, error) {
	// iOS Simulator logs via simctl spawn log
	args := []string{"simctl", "spawn", device, "log", "show", "--last", fmt.Sprintf("%dm", 5), "--style", "compact"}
	if pkg != "" {
		args = append(args, "--predicate", fmt.Sprintf("subsystem CONTAINS %q OR processImagePath CONTAINS %q", pkg, pkg))
	}
	out, stderr, err := runCommand(ctx, 30*time.Second, b.xcrunPath, args...)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("log show failed: %v\n%s", err, stderr)}, nil
	}
	allLines := strings.Split(strings.TrimSpace(out), "\n")
	if len(allLines) > lines {
		allLines = allLines[len(allLines)-lines:]
	}
	return Result{Content: strings.Join(allLines, "\n")}, nil
}

func (b *iosBackend) listApps(ctx context.Context, device string) (Result, error) {
	out, stderr, err := runCommand(ctx, 15*time.Second, b.xcrunPath, "simctl", "listapps", device)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("listapps failed: %v\n%s", err, stderr)}, nil
	}
	// simctl listapps outputs a plist; extract bundle identifiers
	var sb strings.Builder
	sb.WriteString("Installed apps:\n")
	count := 0
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "CFBundleIdentifier") {
			// Next line or same line has the value
			parts := strings.SplitN(line, "=", 2)
			if len(parts) == 2 {
				bundleID := strings.TrimSpace(parts[1])
				if !strings.HasPrefix(bundleID, "com.apple.") {
					sb.WriteString(fmt.Sprintf("  %s\n", bundleID))
					count++
				}
			}
		}
	}
	if count == 0 {
		sb.WriteString("  (no third-party apps found)")
	}
	return Result{Content: sb.String()}, nil
}
