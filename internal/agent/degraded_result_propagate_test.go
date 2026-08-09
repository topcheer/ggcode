package agent

import (
	"strings"
	"testing"
)

func TestIsDegradedResult(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    bool
	}{
		{"empty string", "", true},
		{"whitespace only", "   \n\t  ", true},
		{"no results found", "No results found", true},
		{"no matches", "0 matches", true},
		{"no files found", "No files found.", true},
		{"nothing found", "Nothing found", true},
		{"not found short", "not found", true},
		{"does not exist", "File does not exist", true},
		{"no such file", "no such file or directory", true},
		{"no data", "no data available", true},
		{"null", "null", true},
		{"undefined", "undefined", true},
		{"no entries", "no entries", true},
		{"no commits", "no commits found", true},
		{"no changes", "no changes", true},
		{"nothing to show", "nothing to show", true},
		{"substantial content", "package main\n\nfunc main() {\n\tprintln(\"hello world\")\n}", false},
		{"long content with 'not found'", strings.Repeat("This is substantial content. ", 10) + "not found", false},
		{"normal output", "Commit: abc123\nAuthor: Test\nMessage: Fix bug", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isDegradedResult(tt.content)
			if got != tt.want {
				t.Errorf("isDegradedResult(%q) = %v, want %v", tt.content, got, tt.want)
			}
		})
	}
}

func TestDegradedResultSilentPropagation(t *testing.T) {
	d := newDegradedResultState()

	// Step 1: grep returns empty result (degraded but not error)
	d.recordDegradedResult("grep", "No matches found", false, 1)

	// Step 2: agent continues without acknowledging the empty result
	guidance := d.checkAcknowledgment("Let me now look at the config file to understand the structure.")
	if guidance == "" {
		t.Error("expected guidance for silent propagation, got empty")
	}
	if !strings.Contains(guidance, "Silent Degradation Propagation") {
		t.Errorf("guidance missing key phrase, got: %s", guidance)
	}
	if !strings.Contains(guidance, "grep") {
		t.Errorf("guidance should mention the tool name, got: %s", guidance)
	}
}

func TestDegradedResultAcknowledged(t *testing.T) {
	d := newDegradedResultState()

	// Step 1: read_file returns empty
	d.recordDegradedResult("read_file", "", false, 1)

	// Step 2: agent acknowledges the empty result
	guidance := d.checkAcknowledgment("The grep returned no results, so let me try a different approach.")
	if guidance != "" {
		t.Errorf("expected no guidance when acknowledged, got: %s", guidance)
	}
}

func TestDegradedResultNoPendingCheck(t *testing.T) {
	d := newDegradedResultState()

	// No prior degraded result - should return empty
	guidance := d.checkAcknowledgment("some text")
	if guidance != "" {
		t.Errorf("expected no guidance with no pending check, got: %s", guidance)
	}
}

func TestDegradedResultNormalResultNoCheck(t *testing.T) {
	d := newDegradedResultState()

	// Normal result with actual content
	d.recordDegradedResult("read_file", "package main\nfunc main() {}", false, 1)

	// Should not trigger any check
	guidance := d.checkAcknowledgment("Now I'll edit the file.")
	if guidance != "" {
		t.Errorf("expected no guidance for normal result, got: %s", guidance)
	}
}

func TestDegradedResultErrorNotDegraded(t *testing.T) {
	d := newDegradedResultState()

	// Error results are not treated as degraded (handled by error system)
	d.recordDegradedResult("grep", "permission denied", true, 1)

	guidance := d.checkAcknowledgment("Now let me continue with the task.")
	if guidance != "" {
		t.Errorf("expected no guidance for error result, got: %s", guidance)
	}
}

func TestDegradedResultMaxFires(t *testing.T) {
	d := newDegradedResultState()

	// Fire 1
	d.recordDegradedResult("grep", "no matches", false, 1)
	g1 := d.checkAcknowledgment("continuing without acknowledging")
	if g1 == "" {
		t.Fatal("expected first guidance")
	}

	// Fire 2
	d.recordDegradedResult("grep", "no matches", false, 2)
	g2 := d.checkAcknowledgment("continuing without acknowledging")
	if g2 == "" {
		t.Fatal("expected second guidance")
	}

	// Fire 3 - should be capped
	d.recordDegradedResult("grep", "no matches", false, 3)
	g3 := d.checkAcknowledgment("continuing without acknowledging")
	if g3 != "" {
		t.Errorf("expected no guidance after max fires, got: %s", g3)
	}
}

func TestDegradedResultNonDataToolNotTracked(t *testing.T) {
	d := newDegradedResultState()

	// edit_file is not in toolsExpectedToReturnData - should not be tracked
	d.recordDegradedResult("edit_file", "", false, 1)

	guidance := d.checkAcknowledgment("let me continue")
	if guidance != "" {
		t.Errorf("expected no guidance for non-data tool, got: %s", guidance)
	}
}

func TestDegradedResultReset(t *testing.T) {
	d := newDegradedResultState()

	d.recordDegradedResult("grep", "no matches", false, 1)
	_ = d.checkAcknowledgment("continuing")
	d.reset()

	if d.pendingCheck != nil {
		t.Error("pendingCheck should be nil after reset")
	}
	if len(d.recentResults) != 0 {
		t.Error("recentResults should be empty after reset")
	}
	if d.guidanceFired != 0 {
		t.Error("guidanceFired should be 0 after reset")
	}
}

func TestDegradedResultAcknowledgmentVariations(t *testing.T) {
	ackTexts := []string{
		"didn't find anything",
		"did not find the file",
		"no results came back",
		"the search returned nothing",
		"returned empty",
		"was not found",
		"couldn't find it",
		"could not find the symbol",
		"no luck finding that",
		"came up empty",
		"dead end",
		"nothing came back from the query",
	}

	for _, text := range ackTexts {
		d := newDegradedResultState()
		d.recordDegradedResult("grep", "no matches", false, 1)
		guidance := d.checkAcknowledgment(text)
		if guidance != "" {
			t.Errorf("expected acknowledgment for text %q, got guidance: %s", text, guidance)
		}
	}
}
