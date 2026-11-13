package agent

import (
	"strings"
	"testing"
)

// #5268 item 3: when the context byte cap (not the tool's own line
// window) truncates a read_file result, the advisory must say so instead
// of telling the model to retry the same offset/limit call.

func TestGuardToolOutputReport_LimitsAndFill(t *testing.T) {
	content := strings.Repeat("x", 50*1024)

	if out, rep := guardToolOutputReport(content, 0.30); rep.Truncated || len(out) != len(content) {
		t.Errorf("low fill should not truncate: truncated=%v", rep.Truncated)
	}
	if _, rep := guardToolOutputReport(content, 0.55); !rep.Truncated || rep.Limit != outputLimitModerate {
		t.Errorf("moderate fill: want truncated at %d, got %+v", outputLimitModerate, rep)
	}
	if _, rep := guardToolOutputReport(content, 0.70); !rep.Truncated || rep.Limit != outputLimitHigh {
		t.Errorf("high fill: want truncated at %d, got %+v", outputLimitHigh, rep)
	}
	if _, rep := guardToolOutputReport(content, 0.85); !rep.Truncated || rep.Limit != outputLimitCritical {
		t.Errorf("critical fill: want truncated at %d, got %+v", outputLimitCritical, rep)
	}
}

func TestTruncationAdvisory_ReadFileNamesByteCapConstraint(t *testing.T) {
	advisory := truncationAdvisoryWithGuard("read_file", 70*1024, outputLimitModerate)
	for _, want := range []string{"byte cap", "not by your line window", "same window returns the same bytes"} {
		if !strings.Contains(advisory, want) {
			t.Errorf("advisory missing %q: %s", want, advisory)
		}
	}
}

func TestTruncationAdvisory_ReadFileWithoutGuardLimitUnchanged(t *testing.T) {
	// Legacy path (guard limit unknown) keeps the offset/limit hint the
	// existing test pins (TestTruncationAdvisory_ReadFile).
	advisory := truncationAdvisoryWithGuard("read_file", 30*1024, 0)
	if !strings.Contains(advisory, "offset and limit") {
		t.Errorf("legacy advisory lost offset/limit hint: %s", advisory)
	}
}

func TestTruncationAdvisory_GuardLimitOtherToolKeepsGenericAdvice(t *testing.T) {
	advisory := truncationAdvisoryWithGuard("run_command", 50*1024, outputLimitHigh)
	if !strings.Contains(advisory, "offset/limit") && !strings.Contains(advisory, "grep/head/tail") {
		t.Errorf("run_command advice should keep its retry guidance: %s", advisory)
	}
}

func TestWithTruncationAdvisoryGuard_AppendsAndIsIdempotent(t *testing.T) {
	truncated := "[... head ...] tail"
	result := withTruncationAdvisoryGuard(truncated, "read_file", 70*1024, outputLimitModerate)
	if !strings.HasSuffix(result, "[Truncation advisory:") && !strings.Contains(result, "byte cap") {
		t.Errorf("expected guard-aware advisory appended, got: %s", result)
	}
	again := withTruncationAdvisoryGuard(result, "read_file", 70*1024, outputLimitModerate)
	if len(again) != len(result) {
		t.Errorf("advisory should be idempotent: %d vs %d", len(result), len(again))
	}
}
