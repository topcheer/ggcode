//go:build darwin

package tool

// idb-first iOS control. The AppleScript path (System Events clicks on the
// simulator window) depends on Accessibility permission, a visible window and
// screen coordinates that only match simulator points when the window sits at
// the screen origin. idb (facebook/idb) talks to the simulator directly over
// the companion: logical-point taps, text input that works without a visible
// window, and an accessibility tree for snapshot/element refs.
//
// When idb is missing on macOS the tool starts a detached one-shot installer
// (Homebrew idb-companion for the companion, a ggcode-owned python venv for the
// fb-idb client) unless GGCODE_MOBILE_NO_INSTALL=1. The installer runs outside
// the tool-call deadline; the action that triggered it falls back to the
// AppleScript path and asks to be retried, and the next call picks idb up.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/topcheer/ggcode/internal/safego"
)

const (
	idbConnectTimeout = 30 * time.Second

	// idbInstallDoneLine closes the installer log; the exit code tells whether
	// both halves (companion + client) are usable.
	idbInstallDoneLine = "GGCODE_IDB_INSTALL_DONE"
)

// idbLocator resolves the idb client once per process. Resolution is cheap
// (PATH + a few known paths); installation is detached and checked via the
// installer log so a slow brew/pip never blocks a tool call.
type idbLocator struct {
	mu     sync.Mutex
	path   string
	reason string
	// done marks a definitive outcome (client found, or installer finished
	// without producing one) so a failure is not retried on every action.
	done bool
	// install is the detached installer bookkeeping (nil until started).
	install *idbInstall

	// injectable for tests
	lookPath     func(string) (string, error)
	stat         func(string) bool // "file exists and is executable"
	home         string
	logDir       string
	startInstall func(script, logPath string) error
}

type idbInstall struct {
	logPath string
	started time.Time
}

var sharedIDBLocator = newIDBLocator()

func newIDBLocator() *idbLocator {
	home, _ := os.UserHomeDir()
	return &idbLocator{
		lookPath: exec.LookPath,
		stat: func(p string) bool {
			st, err := os.Stat(p)
			return err == nil && !st.IsDir() && st.Mode()&0o111 != 0
		},
		home:         home,
		logDir:       filepath.Join(home, ".ggcode", "logs"),
		startInstall: startDetachedInstall,
	}
}

func idbInstallDisabled() bool {
	return os.Getenv("GGCODE_MOBILE_NO_INSTALL") == "1"
}

// idbClientCandidates are checked after PATH: pipx (~/.local/bin), the
// ggcode-owned venv, and the Homebrew prefixes (a GUI-launched ggcode has a
// minimal PATH).
func (l *idbLocator) idbClientCandidates() []string {
	return []string{
		filepath.Join(l.home, ".ggcode", "idb", "bin", "idb"),
		filepath.Join(l.home, ".local", "bin", "idb"),
		"/opt/homebrew/bin/idb",
		"/usr/local/bin/idb",
	}
}

func (l *idbLocator) companionCandidates() []string {
	return []string{"/opt/homebrew/bin/idb_companion", "/usr/local/bin/idb_companion"}
}

func (l *idbLocator) brewCandidates() []string {
	return []string{"/opt/homebrew/bin/brew", "/usr/local/bin/brew"}
}

func (l *idbLocator) find(name string, extra []string) string {
	if l.lookPath != nil {
		if p, err := l.lookPath(name); err == nil && p != "" {
			return p
		}
	}
	if l.stat == nil {
		return ""
	}
	for _, c := range extra {
		if l.stat(c) {
			return c
		}
	}
	return ""
}

func (l *idbLocator) findClient() string    { return l.find("idb", l.idbClientCandidates()) }
func (l *idbLocator) findCompanion() string { return l.find("idb_companion", l.companionCandidates()) }
func (l *idbLocator) findBrew() string      { return l.find("brew", l.brewCandidates()) }
func (l *idbLocator) idbOnPath() bool {
	p, err := l.lookPath("idb")
	return err == nil && p != ""
}

// resolve returns the idb client path ("" when unavailable) and the reason.
func (l *idbLocator) resolve(ctx context.Context) (string, string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.done {
		return l.path, l.reason
	}
	if p := l.findClient(); p != "" {
		l.path, l.reason, l.done = p, "", true
		return p, ""
	}
	if runtime.GOOS != "darwin" {
		l.reason, l.done = "idb is only available on macOS", true
		return "", l.reason
	}
	if idbInstallDisabled() {
		l.reason, l.done = "idb not found (auto-install disabled by GGCODE_MOBILE_NO_INSTALL=1)", true
		return "", l.reason
	}
	if l.install != nil {
		if finished, tail := l.installFinished(); !finished {
			return "", fmt.Sprintf("idb not found - installation in progress (started %s ago; log: %s); retry the action in a minute", time.Since(l.install.started).Round(time.Second), l.install.logPath)
		} else {
			l.reason, l.done = "idb auto-install finished without a usable client: "+tail, true
			return "", l.reason
		}
	}
	if ctx.Err() != nil {
		return "", "idb not found and the call is already cancelled; installation not started"
	}
	if err := l.startInstall(l.installScript(), l.installLogPath()); err != nil {
		l.reason, l.done = "idb not found and the installer could not be started: "+err.Error(), true
		return "", l.reason
	}
	l.install = &idbInstall{logPath: l.installLogPath(), started: time.Now()}
	l.reason = fmt.Sprintf("idb not found - installation started in the background (brew install idb-companion + fb-idb client into ~/.ggcode/idb); retry the action in 1-2 minutes; log: %s", l.install.logPath)
	return "", l.reason
}

func (l *idbLocator) installLogPath() string { return filepath.Join(l.logDir, "idb-install.log") }

// installFinished reports whether the detached installer wrote its done line
// and returns the last lines of its log for diagnostics.
func (l *idbLocator) installFinished() (bool, string) {
	data, err := os.ReadFile(l.install.logPath)
	if err != nil {
		return false, ""
	}
	text := string(data)
	if !strings.Contains(text, idbInstallDoneLine) {
		return false, ""
	}
	lines := strings.Split(strings.TrimSpace(text), "\n")
	if len(lines) > 6 {
		lines = lines[len(lines)-6:]
	}
	return true, strings.Join(lines, " | ")
}

// installScript is the detached installer. It installs the companion through
// Homebrew when brew is available and the client into a ggcode-owned venv
// (no pipx / PEP 668 / PATH dependencies), then writes the done line.
func (l *idbLocator) installScript() string {
	return `#!/bin/sh
export PATH=/opt/homebrew/bin:/usr/local/bin:$HOME/.local/bin:$PATH
echo "[idb-install] start $(date)"
if command -v brew >/dev/null 2>&1; then
  if command -v idb_companion >/dev/null 2>&1; then
    echo "[idb-install] idb_companion present"
  else
    # Homebrew >= 7 refuses to load formulae from taps the user has not trusted
    # (idb-companion lives in the meta/fb tap). Trust only the idb maintainers'
    # tap, then retry; any other tap is reported, never trusted blindly.
    out=$(brew install idb-companion 2>&1) && out_rc=0 || out_rc=1
    if [ "$out_rc" -ne 0 ]; then
      # "Error: Refusing to load formula meta/fb/idb-companion from untrusted tap meta/fb."
      tap=$(printf '%s' "$out" | sed -n 's/.*from untrusted tap \([A-Za-z0-9_./-]*\)\.\{0,1\}.*/\1/p' | head -1 | sed 's/\.$//')
      case "$tap" in
        meta/fb|facebook/fb)
          echo "[idb-install] trusting idb tap $tap"
          brew trust --tap "$tap" >/dev/null 2>&1
          out=$(brew install idb-companion 2>&1) && out_rc=0 || out_rc=1
          ;;
        "")
          ;;
        *)
          echo "[idb-install] refusing to trust unknown tap '$tap'"
          ;;
      esac
    fi
    if [ "$out_rc" -eq 0 ] && command -v idb_companion >/dev/null 2>&1; then
      echo "[idb-install] idb_companion installed"
    else
      echo "[idb-install] brew install idb-companion FAILED"
      printf '%s\n' "$out" | tail -3 | sed 's/^/[idb-install]   /'
    fi
  fi
else
  echo "[idb-install] brew not found (install Homebrew or idb_companion manually)"
fi
VENV="$HOME/.ggcode/idb"
if [ -x "$VENV/bin/idb" ]; then
  echo "[idb-install] client present at $VENV/bin/idb"
else
  PY=""
  for c in python3 /usr/bin/python3 /opt/homebrew/bin/python3; do
    if command -v "$c" >/dev/null 2>&1; then PY="$c"; break; fi
  done
  if [ -n "$PY" ]; then
    rm -rf "$VENV"
    if "$PY" -m venv "$VENV"; then
      "$VENV/bin/pip" install --quiet --upgrade pip >/dev/null 2>&1
      "$VENV/bin/pip" install --quiet fb-idb && echo "[idb-install] fb-idb installed" || echo "[idb-install] pip install fb-idb FAILED"
    else
      echo "[idb-install] venv creation failed"
    fi
  else
    echo "[idb-install] python3 not found"
  fi
fi
if [ -x "$VENV/bin/idb" ] && command -v idb_companion >/dev/null 2>&1; then
  echo "` + idbInstallDoneLine + ` rc=0"
else
  echo "` + idbInstallDoneLine + ` rc=1"
fi
`
}

// startDetachedInstall runs the installer in its own session with stdin
// closed and output appended to the log. A waiter goroutine reaps it so no
// zombie is left behind.
func startDetachedInstall(script, logPath string) error {
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return err
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	cmd := exec.Command("/bin/sh", "-c", script)
	cmd.SysProcAttr = detachSysProcAttr()
	cmd.Stdin = nil
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		logFile.Close()
		return err
	}
	safego.Go("mobile.idb-install-wait", func() {
		_ = cmd.Wait()
		_ = logFile.Close()
	})
	return nil
}

// idbKeyCodeV is the HID keycode for the "v" key (used with --command to paste).
const idbKeyCodeV = 9

// copyPasteboard puts text on the simulator pasteboard and waits until the
// device actually sees it. With the simulator's automatic pasteboard sync on,
// the device pasteboard is a lazily mirrored copy of the host pasteboard
// (`simctl pbcopy` is a silent no-op there) and the mirror lands ~1s after the
// host write; with sync off, `simctl pbcopy` is the way in. Both are tried in
// turn and verified through `simctl pbpaste`, because a paste issued before
// the content arrives pastes whatever was there before.
func (b *iosBackend) copyPasteboard(ctx context.Context, device, text string) error {
	if b.paste != nil {
		return b.paste(ctx, device, text)
	}
	if b.xcrunPath == "" {
		return fmt.Errorf("xcrun unavailable")
	}
	var notes []string
	writers := []struct {
		name string
		run  func() error
	}{
		{"host pbcopy", func() error {
			_, stderr, err := runCommandStdin(ctx, 10*time.Second, text, "/usr/bin/pbcopy")
			if err != nil {
				return fmt.Errorf("%v %s", err, strings.TrimSpace(stderr))
			}
			return nil
		}},
		{"simctl pbcopy", func() error {
			_, stderr, err := runCommandStdin(ctx, 10*time.Second, text, b.xcrunPath, "simctl", "pbcopy", device)
			if err != nil {
				return fmt.Errorf("%v %s", err, strings.TrimSpace(stderr))
			}
			return nil
		}},
	}
	for _, w := range writers {
		if err := w.run(); err != nil {
			notes = append(notes, w.name+": "+err.Error())
			continue
		}
		deadline := time.Now().Add(4 * time.Second)
		for {
			out, _, err := runCommand(ctx, 5*time.Second, b.xcrunPath, "simctl", "pbpaste", device)
			if err == nil && pasteboardMatches(out, text) {
				return nil
			}
			if time.Now().After(deadline) {
				seen := strings.TrimSpace(out)
				if len(seen) > 40 {
					seen = seen[:40] + "..."
				}
				notes = append(notes, fmt.Sprintf("%s: device pasteboard still holds %q after 4s", w.name, seen))
				break
			}
			time.Sleep(200 * time.Millisecond)
		}
	}
	return fmt.Errorf("could not place the text on the simulator pasteboard (%s)", strings.Join(notes, "; "))
}

// pasteboardMatches compares the device pasteboard read-back with the text.
// A single trailing newline is tolerated (some pasteboard bridges add one).
func pasteboardMatches(got, want string) bool {
	if got == want {
		return true
	}
	return strings.TrimSuffix(got, "\n") == strings.TrimSuffix(want, "\n")
}

// focusedIDBField describes the focused text field: its centre (for a long
// press) and whether it is empty.
type focusedIDBField struct {
	x, y  int
	empty bool
}

// idbFocusedField finds the element carrying the "IsEditing" trait.
func idbFocusedField(out string) *focusedIDBField {
	var items []idbElement
	if err := json.Unmarshal([]byte(out), &items); err != nil {
		return nil
	}
	for _, it := range items {
		editing := false
		for _, tr := range it.Traits {
			if tr == "IsEditing" {
				editing = true
			}
		}
		if !editing || it.Frame.Width <= 0 || it.Frame.Height <= 0 {
			continue
		}
		return &focusedIDBField{
			x:     int(it.Frame.X + it.Frame.Width/2),
			y:     int(it.Frame.Y + it.Frame.Height/2),
			empty: strings.TrimSpace(it.AXValue) == "",
		}
	}
	return nil
}

// waitFocusedField polls the accessibility tree for a focused text field.
func (b *iosBackend) waitFocusedField(ctx context.Context, idbPath, device string) *focusedIDBField {
	for attempt := 0; attempt < 6; attempt++ {
		if attempt > 0 {
			time.Sleep(250 * time.Millisecond)
		}
		out, _, err := b.idb(ctx, idbPath, device, 15*time.Second, "ui", "describe-all", "--json")
		if err != nil {
			return nil
		}
		if f := idbFocusedField(out); f != nil {
			return f
		}
	}
	return nil
}

// idbPasteMenuItem returns the centre of the edit-menu item with the given
// label, if present in the accessibility tree.
func idbPasteMenuItem(out string, label string) (int, int, bool) {
	var items []idbElement
	if err := json.Unmarshal([]byte(out), &items); err != nil {
		return 0, 0, false
	}
	for _, it := range items {
		if strings.TrimSpace(it.AXLabel) == label && it.Frame.Width > 0 && it.Frame.Height > 0 {
			return int(it.Frame.X + it.Frame.Width/2), int(it.Frame.Y + it.Frame.Height/2), true
		}
	}
	return 0, 0, false
}

// pasteViaLongPress opens the text field's edit menu with a long press and
// taps its "Paste" item. This is a real user paste gesture, so iOS never
// raises the cross-app paste confirmation that swallows Cmd+V for some
// pasteboard contents. If the menu shows Cut/Copy a word is selected and
// Paste would replace it, so the menu is dismissed instead (returns false).
func (b *iosBackend) pasteViaLongPress(ctx context.Context, idbPath, device string, f *focusedIDBField) (bool, error) {
	if f == nil {
		return false, nil
	}
	if _, stderr, err := b.idb(ctx, idbPath, device, 15*time.Second, "ui", "tap", strconv.Itoa(f.x), strconv.Itoa(f.y), "--duration", "1.0"); err != nil {
		return false, fmt.Errorf("long press at (%d,%d): %v %s", f.x, f.y, err, strings.TrimSpace(stderr))
	}
	for attempt := 0; attempt < 5; attempt++ {
		if attempt > 0 {
			time.Sleep(300 * time.Millisecond)
		}
		out, _, err := b.idb(ctx, idbPath, device, 15*time.Second, "ui", "describe-all", "--json")
		if err != nil {
			return false, nil
		}
		if _, _, selected := idbPasteMenuItem(out, "Cut"); selected {
			_, _, _ = b.idb(ctx, idbPath, device, 10*time.Second, "ui", "key", strconv.Itoa(idbKeyEscape))
			return false, nil
		}
		if cx, cy, ok := idbPasteMenuItem(out, "Paste"); ok {
			if _, stderr, terr := b.idb(ctx, idbPath, device, 10*time.Second, "ui", "tap", strconv.Itoa(cx), strconv.Itoa(cy)); terr != nil {
				return true, fmt.Errorf("paste menu tap at (%d,%d): %v %s", cx, cy, terr, strings.TrimSpace(stderr))
			}
			return true, nil
		}
	}
	return false, nil
}

// confirmPasteCallout dismisses the iOS paste confirmation that Cmd+V raises
// for pasteboard content from another app: a "Paste" callout next to the
// focused field. The callout shows up with a variable delay, so the tree is
// polled for ~2s. Reports whether a callout was tapped.
func (b *iosBackend) confirmPasteCallout(ctx context.Context, idbPath, device string) (bool, error) {
	for attempt := 0; attempt < 5; attempt++ {
		if attempt > 0 {
			time.Sleep(400 * time.Millisecond)
		}
		out, _, err := b.idb(ctx, idbPath, device, 15*time.Second, "ui", "describe-all", "--json")
		if err != nil {
			return false, nil
		}
		if cx, cy, ok := idbPasteMenuItem(out, "Paste"); ok {
			if _, stderr, terr := b.idb(ctx, idbPath, device, 10*time.Second, "ui", "tap", strconv.Itoa(cx), strconv.Itoa(cy)); terr != nil {
				return true, fmt.Errorf("paste callout tap at (%d,%d): %v %s", cx, cy, terr, strings.TrimSpace(stderr))
			}
			return true, nil
		}
	}
	return false, nil
}

// idbTypeText enters text into the focused control. `idb ui text` sends HID
// keystrokes, which pass through the simulator keyboard's input method: with a
// CJK IME active, punctuation and long ASCII runs get recomposed into
// candidate words (a wss URL came out as "wss<FULLWIDTH COLON>//gateway<FULLWIDTH DOT>ggcode...").
// Pasting from the simulator pasteboard bypasses the IME and is exact for any
// Unicode: the edit menu's Paste item (long press) is tried first because it
// is a real user gesture, then Cmd+V with its confirmation callout, and
// `ui text` remains the last resort when the pasteboard is unavailable.
func (b *iosBackend) idbTypeText(ctx context.Context, idbPath, device, text string) (string, error) {
	field := b.waitFocusedField(ctx, idbPath, device)
	if field == nil {
		return "", fmt.Errorf("no text field has keyboard focus - tap the field first (pass its ref or x/y); nothing was typed")
	}
	var pasteErr error
	if err := b.copyPasteboard(ctx, device, text); err != nil {
		pasteErr = err
	} else if field.empty {
		if done, lerr := b.pasteViaLongPress(ctx, idbPath, device, field); lerr != nil {
			pasteErr = lerr
		} else if done {
			return "pasteboard, Paste menu item", nil
		} else if _, stderr, err := b.idb(ctx, idbPath, device, 15*time.Second, "ui", "key", strconv.Itoa(idbKeyCodeV), "--command"); err != nil {
			pasteErr = fmt.Errorf("cmd+v: %v %s", err, strings.TrimSpace(stderr))
		} else if tapped, cerr := b.confirmPasteCallout(ctx, idbPath, device); cerr != nil {
			pasteErr = cerr
		} else if !tapped {
			return "pasteboard; no Paste callout appeared - if the text did not land, focus the field (tap it) and retry", nil
		} else {
			return "pasteboard, Paste callout confirmed", nil
		}
	} else {
		// Populated field: a long press may select a word; try Cmd+V first and
		// only fall back to the edit menu when no callout shows up.
		if _, stderr, err := b.idb(ctx, idbPath, device, 15*time.Second, "ui", "key", strconv.Itoa(idbKeyCodeV), "--command"); err != nil {
			pasteErr = fmt.Errorf("cmd+v: %v %s", err, strings.TrimSpace(stderr))
		} else if tapped, cerr := b.confirmPasteCallout(ctx, idbPath, device); cerr != nil {
			pasteErr = cerr
		} else if tapped {
			return "pasteboard, Paste callout confirmed", nil
		} else if done, lerr := b.pasteViaLongPress(ctx, idbPath, device, field); lerr != nil {
			pasteErr = lerr
		} else if done {
			return "pasteboard, Paste menu item", nil
		} else {
			return "pasteboard; neither a Paste callout nor an edit-menu Paste item appeared - if the text did not land, clear the field and retry", nil
		}
	}
	if _, stderr, err := b.idb(ctx, idbPath, device, 30*time.Second, "ui", "text", text); err != nil {
		return "", fmt.Errorf("%v %s (pasteboard path failed first: %v)", err, strings.TrimSpace(stderr), pasteErr)
	}
	return "keystrokes via idb ui text (pasteboard unavailable: " + pasteErr.Error() + ")", nil
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

// idbElement mirrors the flat JSON emitted by `idb ui describe-all --json`.
type idbElement struct {
	AXLabel    string   `json:"AXLabel"`
	AXType     string   `json:"AXType"`
	AXValue    string   `json:"AXValue"`
	AXUniqueId string   `json:"AXUniqueId"`
	Traits     []string `json:"traits"`
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

// runner returns the command runner for idb invocations (injectable in tests).
func (b *iosBackend) runner() simCmdRunner {
	if b.run != nil {
		return b.run
	}
	return runCommand
}

// idbPrefix returns the global idb arguments: when the companion is only
// reachable through a known Homebrew path (not on PATH) it must be named
// explicitly or the client fails with "Could not find idb_companion".
func (b *iosBackend) idbPrefix(locator *idbLocator) []string {
	if locator == nil || locator.lookPath == nil || locator.stat == nil {
		return nil
	}
	if _, err := locator.lookPath("idb_companion"); err == nil {
		return nil // on PATH: the client finds it by name
	}
	for _, c := range locator.companionCandidates() {
		if locator.stat(c) {
			return []string{"--companion-path", c}
		}
	}
	return nil
}

// idbConnectError reports whether a failed idb invocation looks like a
// companion/connection problem (worth one reconnect + retry) rather than a
// rejected action (retrying `ui text` would type twice).
func idbConnectError(stderr string) bool {
	s := strings.ToLower(stderr)
	for _, k := range []string{"companion", "connect", "connection", "unreachable", "grpc", "transport", "timed out", "timeout", "not running", "refused", "closed"} {
		if strings.Contains(s, k) {
			return true
		}
	}
	return false
}

func (b *iosBackend) connectIDB(ctx context.Context, locator *idbLocator, idbPath, device string) error {
	_, stderr, err := b.runner()(ctx, idbConnectTimeout, idbPath, append(append(append([]string{}, b.idbPrefix(locator)...), "connect"), device)...)
	if err != nil {
		hint := ""
		if strings.Contains(strings.ToLower(stderr), "companion") {
			hint = " (idb_companion missing? brew install idb-companion)"
		}
		return fmt.Errorf("idb connect %s failed: %v %s%s", device, err, strings.TrimSpace(stderr), hint)
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

// idb runs one `idb` invocation against the device, connecting first. Only
// connection-class failures get one reconnect + retry (a stale companion after
// a simulator restart); a rejected action is returned as is.
func (b *iosBackend) idb(ctx context.Context, idbPath, device string, timeout time.Duration, args ...string) (string, string, error) {
	run := b.runner()
	locator := b.idbLocator
	prefix := b.idbPrefix(locator)
	full := append(append(append([]string{}, prefix...), args...), "--udid", device)
	b.mu.Lock()
	connected := b.idbConnected[device]
	b.mu.Unlock()
	if !connected {
		if err := b.connectIDB(ctx, locator, idbPath, device); err != nil {
			return "", "", err
		}
	}
	out, stderr, err := run(ctx, timeout, idbPath, full...)
	if err == nil {
		return out, stderr, nil
	}
	if !idbConnectError(stderr + " " + err.Error()) {
		return out, stderr, fmt.Errorf("%v %s", err, strings.TrimSpace(stderr))
	}
	// Reconnect once and retry.
	b.markIDBDisconnected(device)
	if cerr := b.connectIDB(ctx, locator, idbPath, device); cerr != nil {
		return out, stderr, cerr
	}
	out, stderr, err = run(ctx, timeout, idbPath, full...)
	if err != nil {
		return out, stderr, fmt.Errorf("%v %s", err, strings.TrimSpace(stderr))
	}
	return out, stderr, nil
}
