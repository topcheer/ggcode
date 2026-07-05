package tool

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// androidBackend implements mobileBackend using ADB (Android Debug Bridge).
// Works on all platforms where adb is installed.
type androidBackend struct {
	adbPath   string
	cachedDev string
}

func (a *androidBackend) cleanup() {}

func (a *androidBackend) defaultDevice() string {
	if a.cachedDev != "" {
		return a.cachedDev
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, _, _ := runCommand(ctx, 5*time.Second, a.adbPath, "devices")
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "List of devices") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) >= 2 && (fields[1] == "device" || fields[1] == "emulator") {
			a.cachedDev = fields[0]
			return fields[0]
		}
	}
	return ""
}

func (a *androidBackend) devices(ctx context.Context) (Result, error) {
	out, stderr, err := runCommand(ctx, 10*time.Second, a.adbPath, "devices", "-l")
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("adb devices failed: %v\n%s", err, stderr)}, nil
	}
	var sb strings.Builder
	sb.WriteString("Connected Android devices:\n")
	hasDevice := false
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "List of devices") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) >= 2 {
			hasDevice = true
			serial := fields[0]
			state := fields[1]
			info := ""
			for _, f := range fields[2:] {
				info += " " + f
			}
			sb.WriteString(fmt.Sprintf("  %s [%s]%s\n", serial, state, info))
		}
	}
	if !hasDevice {
		sb.WriteString("  (no devices connected)\n")
		sb.WriteString("\nTips: Start an emulator with `emulator -avd <name>` or connect a device via USB.")
	}
	return Result{Content: sb.String()}, nil
}

func (a *androidBackend) boot(ctx context.Context, device string) (Result, error) {
	// Android emulators are booted via emulator command, not adb.
	// adb can wait for device to be ready.
	out, _, err := runCommand(ctx, 30*time.Second, a.adbPath, "-s", device, "wait-for-device")
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("boot/wait failed: %v", err)}, nil
	}
	return Result{Content: fmt.Sprintf("Device %s is ready.%s", device, out)}, nil
}

func (a *androidBackend) install(ctx context.Context, device, appPath string) (Result, error) {
	args := []string{}
	if device != "" {
		args = append(args, "-s", device)
	}
	args = append(args, "install", "-r", appPath)
	_, stderr, err := runCommand(ctx, 120*time.Second, a.adbPath, args...)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("install failed: %v\n%s", err, stderr)}, nil
	}
	return Result{Content: fmt.Sprintf("Installed %s successfully.", appPath)}, nil
}

func (a *androidBackend) uninstall(ctx context.Context, device, pkg string) (Result, error) {
	args := []string{}
	if device != "" {
		args = append(args, "-s", device)
	}
	args = append(args, "uninstall", pkg)
	_, stderr, err := runCommand(ctx, 30*time.Second, a.adbPath, args...)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("uninstall failed: %v\n%s", err, stderr)}, nil
	}
	return Result{Content: fmt.Sprintf("Uninstalled %s.", pkg)}, nil
}

func (a *androidBackend) launch(ctx context.Context, device, pkg string) (Result, error) {
	args := []string{}
	if device != "" {
		args = append(args, "-s", device)
	}
	args = append(args, "shell", "monkey", "-p", pkg, "-c", "android.intent.category.LAUNCHER", "1")
	out, stderr, err := runCommand(ctx, 15*time.Second, a.adbPath, args...)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("launch failed: %v\n%s", err, stderr)}, nil
	}
	return Result{Content: fmt.Sprintf("Launched %s.\n%s", pkg, out)}, nil
}

func (a *androidBackend) close(ctx context.Context, device, pkg string) (Result, error) {
	args := []string{}
	if device != "" {
		args = append(args, "-s", device)
	}
	args = append(args, "shell", "am", "force-stop", pkg)
	_, stderr, err := runCommand(ctx, 10*time.Second, a.adbPath, args...)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("close failed: %v\n%s", err, stderr)}, nil
	}
	return Result{Content: fmt.Sprintf("Force-stopped %s.", pkg)}, nil
}

func (a *androidBackend) snapshot(ctx context.Context, device string) (Result, error) {
	args := []string{}
	if device != "" {
		args = append(args, "-s", device)
	}
	// Dump UI hierarchy to device, then pull it
	dumpArgs := append(args, "shell", "uiautomator", "dump", "/sdcard/window_dump.xml")
	_, stderr, err := runCommand(ctx, 15*time.Second, a.adbPath, dumpArgs...)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("uiautomator dump failed: %v\n%s", err, stderr)}, nil
	}

	catArgs := append(args, "shell", "cat", "/sdcard/window_dump.xml")
	xmlData, stderr, err := runCommand(ctx, 10*time.Second, a.adbPath, catArgs...)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("failed to read dump: %v\n%s", err, stderr)}, nil
	}

	root := parseAndroidUIXML(xmlData)
	if root == nil {
		return Result{IsError: true, Content: "failed to parse UI hierarchy"}, nil
	}

	// Get device info
	devInfo := fmt.Sprintf("Platform: Android\nDevice: %s", device)

	formatted := formatSnapshot(root, devInfo)
	return Result{Content: formatted}, nil
}

func (a *androidBackend) screenshot(ctx context.Context, device, format string, quality int, headless bool) (Result, error) {
	tmpFile := filepath.Join(os.TempDir(), fmt.Sprintf("ggcode_screenshot_%d.png", time.Now().UnixNano()))
	defer os.Remove(tmpFile)

	args := []string{}
	if device != "" {
		args = append(args, "-s", device)
	}
	args = append(args, "exec-out", "screencap", "-p")
	cmd := exec.CommandContext(ctx, a.adbPath, args...)
	f, err := os.Create(tmpFile)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("failed to create temp file: %v", err)}, nil
	}
	cmd.Stdout = f
	cmd.Stderr = nil
	err = cmd.Run()
	f.Close()
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("screenshot failed: %v", err)}, nil
	}

	data, err := os.ReadFile(tmpFile)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("failed to read screenshot: %v", err)}, nil
	}
	if len(data) == 0 {
		return Result{IsError: true, Content: "screenshot captured 0 bytes — device may be offline"}, nil
	}

	if headless {
		return Result{Content: fmt.Sprintf("Screenshot captured (%d bytes, PNG).", len(data))}, nil
	}

	return imageResult(data, format, quality,
		fmt.Sprintf("Screenshot captured (%d bytes, %s). The image is included for visual analysis.", len(data), strings.ToUpper(format))), nil
}

func (a *androidBackend) tap(ctx context.Context, device, ref string, x, y int) (Result, error) {
	if ref != "" {
		return Result{IsError: true, Content: "element reference requires a prior snapshot — use coordinates instead, or call snapshot first"}, nil
	}
	args := []string{}
	if device != "" {
		args = append(args, "-s", device)
	}
	args = append(args, "shell", "input", "tap", strconv.Itoa(x), strconv.Itoa(y))
	_, stderr, err := runCommand(ctx, 10*time.Second, a.adbPath, args...)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("tap failed: %v\n%s", err, stderr)}, nil
	}
	return Result{Content: fmt.Sprintf("Tapped at (%d, %d).", x, y)}, nil
}

func (a *androidBackend) typeText(ctx context.Context, device, ref, text string, x, y int) (Result, error) {
	if ref != "" {
		return Result{IsError: true, Content: "element reference requires a prior snapshot — tap the field first, then type"}, nil
	}
	// Tap the field first if coordinates provided
	if x > 0 || y > 0 {
		if _, _, err := runCommand(ctx, 5*time.Second, a.adbPath, "shell", "input", "tap", strconv.Itoa(x), strconv.Itoa(y)); err != nil {
			// Non-fatal, continue with typing
		}
		time.Sleep(200 * time.Millisecond)
	}
	// adb input text doesn't handle spaces well, use %s
	escapedText := strings.ReplaceAll(text, " ", "%s")
	args := []string{}
	if device != "" {
		args = append(args, "-s", device)
	}
	args = append(args, "shell", "input", "text", escapedText)
	_, stderr, err := runCommand(ctx, 10*time.Second, a.adbPath, args...)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("type failed: %v\n%s", err, stderr)}, nil
	}
	return Result{Content: fmt.Sprintf("Typed: %q", text)}, nil
}

func (a *androidBackend) swipe(ctx context.Context, device, ref string, x, y, endX, endY int) (Result, error) {
	args := []string{}
	if device != "" {
		args = append(args, "-s", device)
	}
	args = append(args, "shell", "input", "swipe",
		strconv.Itoa(x), strconv.Itoa(y), strconv.Itoa(endX), strconv.Itoa(endY), "300")
	_, stderr, err := runCommand(ctx, 10*time.Second, a.adbPath, args...)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("swipe failed: %v\n%s", err, stderr)}, nil
	}
	return Result{Content: fmt.Sprintf("Swiped from (%d,%d) to (%d,%d).", x, y, endX, endY)}, nil
}

func (a *androidBackend) press(ctx context.Context, device, key string) (Result, error) {
	keyCode := androidKeyCode(key)
	args := []string{}
	if device != "" {
		args = append(args, "-s", device)
	}
	args = append(args, "shell", "input", "keyevent", keyCode)
	_, stderr, err := runCommand(ctx, 10*time.Second, a.adbPath, args...)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("press %s failed: %v\n%s", key, err, stderr)}, nil
	}
	return Result{Content: fmt.Sprintf("Pressed %s.", key)}, nil
}

func (a *androidBackend) logs(ctx context.Context, device, pkg string, lines int) (Result, error) {
	args := []string{}
	if device != "" {
		args = append(args, "-s", device)
	}
	if pkg != "" {
		args = append(args, "shell", "logcat", "-d", "--pid=$(pidof", "-s", pkg+" 2>/dev/null)")
	} else {
		args = append(args, "shell", "logcat", "-d")
	}
	out, stderr, err := runCommand(ctx, 15*time.Second, a.adbPath, args...)
	if err != nil {
		// Fallback to simple logcat
		args = []string{}
		if device != "" {
			args = append(args, "-s", device)
		}
		args = append(args, "shell", "logcat", "-d", "-t", strconv.Itoa(lines))
		out, stderr, err = runCommand(ctx, 15*time.Second, a.adbPath, args...)
		if err != nil {
			return Result{IsError: true, Content: fmt.Sprintf("logcat failed: %v\n%s", err, stderr)}, nil
		}
	}
	// Trim to last N lines
	allLines := strings.Split(strings.TrimSpace(out), "\n")
	if len(allLines) > lines {
		allLines = allLines[len(allLines)-lines:]
	}
	return Result{Content: strings.Join(allLines, "\n")}, nil
}

func (a *androidBackend) listApps(ctx context.Context, device string) (Result, error) {
	args := []string{}
	if device != "" {
		args = append(args, "-s", device)
	}
	args = append(args, "shell", "pm", "list", "packages", "-3")
	out, stderr, err := runCommand(ctx, 10*time.Second, a.adbPath, args...)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("list apps failed: %v\n%s", err, stderr)}, nil
	}
	// Clean up "package:" prefix
	var sb strings.Builder
	sb.WriteString("Installed third-party apps:\n")
	count := 0
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "package:") {
			sb.WriteString("  ")
			sb.WriteString(strings.TrimPrefix(line, "package:"))
			sb.WriteString("\n")
			count++
		}
	}
	if count == 0 {
		sb.WriteString("  (no third-party apps installed)")
	}
	return Result{Content: sb.String()}, nil
}

// =============================================================================
// Android UI XML parsing
// =============================================================================

var androidBoundsRe = regexp.MustCompile(`\[(\d+),(\d+)\]\[(\d+),(\d+)\]`)

// parseAndroidUIXML parses the XML output of `adb shell uiautomator dump`.
func parseAndroidUIXML(xmlData string) *uiElement {
	// Simple XML parser for the uiautomator hierarchy format.
	// We don't use encoding/xml because uiautomator output can be malformed.
	// Instead, we use regex to extract node elements.
	nodeRe := regexp.MustCompile(`<node\b([^>]*?)/?>`)
	nodes := nodeRe.FindAllStringSubmatch(xmlData, -1)
	if len(nodes) == 0 {
		return nil
	}

	// Build root from first node
	root := parseAndroidNode(nodes[0][1])
	// Android XML is a flat list but hierarchical via parent pointers.
	// In practice, uiautomator dump creates a nested hierarchy.
	// For simplicity, we flatten into root's children.
	for i := 1; i < len(nodes); i++ {
		child := parseAndroidNode(nodes[i][1])
		if child != nil {
			root.Children = append(root.Children, child)
		}
	}

	// Trim the tree to avoid excessive depth
	trimTree(root, 0)

	return root
}

func parseAndroidNode(attrs string) *uiElement {
	el := &uiElement{Type: "view", Platform: "android"}

	if v := xmlAttr(attrs, "class"); v != "" {
		// Simplify Android class names
		el.Type = simplifyAndroidClass(v)
	}
	if v := xmlAttr(attrs, "text"); v != "" {
		el.Label = v
	}
	if v := xmlAttr(attrs, "content-desc"); v != "" && el.Label == "" {
		el.Label = v
	}
	if v := xmlAttr(attrs, "resource-id"); v != "" {
		if el.Label == "" {
			el.Label = v
		}
	}

	bounds := xmlAttr(attrs, "bounds")
	if bounds != "" {
		if m := androidBoundsRe.FindStringSubmatch(bounds); m != nil {
			x1, _ := strconv.Atoi(m[1])
			y1, _ := strconv.Atoi(m[2])
			x2, _ := strconv.Atoi(m[3])
			y2, _ := strconv.Atoi(m[4])
			el.Rect = &uiRect{X: x1, Y: y1, Width: x2 - x1, Height: y2 - y1}
		}
	}

	// Skip nodes with no useful info
	if el.Label == "" && el.Rect == nil {
		return nil
	}

	return el
}

func simplifyAndroidClass(class string) string {
	// Android.Widget.Button -> button
	// android.widget.TextView -> text
	parts := strings.Split(class, ".")
	last := parts[len(parts)-1]
	last = strings.TrimSuffix(last, "View")
	last = strings.TrimSuffix(last, "Layout")
	last = strings.ToLower(last[:1]) + last[1:]
	return last
}

// xmlAttr extracts an attribute value from a string of XML attributes.
func xmlAttr(attrs, name string) string {
	re := regexp.MustCompile(name + `="([^"]*)"`)
	m := re.FindStringSubmatch(attrs)
	if m != nil {
		return strings.TrimSpace(m[1])
	}
	return ""
}

// trimTree removes deeply nested empty nodes to keep output token-efficient.
func trimTree(el *uiElement, depth int) {
	if el == nil || depth > 8 {
		return
	}
	filtered := el.Children[:0]
	for _, child := range el.Children {
		if child.Label != "" || child.Rect != nil || len(child.Children) > 0 {
			filtered = append(filtered, child)
			trimTree(child, depth+1)
		}
	}
	el.Children = filtered
}

// androidKeyCode maps common key names to Android keycodes.
func androidKeyCode(key string) string {
	switch strings.ToLower(key) {
	case "home":
		return "3"
	case "back":
		return "4"
	case "menu":
		return "82"
	case "volume_up":
		return "24"
	case "volume_down":
		return "25"
	case "power":
		return "26"
	case "enter":
		return "66"
	case "delete", "backspace":
		return "67"
	case "tab":
		return "61"
	case "escape":
		return "111"
	case "recent", "app_switch":
		return "187"
	default:
		// Allow raw keycodes
		return key
	}
}
