//go:build darwin

package tool

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

const sampleDescribeAll = `[
 {"AXLabel":"GGCode Mobile","AXUniqueId":null,"frame":{"height":874,"width":402,"y":0,"x":0},"AXValue":null},
 {"AXLabel":"Theme: Midnight","AXType":"Button","frame":{"height":48,"y":66,"x":312,"width":48},"AXValue":null},
 {"AXLabel":"","AXType":"TextField","frame":{"height":44,"y":500,"x":20,"width":362},"AXValue":"wss://example"},
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

	// Wrapped {"result":[...]} form is accepted too.
	wrapped := `{"result":[{"AXLabel":"Ok","AXType":"Button","frame":{"x":1,"y":2,"width":10,"height":10}}]}`
	if _, err := parseIDBDescribeAll(wrapped, "u"); err != nil {
		t.Fatalf("wrapped form should parse: %v", err)
	}
	if _, err := parseIDBDescribeAll("not json", "u"); err == nil {
		t.Fatal("garbage input must fail")
	}
	if _, err := parseIDBDescribeAll(`[]`, "u"); err == nil {
		t.Fatal("empty element list must fail (nothing to reference)")
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

func TestIDBLocatorFindsClientWithoutInstalling(t *testing.T) {
	l := newIDBLocator()
	l.lookPath = func(name string) (string, error) {
		if name == "idb" {
			return "/opt/homebrew/bin/idb", nil
		}
		return "", errors.New("not found")
	}
	ran := false
	l.runner = func(ctx context.Context, d time.Duration, name string, args ...string) (string, string, error) {
		ran = true
		return "", "", nil
	}
	p, reason := l.resolve(context.Background())
	if p != "/opt/homebrew/bin/idb" || reason != "" {
		t.Fatalf("resolve = %q,%q", p, reason)
	}
	if _, _ = l.resolve(context.Background()); ran {
		t.Fatal("no installer must run when idb is on PATH")
	}
}

func TestIDBLocatorInstallsViaBrew(t *testing.T) {
	l := newIDBLocator()
	installed := false
	l.lookPath = func(name string) (string, error) {
		switch name {
		case "idb":
			if installed {
				return "/opt/homebrew/bin/idb", nil
			}
			return "", errors.New("not found")
		case "brew":
			return "/opt/homebrew/bin/brew", nil
		}
		return "", errors.New("not found")
	}
	var cmds []string
	l.runner = func(ctx context.Context, d time.Duration, name string, args ...string) (string, string, error) {
		cmds = append(cmds, name+" "+strings.Join(args, " "))
		if name == "brew" && args[0] == "install" {
			installed = true
		}
		return "", "", nil
	}
	p, reason := l.resolve(context.Background())
	if p != "/opt/homebrew/bin/idb" || reason != "" {
		t.Fatalf("resolve = %q,%q", p, reason)
	}
	if len(cmds) != 1 || cmds[0] != "brew install idb-companion" {
		t.Fatalf("unexpected install commands: %v", cmds)
	}
}

func TestIDBLocatorRespectsNoInstallAndCachesFailure(t *testing.T) {
	t.Setenv("GGCODE_MOBILE_NO_INSTALL", "1")
	l := newIDBLocator()
	l.lookPath = func(string) (string, error) { return "", errors.New("not found") }
	ran := false
	l.runner = func(ctx context.Context, d time.Duration, name string, args ...string) (string, string, error) {
		ran = true
		return "", "", nil
	}
	p, reason := l.resolve(context.Background())
	if p != "" || !strings.Contains(reason, "GGCODE_MOBILE_NO_INSTALL") {
		t.Fatalf("resolve = %q,%q", p, reason)
	}
	if ran {
		t.Fatal("installer must not run when disabled")
	}

	// A failing install is attempted once and the failure is cached.
	t.Setenv("GGCODE_MOBILE_NO_INSTALL", "0")
	l2 := newIDBLocator()
	l2.lookPath = func(name string) (string, error) {
		if name == "brew" {
			return "/opt/homebrew/bin/brew", nil
		}
		return "", errors.New("not found")
	}
	calls := 0
	l2.runner = func(ctx context.Context, d time.Duration, name string, args ...string) (string, string, error) {
		calls++
		return "", "boom", errors.New("exit 1")
	}
	p, reason = l2.resolve(context.Background())
	if p != "" || !strings.Contains(reason, "auto-install failed") || !strings.Contains(reason, "brew install idb-companion") {
		t.Fatalf("resolve = %q,%q", p, reason)
	}
	first := calls
	if _, _ = l2.resolve(context.Background()); calls != first {
		t.Fatalf("failed install retried on second resolve (%d -> %d calls)", first, calls)
	}
}

// fakeIDBRunner records idb invocations and answers per subcommand.
type fakeIDBRunner struct {
	calls []string
	// failOnce makes the next "ui <sub>" call fail once (stale companion).
	failOnce bool
	describe string
}

func (f *fakeIDBRunner) run(_ context.Context, _ time.Duration, name string, args ...string) (string, string, error) {
	if n := len(args); n >= 2 && args[n-2] == "--udid" {
		args = args[:n-2] // drop --udid <udid>
	}
	line := "idb " + strings.Join(args, " ") // name is the client path; record by subcommand
	f.calls = append(f.calls, line)
	if strings.HasPrefix(line, "idb connect") {
		return "connected", "", nil
	}
	if f.failOnce && strings.Contains(line, "idb ui ") {
		f.failOnce = false
		return "", "companion not reachable", errors.New("exit 1")
	}
	if strings.HasPrefix(line, "idb ui describe-all") {
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
		t.Fatalf("snapshot content:\n%s\ncalls: %s", res.Content, strings.Join(f.calls, " | "))
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
		"idb ui tap 201 522", // pre-tap on the text field centre (20 + 362/2, 500 + 44/2)
		"idb ui text hello",
		"idb ui swipe 336 90 336 600 --duration 0.3",
		"idb ui button HOME",
		"idb ui key 40",
	}
	if strings.Join(f.calls, "\n") != strings.Join(want, "\n") {
		t.Fatalf("idb calls:\n%s\nwant:\n%s", strings.Join(f.calls, "\n"), strings.Join(want, "\n"))
	}

	// Unknown ref / stale ref produce actionable errors, never a blind tap.
	if res, _ := b.tap(context.Background(), "OTHER-UDID", "@e2", 0, 0); !res.IsError || !strings.Contains(res.Content, "prior snapshot") {
		t.Fatalf("expected missing-snapshot error, got: %s", res.Content)
	}
	if res, _ := b.tap(context.Background(), udid, "@e99", 0, 0); !res.IsError || !strings.Contains(res.Content, "not found") {
		t.Fatalf("expected stale-ref error, got: %s", res.Content)
	}
}

func TestIOSBackendIDBReconnectsOnceOnStaleCompanion(t *testing.T) {
	f := &fakeIDBRunner{describe: sampleDescribeAll}
	b := newTestIOSBackend(f)
	const udid = "SIM-UDID"
	if _, err := b.snapshot(context.Background(), udid); err != nil {
		t.Fatal(err)
	}
	// Simulate a companion that went away (simulator rebooted): next ui call fails.
	f.failOnce = true
	f.calls = nil
	res, _ := b.tap(context.Background(), udid, "", 10, 20) // explicit coords: scale probe falls back to 1,1
	if res.IsError {
		t.Fatalf("tap should succeed after reconnect: %s", res.Content)
	}
	joined := strings.Join(f.calls, "\n")
	// connect (cached from snapshot) -> failing tap -> reconnect -> tap
	if strings.Count(joined, "idb ui tap") != 2 || strings.Count(joined, "idb connect") < 1 {
		t.Fatalf("expected one retry after reconnect, calls:\n%s", joined)
	}
}

func TestIOSBackendFallsBackWithoutIDB(t *testing.T) {
	// idb unavailable and install disabled -> snapshot explains, tap by ref refuses.
	t.Setenv("GGCODE_MOBILE_NO_INSTALL", "1")
	b := &iosBackend{
		xcrunPath:    "/usr/bin/false",
		snapshots:    map[string]*uiElement{},
		idbConnected: map[string]bool{},
		idbLocator:   &idbLocator{lookPath: func(string) (string, error) { return "", errors.New("not found") }},
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

func TestSnapshotTextCollapsesAndTruncates(t *testing.T) {
	got := snapshotText("  line one\nline   two\ttab  ")
	if got != "line one line two tab" {
		t.Fatalf("whitespace not collapsed: %q", got)
	}
	long := strings.Repeat("中文ab", 100)
	got = snapshotText(long)
	if !strings.HasSuffix(got, "…") || len([]rune(got)) > snapshotMaxText/3+2 {
		t.Fatalf("long label not truncated on a rune boundary: %d runes", len([]rune(got)))
	}
}
