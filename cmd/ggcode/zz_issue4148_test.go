package main

import (
	"testing"

	"github.com/topcheer/ggcode/internal/permission"
)

// #4148-B1: runtime permission-mode switches live in the ConfigPolicy, not in
// the CLI flag; detach argv reconstruction must consult the live policy.
func TestRuntimeBypassActive(t *testing.T) {
	cp := permission.NewConfigPolicyWithMode(nil, nil, permission.SupervisedMode)
	if runtimeBypassActive(cp, permission.SupervisedMode) {
		t.Fatalf("supervised policy must not report bypass")
	}
	// Runtime switch_mode transition: policy flips after process start.
	cp.SetMode(permission.BypassMode)
	if !runtimeBypassActive(cp, permission.SupervisedMode) {
		t.Fatalf("live ConfigPolicy bypass must win over stale start-time mode copy")
	}
	// Fallback path when the policy is not a *ConfigPolicy (nil type-asserts
	// to false, exercising the mode-copy fallback).
	if runtimeBypassActive(nil, permission.BypassMode) != true {
		t.Fatalf("fallback must use the provided mode copy")
	}
	if runtimeBypassActive(nil, permission.AutoMode) {
		t.Fatalf("fallback must not report bypass for auto mode")
	}
}
