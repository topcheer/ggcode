//go:build goolm

package agent

import (
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/provider"
)

func TestIssue4389_ShortMatchPatternFloor(t *testing.T) {
	rs := NewRuleStore(t.TempDir())

	rs.AddRule(Rule{
		Category:     "build",
		Rule:         "Run gofmt before build",
		MatchPattern: "fail",
		ToolPattern:  "go build",
	})
	if n := len(rs.Rules()); n != 0 {
		t.Fatalf("short MatchPattern rule must be rejected, got %d rules (#4389)", n)
	}

	rs.AddRule(Rule{
		Category:     "convention",
		Rule:         "The user manually adjusts agent output in a.go (observed 2 times across 2 turns): re-read the file before editing it to avoid overwriting their changes",
		MatchPattern: "",
		ToolPattern:  `a\.go`,
		Source:       ruleSourceUserEdit,
	})
	if n := len(rs.Rules()); n != 1 {
		t.Fatalf("empty MatchPattern rule must be admitted, got %d rules (#4389)", n)
	}

	rs.rules = append(rs.rules, Rule{
		ID:           "r-short",
		Category:     "build",
		Rule:         "Run gofmt before build",
		MatchPattern: "fail",
		ToolPattern:  "go build",
		HitCount:     1,
		LastSeen:     time.Now(),
		CreatedAt:    time.Now(),
	})
	matched, unmatched := rs.MatchErrors([]string{"build fail: cannot find package olm"})
	if len(matched) != 0 {
		t.Fatalf("short-pattern rule must not match, got %d matched (#4389)", len(matched))
	}
	if len(unmatched) != 1 {
		t.Fatalf("error must stay unmatched for re-extraction, got %d unmatched (#4389)", len(unmatched))
	}
}

func TestIssue4390_DedupThresholdAligned(t *testing.T) {
	rs := NewRuleStore(t.TempDir())
	base := time.Now()

	rs.rules = []Rule{
		{ID: "r-1", Category: "build", Rule: "always run gofmt on edited files before staging them", MatchPattern: "gofmt.*differs", ToolPattern: "go build", HitCount: 3, LastSeen: base, CreatedAt: base},
		{ID: "r-2", Category: "build", Rule: "check go.sum freshness whenever modules change unexpectedly", MatchPattern: "missing go.sum entry", ToolPattern: "go build", HitCount: 2, LastSeen: base, CreatedAt: base},
	}
	if merged := rs.DeduplicateRules(); merged != 0 {
		t.Fatalf("rules below the 0.75 gate must not merge, merged %d (#4390)", merged)
	}
	if n := len(rs.Rules()); n != 2 {
		t.Fatalf("both rules must survive dedup, got %d (#4390)", n)
	}

	rs.rules = []Rule{
		{ID: "r-1", Category: "build", Rule: "always run gofmt on edited files before staging them", MatchPattern: "gofmt.*differs", ToolPattern: "go build", HitCount: 3, LastSeen: base, CreatedAt: base},
		{ID: "r-2", Category: "build", Rule: "always run gofmt on edited files before staging", MatchPattern: "gofmt.*differs", ToolPattern: "go build", HitCount: 2, LastSeen: base.Add(time.Minute), CreatedAt: base},
	}
	if merged := rs.DeduplicateRules(); merged != 1 {
		t.Fatalf("genuinely duplicate rules must still merge, merged %d (#4390)", merged)
	}
}

func TestIssue4391_StripKeepsAuditStyleLines(t *testing.T) {
	in := strings.Join([]string{
		"auditing deps...",
		"Risk: high vulnerability found in dependency X",
		"Critical: severity score 9.8 CVE-2026-1234",
	}, "\n")
	out := stripHarnessAdvisories(in)
	if !strings.Contains(out, "Risk: high vulnerability") {
		t.Fatalf("audit-tool Risk: line must survive, got %q (#4391)", out)
	}
	if !strings.Contains(out, "Critical: severity score") {
		t.Fatalf("bare Critical: line must survive, got %q (#4391)", out)
	}

	banner := strings.Join([]string{
		"DESTRUCTIVE GIT OPERATION DETECTED",
		"",
		"CRITICAL: [discard_all]",
		"  Risk: discards ALL uncommitted changes",
		"  Safer alternative: Use `git stash` to save changes",
		"",
		"git checkout -B fix origin/main",
	}, "\n")
	stripped := stripHarnessAdvisories(banner)
	if strings.Contains(stripped, "CRITICAL:") {
		t.Fatalf("harness CRITICAL banner must drop, got %q (#4391)", stripped)
	}
	if strings.Contains(stripped, "Risk: discards") {
		t.Fatalf("banner continuation Risk line must drop, got %q (#4391)", stripped)
	}
	if !strings.Contains(stripped, "git checkout -B fix origin/main") {
		t.Fatalf("payload command line must survive, got %q (#4391)", stripped)
	}
}

func TestIssue4392_HasNonThinkingContent(t *testing.T) {
	cases := []struct {
		name   string
		blocks []provider.ContentBlock
		want   bool
	}{
		{"empty", nil, false},
		{"thinking-only", []provider.ContentBlock{{Type: "thinking", Text: "let me think about this"}}, false},
		{"redacted-only", []provider.ContentBlock{{Type: "redacted_thinking", Text: "opaque"}}, false},
		{"thinking+blank-text", []provider.ContentBlock{
			{Type: "thinking", Text: "hmm"},
			{Type: "text", Text: "   "},
		}, false},
		{"text", []provider.ContentBlock{
			{Type: "thinking", Text: "hmm"},
			{Type: "text", Text: "answer"},
		}, true},
		{"tool-use", []provider.ContentBlock{
			{Type: "thinking", Text: "hmm"},
			{Type: "tool_use", Text: ""},
		}, true},
	}
	for _, c := range cases {
		if got := hasNonThinkingContent(c.blocks); got != c.want {
			t.Fatalf("%s: hasNonThinkingContent = %v, want %v (#4392)", c.name, got, c.want)
		}
	}
}
