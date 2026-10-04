package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// /tape command probes: env side effects of start/replay/stop and the
// status rendering path. The tape mode itself is agent-constructor-time,
// so the TUI layer's contract is "writes the right env value + tells the
// user /restart is required".

func TestTapeCommandStartSetsRecordEnv(t *testing.T) {
	t.Setenv("GGCODE_TOOL_TAPE", "")
	m := &Model{}
	path := filepath.Join(t.TempDir(), "s.tape.json")
	m.handleTapeCommand([]string{"/tape", "start", path})
	if got := os.Getenv("GGCODE_TOOL_TAPE"); got != "record:"+path {
		t.Fatalf("start must set record env, got %q", got)
	}
}

func TestTapeCommandReplayRequiresExistingTape(t *testing.T) {
	t.Setenv("GGCODE_TOOL_TAPE", "")
	m := &Model{}
	m.handleTapeCommand([]string{"/tape", "replay", filepath.Join(t.TempDir(), "missing.json")})
	if got := os.Getenv("GGCODE_TOOL_TAPE"); got != "" {
		t.Fatalf("replay of a missing tape must not arm env, got %q", got)
	}
	// A real file arms replay mode.
	tape := filepath.Join(t.TempDir(), "ok.json")
	if err := os.WriteFile(tape, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	m.handleTapeCommand([]string{"/tape", "replay", tape})
	if got := os.Getenv("GGCODE_TOOL_TAPE"); got != "replay:"+tape {
		t.Fatalf("replay must set replay env, got %q", got)
	}
}

func TestTapeCommandStopClearsEnv(t *testing.T) {
	t.Setenv("GGCODE_TOOL_TAPE", "record:/old.tape.json")
	m := &Model{}
	m.handleTapeCommand([]string{"/tape", "stop"})
	if got := os.Getenv("GGCODE_TOOL_TAPE"); got != "" {
		t.Fatalf("stop must clear env, got %q", got)
	}
}

// status with no live agent falls back to "off" without panicking.
func TestTapeCommandStatusWithoutAgent(t *testing.T) {
	t.Setenv("GGCODE_TOOL_TAPE", "")
	m := &Model{}
	if cmd := m.handleTapeCommand([]string{"/tape", "status"}); cmd != nil {
		t.Fatalf("status returns nil cmd, got %v", cmd)
	}
}

// defaultTapePath lands under ~/.ggcode/tapes with the .tape.json suffix.
func TestTapeDefaultPathShape(t *testing.T) {
	p := defaultTapePath()
	if !strings.Contains(p, ".ggcode") || !strings.HasSuffix(p, ".tape.json") {
		t.Fatalf("unexpected default tape path: %s", p)
	}
}
