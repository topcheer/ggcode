package tui

import (
	"encoding/json"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
)

func statuslineTestConfig() *config.Config {
	cfg := &config.Config{}
	cfg.StatusLine.Command = "true"
	return cfg
}

func statuslineTestCommand(t *testing.T, script string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("sh-based statusline script test is unix-only")
	}
	return script
}

func TestRunStatuslineFirstLineAndStdin(t *testing.T) {
	cmd := statuslineTestCommand(t, `grep -q '"model"' && printf 'AAA\nBBB\n'`)
	payload := statuslinePayload{Version: "ggcode"}
	payload.Model.ID = "test-model"
	got := runStatuslineCommand(cmd, payload, 2*time.Second)
	if got != "AAA" {
		t.Fatalf("runStatuslineCommand = %q, want AAA (first line only)", got)
	}
}

func TestRunStatuslineTimeout(t *testing.T) {
	cmd := statuslineTestCommand(t, `sleep 5`)
	start := time.Now()
	got := runStatuslineCommand(cmd, statuslinePayload{}, 80*time.Millisecond)
	if got != "" {
		t.Fatalf("timeout run = %q, want empty", got)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatalf("timeout not enforced, took %s", time.Since(start))
	}
}

func TestRunStatuslineErrorReturnsEmpty(t *testing.T) {
	cmd := statuslineTestCommand(t, `exit 3`)
	got := runStatuslineCommand(cmd, statuslinePayload{}, time.Second)
	if got != "" {
		t.Fatalf("failing run = %q, want empty", got)
	}
}

func TestStatuslineTruncate(t *testing.T) {
	long := strings.Repeat("x", 500)
	got := statuslineTruncate(long, 20)
	if len([]rune(got)) > 21 {
		t.Fatalf("truncate produced %d runes, want <=21", len([]rune(got)))
	}
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("truncate missing ellipsis: %q", got)
	}
}

func TestBuildStatuslinePayloadJSON(t *testing.T) {
	m := Model{}
	m.config = statuslineTestConfig()
	p := m.buildStatuslinePayload()
	if p.HookEventName != "statusline" || p.Version == "" {
		t.Fatalf("unexpected payload: %+v", p)
	}
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, key := range []string{"hook_event_name", "model", "workspace", "context", "cost"} {
		if !strings.Contains(string(b), `"`+key+`"`) {
			t.Fatalf("payload JSON missing %q: %s", key, b)
		}
	}
}

func TestHandleStatuslineMsgCacheAndStale(t *testing.T) {
	m := Model{}
	m.config = statuslineTestConfig()
	m.statusline = &statuslineState{seq: 2}
	updated, _ := m.handleStatuslineMsg(statuslineMsg{seq: 2, text: "hello"})
	if got := updated.statusline.text; got != "hello" {
		t.Fatalf("cache = %q, want hello", got)
	}
	// Stale seq (older than sl.seq) must not overwrite.
	m.statusline.seq = 5
	updated, _ = m.handleStatuslineMsg(statuslineMsg{seq: 3, text: "stale"})
	if got := updated.statusline.text; got != "hello" {
		t.Fatalf("stale overwrite: %q, want hello", got)
	}
}

func TestStatuslineBarNilSafe(t *testing.T) {
	m := Model{}
	m.config = statuslineTestConfig()
	if got := m.statuslineBar(); got != "" {
		t.Fatalf("nil state bar = %q, want empty", got)
	}
}

func TestRefreshStatuslineDisabledNoop(t *testing.T) {
	m := Model{}
	m.config = &config.Config{} // no command: disabled
	m.refreshStatusline()       // must not panic or spawn anything
	if m.statusline != nil {
		t.Fatal("disabled config should not allocate statusline state")
	}
}
