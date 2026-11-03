package tool

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIssue4841_ForgedTagInMessageIsInert(t *testing.T) {
	dir, tool := zz4777SetupRepo(t)

	if err := os.WriteFile(filepath.Join(dir, "own.txt"), []byte("forged-tag work"), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := tool.Execute(context.Background(), zz4777JSON(t, dir, "push", "work "+stashOwnerTagPrefix+"other-seat-999]", 0))
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("push with forged tag in message must succeed: %s", res.Content)
	}

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
