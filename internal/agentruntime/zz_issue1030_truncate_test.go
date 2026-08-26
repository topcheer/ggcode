package agentruntime

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/topcheer/ggcode/internal/util"
)

// Regression guard for #1030: the local byte-sliced truncate() in
// config_access.go split multi-byte runes and injected invalid UTF-8 into
// config tool output (listSectionCore extra_prompt / listSectionMCP cmd).
// It was replaced by util.Truncate (rune-safe). This test pins both the
// rune-safety of the replacement and the removal of the old behavior.
func TestConfigTruncateRuneSafe(t *testing.T) {
	// 40 CJK chars = 120 bytes; the old truncate(_, 80) cut at byte 80,
	// which is mid-rune (80 mod 3 == 2), producing invalid UTF-8.
	cjk := strings.Repeat("汉", 40)

	got := util.Truncate(cjk, 80)
	if !utf8.ValidString(got) {
		t.Errorf("util.Truncate(cjk, 80) invalid UTF-8: %q", got)
	}
	if want := 80; len([]rune(strings.TrimSuffix(got, "..."))) > want {
		t.Errorf("truncated runes exceed cap %d", want)
	}

	// MCP cmd case: shorter cap (60), still rune-safe.
	got60 := util.Truncate(cjk, 60)
	if !utf8.ValidString(got60) {
		t.Errorf("util.Truncate(cjk, 60) invalid UTF-8: %q", got60)
	}

	// Short input passes through untouched.
	if got := util.Truncate("short", 80); got != "short" {
		t.Errorf("short input must pass through, got %q", got)
	}
}
