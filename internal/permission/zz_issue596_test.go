package permission

import (
	"testing"
)

// TestIssue596_P1_EnvPrefixSecurity verifies that only valid env references
// ($VAR or ${VAR}) are stripped, not $0, $@, $(), ${IFS}, etc.
func TestIssue596_P1_EnvPrefixSecurity(t *testing.T) {
	rs := NewCommandRuleSetFromLists([]string{"make*"}, nil)

	tests := []struct {
		name     string
		command  string
		expected Decision
	}{
		// Valid env prefixes should be stripped and match allow rule
		{"valid_simple_env", "$FOO make build", Allow},
		{"valid_braced_env", "${BAR} make build", Allow},
		{"valid_env_underscore", "$FOO_BAR make build", Allow},
		{"valid_env_number", "$FOO1 make build", Allow},
		{"valid_braced_env_number", "${BAR2} make build", Allow},
		{"env_assignment_and_var", "FOO=bar $BAZ make build", Allow},

		// Invalid $ prefixes should NOT be stripped and NOT match
		{"dollar_zero", "$0 make build", Ask},
		{"dollar_at", "$@ make build", Ask},
		{"dollar_hash", "$# make build", Ask},
		{"dollar_star", "$* make build", Ask},
		{"dollar_question", "$? make build", Ask},
		{"dollar_dollar", "$$ make build", Ask},
		{"dollar_exclamation", "$! make build", Ask},
		{"command_subst", "$(rm -rf /tmp/x) make build", Ask},
		{"command_subst_no_space", "$(echo evil)make build", Ask},
		{"ifs_exploit", "${IFS} make build", Ask},
		{"braced_invalid", "${0} make build", Ask},
		{"braced_special", "${@} make build", Ask},
		{"braced_command_subst", "${rm -rf /tmp/x} make build", Ask},
		{"arith_expansion", "$((1+1)) make build", Ask},
		{"single_dollar", "$ make build", Ask},

		// Direct command without env prefix should match
		{"no_env_prefix", "make build", Allow},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decision, matched := rs.Check(tt.command)
			if matched && decision != tt.expected {
				t.Errorf("Check(%q) = (%v, matched=%v), want (%v, matched=%v)",
					tt.command, decision, matched, tt.expected, true)
			}
			if !matched && decision == Allow {
				t.Errorf("Check(%q) should not match allow rule (got %v)", tt.command, decision)
			}
		})
	}
}

// TestIssue596_P3_WildcardWordBoundary verifies that wildcard patterns
// require word boundaries after the literal prefix.
func TestIssue596_P3_WildcardWordBoundary(t *testing.T) {
	rs := NewCommandRuleSetFromLists([]string{"make*", "go build*"}, nil)

	tests := []struct {
		name     string
		command  string
		expected Decision
	}{
		// Valid matches with word boundary
		{"make_exact", "make", Allow},
		{"make_with_space", "make build", Allow},
		{"make_with_tab", "make\tbuild", Allow},
		{"makeevil_hyphen_no_match", "makeevil -pwn", Ask},
		{"makeevil_no_space_no_match", "makeevil", Ask},
		{"go_build_exact", "go build", Allow},
		{"go_build_with_space", "go build ./...", Allow},
		{"go_buildevil_hyphen_no_match", "go buildevil", Ask},
		{"go_buildevil_with_dash_no_match", "go buildevil -tags", Ask},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decision, matched := rs.Check(tt.command)
			if matched && decision != tt.expected {
				t.Errorf("Check(%q) = (%v, matched=%v), want (%v, matched=%v)",
					tt.command, decision, matched, tt.expected, true)
			}
			if !matched && decision == Allow {
				t.Errorf("Check(%q) should not match allow rule (got %v)", tt.command, decision)
			}
		})
	}
}

// TestIssue596_P1_EnvStrippingEdgeCases tests edge cases in env var stripping.
func TestIssue596_P1_EnvStrippingEdgeCases(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string // Expected stripped command
	}{
		{"empty", "", ""},
		{"only_space", "   ", ""},
		{"no_env", "make build", "make build"},
		{"simple_env", "$FOO make", "make"},
		{"braced_env", "${BAR} make", "make"},
		{"env_with_number", "$FOO1 make", "make"},
		{"invalid_start_digit", "$1FOO make", "$1FOO make"},
		{"multiple_valid_envs", "$FOO $BAR make", "make"},
		{"mixed_valid_invalid", "$FOO $0 make", "$0 make"},
		{"env_assignment_before_dollar", "FOO=bar $BAZ make", "make"},
		{"dollar_at_stop", "$@ make", "$@ make"},
		{"command_subst_stop", "$(echo x) make", "$(echo x) make"},
		{"single_dollar_stop", "$ make", "$ make"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stripLeadingEnvAssignments(tt.input)
			if got != tt.expected {
				t.Errorf("stripLeadingEnvAssignments(%q) = %q, want %q",
					tt.input, got, tt.expected)
			}
		})
	}
}

// TestIssue596_P2_MCPReadOnlyInPlanMode verifies that MCP tools are NOT
// automatically read-only in plan mode. Write operations require Ask.
func TestIssue596_P2_MCPReadOnlyInPlanMode(t *testing.T) {
	tests := []struct {
		name     string
		toolName string
		isRO     bool // expected IsReadOnlyTool result in plan mode
	}{
		// Write operations should NOT be read-only (require Ask)
		{"drop_table", "mcp__db__drop_table", false},
		{"apply_patch", "mcp__patch__apply_patch", false},
		{"send_message", "mcp__telegram__send_message", false},
		{"delete", "mcp__file__delete", false},
		{"create", "mcp__file__create", false},

		// Whitelisted read-only tools ARE read-only (auto-allow in plan mode)
		{"web_reader", "mcp__web_reader__webReader", true},
		{"web_search_prime", "mcp__web-search-prime__web_search_prime", true},

		// Non-whitelisted read-sounding tools should NOT be auto-allowed
		// (conservative: Ask unless explicitly whitelisted)
		{"get_data", "mcp__db__get_data", false},
		{"read_file", "mcp__file__read_file", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsReadOnlyTool(tt.toolName)
			if got != tt.isRO {
				t.Errorf("IsReadOnlyTool(%q) = %v, want %v",
					tt.toolName, got, tt.isRO)
			}
		})
	}
}
