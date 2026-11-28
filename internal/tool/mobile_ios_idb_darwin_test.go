//go:build darwin

package tool

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

const sampleDescribeAll = `[
 {"AXLabel":"GGCode Mobile","AXUniqueId":null,"frame":{"height":874,"width":402,"y":0,"x":0},"AXValue":null},
 {"AXLabel":"Theme: Midnight","AXType":"Button","frame":{"height":48,"y":66,"x":312,"width":48},"AXValue":null},
 {"AXLabel":"","AXType":"TextField","frame":{"height":44,"y":500,"x":20,"width":362},"AXValue":"wss://example","traits":["TextEntry","IsEditing","Scrollable"]},
 {"AXLabel":"no frame","AXType":"StaticText","frame":{"height":0,"width":0,"y":0,"x":0}}
]`

func TestIDBParseDescribeAll(t *testing.T) {
	root, err := parseIDBDescribeAll(sampleDescribeAll, "SIM-UDID")
	if err != nil {
		t.Fatal(err)
	}
	if len(root.Children) != 3 {
		t.Fatalf("expected 3 elements with frames (zero-frame dropped), got %d", len(root.Children))
	}
	theme := root.Children[1]
	if theme.Label != "Theme: Midnight" || theme.Type != "button" {
		t.Fatalf("unexpected theme element: %+v", theme)
	}
	if cx, cy := theme.Rect.center(); cx != 336 || cy != 90 {
		t.Fatalf("theme center = %d,%d, want 336,90", cx, cy)
	}
	if root.Children[2].Value != "wss://example" {
		t.Fatalf("value not carried: %+v", root.Children[2])
	}
	out := formatSnapshot(root, "Platform: iOS")
	if !strings.Contains(out, "@e2 [button] \"Theme: Midnight\"") {
		t.Fatalf("snapshot text missing ref line:\n%s", out)
	}
	wrapped := `{"result":[{"AXLabel":"Ok","AXType":"Button","frame":{"x":1,"y":2,"width":10,"height":10}}]}`
	if _, err := parseIDBDescribeAll(wrapped, "u"); err != nil {
		t.Fatalf("wrapped form should parse: %v", err)
	}
	if _, err := parseIDBDescribeAll("not json", "u"); err == nil {
		t.Fatal("garbage input must fail")
	}
	if _, err := parseIDBDescribeAll(`[]`, "u"); err == nil {
		t.Fatal("empty element list must fail")
	}
}

func TestIDBKeyMapping(t *testing.T) {
	for key, want := range map[string]int{"Enter": 40, "escape": 41, "Tab": 43, "ArrowUp": 82} {
		if got, ok := idbKeyFor(key); !ok || got != want {
			t.Fatalf("idbKeyFor(%q) = %d,%v want %d", key, got, ok, want)
		}
	}
	if _, ok := idbKeyFor("home"); ok {
		t.Fatal("home is a button press, not a keycode")
	}
}

func TestSnapshotTextCollapsesAndTruncates(t *testing.T) {
	if got := snapshotText("  line one\nline   two\ttab  "); got != "line one line two tab" {
		t.Fatalf("whitespace not collapsed: %q", got)
	}
	got := snapshotText(strings.Repeat("中文ab", 100))
	// 160 bytes = 20 x "中文ab" (4 runes each) + the ellipsis = 81 runes, valid UTF-8 throughout.
	if !strings.HasSuffix(got, "…") || len([]rune(got)) != 81 || !utf8.ValidString(got) {
		t.Fatalf("long label not truncated on a rune boundary: %d runes %q", len([]rune(got)), got[len(got)-6:])
	}
}

func TestPasteboardMatchesToleratesSingleTrailingNewline(t *testing.T) {
	if !pasteboardMatches("abc", "abc") || !pasteboardMatches("abc\n", "abc") || !pasteboardMatches("测试🎉", "测试🎉") {
		t.Fatal("exact match / trailing-newline tolerance broken")
	}
	if pasteboardMatches("abc", "abd") || pasteboardMatches("old wss url", "测试输入abc🎉") || pasteboardMatches("abc\n\n", "abc") {
		t.Fatal("stale or different pasteboard content must not count as a match")
	}
}

func TestIDBConnectErrorClassification(t *testing.T) {
	yes := []string{
		"idb: Could not find idb_companion",
		"grpc.RpcError: failed to connect",
		"companion not reachable",
		"Error: connection closed",
	}
	no := []string{"Invalid keycode", "text contains unsupported characters", "Unknown command"}
	for _, s := range yes {
		if !idbConnectError(s) {
			t.Errorf("should be a connection error: %q", s)
		}
	}
	for _, s := range no {
		if idbConnectError(s) {
			t.Errorf("should NOT be a connection error: %q", s)
		}
	}
}

// locatorFixture builds a locator with an in-memory filesystem for the known
// paths and a recorded installer.
type locatorFixture struct {
	l        *idbLocator
	onPath   map[string]string // name -> path (LookPath)
	exists   map[string]bool   // known-path stat
	started  []string          // scripts handed to the installer
	logPath  string
	logWrite func(logPath, script string)
}

func newLocatorFixture(home string) *locatorFixture {
	f := &locatorFixture{l: newIDBLocator(), onPath: map[string]string{}, exists: map[string]bool{}}
	f.l.home = home
	f.l.logDir = filepath.Join(home, ".ggcode", "logs")
	f.l.lookPath = func(name string) (string, error) {
		if p, ok := f.onPath[name]; ok {
			return p, nil
		}
		return "", errors.New("not found")
	}
	f.l.stat = func(p string) bool { return f.exists[p] }
	f.l.startInstall = func(script, logPath string) error {
		f.started = append(f.started, script)
		f.logPath = logPath
		if f.logWrite != nil {
			f.logWrite(logPath, script)
		}
		return nil
	}
	return f
}

func (f *locatorFixture) homePath(parts ...string) string {
	return filepath.Join(append([]string{f.l.home}, parts...)...)
}

func TestIDBLocatorResolution(t *testing.T) {
	home := t.TempDir()

	t.Run("on PATH", func(t *testing.T) {
		f := newLocatorFixture(home)
		f.onPath["idb"] = "/opt/homebrew/bin/idb"
		p, reason := f.l.resolve(context.Background())
		if p != "/opt/homebrew/bin/idb" || reason != "" || len(f.started) != 0 {
			t.Fatalf("resolve = %q,%q started=%v", p, reason, f.started)
		}
	})
	t.Run("known path when PATH lacks it (GUI launch)", func(t *testing.T) {
		f := newLocatorFixture(home)
		f.exists[f.homePath(".ggcode", "idb", "bin", "idb")] = true
		p, _ := f.l.resolve(context.Background())
		if p != f.homePath(".ggcode", "idb", "bin", "idb") {
			t.Fatalf("resolve = %q", p)
		}
	})
	t.Run("disabled by env", func(t *testing.T) {
		t.Setenv("GGCODE_MOBILE_NO_INSTALL", "1")
		f := newLocatorFixture(home)
		p, reason := f.l.resolve(context.Background())
		if p != "" || !strings.Contains(reason, "GGCODE_MOBILE_NO_INSTALL") || len(f.started) != 0 {
			t.Fatalf("resolve = %q,%q started=%v", p, reason, f.started)
		}
	})
	t.Run("install is detached, then picked up once the client exists", func(t *testing.T) {
		f := newLocatorFixture(home)
		p, reason := f.l.resolve(context.Background())
		if p != "" || !strings.Contains(reason, "installation started") || len(f.started) != 1 {
			t.Fatalf("first resolve = %q,%q started=%v", p, reason, f.started)
		}
		if !strings.Contains(f.started[0], "brew install idb-companion") || !strings.Contains(f.started[0], "fb-idb") || !strings.Contains(f.started[0], idbInstallDoneLine) {
			t.Fatalf("installer script missing steps:\n%s", f.started[0])
		}
		// Second call while the installer runs: in progress, NOT started again.
		_, reason = f.l.resolve(context.Background())
		if !strings.Contains(reason, "in progress") || len(f.started) != 1 {
			t.Fatalf("second resolve = %q started=%d", reason, len(f.started))
		}
		// Installer produced the client -> resolved from the known path.
		f.exists[f.homePath(".ggcode", "idb", "bin", "idb")] = true
		p, reason = f.l.resolve(context.Background())
		if p != f.homePath(".ggcode", "idb", "bin", "idb") || reason != "" {
			t.Fatalf("after install resolve = %q,%q", p, reason)
		}
	})
	t.Run("installer finished without a client -> definitive failure, not retried", func(t *testing.T) {
		f := newLocatorFixture(home)
		f.logWrite = func(logPath, script string) {
			_ = os.MkdirAll(filepath.Dir(logPath), 0o755)
			_ = os.WriteFile(logPath, []byte("[idb-install] brew not found\n"+idbInstallDoneLine+" rc=1\n"), 0o644)
		}
		f.l.resolve(context.Background())
		_, reason := f.l.resolve(context.Background())
		if !strings.Contains(reason, "finished without a usable client") || !strings.Contains(reason, "brew not found") {
			t.Fatalf("reason = %q", reason)
		}
		// No further install attempts, even later.
		_, _ = f.l.resolve(context.Background())
		if len(f.started) != 1 {
			t.Fatalf("install retried after a finished failure: %d", len(f.started))
		}
	})
	t.Run("installer cannot start -> definitive reason", func(t *testing.T) {
		f := newLocatorFixture(home)
		f.l.startInstall = func(string, string) error { return errors.New("permission denied") }
		_, reason := f.l.resolve(context.Background())
		if !strings.Contains(reason, "could not be started") || !strings.Contains(reason, "permission denied") {
			t.Fatalf("reason = %q", reason)
		}
	})
}

func TestIDBInstallScriptIsDetachedSafe(t *testing.T) {
	l := newIDBLocator()
	script := l.installScript()
	// Must never read stdin (stdin is closed) and must always terminate with the done line.
	if strings.Contains(script, "read ") {
		t.Fatal("installer must not read stdin")
	}
	if strings.Count(script, idbInstallDoneLine) != 2 {
		t.Fatalf("expected a done line on both success and failure paths, got %d", strings.Count(script, idbInstallDoneLine))
	}
	if !strings.HasPrefix(strings.TrimSpace(script), "#!/bin/sh") {
		t.Fatal("installer must be a /bin/sh script")
	}
	// Homebrew 7 untrusted-tap handling: only the idb maintainers' tap may be trusted.
	if !strings.Contains(script, "brew trust --tap") || !strings.Contains(script, "meta/fb|facebook/fb") || !strings.Contains(script, "refusing to trust unknown tap") {
		t.Fatal("installer must handle the untrusted-tap refusal without trusting arbitrary taps")
	}
	// The tap-name extraction must yield exactly "meta/fb" from Homebrew's real
	// refusal line (a trailing sentence dot must not leak into the tap name).
	extract := `out="Error: Refusing to load formula meta/fb/idb-companion from untrusted tap meta/fb.\nRun brew trust meta/fb to trust it."; tap=$(printf '%s' "$out" | sed -n 's/.*from untrusted tap \([A-Za-z0-9_./-]*\)\.\{0,1\}.*/\1/p' | head -1 | sed 's/\.$//'); printf '%s' "$tap"`
	got, stderr, err := runCommand(context.Background(), 10*time.Second, "/bin/sh", "-c", extract)
	if err != nil || strings.TrimSpace(got) != "meta/fb" {
		t.Fatalf("tap extraction = %q (%v %s), want meta/fb", got, err, stderr)
	}
	if !strings.Contains(script, "from untrusted tap") {
		t.Fatal("installer must parse the tap from Homebrew's refusal message")
	}
	// Sanity: the script must be syntactically valid for /bin/sh.
	if err := shSyntaxCheck(script); err != nil {
		t.Fatalf("installer script does not parse: %v", err)
	}
}

func shSyntaxCheck(script string) error {
	f, err := os.CreateTemp("", "idb-install-*.sh")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.WriteString(script); err != nil {
		f.Close()
		return err
	}
	f.Close()
	_, stderr, err := runCommand(context.Background(), 10*time.Second, "/bin/sh", "-n", f.Name())
	if err != nil {
		return fmt.Errorf("%v %s", err, stderr)
	}
	return nil
}

// fakeIDBRunner records idb invocations (without the client path / --udid)
// and answers per subcommand. failWith simulates a companion outage for the
// next ui call.
type fakeIDBRunner struct {
	calls    []string
	failWith string
	describe string
	// describeSeq, when non-empty, is consumed one entry per describe-all call
	// (falling back to describe) so tests can script a changing UI.
	describeSeq []string
	pasted      []string // text handed to the pasteboard by the backend
	pasteErr    error
}

func (f *fakeIDBRunner) run(_ context.Context, _ time.Duration, name string, args ...string) (string, string, error) {
	if n := len(args); n >= 2 && args[n-2] == "--udid" {
		args = args[:n-2]
	}
	line := "idb " + strings.Join(args, " ")
	f.calls = append(f.calls, line)
	if line == "idb connect SIM-UDID" {
		return "connected", "", nil
	}
	if f.failWith != "" && strings.HasPrefix(line, "idb ui ") && !strings.Contains(line, "describe-all") {
		msg := f.failWith
		f.failWith = ""
		return "", msg, errors.New("exit 1")
	}
	if strings.HasPrefix(line, "idb ui describe-all") {
		if len(f.describeSeq) > 0 {
			out := f.describeSeq[0]
			f.describeSeq = f.describeSeq[1:]
			return out, "", nil
		}
		return f.describe, "", nil
	}
	return "", "", nil
}

func newTestIOSBackend(f *fakeIDBRunner) *iosBackend {
	return &iosBackend{
		xcrunPath:    "/usr/bin/xcrun",
		snapshots:    map[string]*uiElement{},
		idbConnected: map[string]bool{},
		idbLocator:   &idbLocator{done: true, path: "/fake/idb", lookPath: func(string) (string, error) { return "", errors.New("x") }},
		run:          f.run,
		paste: func(_ context.Context, device, text string) error {
			f.pasted = append(f.pasted, device+"|"+text)
			return f.pasteErr
		},
	}
}

func TestIOSBackendSnapshotAndRefActionsThroughIDB(t *testing.T) {
	f := &fakeIDBRunner{describe: sampleDescribeAll}
	b := newTestIOSBackend(f)
	const udid = "SIM-UDID"

	res, err := b.snapshot(context.Background(), udid)
	if err != nil || res.IsError {
		t.Fatalf("snapshot: %v %s", err, res.Content)
	}
	if !strings.Contains(res.Content, "Driver: idb (3 elements)") || !strings.Contains(res.Content, "@e2 [button] \"Theme: Midnight\"") {
		t.Fatalf("snapshot content:\n%s", res.Content)
	}

	f.calls = nil
	if res, _ := b.tap(context.Background(), udid, "@e2", 0, 0); res.IsError {
		t.Fatalf("tap by ref: %s", res.Content)
	}
	if res, _ := b.typeText(context.Background(), udid, "@e3", "hello", 0, 0); res.IsError {
		t.Fatalf("type by ref: %s", res.Content)
	}
	ex, ey := 336, 600
	if res, _ := b.swipe(context.Background(), udid, "@e2", 0, 0, &ex, &ey); res.IsError {
		t.Fatalf("swipe by ref: %s", res.Content)
	}
	if res, _ := b.press(context.Background(), udid, "home"); res.IsError {
		t.Fatalf("press home: %s", res.Content)
	}
	if res, _ := b.press(context.Background(), udid, "Enter"); res.IsError {
		t.Fatalf("press enter: %s", res.Content)
	}
	want := []string{
		"idb ui tap 336 90",
		"idb ui tap 201 522",                // pre-tap on the text field centre (20 + 362/2, 500 + 44/2)
		"idb ui describe-all --json",        // wait for the IsEditing (focused) trait
		"idb ui key 9 --command",            // Cmd+V (populated field: tried first)
		"idb ui describe-all --json",        // poll for the paste-confirmation callout (none here)
		"idb ui tap 201 522 --duration 1.0", // long press on the focused field for the edit menu
		"idb ui describe-all --json",        // poll for the menu's Paste item (none here)
		"idb ui swipe 336 90 336 600 --duration 0.3",
		"idb ui button HOME",
		"idb ui key 40",
	}
	// Focus/callout/menu polling issues several describe-all calls; compare the
	// sequence with consecutive duplicates collapsed.
	collapsed := []string{}
	for i, c := range f.calls {
		if i > 0 && c == "idb ui describe-all --json" && f.calls[i-1] == "idb ui describe-all --json" {
			continue
		}
		collapsed = append(collapsed, c)
	}
	if strings.Join(collapsed, "\n") != strings.Join(want, "\n") {
		t.Fatalf("idb calls:\n%s\nwant:\n%s", strings.Join(collapsed, "\n"), strings.Join(want, "\n"))
	}
	if len(f.pasted) != 1 || f.pasted[0] != "SIM-UDID|hello" {
		t.Fatalf("type must put the exact text on the device pasteboard once, got %v", f.pasted)
	}

	if res, _ := b.tap(context.Background(), "OTHER-UDID", "@e2", 0, 0); !res.IsError || !strings.Contains(res.Content, "prior snapshot") {
		t.Fatalf("expected missing-snapshot error, got: %s", res.Content)
	}
	if res, _ := b.tap(context.Background(), udid, "@e99", 0, 0); !res.IsError || !strings.Contains(res.Content, "not found") {
		t.Fatalf("expected stale-ref error, got: %s", res.Content)
	}
}

func TestIOSBackendRetriesOnlyConnectionErrors(t *testing.T) {
	f := &fakeIDBRunner{describe: sampleDescribeAll}
	b := newTestIOSBackend(f)
	const udid = "SIM-UDID"
	if _, err := b.snapshot(context.Background(), udid); err != nil {
		t.Fatal(err)
	}

	// Connection-class failure: reconnect + retry once.
	f.failWith = "Could not find idb_companion"
	f.calls = nil
	if res, _ := b.tap(context.Background(), udid, "", 10, 20); res.IsError {
		t.Fatalf("tap should succeed after reconnect: %s", res.Content)
	}
	joined := strings.Join(f.calls, "\n")
	if strings.Count(joined, "idb ui tap") != 2 || strings.Count(joined, "idb connect") != 1 {
		t.Fatalf("expected one retry after a reconnect, calls:\n%s", joined)
	}

	// Rejected action (not a connection problem): must NOT be replayed, or a
	// partially-typed `ui text` would be sent twice. Paste is disabled here so
	// the `ui text` fallback is what gets rejected.
	f.pasteErr = errors.New("no pasteboard")
	f.failWith = "text contains unsupported characters"
	f.calls = nil
	res, _ := b.typeText(context.Background(), udid, "", "abc", 0, 0)
	if !res.IsError || !strings.Contains(res.Content, "pasteboard path failed first") {
		t.Fatalf("rejected text must surface as an error that names the paste failure, got: %s", res.Content)
	}
	joined = strings.Join(f.calls, "\n")
	if strings.Count(joined, "idb ui text") != 1 || strings.Contains(joined, "idb connect") || strings.Contains(joined, "ui key 9") {
		t.Fatalf("rejected action must not be retried (and no cmd+v without a paste), calls:\n%s", joined)
	}
	// Paste path healthy: exactly one cmd+v, no keystroke typing at all.
	f.pasteErr = nil
	f.failWith = ""
	f.calls = nil
	if res, _ := b.typeText(context.Background(), udid, "", "abc", 0, 0); res.IsError {
		t.Fatalf("paste path must succeed: %s", res.Content)
	}
	// No callout / no menu item in the fake tree: 1 focus poll + 5 callout polls
	// + 5 menu polls, never typed via keystrokes.
	if joined = strings.Join(f.calls, "\n"); strings.Count(joined, "idb ui describe-all --json") != 11 || strings.Contains(joined, "idb ui text") {
		t.Fatalf("healthy paste path must issue cmd+v + describe-all polls only, calls:\n%s", joined)
	}
	if res, _ := b.typeText(context.Background(), udid, "", "abc", 0, 0); !strings.Contains(res.Content, "neither a Paste callout") {
		t.Fatalf("result should state that neither callout nor menu item appeared: %s", res.Content)
	}
	// cmd+v rejected (not a connection error): fall back to ui text exactly once.
	f.failWith = "key rejected"
	f.calls = nil
	if res, _ := b.typeText(context.Background(), udid, "", "abc", 0, 0); res.IsError {
		t.Fatalf("fallback to ui text must succeed: %s", res.Content)
	}
	if joined = strings.Join(f.calls, "\n"); strings.Count(joined, "ui key 9") != 1 || strings.Count(joined, "idb ui text") != 1 {
		t.Fatalf("expected one cmd+v then one ui text, calls:\n%s", joined)
	}
}

const (
	fakeTreeEmptyField     = `[{"AXLabel":"","AXType":"TextField","frame":{"x":68,"y":706,"width":266,"height":154},"traits":["TextEntry","IsEditing"]}]`
	fakeTreePopulatedField = `[{"AXLabel":"","AXType":"TextField","frame":{"x":68,"y":706,"width":266,"height":154},"AXValue":"draft","traits":["TextEntry","IsEditing"]}]`
	fakeTreeMenuPaste      = `[{"AXLabel":"","AXType":"TextField","frame":{"x":68,"y":706,"width":266,"height":154},"traits":["TextEntry","IsEditing"]},{"AXLabel":"Paste","frame":{"x":50,"y":444,"width":70,"height":44}}]`
	fakeTreeMenuCut        = `[{"AXLabel":"","AXType":"TextField","frame":{"x":68,"y":706,"width":266,"height":154},"AXValue":"draft","traits":["TextEntry","IsEditing"]},{"AXLabel":"Cut","frame":{"x":50,"y":444,"width":70,"height":44}},{"AXLabel":"Paste","frame":{"x":130,"y":444,"width":70,"height":44}}]`
)

func TestIOSBackendTypeUsesEditMenuPasteOnEmptyField(t *testing.T) {
	// iOS 16+ raises a "Paste" confirmation (or silently drops the paste) for
	// pasteboard content from another app; the edit menu's Paste item is a real
	// user gesture and works for any content, so an empty field goes there first.
	f := &fakeIDBRunner{describeSeq: []string{fakeTreeEmptyField, fakeTreeMenuPaste}}
	b := newTestIOSBackend(f)
	res, _ := b.typeText(context.Background(), "SIM-UDID", "", "测试 wss://x.y", 0, 0)
	if res.IsError || !strings.Contains(res.Content, "Paste menu item") {
		t.Fatalf("empty field must paste through the edit menu: %s", res.Content)
	}
	if len(f.pasted) != 1 || f.pasted[0] != "SIM-UDID|测试 wss://x.y" {
		t.Fatalf("pasteboard must receive the exact unicode text once, got %v", f.pasted)
	}
	joined := strings.Join(f.calls, "\n")
	if !strings.Contains(joined, "idb ui tap 201 783 --duration 1.0") || !strings.Contains(joined, "idb ui tap 85 466") {
		t.Fatalf("expected long press on the field centre (201,783) then the Paste item (85,466), calls:\n%s", joined)
	}
	if strings.Contains(joined, "ui key 9") || strings.Contains(joined, "idb ui text") {
		t.Fatalf("edit-menu path must not send Cmd+V or keystrokes, calls:\n%s", joined)
	}
}

func TestIOSBackendTypeFallsBackToCmdVThenCallout(t *testing.T) {
	// Empty field, edit menu shows no Paste item -> Cmd+V -> the "Paste"
	// callout appears on the 2nd poll and is tapped.
	f := &fakeIDBRunner{describeSeq: []string{
		fakeTreeEmptyField, // focus
		fakeTreeEmptyField, // long press, poll 1: no menu yet
		fakeTreeMenuPaste,  // long press, poll 2: Paste item present
	}}
	b := newTestIOSBackend(f)
	res, _ := b.typeText(context.Background(), "SIM-UDID", "", "abc", 0, 0)
	if res.IsError || !strings.Contains(res.Content, "Paste menu item") {
		t.Fatalf("menu item appearing late must still be used: %s", res.Content)
	}
}

func TestIOSBackendTypeNeverReplacesASelectedWord(t *testing.T) {
	// Populated field: Cmd+V raises nothing, the long press selects a word
	// (menu shows Cut) -> the menu is dismissed with Escape and nothing is
	// pasted over the selection.
	f := &fakeIDBRunner{describe: fakeTreePopulatedField, describeSeq: nil}
	f.describeSeq = append([]string{}, fakeTreePopulatedField)
	for i := 0; i < 5; i++ { // callout polls: nothing
		f.describeSeq = append(f.describeSeq, fakeTreePopulatedField)
	}
	f.describeSeq = append(f.describeSeq, fakeTreeMenuCut)
	b := newTestIOSBackend(f)
	res, _ := b.typeText(context.Background(), "SIM-UDID", "", "abc", 0, 0)
	if res.IsError || !strings.Contains(res.Content, "neither a Paste callout") {
		t.Fatalf("a selected word must not be pasted over; result: %s", res.Content)
	}
	joined := strings.Join(f.calls, "\n")
	if strings.Count(joined, "idb ui key 41") != 1 || strings.Contains(joined, "idb ui tap 85 466") || strings.Contains(joined, "idb ui tap 165 466") {
		t.Fatalf("expected Escape to dismiss the menu and no Paste tap, calls:\n%s", joined)
	}
}

func TestIOSBackendTypeCmdVCalloutOnPopulatedField(t *testing.T) {
	f := &fakeIDBRunner{describe: fakeTreePopulatedField}
	f.describeSeq = []string{fakeTreePopulatedField, fakeTreeMenuPaste}
	b := newTestIOSBackend(f)
	res, _ := b.typeText(context.Background(), "SIM-UDID", "", "abc", 0, 0)
	if res.IsError || !strings.Contains(res.Content, "Paste callout confirmed") {
		t.Fatalf("populated field: Cmd+V callout must be used: %s", res.Content)
	}
	joined := strings.Join(f.calls, "\n")
	if strings.Contains(joined, "--duration 1.0") {
		t.Fatalf("no long press needed when the callout appeared, calls:\n%s", joined)
	}
}

func TestIOSBackendTypeRefusesWithoutFocusedField(t *testing.T) {
	// Keystrokes/paste sent before a field has focus are dropped silently; the
	// tool must refuse instead of reporting "Typed".
	f := &fakeIDBRunner{describe: `[{"AXLabel":"Connect","frame":{"x":28,"y":552,"width":346,"height":50}},{"AXLabel":"","AXType":"TextField","frame":{"x":68,"y":706,"width":266,"height":154},"traits":["TextEntry"]}]`}
	b := newTestIOSBackend(f)
	res, _ := b.typeText(context.Background(), "SIM-UDID", "", "abc", 0, 0)
	if !res.IsError || !strings.Contains(res.Content, "keyboard focus") {
		t.Fatalf("expected a focus error, got: %s", res.Content)
	}
	if len(f.pasted) != 0 || strings.Contains(strings.Join(f.calls, "\n"), "ui key 9") {
		t.Fatalf("nothing must be pasted without focus, pasted=%v calls=%v", f.pasted, f.calls)
	}
}

func TestIOSBackendCompanionPrefixWhenNotOnPath(t *testing.T) {
	f := &fakeIDBRunner{describe: sampleDescribeAll}
	b := newTestIOSBackend(f)
	// companion only at the Homebrew path -> every invocation names it.
	b.idbLocator = &idbLocator{
		done: true, path: "/fake/idb",
		lookPath: func(string) (string, error) { return "", errors.New("not on PATH") },
		stat:     func(p string) bool { return p == "/opt/homebrew/bin/idb_companion" },
	}
	if _, err := b.snapshot(context.Background(), "SIM-UDID"); err != nil {
		t.Fatal(err)
	}
	if len(f.calls) == 0 || !strings.HasPrefix(f.calls[0], "idb --companion-path /opt/homebrew/bin/idb_companion connect SIM-UDID") {
		t.Fatalf("calls: %v", f.calls)
	}
}

func TestIOSBackendFallsBackWithoutIDB(t *testing.T) {
	t.Setenv("GGCODE_MOBILE_NO_INSTALL", "1")
	b := &iosBackend{
		xcrunPath:    "/usr/bin/false",
		snapshots:    map[string]*uiElement{},
		idbConnected: map[string]bool{},
		idbLocator:   newLocatorFixture(t.TempDir()).l, // nothing on PATH, install disabled by env
		run: func(_ context.Context, _ time.Duration, name string, args ...string) (string, string, error) {
			return "", "", errors.New("no xcrun")
		},
	}
	res, _ := b.snapshot(context.Background(), "U")
	if res.IsError || !strings.Contains(res.Content, "idb unavailable") || !strings.Contains(res.Content, "without idb") {
		t.Fatalf("snapshot fallback content: %s", res.Content)
	}
	if res, _ := b.tap(context.Background(), "U", "@e1", 0, 0); !res.IsError {
		t.Fatal("ref tap without a snapshot must refuse")
	}
	if res, _ := b.press(context.Background(), "U", "back"); !res.IsError {
		t.Fatal("back must be refused on iOS")
	}
}
