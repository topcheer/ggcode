package main

import (
	"testing"

	"github.com/topcheer/ggcode/internal/permission"
)

func TestRuntimeBypassActive(t *testing.T) {
	cp := permission.NewConfigPolicyWithMode(nil, nil, permission.SupervisedMode)
	if runtimeBypassActive(cp, permission.SupervisedMode) {
		t.Fatalf("supervised policy must not report bypass")
	}

	cp.SetMode(permission.BypassMode)
	if !runtimeBypassActive(cp, permission.SupervisedMode) {
		t.Fatalf("live ConfigPolicy bypass must win over stale start-time mode copy")
	}

	if runtimeBypassActive(nil, permission.BypassMode) != true {
		t.Fatalf("fallback must use the provided mode copy")
	}
	if runtimeBypassActive(nil, permission.AutoMode) {
		t.Fatalf("fallback must not report bypass for auto mode")
	}
}
