package agent

import (
	"strings"
	"testing"
)

// #4340 companion: the old `leave\b[^n]{0,5}alone` alternative was
// functionally dead - {0,5} over a gap that includes two spaces capped the
// object at 3 chars, and [^n] excluded every object containing "n". Real
// refusals like "leave config.yaml alone" were silently never recorded, so
// the 30-day hard block never fired for this phrasing. The gap is now
// [^.\n]{0,40}: objects up to ~37 chars match, no sentence/line crossing.

func TestZZLeaveAloneExtraction(t *testing.T) {
	cases := []struct {
		name string
		text string
		want bool // extractRefusals yields ≥1 entry
	}{
		// Core regression: real objects previously dead ([^n] or >3 chars).
		{"file object", "leave config.yaml alone, please", true},
		{"go file object", "leave main.go alone, please", true},
		{"n-containing object", "leave nginx.conf alone, please", true},
		{"pronoun them", "leave them alone, please", true},
		{"pronoun that", "leave that alone, please", true},
		{"long path object", "leave internal/agent/agent.go alone, thanks", true},
		{"mixed case", "Leave Config.Yaml Alone, please", true},
		// Controls: the widening must not blur word or line boundaries.
		{"leaven is not leave", "leaven alone, please", false},
		// Period inside the gap is accepted (L161 reaffirm-side convention:
		// the gap is line-bounded only) - dotted filenames must survive.
		{"period in gap accepted", "leave it. alone, please", true},
		{"no mention", "config.yaml is fine, please", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := extractRefusals(tc.text)
			if (len(got) > 0) != tc.want {
				t.Fatalf("extractRefusals(%q) = %v, want nonempty=%v", tc.text, got, tc.want)
			}
		})
	}
}

// The widened gap must not add false-block surface: pronoun-object entries
// record but anchor nothing (refusalPathPattern requires a path separator or
// dotted extension), while file objects anchor exactly like "don't touch".
func TestZZLeaveAloneAnchorSurface(t *testing.T) {
	if got := refusalTargets("leave config.yaml alone, please"); !strings.Contains(strings.Join(got, ","), "config.yaml") {
		t.Fatalf("file object must anchor config.yaml, got %v", got)
	}
	if got := refusalTargets("leave main.go alone, please"); !strings.Contains(strings.Join(got, ","), "main.go") {
		t.Fatalf("go file object must anchor main.go, got %v", got)
	}
	if got := refusalTargets("leave them alone, please"); len(got) != 0 {
		t.Fatalf("pronoun object must anchor nothing, got %v", got)
	}
	if got := refusalTargets("leave that alone, please"); len(got) != 0 {
		t.Fatalf("'that' must anchor nothing, got %v", got)
	}
}

// End-to-end parity with "don't touch": same sentence, same anchor, the
// persisted leave-alone entry must hard-block the write tool call.
func TestZZLeaveAloneBlocksLikeDontTouch(t *testing.T) {
	l := newRefusalLedger(t.TempDir())
	l.record("leave config.yaml alone, please", "nl", refusalKindDirect)
	if msg := l.checkBlocked("edit_file", `{"file_path":"/repo/config.yaml"}`); msg == "" {
		t.Fatal("leave-alone refusal must block edit of the named file")
	}
}
