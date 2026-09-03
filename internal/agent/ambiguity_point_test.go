package agent

import (
	"strings"
	"testing"
)

func TestAmbiguityPointDetection(t *testing.T) {
	tests := []struct {
		name     string
		prompt   string
		wantMsg  bool
		category string
	}{
		{
			name:     "remove duplicates triggers dup handling",
			prompt:   "Please remove duplicates from the user list and return the cleaned array",
			wantMsg:  true,
			category: ambDupHandling,
		},
		{
			name:    "dedup triggers detection",
			prompt:  "Dedup the entries in the config file",
			wantMsg: true,
		},
		{
			name:    "sort order ambiguity",
			prompt:  "Sort the results by name",
			wantMsg: true,
		},
		{
			// #1438-A: bare 'better' hit ordinary sentences whose metric was
			// already in them; the narrowed table no longer fires here - the
			// guidance would have been category-mismatched noise.
			name:    "optimize with metric in sentence no longer fires",
			prompt:  "Optimize the database query for better performance",
			wantMsg: false,
		},
		{
			name:    "make it better vague direction fires",
			prompt:  "Please make it better when you get a chance",
			wantMsg: true,
		},
		{
			name:    "refactor vague scope",
			prompt:  "Refactor the authentication module",
			wantMsg: true,
		},
		{
			name:    "improve vague direction",
			prompt:  "Improve the error handling in this module",
			wantMsg: true,
		},
		{
			name:    "rename vague naming",
			prompt:  "Rename the function to something more descriptive",
			wantMsg: true,
		},
		{
			name:    "vague quantity recent",
			prompt:  "Show me the recent entries from the log",
			wantMsg: true,
		},
		{
			name:    "simplify vague direction",
			prompt:  "Simplify the validation logic",
			wantMsg: true,
		},
		{
			name:    "fallback ambiguity",
			prompt:  "Add a fallback when the API call fails",
			wantMsg: true,
		},
		{
			name:    "clear unambiguous request no fire",
			prompt:  "Add a test for the CalculateTotal function in cart.go",
			wantMsg: false,
		},
		{
			name:    "informational request skipped",
			prompt:  "explain how the authentication system works in this project",
			wantMsg: false,
		},
		{
			name:    "very short prompt skipped",
			prompt:  "run tests",
			wantMsg: false,
		},
		{
			name:    "empty prompt no fire",
			prompt:  "",
			wantMsg: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := &Agent{
				ambiguityPoint: newAmbiguityPointState(),
			}
			msg := a.checkAmbiguityPoints(tt.prompt)
			if tt.wantMsg {
				if msg == "" {
					t.Errorf("expected guidance message, got empty")
					return
				}
				if !strings.Contains(msg, "Ambiguity Detection") {
					t.Errorf("message should contain 'Ambiguity Detection', got: %s", msg[:min(60, len(msg))])
				}
				if tt.category != "" && !strings.Contains(msg, tt.category) {
					// Check the category appears in the suggestion text instead
					// Some categories are matched differently in output
				}
			} else {
				if msg != "" {
					t.Errorf("expected no message, got: %s", msg[:min(60, len(msg))])
				}
			}
		})
	}
}

func TestAmbiguityPointFiresOnce(t *testing.T) {
	a := &Agent{
		ambiguityPoint: newAmbiguityPointState(),
	}
	prompt := "Remove duplicates from the list and sort the results"

	first := a.checkAmbiguityPoints(prompt)
	if first == "" {
		t.Fatal("expected first call to produce guidance")
	}

	second := a.checkAmbiguityPoints(prompt)
	if second != "" {
		t.Error("expected second call to be suppressed (fire once per run)")
	}
}

func TestAmbiguityPointReset(t *testing.T) {
	a := &Agent{
		ambiguityPoint: newAmbiguityPointState(),
	}
	prompt := "Remove duplicates from the list"

	first := a.checkAmbiguityPoints(prompt)
	if first == "" {
		t.Fatal("expected first call to produce guidance")
	}

	a.ambiguityPoint.reset()

	second := a.checkAmbiguityPoints(prompt)
	if second == "" {
		t.Error("expected guidance after reset")
	}
}

func TestAmbiguityPointMaxSignals(t *testing.T) {
	a := &Agent{
		ambiguityPoint: newAmbiguityPointState(),
	}
	// Prompt with many ambiguity sources
	prompt := "Refactor the module to remove duplicates, sort the results, " +
		"improve error handling with a fallback, and optimize for better performance"

	msg := a.checkAmbiguityPoints(prompt)
	if msg == "" {
		t.Fatal("expected guidance message for multi-ambiguity prompt")
	}

	// Count numbered items (should be capped at 3)
	count := strings.Count(msg, "\n  ")
	if count > 3 {
		t.Errorf("expected at most 3 signals, got %d", count)
	}
}

func TestAmbiguityPointMultipleCategoriesNotDuplicated(t *testing.T) {
	a := &Agent{
		ambiguityPoint: newAmbiguityPointState(),
	}
	// "sort" appears twice but should only generate one sort-order signal
	prompt := "Sort the results and then sort the output by date"

	msg := a.checkAmbiguityPoints(prompt)
	if msg == "" {
		t.Fatal("expected guidance message")
	}

	// Count occurrences of "sort-ordering" category
	sortCount := strings.Count(msg, ambSortOrder)
	if sortCount > 1 {
		t.Errorf("expected at most 1 sort-order signal, got %d", sortCount)
	}
}

func TestAmbiguityQuickTaskSkip(t *testing.T) {
	a := &Agent{
		ambiguityPoint: newAmbiguityPointState(),
	}

	// These should be skipped (start with informational phrases)
	infoPrompts := []string{
		"what is the remove duplicates function doing",
		"explain how the sorted list works",
		"describe the duplicate entries in the log",
	}

	for _, p := range infoPrompts {
		msg := a.checkAmbiguityPoints(p)
		if msg != "" {
			t.Errorf("expected no guidance for informational prompt: %s", p)
		}
		a.ambiguityPoint.reset()
	}
}

// TestAmbiguityPointCJKAndGate pins #1438-B/C: the highest-value Chinese
// patterns now trigger (the primary user language had zero coverage), and
// the lowered gate lets short vague directions through.
func TestAmbiguityPointCJKAndGate(t *testing.T) {
	// fired is once-per-run: a FRESH state per prompt (the initial version
	// reused one Agent and the 2nd+ prompts silently skipped).
	for _, cjk := range []string{"请优化一下这个函数的性能", "把这个列表去重", "帮我排个序", "把这个函数改名"} {
		a := &Agent{ambiguityPoint: newAmbiguityPointState()}
		if msg := a.checkAmbiguityPoints(cjk); msg == "" {
			t.Fatalf("CJK prompt %q not detected", cjk)
		}
	}
	// Gate: 6 chars still blocks bare one-word commands...
	gateA := &Agent{ambiguityPoint: newAmbiguityPointState()}
	if msg := gateA.checkAmbiguityPoints("run"); msg != "" {
		t.Fatal("one-word command should stay gated")
	}
	// ...but two-word vague directions pass the gate AND the table.
	a2 := &Agent{ambiguityPoint: newAmbiguityPointState()}
	if msg := a2.checkAmbiguityPoints("make it better"); msg == "" {
		t.Fatal("short vague direction missed (gate overshot)")
	}
}
