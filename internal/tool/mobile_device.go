package tool

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"
)

// MobileDeviceTool controls native mobile apps on iOS Simulator and Android
// Emulator/Device. It provides screenshot, UI tree snapshot, tap, type, swipe,
// launch, install, and log collection capabilities — all through system tools
// (xcrun simctl / adb) with zero external dependencies.
type MobileDeviceTool struct {
	mu      sync.Mutex
	android *androidBackend
	ios     *iosBackend
}

// NewMobileDeviceTool creates a new mobile device control tool.
// Returns nil if no mobile development tools are detected.
func NewMobileDeviceTool() *MobileDeviceTool {
	t := &MobileDeviceTool{}
	haveBackend := false

	if path, err := exec.LookPath("adb"); err == nil {
		t.android = &androidBackend{adbPath: path}
		haveBackend = true
	}

	if ios := newIOSBackend(); ios != nil {
		t.ios = ios
		haveBackend = true
	}

	if !haveBackend {
		return nil
	}
	return t
}

func shouldRegisterMobileDevice() bool {
	if _, err := exec.LookPath("adb"); err == nil {
		return true
	}
	if runtime.GOOS == "darwin" {
		if _, err := exec.LookPath("xcrun"); err == nil {
			return true
		}
	}
	return false
}

func (t *MobileDeviceTool) Name() string { return "mobile_device" }

func (t *MobileDeviceTool) Description() string {
	return "Control native mobile apps on iOS Simulator or Android Emulator/Device. " +
		"Supports: devices (list), boot, install, launch, snapshot (UI tree), " +
		"screenshot, tap, type, swipe, press (hardware keys), logs, close, list_apps. " +
		"Use action=\"devices\" first to see available devices. " +
		"snapshot returns a token-efficient accessibility tree with @eN element references " +
		"that can be used in tap/type/swipe actions."
}

func (t *MobileDeviceTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
  "type": "object",
  "properties": {
    "action": {
      "type": "string",
      "enum": ["devices", "boot", "install", "launch", "snapshot", "screenshot", "tap", "type", "swipe", "press", "logs", "close", "list_apps", "uninstall"],
      "description": "The action to perform.",
      "default": "devices"
    },
    "platform": {
      "type": "string",
      "enum": ["android", "ios", "auto"],
      "description": "Target platform. \"auto\" selects the first available backend.",
      "default": "auto"
    },
    "device": {
      "type": "string",
      "description": "Device identifier: Android serial number or iOS Simulator UDID."
    },
    "ref": {
      "type": "string",
      "description": "Element reference from snapshot (e.g. \"@e3\")."
    },
    "x": { "type": "integer", "description": "X coordinate for tap/swipe." },
    "y": { "type": "integer", "description": "Y coordinate for tap/swipe." },
    "end_x": { "type": "integer", "description": "End X coordinate for swipe." },
    "end_y": { "type": "integer", "description": "End Y coordinate for swipe." },
    "text": {
      "type": "string",
      "description": "Text to type or key name for press (e.g. \"home\", \"back\")."
    },
    "app": {
      "type": "string",
      "description": "Path to .apk/.app for install, or bundle_id/package for launch/close."
    },
    "bundle_id": {
      "type": "string",
      "description": "App bundle identifier (iOS) or package name (Android)."
    },
    "format": {
      "type": "string",
      "enum": ["png", "jpeg"],
      "description": "Screenshot format.",
      "default": "png"
    },
    "quality": {
      "type": "integer",
      "description": "JPEG quality (1-100).",
      "default": 85
    },
    "lines": {
      "type": "integer",
      "description": "Max log lines to return (for logs action).",
      "default": 100
    }
  }
}`)
}

type mobileDeviceParams struct {
	Action   string `json:"action"`
	Platform string `json:"platform"`
	Device   string `json:"device"`
	Ref      string `json:"ref"`
	X        int    `json:"x"`
	Y        int    `json:"y"`
	EndX     int    `json:"end_x"`
	EndY     int    `json:"end_y"`
	Text     string `json:"text"`
	App      string `json:"app"`
	BundleID string `json:"bundle_id"`
	Headless bool   `json:"headless"`
	Format   string `json:"format"`
	Quality  int    `json:"quality"`
	Lines    int    `json:"lines"`
}

func (t *MobileDeviceTool) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	var p mobileDeviceParams
	if err := json.Unmarshal(input, &p); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid parameters: %v", err)}, nil
	}
	if p.Action == "" {
		p.Action = "devices"
	}
	if p.Format == "" {
		p.Format = "png"
	}
	if p.Quality == 0 {
		p.Quality = 85
	}
	if p.Lines == 0 {
		p.Lines = 100
	}

	backend, err := t.resolveBackend(p.Platform)
	if err != nil {
		return Result{IsError: true, Content: err.Error()}, nil
	}

	device := p.Device
	if device == "" {
		device = backend.defaultDevice()
	}

	switch p.Action {
	case "devices":
		return backend.devices(ctx)
	case "boot":
		if device == "" {
			return Result{IsError: true, Content: "no device specified"}, nil
		}
		return backend.boot(ctx, device)
	case "install":
		if p.App == "" {
			return Result{IsError: true, Content: "app path is required for install"}, nil
		}
		return backend.install(ctx, device, p.App)
	case "uninstall":
		pkg := p.BundleID
		if pkg == "" {
			pkg = p.App
		}
		if pkg == "" {
			return Result{IsError: true, Content: "bundle_id or app is required for uninstall"}, nil
		}
		return backend.uninstall(ctx, device, pkg)
	case "launch":
		pkg := p.BundleID
		if pkg == "" {
			pkg = p.App
		}
		if pkg == "" {
			return Result{IsError: true, Content: "bundle_id or app is required for launch"}, nil
		}
		return backend.launch(ctx, device, pkg)
	case "close":
		pkg := p.BundleID
		if pkg == "" {
			pkg = p.App
		}
		if pkg == "" {
			return Result{IsError: true, Content: "bundle_id or app is required for close"}, nil
		}
		return backend.close(ctx, device, pkg)
	case "snapshot":
		return backend.snapshot(ctx, device)
	case "screenshot":
		return backend.screenshot(ctx, device, p.Format, p.Quality, false)
	case "tap":
		return backend.tap(ctx, device, p.Ref, p.X, p.Y)
	case "type":
		if p.Text == "" {
			return Result{IsError: true, Content: "text is required for type"}, nil
		}
		return backend.typeText(ctx, device, p.Ref, p.Text, p.X, p.Y)
	case "swipe":
		return backend.swipe(ctx, device, p.Ref, p.X, p.Y, p.EndX, p.EndY)
	case "press":
		if p.Text == "" {
			return Result{IsError: true, Content: "text (key name) is required for press"}, nil
		}
		return backend.press(ctx, device, p.Text)
	case "logs":
		pkg := p.BundleID
		if pkg == "" {
			pkg = p.App
		}
		return backend.logs(ctx, device, pkg, p.Lines)
	case "list_apps":
		return backend.listApps(ctx, device)
	default:
		return Result{IsError: true, Content: fmt.Sprintf("unknown action %q", p.Action)}, nil
	}
}

func (t *MobileDeviceTool) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	return nil
}

func (t *MobileDeviceTool) resolveBackend(platform string) (mobileBackend, error) {
	switch platform {
	case "android":
		if t.android != nil {
			return t.android, nil
		}
		return nil, fmt.Errorf("Android backend not available (adb not found)")
	case "ios":
		if t.ios != nil {
			return t.ios, nil
		}
		return nil, fmt.Errorf("iOS backend not available (not on macOS or Xcode not found)")
	case "", "auto":
		if t.ios != nil {
			if dev := t.ios.defaultDevice(); dev != "" {
				return t.ios, nil
			}
		}
		if t.android != nil {
			if dev := t.android.defaultDevice(); dev != "" {
				return t.android, nil
			}
		}
		if t.ios != nil {
			return t.ios, nil
		}
		if t.android != nil {
			return t.android, nil
		}
		return nil, fmt.Errorf("no mobile backend available")
	default:
		return nil, fmt.Errorf("unknown platform %q (use android, ios, or auto)", platform)
	}
}

type mobileBackend interface {
	devices(ctx context.Context) (Result, error)
	boot(ctx context.Context, device string) (Result, error)
	install(ctx context.Context, device, appPath string) (Result, error)
	uninstall(ctx context.Context, device, pkg string) (Result, error)
	launch(ctx context.Context, device, pkg string) (Result, error)
	close(ctx context.Context, device, pkg string) (Result, error)
	snapshot(ctx context.Context, device string) (Result, error)
	screenshot(ctx context.Context, device, format string, quality int, headless bool) (Result, error)
	tap(ctx context.Context, device, ref string, x, y int) (Result, error)
	typeText(ctx context.Context, device, ref, text string, x, y int) (Result, error)
	swipe(ctx context.Context, device, ref string, x, y, endX, endY int) (Result, error)
	press(ctx context.Context, device, key string) (Result, error)
	logs(ctx context.Context, device, pkg string, lines int) (Result, error)
	listApps(ctx context.Context, device string) (Result, error)
	defaultDevice() string
}

// uiElement represents a single node in the UI accessibility tree.
type uiElement struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Label    string       `json:"label"`
	Value    string       `json:"value"`
	Hint     string       `json:"hint"`
	Rect     *uiRect      `json:"rect"`
	Children []*uiElement `json:"children,omitempty"`
	Platform string       `json:"-"`
}

type uiRect struct {
	X      int `json:"x"`
	Y      int `json:"y"`
	Width  int `json:"width"`
	Height int `json:"height"`
}

func (r *uiRect) center() (int, int) {
	return r.X + r.Width/2, r.Y + r.Height/2
}

func formatSnapshot(root *uiElement, deviceInfo string) string {
	var sb strings.Builder
	if deviceInfo != "" {
		sb.WriteString(deviceInfo)
		sb.WriteString("\n\n")
	}
	counter := 0
	formatElement(&sb, root, 0, &counter)
	return sb.String()
}

func formatElement(sb *strings.Builder, el *uiElement, indent int, counter *int) {
	if el == nil {
		return
	}
	refID := ""
	if el.Rect != nil {
		*counter++
		refID = fmt.Sprintf("@e%d", *counter)
		el.ID = refID
	}
	prefix := strings.Repeat("  ", indent)
	if refID != "" {
		sb.WriteString(prefix)
		sb.WriteString(refID)
		sb.WriteString(" [")
		sb.WriteString(el.Type)
		sb.WriteString("]")
		if el.Label != "" {
			sb.WriteString(" \"")
			sb.WriteString(el.Label)
			sb.WriteString("\"")
		}
		if el.Value != "" {
			sb.WriteString(" value=\"")
			sb.WriteString(el.Value)
			sb.WriteString("\"")
		}
		if el.Rect != nil {
			sb.WriteString(fmt.Sprintf(" rect={%d,%d,%d,%d}", el.Rect.X, el.Rect.Y, el.Rect.Width, el.Rect.Height))
		}
		sb.WriteString("\n")
	}
	for _, child := range el.Children {
		formatElement(sb, child, indent+1, counter)
	}
}

func findElementByID(root *uiElement, id string) *uiElement {
	if root == nil {
		return nil
	}
	if root.ID == id {
		return root
	}
	for _, child := range root.Children {
		if found := findElementByID(child, id); found != nil {
			return found
		}
	}
	return nil
}

func runCommand(ctx context.Context, timeout time.Duration, name string, args ...string) (string, string, error) {
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, name, args...)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}

func imageResult(data []byte, format string, quality int, desc string) Result {
	mime := "image/png"
	if format == "jpeg" {
		mime = "image/jpeg"
	}
	return Result{
		Content: desc,
		Images:  []ResultImage{{MIME: mime, Base64: base64.StdEncoding.EncodeToString(data)}},
	}
}

func screenshotFromFile(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read screenshot: %w", err)
	}
	os.Remove(path)
	return data, nil
}
