package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #3951 companion (from the issue author, verified red on pre-fix main):
// out-of-sandbox Move to must error with ZERO writes and zero source
// deletion; in-sandbox Move to must keep working.
func TestIssue3951AuthorCompanionBidirectional(t *testing.T) {
	base := t.TempDir()
	sandbox := filepath.Join(base, "sandbox")
	if err := os.MkdirAll(sandbox, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sandbox, "orig.txt"), []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	inside := func(p string) bool {
		rel, err := filepath.Rel(sandbox, p)
		return err == nil && (rel == "." || (!strings.HasPrefix(rel, "..") && rel != ""))
	}
	mkTool := func() *ApplyPatch { return &ApplyPatch{WorkingDir: sandbox, SandboxCheck: inside} }

	// 1) Escape: rejected before any write; source intact; nothing on disk outside.
	diff := "*** Update File: orig.txt\n@@\n-old\n+new\n*** Move to: ../../escaped.txt\n"
	b, _ := json.Marshal(map[string]string{"path": "orig.txt", "diff": diff})
	res, err := mkTool().Execute(context.Background(), b)
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || !strings.Contains(res.Content, "sandbox") {
		t.Fatalf("escape must error, got: %+v", res)
	}
	if got, rerr := os.ReadFile(filepath.Join(sandbox, "orig.txt")); rerr != nil || string(got) != "old\n" {
		t.Fatalf("rejected move must leave source untouched, got %q err=%v", got, rerr)
	}
	if _, serr := os.Stat(filepath.Join(base, "escaped.txt")); serr == nil {
		t.Fatal("file escaped the sandbox")
	}

	// 2) In-sandbox: normal move works, content updated at the new location.
	if err := os.WriteFile(filepath.Join(sandbox, "orig.txt"), []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	diff2 := "*** Update File: orig.txt\n@@\n-old\n+new\n*** Move to: sub/renamed.txt\n"
	if err := os.MkdirAll(filepath.Join(sandbox, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	b2, _ := json.Marshal(map[string]string{"path": "orig.txt", "diff": diff2})
	res2, err := mkTool().Execute(context.Background(), b2)
	if err != nil {
		t.Fatal(err)
	}
	if res2.IsError {
		t.Fatalf("in-sandbox move must succeed: %s", res2.Content)
	}
	if got, rerr := os.ReadFile(filepath.Join(sandbox, "sub", "renamed.txt")); rerr != nil || string(got) != "new\n" {
		t.Fatalf("renamed content wrong: %q err=%v", got, rerr)
	}
	if _, serr := os.Stat(filepath.Join(sandbox, "orig.txt")); !os.IsNotExist(serr) {
		t.Fatal("source must be gone after a real move")
	}
}
