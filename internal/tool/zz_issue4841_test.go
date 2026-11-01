package tool

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #4841: a forged owner tag inside the user's push message must be inert.
// The push path appends the tool's own tag LAST, so a message like
// "work [ggcode-seat:other-seat-999]" used to be extracted FIRST by the old
// regex - the entry then read as owned by other-seat-999, enabling self-DoS
// (own entry refused) and victim worktree injection. Tail-anchored extraction
// must trust only the tool-appended tag: pop on the pushed entry succeeds.
func TestIssue4841_ForgedTagInMessageIsInert(t *testing.T) {
	dir, tool := zz4777SetupRepo(t)

	if err := os.WriteFile(filepath.Join(dir, "own.txt"), []byte("forged-tag work"), 0o644); err != nil {
		t.Fatal(err)
	}
	// The user message carries a forged tag for a different seat.
	res, err := tool.Execute(context.Background(), zz4777JSON(t, dir, "push", "work "+stashOwnerTagPrefix+"other-seat-999]", 0))
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("push with forged tag in message must succeed: %s", res.Content)
	}

	// Own push -> pop must succeed (extraction ignored the forged prefix and
	// matched the tool-appended tail tag = this seat).
	res2, err2 := tool.Execute(context.Background(), zz4777JSON(t, dir, "pop", "", 0))
	if err2 != nil {
		t.Fatal(err2)
	}
	if res2.IsError {
		t.Fatalf("#4841: pop on own push must succeed despite forged prefix tag, got: %s", res2.Content)
	}
	if got, err := os.ReadFile(filepath.Join(dir, "own.txt")); err != nil || string(got) != "forged-tag work" {
		t.Fatalf("#4841: popped content mismatch: %q err=%v", got, err)
	}
}

// #4841 regression guard: a foreign TAIL tag (direct git push, the only way
// a foreign tag can legitimately sit at the end) must still be refused.
func TestIssue4841_ForeignTailTagStillRefused(t *testing.T) {
	dir, tool := zz4777SetupRepo(t)
	zz4777DirectStash(t, dir, "work "+stashOwnerTagPrefix+"other-seat-999]")
	res, err := tool.Execute(context.Background(), zz4777JSON(t, dir, "drop", "", 0))
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || !strings.Contains(res.Content, "another seat/process") {
		t.Fatalf("foreign tail tag must still be refused: %s", res.Content)
	}
}
