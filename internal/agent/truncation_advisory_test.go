package agent

import (
	"strings"
	"testing"
)

func TestTruncationAdvisory_RunCommand(t *testing.T) {
	advisory := truncationAdvisory("run_command", 60000)
	if !strings.Contains(advisory, "58.6KB") {
		t.Errorf("expected size in advisory, got: %s", advisory)
	}
	if !strings.Contains(advisory, "read_command_output") {
		t.Errorf("expected read_command_output hint, got: %s", advisory)
	}
	if !strings.Contains(advisory, "grep/head/tail") {
		t.Errorf("expected grep/head/tail hint, got: %s", advisory)
	}
}

func TestTruncationAdvisory_ReadFile(t *testing.T) {
	advisory := truncationAdvisory("read_file", 30000)
	if !strings.Contains(advisory, "offset and limit") {
		t.Errorf("expected offset/limit hint for read_file, got: %s", advisory)
	}
}

func TestTruncationAdvisory_Grep(t *testing.T) {
	advisory := truncationAdvisory("grep", 20000)
	if !strings.Contains(advisory, "max_results") {
		t.Errorf("expected max_results hint for grep, got: %s", advisory)
	}
}

func TestTruncationAdvisory_UnknownTool(t *testing.T) {
	advisory := truncationAdvisory("custom_tool", 15000)
	if !strings.Contains(advisory, "truncated due to context pressure") {
		t.Errorf("expected generic advisory for unknown tool, got: %s", advisory)
	}
}

func TestAdvisoryForTruncation_SmallOutput(t *testing.T) {
	// Below 8KB threshold, no advisory should be generated
	if advisory := advisoryForTruncation("run_command", 4000); advisory != "" {
		t.Errorf("expected empty advisory for small output, got: %s", advisory)
	}
}

func TestAdvisoryForTruncation_LargeOutput(t *testing.T) {
	advisory := advisoryForTruncation("run_command", 50000)
	if advisory == "" {
		t.Error("expected non-empty advisory for large output")
	}
}

func TestWithTruncationAdvisory(t *testing.T) {
	truncated := "head of output\n[... truncated ...]\ntail of output"
	result := withTruncationAdvisory(truncated, "run_command", 50000)
	if result == truncated {
		t.Error("expected advisory to be appended")
	}
	if !strings.Contains(result, "[Truncation advisory:") {
		t.Errorf("expected advisory marker in result")
	}
	if !strings.Contains(result, "read_command_output") {
		t.Errorf("expected tool-specific hint in result")
	}
}

func TestWithTruncationAdvisory_Idempotent(t *testing.T) {
	truncated := "head\n[... truncated ...]\ntail"
	result := withTruncationAdvisory(truncated, "run_command", 50000)
	result2 := withTruncationAdvisory(result, "run_command", 50000)
	if result != result2 {
		t.Error("expected idempotent behavior when advisory already present")
	}
}

func TestWithTruncationAdvisory_SmallOutput(t *testing.T) {
	truncated := "small output"
	result := withTruncationAdvisory(truncated, "run_command", 4000)
	if result != truncated {
		t.Error("expected no advisory for small output")
	}
}

func TestTruncationAdvisory_CodeSearch(t *testing.T) {
	advisory := truncationAdvisory("code_search", 25000)
	if !strings.Contains(advisory, "Refine your query") {
		t.Errorf("expected refine hint for code_search, got: %s", advisory)
	}
}

func TestTruncationAdvisory_GitDiff(t *testing.T) {
	advisory := truncationAdvisory("git_diff", 40000)
	if !strings.Contains(advisory, "file paths") {
		t.Errorf("expected file paths hint for git_diff, got: %s", advisory)
	}
}

func TestTruncationAdvisory_Browser(t *testing.T) {
	advisory := truncationAdvisory("browser", 50000)
	if !strings.Contains(advisory, "extract") {
		t.Errorf("expected extract hint for browser, got: %s", advisory)
	}
}
