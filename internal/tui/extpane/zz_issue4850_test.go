//go:build darwin

package extpane

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// TestIssue4850_BackendNilWhenNotRunning pins #4850: when the iTerm2 process
// is not running, the backend constructor must return nil WITHOUT ever
// sending `tell application "iTerm2"` to osascript - that AppleScript form
// launches a non-running app, which is how surviving daemon probes
// resurrected a terminal the user had deliberately quit (~30-60s after
// quit, matching the inbound-trigger cycle).
func TestIssue4850_BackendNilWhenNotRunning(t *testing.T) {
	var scripts []string
	restore := stubOsa(t, func(_ context.Context, script string) (string, error) {
		scripts = append(scripts, script)
		if strings.Contains(script, `exists process "iTerm2"`) {
			return "false\n", nil // System Events probe: not running
		}
		return "", errors.New("stub: must not be reached")
	})
	defer restore()

	b := newITerm2Backend()
	if b != nil {
		t.Fatalf("newITerm2Backend() = %v, want nil when iTerm2 is not running", b)
	}
	if len(scripts) != 1 {
		t.Fatalf("expected exactly 1 probe script, got %d: %q", len(scripts), scripts)
	}
	if strings.Contains(scripts[0], `tell application "iTerm2"`) {
		t.Fatalf("probe script launches iTerm2 (tell application form): %q", scripts[0])
	}
	if !strings.Contains(scripts[0], "System Events") {
		t.Fatalf("probe script does not use System Events exists process: %q", scripts[0])
	}
}

// TestIssue4850_CreateTabRefusesQuitTerminal pins the CreateTab half of
// #4850: the create script begins with `activate`, so running it against a
// quit iTerm2 would relaunch it. CreateTab must guard at the boundary and
// return an error instead - the Manager failed-map then marks the pane
// permanently dark, never a resurrection.
func TestIssue4850_CreateTabRefusesQuitTerminal(t *testing.T) {
	restore := stubOsa(t, func(_ context.Context, script string) (string, error) {
		if strings.Contains(script, `exists process "iTerm2"`) {
			return "false\n", nil
		}
		return "", errors.New("stub: must not be reached")
	})
	defer restore()

	b := &iterm2Backend{}
	_, err := b.CreateTab(context.Background(), "agent", "/tmp/agent.log")
	if err == nil {
		t.Fatal("CreateTab succeeded against a non-running iTerm2, want refusal error")
	}
	if !strings.Contains(err.Error(), "#4850") {
		t.Errorf("error lacks #4850 context: %v", err)
	}
}

// TestIssue4850_CreateTabProceedsWhenRunning pins that a live iTerm2 still
// gets its tab: the guard must not break the normal path. The stub fakes a
// running process and a plausible session ID.
func TestIssue4850_CreateTabProceedsWhenRunning(t *testing.T) {
	restore := stubOsa(t, func(_ context.Context, script string) (string, error) {
		if strings.Contains(script, `exists process "iTerm2"`) {
			return "true\n", nil
		}
		if strings.Contains(script, "create tab") {
			return "session-1234\n", nil
		}
		return "", errors.New("stub: unexpected script " + script)
	})
	defer restore()

	b := &iterm2Backend{}
	id, err := b.CreateTab(context.Background(), "agent", "/tmp/agent.log")
	if err != nil {
		t.Fatalf("CreateTab on running iTerm2: %v", err)
	}
	if id != "session-1234" {
		t.Errorf("session ID = %q, want session-1234", id)
	}
}

// stubOsa swaps the package osascript runner for the test duration so no
// real osascript ever spawns from these tests (which on macOS would launch
// iTerm2 - the very defect #4850 describes, from the test side).
func stubOsa(t *testing.T, fn func(ctx context.Context, script string) (string, error)) func() {
	t.Helper()
	old := osascriptCommand
	osascriptCommand = fn
	return func() { osascriptCommand = old }
}
