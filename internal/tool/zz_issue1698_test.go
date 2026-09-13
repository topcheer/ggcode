package tool

// #1698 regressions:
//   - 1: a >10MB image/document with offset/limit streamed raw binary as
//     numbered text into context (the mime dispatch only ran <=10MB).
//   - 4: the restart schema marks reason REQUIRED but an empty reason
//     silently restarted the process.
//   - 5: hasPrecommitMakeTarget TrimLeft'd tabs, so a recipe line
//     `\tbuild:` read as a target definition.
//   - 6: the streaming range reader scanned to EOF past the limit just
//     to count total lines.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Case 1: oversized image with a range request is refused, not streamed.
func TestIssue1698Case1_OversizedImageRangeRefused(t *testing.T) {
	dir := t.TempDir()
	// A tiny valid PNG whose size we cannot exceed - instead verify via a
	// .png path: the sniff must reject BEFORE range streaming regardless
	// of size, so craft a >maxFileSize .png by writing a big header.
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

// Case 4: empty reason is rejected before the requester fires.
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

// Case 5: a TAB-indented recipe line is not a target definition.
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
