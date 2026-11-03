package tool

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestIssue4553A_MacroDefineRejectsNonObjectArgs(t *testing.T) {
	mt := &MacroTool{WorkingDir: t.TempDir(), Registry: NewRegistry()}
	out, err := mt.Execute(context.Background(), json.RawMessage(
		`{"action":"define","name":"bad","steps":[{"tool":"read_file","args":["/etc/hosts"]}]}`))
	if err == nil && !out.IsError {
		t.Fatalf("array args must be rejected at define time: %+v", out)
	}
	if !strings.Contains(out.Content, "args must be a JSON object") {
		t.Fatalf("unexpected rejection text: %q", out.Content)
	}
}

func TestIssue4553B_KittySplitRejectsOutOfRangeSize(t *testing.T) {
	k := &KittyTool{}
	for _, size := range []int{100, 150, -5} {
		out := k.executeSplit(context.Background(), 1, "right", size, "", "")
		if !out.IsError {
			t.Fatalf("size=%d must be rejected, got: %+v", size, out)
		}
		if !strings.Contains(out.Content, "1-99") {
			t.Fatalf("size=%d rejection must state the valid range: %q", size, out.Content)
		}
	}
}

func TestIssue4553C_WildcardAnywhereBroadcasts(t *testing.T) {
	raw, err := os.ReadFile("lanchat_tool.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(raw)
	anchor := strings.Index(src, "doBroadcastAll(ctx, content, asAgent)")
	if anchor < 0 {
		t.Fatal("broadcast call not found - lanchat_tool.go drifted")
	}
	window := src[max(0, anchor-600):anchor]
	if !strings.Contains(window, "for _, id := range toNodeIDs") || !strings.Contains(window, `id == "*"`) {
		t.Fatal("wildcard broadcast must scan every recipient (mixed lists keep broadcast intent) (#4553-C)")
	}
}
