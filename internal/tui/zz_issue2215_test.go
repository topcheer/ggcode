package tui

// #2215: the tg create-bot path persisted garbage tokens (Enabled:true)
// and never rolled back a failed start - the dual guard slack got in
// #1392-B(1). Pins: the token form gate, and the rollback wiring at both
// failure points.

import (
	"os"
	"strings"
	"testing"
)

func TestIssue2215TGTokenFormGate(t *testing.T) {
	bad := []string{"garbage", "123", "abc:def", "123456:short", ":AAFFxx", "12 3:AAAAbbbbcccc"}
	for _, b := range bad {
		if tgTokenForm.MatchString(b) {
			t.Errorf("form gate must reject %q", b)
		}
	}
	good := []string{"123456789:AAFFgghhiijjkkll", "42:AbCdEfGhIjKlMnOp"}
	for _, g := range good {
		if !tgTokenForm.MatchString(g) {
			t.Errorf("form gate must accept %q", g)
		}
	}
}

func TestIssue2215TGRollbackWired(t *testing.T) {
	b, err := os.ReadFile("tg_panel.go")
	if err != nil {
		t.Skipf("layout changed: %v", err)
	}
	src := string(b)
	for _, frag := range []string{
		"rollbackTGCreate(name, err)",   // ensure failure
		"m.rollbackTGCreate(name, err)", // start failure (method recv)
		"RemoveIMAdapter(name)",         // rollback removes
	} {
		if !strings.Contains(src, frag) {
			t.Errorf("tg create path must wire %s (#2215 rollback)", frag)
		}
	}
	if strings.Count(src, "rollbackTGCreate(name, err)") < 2 {
		t.Error("both failure points (ensure + start) must roll back")
	}
}
