package main

import (
	"os"
	"strings"
	"testing"
)

func Test3994ProviderSwitchHookReadsLocalSnapshot(t *testing.T) {
	src, err := os.ReadFile("daemon.go")
	if err != nil {
		t.Fatalf("read daemon.go: %v", err)
	}
	text := string(src)

	start := strings.Index(text, "SetProviderSwitchHook(func(")
	if start < 0 {
		t.Fatal("provider-switch hook not found in daemon.go")
	}

	endRel := strings.Index(text[start:], "\n\t})")
	if endRel < 0 {
		t.Fatal("hook body end marker not found")
	}
	body := text[start : start+endRel]

	for i, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.Contains(line, "resolved.") && !strings.HasPrefix(trimmed, "//") {
			t.Fatalf("hook reads the outer `resolved` without the lock (line %d: %q) - a concurrent switch races it again (#3994 regression); use the local r snapshot", i, trimmed)
		}
	}
	if !strings.Contains(body, "resolved = r") {
		t.Fatal("locked write-back assignment missing - #2868 regression")
	}
	if !strings.Contains(body, "#3994") {
		t.Fatal("#3994 rationale comment missing at the hook")
	}
}
