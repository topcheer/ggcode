package usage

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// #2150 batch 3: the IM /usage renderer must keep the balance/windows
// payload and degrade to one explanatory line on error/nil.
func TestRenderText(t *testing.T) {
	bal := 12.5
	got := RenderText("zai", &UsageInfo{
		Vendor:  "zai",
		Balance: &bal,
		Windows: []UsageWindow{{Label: "5h", UsedPercent: 42.3, ResetsAt: time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)}},
	}, nil)
	for _, want := range []string{"zai", "12.50", "5h", "42%", "resets"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in %q", want, got)
		}
	}

	if got := RenderText("kimi", nil, errors.New("unsupported vendor")); !strings.Contains(got, "kimi") || !strings.Contains(got, "unsupported") {
		t.Fatalf("error case: %q", got)
	}
	if got := RenderText("x", nil, nil); !strings.Contains(got, "no data") {
		t.Fatalf("nil case: %q", got)
	}
	// No balance concept: no "| balance" segment.
	if got := RenderText("deepseek", &UsageInfo{Vendor: "deepseek", Windows: []UsageWindow{{Label: "weekly", UsedPercent: 7}}}, nil); strings.Contains(got, "balance") {
		t.Fatalf("nil balance must not render a balance line: %q", got)
	}
}
