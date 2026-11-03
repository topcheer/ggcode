package agent

import (
	"strings"
	"testing"
)

func TestGuidanceCriticalOnlyViaHeadTag(t *testing.T) {

	body := "[TODO-DROP] Reminder: unlike [CRITICAL messages, this is advisory."
	if isCriticalGuidance(body) {
		t.Error("advisory guidance quoting '[CRITICAL' must not bypass budget")
	}

	if !isCriticalGuidance("[CRITICAL] something bad") {
		t.Error("true critical head tag must bypass budget")
	}

	if isCriticalTag("SECURITY-TIP") {
		t.Error("SECURITY-TIP must not match by substring")
	}
	if isCriticalTag("BLOCKED-FOR-NOW") {
		t.Error("BLOCKED-FOR-NOW must not match by substring")
	}
	if !isCriticalTag("SECURITY") || !isCriticalTag("security") {
		t.Error("exact tags (any case) must match")
	}
	if !isCriticalTag("pre-commit-build-gate") {
		t.Error("exact lowercase tag must match")
	}
}

func TestGuidanceBudgetBinds(t *testing.T) {
	var g guidanceBudget
	g.reset()
	injected := 0
	for i := 0; i < 8; i++ {
		if g.allow("[TODO-DROP] advisory note " + strings.Repeat("x", i)) {
			injected++
		}
	}
	if injected != guidanceBudgetPerTurn {
		t.Errorf("expected exactly %d injections, got %d", guidanceBudgetPerTurn, injected)
	}

	if !g.allow("[CRITICAL] real emergency") {
		t.Error("critical must always pass")
	}
}
