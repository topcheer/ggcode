package tui

import (
	"testing"

	"github.com/topcheer/ggcode/internal/permission"
)

func TestApplyStrictWriteGuard_DeniesWriteTools(t *testing.T) {
	policy := permission.NewConfigPolicy(nil, nil)
	m := NewModel(nil, policy)

	// Before guard, write tools should not be denied
	if policy.GetDecision("write_file") == permission.Deny {
		t.Error("write_file should not be denied before guard")
	}
	if policy.GetDecision("edit_file") == permission.Deny {
		t.Error("edit_file should not be denied before guard")
	}

	// Apply guard
	m.applyStrictWriteGuard()

	// After guard, write tools should be denied
	if policy.GetDecision("write_file") != permission.Deny {
		t.Error("write_file should be denied after guard")
	}
	if policy.GetDecision("edit_file") != permission.Deny {
		t.Error("edit_file should be denied after guard")
	}
	if policy.GetDecision("multi_edit_file") != permission.Deny {
		t.Error("multi_edit_file should be denied after guard")
	}
}

func TestApplyStrictWriteGuard_NilPolicy_NoPanic(t *testing.T) {
	m := NewModel(nil, nil)
	// Should not panic — nil policy is not *ConfigPolicy
	m.applyStrictWriteGuard()
}
