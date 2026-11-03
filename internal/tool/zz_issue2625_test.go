//go:build darwin

package tool

import (
	"context"
	"strings"
	"testing"
)

func TestIssue2625_CtrlNonLetterRejected(t *testing.T) {
	tool := &Iterm2Tool{}
	for _, key := range []string{"1", "/", ";", "5"} {
		res := tool.executeSendKey(context.Background(), "", key, "ctrl")
		if !res.IsError {
			t.Fatalf("ctrl+%s must be rejected as unsupported (was: bare key + false success, #2625): %s", key, res.Content)
		}
		if !strings.Contains(res.Content, "unsupported key combo") {
			t.Fatalf("ctrl+%s error should name the unsupported combo, got: %s", key, res.Content)
		}
	}

}
