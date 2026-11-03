package acp

import (
	"os"
	"strings"
	"testing"
)

func TestIssue4634_DiffConfirmDoesNotAutoApproveLegacyAuto(t *testing.T) {
	data, err := os.ReadFile("agent_loop.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	fn := strings.Index(text, "SetDiffConfirm(func(")
	if fn < 0 {
		t.Fatal("DiffConfirm registration not found")
	}
	end := fn + 1600
	if end > len(text) {
		end = len(text)
	}
	body := text[fn:end]
	if strings.Contains(body, `if mode == "auto"`) {
		t.Fatal("DiffConfirm must not special-case legacy auto mode (#4634)")
	}
	if !strings.Contains(body, `mode == "bypass" || mode == "autopilot"`) {
		t.Fatal("explicit bypass/autopilot auto-approve must remain (#4634)")
	}
	if !strings.Contains(body, "RequestPermission") {
		t.Fatal("default path must route through RequestPermission (#4634)")
	}
}
