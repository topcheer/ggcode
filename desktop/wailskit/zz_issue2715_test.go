package wailskit

// #2715: the wailskit reflection path must use LoadKey (single-key read),
// not LoadAll (merges EVERY memory key), when refreshing run-insights -
// and must record provenance via SaveMemoryWithSource, matching the TUI
// (#1388) and daemon (#1752 case 3) reflection paths.

import (
	"os"
	"strings"
	"testing"
)

func TestIssue2715ReflectionUsesLoadKeyNotLoadAll(t *testing.T) {
	b, err := os.ReadFile("chat.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)

	if strings.Contains(src, "autoMem.LoadAll()") {
		t.Fatal("#2715: reflection must not use LoadAll() - it merges every memory key into run-insights")
	}
	if !strings.Contains(src, `autoMem.LoadKey(key)`) {
		t.Fatal("#2715: reflection must read back run-insights via LoadKey")
	}
	if !strings.Contains(src, `autoMem.SaveMemoryWithSource(key, insights, "run-reflection")`) {
		t.Fatal("#2715: reflection must persist via SaveMemoryWithSource with provenance \"run-reflection\"")
	}
}
