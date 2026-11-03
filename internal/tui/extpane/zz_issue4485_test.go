package extpane

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

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
		fmt.Errorf("create tab: %w", context.DeadlineExceeded),
		fmt.Errorf("outer: %w", fmt.Errorf("inner: %w", context.Canceled)),
	}
	for _, err := range transient {
		if !isTransientCreateTabErr(err) {
			t.Errorf("isTransientCreateTabErr(%v) = false, want true (bounded retry per #1369)", err)
		}
	}
	permanent := []error{
		errors.New("tab may already exist"),
		fmt.Errorf("exec: %s", "not found"),
	}
	for _, err := range permanent {
		if isTransientCreateTabErr(err) {
			t.Errorf("isTransientCreateTabErr(%v) = true, want false (runaway/unknown keeps permanent)", err)
		}
	}
}
