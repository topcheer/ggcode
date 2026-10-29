//go:build goolm

package agent

// #4389/#4390/#4391/#4392 regression tests (third business-logic audit
// round, ratchet + empty-response guard).

import (
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/provider"
)

// #4389: a rule with a non-empty MatchPattern below minMatchPatternLen
// must be rejected at AddRule and never match in MatchErrors (the old
// behavior accrued HitCount on a keyword fragment like "fail" that can
// never be injected reactively - the #1008 count-but-invisible shape).
func TestIssue4389_ShortMatchPatternFloor(t *testing.T) {
	rs := NewRuleStore(t.TempDir())

	// Short pattern rejected at the door.
	rs.AddRule(Rule{
		Category:     "build",
		Rule:         "Run gofmt before build",
		MatchPattern: "fail",
		ToolPattern:  "go build",
	})
	if n := len(rs.Rules()); n != 0 {
		t.Fatalf("short MatchPattern rule must be rejected, got %d rules (#4389)", n)
	}

	// Empty MatchPattern stays legal (user_edit / preventive-only rules
	// are keyed on ToolPattern alone - r444/#3229 family).
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

	// Pre-existing short-pattern rule (learned before the floor) must not
	// swallow errors in MatchErrors: the error stays unmatched and remains
	// eligible for new-rule extraction.
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

// #4390: DeduplicateRules must use the same 0.75 verdict as AddRule and
// honor the #3229 user_edit identity guard. A rule pair at sim ~0.55-0.74
// (same ToolPattern bonus + low Jaccard) was merged by the old 0.55 line,
// silently dropping the merged rule's MatchPattern/FixHint.
func TestIssue4390_DedupThresholdAligned(t *testing.T) {
	rs := NewRuleStore(t.TempDir())
	base := time.Now()
	// Same ToolPattern (+0.3 bonus) with disjoint rule text (Jaccard 0)
	// => sim = 0.3 + ~0.25 tokens overlap ~= 0.55 old-merge zone, well
	// below the 0.75 enqueue gate.
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

	// Near-identical pair (>=0.75) still merges - the cleanup keeps its
	// purpose.
	rs.rules = []Rule{
		{ID: "r-1", Category: "build", Rule: "always run gofmt on edited files before staging them", MatchPattern: "gofmt.*differs", ToolPattern: "go build", HitCount: 3, LastSeen: base, CreatedAt: base},
		{ID: "r-2", Category: "build", Rule: "always run gofmt on edited files before staging", MatchPattern: "gofmt.*differs", ToolPattern: "go build", HitCount: 2, LastSeen: base.Add(time.Minute), CreatedAt: base},
	}
	if merged := rs.DeduplicateRules(); merged != 1 {
		t.Fatalf("genuinely duplicate rules must still merge, merged %d (#4390)", merged)
	}
}

// #4391: bare "Critical:"/"Risk:" payload lines from audit/lint tools
// must survive stripHarnessAdvisories; harness banner continuation lines
// ("  Risk: ..." indented under a dropped banner line) must still go.
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

	// Harness banner: bracketed CRITICAL drops, its indented continuations
	// drop with it, payload around survives.
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

// #4392: thinking-only responses must count as empty for the
// empty-response guard. Assembly prepends thinking blocks into
// Message.Content, so the old len(Content)==0 conjunct never fired for
// them; zero-usage gateways + DeepSeek-style reasoning-then-stop produced
// silent false success for cron/subagent callers.
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
