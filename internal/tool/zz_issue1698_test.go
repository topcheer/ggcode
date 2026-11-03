package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIssue1698Case1_OversizedImageRangeRefused(t *testing.T) {
	dir := t.TempDir()

	p := filepath.Join(dir, "big.png")
	big := make([]byte, maxFileSize+1)
	for i := range big {
		big[i] = 'x'
	}
	if err := os.WriteFile(p, big, 0o644); err != nil {
		t.Fatal(err)
	}
	rt := ReadFile{}
	res, err := rt.Execute(context.Background(), mustJSON1698(t, map[string]any{"path": p, "offset": 1, "limit": 50}))
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("oversized image range read must be refused, not streamed as text")
	}
	if !strings.Contains(res.Content, "image") {
		t.Fatalf("refusal must name the mime reason, got %q", res.Content)
	}
}

func TestIssue1698Case4_EmptyReasonRejected(t *testing.T) {
	rt := &RestartTool{Requester: &fakeRestartRequester{}}
	raw, _ := json.Marshal(map[string]any{"reason": "   ", "debug": false})
	res, err := rt.Execute(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("blank reason must be rejected (schema says REQUIRED)")
	}
	if fr, ok := rt.Requester.(*fakeRestartRequester); ok && fr.called {
		t.Fatal("requester must NOT fire on blank reason")
	}
}

func TestIssue1698Case5_RecipeLineNotTarget(t *testing.T) {
	mk := "install:\n\tbuild:\n\techo done\n"
	if hasPrecommitMakeTarget(mk, "build") {
		t.Fatal("recipe line \\tbuild: must not count as a build target")
	}
	if !hasPrecommitMakeTarget(mk, "install") {
		t.Fatal("real target must still be found")
	}
}

func mustJSON1698(t *testing.T, m map[string]any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
