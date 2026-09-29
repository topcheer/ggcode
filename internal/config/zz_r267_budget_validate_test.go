package config

import (
	"strings"
	"testing"
)

// r267: negative sub-agent budget configs are rejected by Validate. The
// max_concurrent guard predates this file; the max_total guard is new - both
// are covered here symmetrically (the config package previously had zero
// Validate() coverage).
func TestValidateSubAgentBudgetsRejectNegative(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*Config)
		want string
	}{
		{
			name: "max_total negative",
			mut:  func(c *Config) { c.SubAgents.MaxTotal = -1 },
			want: "subagents.max_total must not be negative",
		},
		{
			name: "max_concurrent negative",
			mut:  func(c *Config) { c.SubAgents.MaxConcurrent = -1 },
			want: "subagents.max_concurrent must not be negative",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := DefaultConfig()
			tc.mut(c)
			err := c.Validate()
			if err == nil {
				t.Fatalf("Validate() = nil, want error containing %q", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Validate() error = %q, want it to contain %q", err.Error(), tc.want)
			}
		})
	}
}

// max_total = 0 (default) must stay valid: 0 means unlimited.
func TestValidateSubAgentBudgetZeroUnlimited(t *testing.T) {
	c := DefaultConfig()
	if c.SubAgents.MaxTotal != 0 {
		t.Fatalf("default MaxTotal = %d, want 0 (unlimited)", c.SubAgents.MaxTotal)
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("Validate() with default MaxTotal should pass, got %v", err)
	}
}
