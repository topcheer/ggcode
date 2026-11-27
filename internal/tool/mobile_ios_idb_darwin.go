//go:build darwin

package tool

// idb-first iOS control. The AppleScript path (System Events clicks on the
// simulator window) depends on Accessibility permission, a visible window and
// screen coordinates that only match simulator points when the window sits at
// the screen origin. idb (facebook/idb) talks to the simulator directly over
// the companion: logical-point taps, text input that works without a visible
// window, and an accessibility tree for snapshot/element refs. When idb is
// missing on macOS the tool tries to install it (Homebrew idb-companion, then
// the fb-idb client) unless GGCODE_MOBILE_NO_INSTALL=1; if that fails it
// falls back to the AppleScript path.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// idbInstallTimeouts bound the one-shot install attempt.
const (
	idbBrewInstallTimeout = 20 * time.Minute
	idbPipInstallTimeout  = 10 * time.Minute
	idbConnectTimeout     = 30 * time.Second
)

type idbInstallResult struct {
	path string
	err  string
}

// idbLocator resolves the idb client binary once per process.
type idbLocator struct {
	mu       sync.Mutex
	done     bool
	path     string
	reason   string
	runner   simCmdRunner
	lookPath func(string) (string, error)
}

var sharedIDBLocator = newIDBLocator()

func newIDBLocator() *idbLocator {
	return &idbLocator{runner: runCommand, lookPath: exec.LookPath}
}

func idbInstallDisabled() bool {
	return os.Getenv("GGCODE_MOBILE_NO_INSTALL") == "1"
}

// resolve returns the idb client path, attempting installation the first time
// it is called when the binary is missing. The outcome (success or reason) is
// cached so a failing install is not retried on every action.
func (l *idbLocator) resolve(ctx context.Context) (string, string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.done {
		return l.path, l.reason
	}
	l.done = true
	if p, err := l.lookPath("idb"); err == nil && p != "" {
		l.path = p
		return l.path, ""
	}
	if runtime.GOOS != "darwin" {
		l.reason = "idb is only available on macOS"
		return "", l.reason
	}
	if idbInstallDisabled() {
		l.reason = "idb not found (auto-install disabled by GGCODE_MOBILE_NO_INSTALL=1)"
		return "", l.reason
	}
	p, err := l.install(ctx)
	if err != nil {
		l.reason = err.Error()
		return "", l.reason
	}
	l.path = p
	return l.path, ""
}

func (l *idbLocator) install(ctx context.Context) (string, error) {
	run := l.runner
	if run == nil {
		run = runCommand
	}
	// A companion without the client is useless and vice versa: the Homebrew
	// formula builds the companion and (on current taps) the client; the pip
	// package installs the client only. Try brew first, then pip.
	var notes []string
	if _, err := l.lookPath("brew"); err == nil {
		_, stderr, err := run(ctx, idbBrewInstallTimeout, "brew", "install", "idb-companion")
		if err != nil {
			notes = append(notes, fmt.Sprintf("brew install idb-companion: %v %s", err, strings.TrimSpace(stderr)))
		}
		if p, err := l.lookPath("idb"); err == nil && p != "" {
			return p, nil
		}
		if _, _, err := run(ctx, idbBrewInstallTimeout, "brew", "tap", "facebook/fb"); err != nil {
			notes = append(notes, fmt.Sprintf("brew tap facebook/fb: %v", err))
		} else if _, stderr, err := run(ctx, idbBrewInstallTimeout, "brew", "install", "facebook/fb/idb-companion"); err != nil {
			notes = append(notes, fmt.Sprintf("brew install facebook/fb/idb-companion: %v %s", err, strings.TrimSpace(stderr)))
		}
		if p, err := l.lookPath("idb"); err == nil && p != "" {
			return p, nil
		}
	} else {
		notes = append(notes, "homebrew not found")
	}
	// Client via pip/pipx (companion is required too; report both).
	pipVars := []struct {
		name string
		args []string
	}{
		{"pipx", []string{"install", "fb-idb"}},
		{"pip3", []string{"install", "--user", "fb-idb"}},
		{"python3", []string{"-m", "pip", "install", "--user", "fb-idb"}},
	}
	for _, v := range pipVars {
		if _, err := l.lookPath(v.name); err != nil {
			continue
		}
		if _, stderr, err := run(ctx, idbPipInstallTimeout, v.name, v.args...); err != nil {
			notes = append(notes, fmt.Sprintf("%s %s: %v %s", v.name, strings.Join(v.args, " "), err, strings.TrimSpace(stderr)))
			continue
		}
		if p, err := l.lookPath("idb"); err == nil && p != "" {
			return p, nil
		}
		// pip --user installs into the user bin dir which may not be on PATH.
		if home, err := os.UserHomeDir(); err == nil {
			for _, c := range []string{
				filepath.Join(home, ".local", "bin", "idb"),
				filepath.Join(home, "Library", "Python", "3.14", "bin", "idb"),
				filepath.Join(home, "Library", "Python", "3.13", "bin", "idb"),
				filepath.Join(home, "Library", "Python", "3.12", "bin", "idb"),
			} {
				if _, err := os.Stat(c); err == nil {
					return c, nil
				}
			}
		}
	}
	if len(notes) == 0 {
		notes = append(notes, "no installer succeeded")
	}
	return "", fmt.Errorf("idb auto-install failed (%s); install manually with `brew install idb-companion && pipx install fb-idb` or set GGCODE_MOBILE_NO_INSTALL=1 to silence this", strings.Join(notes, "; "))
}

// idbElement mirrors the flat JSON emitted by `idb ui describe-all --json`.
type idbElement struct {
	AXLabel    string `json:"AXLabel"`
	AXType     string `json:"AXType"`
	AXValue    string `json:"AXValue"`
	AXUniqueId string `json:"AXUniqueId"`
	Enabled    bool   `json:"enabled"`
	Frame      struct {
		X      float64 `json:"x"`
		Y      float64 `json:"y"`
		Width  float64 `json:"width"`
		Height float64 `json:"height"`
	} `json:"frame"`
}

// parseIDBDescribeAll turns the flat accessibility list into the uiElement
// tree the shared snapshot formatter expects (one root, flat children ordered
// top-left to bottom-right). Elements without a usable frame are dropped.
func parseIDBDescribeAll(out string, device string) (*uiElement, error) {
	var items []idbElement
	if err := json.Unmarshal([]byte(out), &items); err != nil {
		// Some idb builds wrap the list in an object.
		var wrapped struct {
			Result []idbElement `json:"result"`
		}
		if err2 := json.Unmarshal([]byte(out), &wrapped); err2 != nil || len(wrapped.Result) == 0 {
			return nil, fmt.Errorf("failed to parse idb describe-all output: %v", err)
		}
		items = wrapped.Result
	}
	root := &uiElement{ID: "root", Type: "Window", Platform: "ios", Label: device}
	for _, it := range items {
		if it.Frame.Width <= 0 || it.Frame.Height <= 0 {
			continue
		}
		typ := strings.ToLower(strings.TrimPrefix(it.AXType, "AX"))
		if typ == "" {
			typ = "element"
		}
		root.Children = append(root.Children, &uiElement{
			Type:     typ,
			Label:    snapshotText(it.AXLabel),
			Value:    snapshotText(it.AXValue),
			Platform: "ios",
			Rect: &uiRect{
				X:      int(it.Frame.X),
				Y:      int(it.Frame.Y),
				Width:  int(it.Frame.Width),
				Height: int(it.Frame.Height),
			},
		})
	}
	if len(root.Children) == 0 {
		return nil, fmt.Errorf("idb describe-all returned no accessibility elements")
	}
	return root, nil
}

// snapshotMaxText bounds one accessibility label/value. Flutter merges whole
// tool cards into one label (a run_command card carries its full command),
// which used to turn the snapshot into a wall of source code.
const snapshotMaxText = 160

func snapshotText(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > snapshotMaxText {
		cut := s[:snapshotMaxText]
		for len(cut) > 0 && !utf8.RuneStart(s[len(cut)]) {
			cut = cut[:len(cut)-1]
		}
		s = cut + "…"
	}
	return s
}

// hidKeycodes for `idb ui key`.
const (
	idbKeyReturn    = 40
	idbKeyEscape    = 41
	idbKeyTab       = 43
	idbKeyBackspace = 42
	idbKeyUp        = 82
	idbKeyDown      = 81
	idbKeyLeft      = 80
	idbKeyRight     = 79
)

func idbKeyFor(key string) (int, bool) {
	switch strings.ToLower(key) {
	case "enter", "return":
		return idbKeyReturn, true
	case "escape", "esc":
		return idbKeyEscape, true
	case "tab":
		return idbKeyTab, true
	case "backspace":
		return idbKeyBackspace, true
	case "up", "arrowup":
		return idbKeyUp, true
	case "down", "arrowdown":
		return idbKeyDown, true
	case "left", "arrowleft":
		return idbKeyLeft, true
	case "right", "arrowright":
		return idbKeyRight, true
	}
	return 0, false
}

// runner returns the command runner for idb invocations (injectable in tests).
func (b *iosBackend) runner() simCmdRunner {
	if b.run != nil {
		return b.run
	}
	return runCommand
}

// ensureIDBConnected makes sure a companion is attached to the simulator.
// `idb connect` is idempotent and fast when already connected; the result is
// cached per UDID and cleared on any idb failure so the next action reconnects.
func (b *iosBackend) ensureIDBConnected(ctx context.Context, idbPath, device string) error {
	b.mu.Lock()
	if b.idbConnected == nil {
		b.idbConnected = map[string]bool{}
	}
	if b.idbConnected[device] {
		b.mu.Unlock()
		return nil
	}
	b.mu.Unlock()
	_, stderr, err := b.runner()(ctx, idbConnectTimeout, idbPath, "connect", device)
	if err != nil {
		return fmt.Errorf("idb connect %s failed: %v %s", device, err, strings.TrimSpace(stderr))
	}
	b.mu.Lock()
	b.idbConnected[device] = true
	b.mu.Unlock()
	return nil
}

func (b *iosBackend) markIDBDisconnected(device string) {
	b.mu.Lock()
	delete(b.idbConnected, device)
	b.mu.Unlock()
}

// idb runs one `idb` invocation against the device, connecting first and
// retrying once after a reconnect when the first attempt fails (stale
// companion after a simulator reboot is the common case).
func (b *iosBackend) idb(ctx context.Context, idbPath, device string, timeout time.Duration, args ...string) (string, string, error) {
	for attempt := 0; attempt < 2; attempt++ {
		if err := b.ensureIDBConnected(ctx, idbPath, device); err != nil {
			if attempt == 1 {
				return "", "", err
			}
			b.markIDBDisconnected(device)
			continue
		}
		full := append(append([]string{}, args...), "--udid", device)
		out, stderr, err := b.runner()(ctx, timeout, idbPath, full...)
		if err == nil {
			return out, stderr, nil
		}
		b.markIDBDisconnected(device)
		if attempt == 1 {
			return out, stderr, err
		}
	}
	return "", "", fmt.Errorf("idb unreachable")
}
