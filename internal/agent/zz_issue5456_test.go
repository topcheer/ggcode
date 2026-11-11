package agent

import (
	"strings"
	"testing"
)

// Issue #5456 pin: three wrapper-flag shapes where the rm head was eaten.
//  1. xargs -0 rm ...      : -0 takes NO value, rm must stay reachable
//  2. sudo --preserve-env=PATH rm ... : '=' form is self-valued, no consume
//  3. env VAR=1 nice -n 5 rm ... : assignments interleave after wrappers
//
// The negative pins keep the legacy consuming semantics for flags that DO
// take values (sudo -u root, timeout 30) and non-rm commands.
func TestIssue5456ValuelessFlagAndAssignmentShapes(t *testing.T) {
	hits := []string{
		"xargs -0 rm -rf /tmp/x",
		"sudo --preserve-env=PATH rm -rf /tmp/x",
		"env VAR=1 nice -n 5 rm -rf /tmp/x",
	}
	for _, cmd := range hits {
		seg := firstRmCommandSegment(cmd)
		if seg == "" || !strings.Contains(seg, "rm") {
			t.Errorf("issue #5456 shape not detected: %q -> segment %q", cmd, seg)
		}
	}
	holds := []string{
		"sudo -u root rm -rf /tmp/x",
		"timeout 30 sudo rm -rf /tmp/x",
		"echo VAR=1 && git status",
	}
	for _, cmd := range holds {
		seg := firstRmCommandSegment(cmd)
		if strings.Contains(cmd, "rm") && seg == "" {
			t.Errorf("legacy shape regressed (must still detect): %q -> empty", cmd)
		}
		if !strings.Contains(cmd, "rm") && seg != "" {
			t.Errorf("negative pin broken: %q -> segment %q (expected none)", cmd, seg)
		}
	}
}
