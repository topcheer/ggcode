package agent

import (
	"strings"
	"testing"
)

// #441: quoting a critical keyword in guidance TEXT must not grant
// permanent budget exemption — only an exact head tag does.
func TestGuidanceCriticalOnlyViaHeadTag(t *testing.T) {
	// Advisory tag + critical keyword in body text: NOT critical.
	body := "[TODO-DROP] Reminder: unlike [CRITICAL messages, this is advisory."
	if isCriticalGuidance(body) {
		t.Error("advisory guidance quoting '[CRITICAL' must not bypass budget")
	}
	// Critical head tag: critical.
	if !isCriticalGuidance("[CRITICAL] something bad") {
		t.Error("true critical head tag must bypass budget")
	}
	// Exact-tag matching: near-miss tags do not inherit criticality.
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

// #441: the budget cap actually binds once the keyword bypass is closed.
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
	// Critical still passes after budget exhaustion.
	if !g.allow("[CRITICAL] real emergency") {
		t.Error("critical must always pass")
	}
}
