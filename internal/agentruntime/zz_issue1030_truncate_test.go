package agentruntime

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/topcheer/ggcode/internal/util"
)

func TestConfigTruncateRuneSafe(t *testing.T) {

	cjk := strings.Repeat("汉", 40)

	got := util.Truncate(cjk, 80)
	if !utf8.ValidString(got) {
		t.Errorf("util.Truncate(cjk, 80) invalid UTF-8: %q", got)
	}
	if want := 80; len([]rune(strings.TrimSuffix(got, "..."))) > want {
		t.Errorf("truncated runes exceed cap %d", want)
	}

	got60 := util.Truncate(cjk, 60)
	if !utf8.ValidString(got60) {
		t.Errorf("util.Truncate(cjk, 60) invalid UTF-8: %q", got60)
	}

	if got := util.Truncate("short", 80); got != "short" {
		t.Errorf("short input must pass through, got %q", got)
	}
}
