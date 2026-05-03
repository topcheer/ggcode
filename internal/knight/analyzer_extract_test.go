package knight

import (
	"testing"
)

func TestExtractSkillDocument(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantOK   bool
		wantHead string // first line of expected output
	}{
		{
			name: "clean skill document",
			input: `---
name: "build-convention"
description: "Use official build path"
---
# Build Convention
Rule text here`,
			wantOK:   true,
			wantHead: "---",
		},
		{
			name: "analysis prepended before skill doc",
			input: `Now I have a clear picture. The session shows the assistant repeatedly stopping to present reports.

---
name: "build-convention"
description: "Use official build path"
---
# Build Convention
Rule text here`,
			wantOK:   true,
			wantHead: "---",
		},
		{
			name: "analysis appended after skill doc",
			input: `---
name: "build-convention"
description: "Use official build path"
---
# Build Convention
Rule text here

Now I have summarized the skill.`,
			wantOK:   true,
			wantHead: "---",
		},
		{
			name: "analysis both before and after",
			input: `Let me analyze the session first.

---
name: "build-convention"
description: "Use official build path"
---
# Build Convention
Rule text here

This is the skill I generated.`,
			wantOK:   true,
			wantHead: "---",
		},
		{
			name: "no frontmatter at all",
			input: `Now I have a clear picture. The session shows the assistant 
repeatedly stopping to present reports and wait for user input, even though 
autopilot mode was active.`,
			wantOK: false,
		},
		{
			name:   "empty output",
			input:  "",
			wantOK: false,
		},
		{
			name:   "only dashes not frontmatter",
			input:  "---\njust some dashes\n---\nno skill here",
			wantOK: true, // has --- so passes basic check
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractSkillDocument(tt.input)
			if tt.wantOK {
				if got == "" {
					t.Fatal("expected non-empty result, got empty string")
				}
				if len(got) < len(tt.wantHead) || got[:len(tt.wantHead)] != tt.wantHead {
					t.Fatalf("expected output starting with %q, got %q", tt.wantHead, got[:minInt(len(got), 50)])
				}
			} else {
				if got != "" {
					t.Fatalf("expected empty result, got %q", got[:minInt(len(got), 100)])
				}
			}
		})
	}
}

func TestBuildKnightSystemPrompt_SkillGenRules(t *testing.T) {
	prompt := buildKnightSystemPrompt("skill-generation")

	mustContain := []string{
		"FINAL text output",
		"skill-generation",
	}
	for _, substr := range mustContain {
		if !contains(prompt, substr) {
			t.Errorf("system prompt missing %q", substr)
		}
	}
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
