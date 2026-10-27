package extpane

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

// zz_issue4485_test.go - companion tests for #4485.
//
// Bug 1: tmux CreateTab unconditionally ran set-hook -g -u after-new-window
// even when show-hooks failed - one transient read failure permanently
// deleted the user's GLOBAL hook (restore early-returns on the empty saved
// value). isHookBackupUsable is the gate: no verified backup, no suppress.
//
// Bug 2: manager EnsurePane sent every CreateTab error - including the 5s
// context deadline, a known transient (subprocess killed before the tab was
// built) - to failPermanent, leaving the #1369 retryBudget dead code for
// CreateTab; one busy tmux server disabled the agent's panels for the whole
// TUI session. isTransientCreateTabErr routes timeouts/cancels to the
// bounded retry budget, keeping permanent for runaway-protection errors.
func TestIssue4485IsHookBackupUsable(t *testing.T) {
	if !isHookBackupUsable(nil) {
		t.Error("isHookBackupUsable(nil) = false, want true (backup verified, suppression safe)")
	}
	for _, err := range []error{
		errors.New("exit status 1"),
		fmt.Errorf("tmux: %w", context.DeadlineExceeded),
	} {
		if isHookBackupUsable(err) {
			t.Errorf("isHookBackupUsable(%v) = true, want false (no verified backup, must not unset)", err)
		}
	}
}

func TestIssue4485IsTransientCreateTabErr(t *testing.T) {
	transient := []error{
		context.DeadlineExceeded,
		context.Canceled,
		fmt.Errorf("create tab: %w", context.DeadlineExceeded), // wrapped counts
		fmt.Errorf("outer: %w", fmt.Errorf("inner: %w", context.Canceled)),
	}
	for _, err := range transient {
		if !isTransientCreateTabErr(err) {
			t.Errorf("isTransientCreateTabErr(%v) = false, want true (bounded retry per #1369)", err)
		}
	}
	permanent := []error{
		errors.New("tab may already exist"), // runaway class - stays permanent
		fmt.Errorf("exec: %s", "not found"),
	}
	for _, err := range permanent {
		if isTransientCreateTabErr(err) {
			t.Errorf("isTransientCreateTabErr(%v) = true, want false (runaway/unknown keeps permanent)", err)
		}
	}
}
