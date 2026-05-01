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
	if policy.GetDecision("run_command") != permission.Deny {
		t.Error("run_command should be denied after guard")
	}
	if policy.GetDecision("notebook_edit") != permission.Deny {
		t.Error("notebook_edit should be denied after guard")
	}
	if policy.GetDecision("git_add") != permission.Deny {
		t.Error("git_add should be denied after guard")
	}
	if policy.GetDecision("git_commit") != permission.Deny {
		t.Error("git_commit should be denied after guard")
	}
	if policy.GetDecision("git_stash") != permission.Deny {
		t.Error("git_stash should be denied after guard")
	}

	// Read tools should NOT be denied
	if policy.GetDecision("read_file") == permission.Deny {
		t.Error("read_file should not be denied by write guard")
	}
	if policy.GetDecision("search_files") == permission.Deny {
		t.Error("search_files should not be denied by write guard")
	}
}

func TestApplyStrictWriteGuard_NilPolicy_NoPanic(t *testing.T) {
	m := NewModel(nil, nil)
	// Should not panic — nil policy is not *ConfigPolicy
	m.applyStrictWriteGuard()
}
