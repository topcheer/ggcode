package tool

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

type androidBackend struct {
	adbPath string

	mu        sync.Mutex
	snapshots map[string]*uiElement
}

func (a *androidBackend) resolveRef(device, ref string) (int, int, error) {
	if !strings.HasPrefix(ref, "@e") {
		return 0, 0, fmt.Errorf("invalid element reference %q (expected @eN)", ref)
	}
	a.mu.Lock()
	root := a.snapshots[device]
	a.mu.Unlock()
	if root == nil {
		return 0, 0, fmt.Errorf("element reference %q requires a prior snapshot - call snapshot first", ref)
	}
	el := findElementByID(root, ref)
	if el == nil || el.Rect == nil {
		return 0, 0, fmt.Errorf("element %q not found in the last snapshot - the UI may have changed; take a new snapshot", ref)
	}
	cx, cy := el.Rect.center()
	return cx, cy, nil
}

func (a *androidBackend) defaultDevice() string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, _, _ := runCommand(ctx, 5*time.Second, a.adbPath, "devices", "-l")
	var firstDevice string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "List of devices") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) >= 2 && (fields[1] == "device" || fields[1] == "emulator") {

			if tid := extractTransportID(fields); tid != "" {
				return tid
			}
			if firstDevice == "" {
				firstDevice = fields[0]
			}
		}
	}
	return firstDevice
}

func extractTransportID(fields []string) string {
	for _, f := range fields {
		if strings.HasPrefix(f, "transport_id:") {
			return strings.TrimPrefix(f, "transport_id:")
		}
	}
	return ""
}

func adbDeviceArgs(device string) []string {
	if device == "" {
		return nil
	}
	if _, err := strconv.Atoi(device); err == nil {
		return []string{"-t", device}
	}
	return []string{"-s", device}
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

			if tid := extractTransportID(fields); tid != "" {
				sb.WriteString(fmt.Sprintf("    (use device=\"%s\" with transport_id for reliable targeting)\n", tid))
			}
		}
	}
	if !hasDevice {
		sb.WriteString("  (no devices connected)\n")
		sb.WriteString("\nTips: Start an emulator with `emulator -avd <name>` or connect a device via USB.")
	}
	return Result{Content: sb.String()}, nil
}

func (a *androidBackend) boot(ctx context.Context, device string) (Result, error) {

	args := adbDeviceArgs(device)
	args = append(args, "wait-for-device")
	out, _, err := runCommand(ctx, 30*time.Second, a.adbPath, args...)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("boot/wait failed: %v", err)}, nil
	}
	return Result{Content: fmt.Sprintf("Device %s is ready.%s", device, out)}, nil
}

func (a *androidBackend) install(ctx context.Context, device, appPath string) (Result, error) {
	args := adbDeviceArgs(device)
	args = append(args, "install", "-r", appPath)
	_, stderr, err := runCommand(ctx, 120*time.Second, a.adbPath, args...)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("install failed: %v\n%s", err, stderr)}, nil
	}
	return Result{Content: fmt.Sprintf("Installed %s successfully.", appPath)}, nil
}

func (a *androidBackend) uninstall(ctx context.Context, device, pkg string) (Result, error) {
	args := adbDeviceArgs(device)
	args = append(args, "uninstall", pkg)
	_, stderr, err := runCommand(ctx, 30*time.Second, a.adbPath, args...)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("uninstall failed: %v\n%s", err, stderr)}, nil
	}
	return Result{Content: fmt.Sprintf("Uninstalled %s.", pkg)}, nil
}

func (a *androidBackend) launch(ctx context.Context, device, pkg string) (Result, error) {
	args := adbDeviceArgs(device)
	args = append(args, "shell", "monkey", "-p", pkg, "-c", "android.intent.category.LAUNCHER", "1")
	out, stderr, err := runCommand(ctx, 15*time.Second, a.adbPath, args...)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("launch failed: %v\n%s", err, stderr)}, nil
	}
	return Result{Content: fmt.Sprintf("Launched %s.\n%s", pkg, out)}, nil
}

func (a *androidBackend) close(ctx context.Context, device, pkg string) (Result, error) {
	args := adbDeviceArgs(device)
	args = append(args, "shell", "am", "force-stop", pkg)
	_, stderr, err := runCommand(ctx, 10*time.Second, a.adbPath, args...)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("close failed: %v\n%s", err, stderr)}, nil
	}
	return Result{Content: fmt.Sprintf("Force-stopped %s.", pkg)}, nil
}

func (a *androidBackend) snapshot(ctx context.Context, device string) (Result, error) {
	args := adbDeviceArgs(device)

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

	devInfo := fmt.Sprintf("Platform: Android\nDevice: %s", device)

	formatted := formatSnapshot(root, devInfo)

	a.mu.Lock()
	if a.snapshots == nil {
		a.snapshots = make(map[string]*uiElement)
	}
	a.snapshots[device] = root
	a.mu.Unlock()
	return Result{Content: formatted}, nil
}

func (a *androidBackend) screenshot(ctx context.Context, device, format string, quality int, headless bool) (Result, error) {
	tmpFile := filepath.Join(os.TempDir(), fmt.Sprintf("ggcode_screenshot_%d.png", time.Now().UnixNano()))
	defer os.Remove(tmpFile)

	args := adbDeviceArgs(device)
	args = append(args, "exec-out", "screencap", "-p")
	cmd := exec.CommandContext(ctx, a.adbPath, args...)
	f, err := os.Create(tmpFile)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("failed to create temp file: %v", err)}, nil
	}
	cmd.Stdout = f
	var errBuf bytes.Buffer
	cmd.Stderr = &errBuf
	err = cmd.Run()
	f.Close()
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("screenshot failed: %v\n%s", err, errBuf.String())}, nil
	}

	data, err := os.ReadFile(tmpFile)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("failed to read screenshot: %v", err)}, nil
	}
	if len(data) == 0 {
		return Result{IsError: true, Content: "screenshot captured 0 bytes - device may be offline"}, nil
	}

	if headless {
		return Result{Content: fmt.Sprintf("Screenshot captured (%d bytes, PNG).", len(data))}, nil
	}

	return imageResult(data, "png", quality,
		fmt.Sprintf("Screenshot captured (%d bytes, PNG). The image is included for visual analysis.", len(data))), nil
}

func (a *androidBackend) tap(ctx context.Context, device, ref string, x, y int) (Result, error) {

	if ref != "" {
		cx, cy, err := a.resolveRef(device, ref)
		if err != nil {
			return Result{IsError: true, Content: err.Error()}, nil
		}
		x, y = cx, cy
	}
	args := adbDeviceArgs(device)
	args = append(args, "shell", "input", "tap", strconv.Itoa(x), strconv.Itoa(y))
	_, stderr, err := runCommand(ctx, 10*time.Second, a.adbPath, args...)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("tap failed: %v\n%s", err, stderr)}, nil
	}
	return Result{Content: fmt.Sprintf("Tapped at (%d, %d).", x, y)}, nil
}

func (a *androidBackend) typeText(ctx context.Context, device, ref, text string, x, y int) (Result, error) {

	if ref != "" {
		cx, cy, err := a.resolveRef(device, ref)
		if err != nil {
			return Result{IsError: true, Content: err.Error()}, nil
		}
		x, y = cx, cy
	}

	if x != 0 || y != 0 {

		preArgs := adbDeviceArgs(device)
		preArgs = append(preArgs, "shell", "input", "tap", strconv.Itoa(x), strconv.Itoa(y))
		if _, _, err := runCommand(ctx, 5*time.Second, a.adbPath, preArgs...); err != nil {
			return Result{IsError: true, Content: fmt.Sprintf("pre-tap at (%d,%d) failed: %v - aborting type to avoid sending text to the wrong control", x, y, err)}, nil
		}
		time.Sleep(200 * time.Millisecond)
	}

	if strings.Contains(text, "%s") {
		return Result{IsError: true, Content: fmt.Sprintf("text contains literal %%s, which adb input text cannot type (the device would decode it to a space and corrupt the payload) - use clipboard paste or type in segments without %%s")}, nil
	}

	shellSafe := "'" + strings.ReplaceAll(text, "'", `'"'"'`) + "'"
	shellSafe = strings.ReplaceAll(shellSafe, " ", "%s")
	args := adbDeviceArgs(device)
	args = append(args, "shell", "input", "text", shellSafe)
	_, stderr, err := runCommand(ctx, 10*time.Second, a.adbPath, args...)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("type failed: %v\n%s", err, stderr)}, nil
	}
	return Result{Content: fmt.Sprintf("Typed: %q", text)}, nil
}

func (a *androidBackend) swipe(ctx context.Context, device, ref string, x, y int, endX, endY *int) (Result, error) {

	if ref != "" {
		cx, cy, err := a.resolveRef(device, ref)
		if err != nil {
			return Result{IsError: true, Content: err.Error()}, nil
		}
		x, y = cx, cy
	}

	if endX == nil || endY == nil {
		return Result{IsError: true, Content: "swipe requires end_x and end_y (the swipe destination); provide both explicitly"}, nil
	}
	args := adbDeviceArgs(device)
	args = append(args, "shell", "input", "swipe",
		strconv.Itoa(x), strconv.Itoa(y), strconv.Itoa(*endX), strconv.Itoa(*endY), "300")
	_, stderr, err := runCommand(ctx, 10*time.Second, a.adbPath, args...)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("swipe failed: %v\n%s", err, stderr)}, nil
	}
	return Result{Content: fmt.Sprintf("Swiped from (%d,%d) to (%d,%d).", x, y, *endX, *endY)}, nil
}

func (a *androidBackend) press(ctx context.Context, device, key string) (Result, error) {
	keyCode := androidKeyCode(key)
	args := adbDeviceArgs(device)
	args = append(args, "shell", "input", "keyevent", keyCode)
	_, stderr, err := runCommand(ctx, 10*time.Second, a.adbPath, args...)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("press %s failed: %v\n%s", key, err, stderr)}, nil
	}
	return Result{Content: fmt.Sprintf("Pressed %s.", key)}, nil
}

func (a *androidBackend) logs(ctx context.Context, device, pkg string, lines int) (Result, error) {
	args := adbDeviceArgs(device)
	if pkg != "" {

		pidArgs := adbDeviceArgs(device)
		pidArgs = append(pidArgs, "shell", "pidof", pkg)

		outPid, _, _ := runCommand(ctx, 5*time.Second, a.adbPath, pidArgs...)
		pid := strings.TrimSpace(outPid)
		if fields := strings.Fields(pid); len(fields) > 0 {
			pid = fields[0]
		}
		if pid != "" {
			args = append(args, "shell", "logcat", "-d", "--pid="+pid, "-t", strconv.Itoa(lines))
		} else {

			return Result{IsError: true, Content: fmt.Sprintf("app %q is not running (no pid); cannot filter logcat by package", pkg)}, nil
		}
	} else {
		args = append(args, "shell", "logcat", "-d", "-t", strconv.Itoa(lines))
	}
	out, stderr, err := runCommand(ctx, 15*time.Second, a.adbPath, args...)
	if err != nil {

		args = adbDeviceArgs(device)
		args = append(args, "shell", "logcat", "-d", "-t", strconv.Itoa(lines))
		out, stderr, err = runCommand(ctx, 15*time.Second, a.adbPath, args...)
		if err != nil {
			return Result{IsError: true, Content: fmt.Sprintf("logcat failed: %v\n%s", err, stderr)}, nil
		}

		out = "[note: pid-filtered logcat failed; showing unfiltered logcat - lines from other processes are mixed in]\n" + out
	}

	allLines := strings.Split(strings.TrimSpace(out), "\n")
	if len(allLines) > lines {
		allLines = allLines[len(allLines)-lines:]
	}
	return Result{Content: strings.Join(allLines, "\n")}, nil
}

func (a *androidBackend) listApps(ctx context.Context, device string) (Result, error) {
	args := adbDeviceArgs(device)
	args = append(args, "shell", "pm", "list", "packages", "-3")
	out, stderr, err := runCommand(ctx, 10*time.Second, a.adbPath, args...)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("list apps failed: %v\n%s", err, stderr)}, nil
	}
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

var androidBoundsRe = regexp.MustCompile(`\[(\d+),(\d+)\]\[(\d+),(\d+)\]`)

func parseAndroidUIXML(xmlData string) *uiElement {

	nodeRe := regexp.MustCompile(`<node\b([^>]*?)/?>`)
	nodes := nodeRe.FindAllStringSubmatch(xmlData, -1)
	if len(nodes) == 0 {
		return nil
	}

	var root *uiElement
	first := 0
	for ; first < len(nodes); first++ {
		if root = parseAndroidNode(nodes[first][1]); root != nil {
			break
		}
	}
	if root == nil {
		return nil
	}

	for i := first + 1; i < len(nodes); i++ {
		child := parseAndroidNode(nodes[i][1])
		if child != nil {
			root.Children = append(root.Children, child)
		}
	}

	trimTree(root, 0)

	return root
}

func parseAndroidNode(attrs string) *uiElement {
	el := &uiElement{Type: "view", Platform: "android"}

	if v := xmlAttr(attrs, "class"); v != "" {

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

	if el.Label == "" && el.Rect == nil {
		return nil
	}

	return el
}

func simplifyAndroidClass(class string) string {

	parts := strings.Split(class, ".")
	last := parts[len(parts)-1]
	last = strings.TrimSuffix(last, "View")
	last = strings.TrimSuffix(last, "Layout")
	if last == "" {
		if len(parts) > 1 && parts[len(parts)-2] != "" {

			last = parts[len(parts)-2]
		} else {

			return strings.ToLower(strings.TrimSpace(class))
		}
	}
	last = strings.ToLower(string([]rune(last)[:1])) + string([]rune(last)[1:])
	return last
}

func xmlAttr(attrs, name string) string {
	re := regexp.MustCompile(name + `="([^"]*)"`)
	m := re.FindStringSubmatch(attrs)
	if m != nil {
		return strings.TrimSpace(m[1])
	}
	return ""
}

func trimTree(el *uiElement, depth int) {
	if el == nil {
		return
	}

	if depth > 8 {
		el.Children = nil
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

		return key
	}
}
