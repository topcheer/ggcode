package tool

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// #4777 (S-05): the shared-workspace stash stack is one namespace for every
// seat and sub-agent. The tool now stamps pushes with a seat tag and refuses
// pop/apply/drop on entries it did not create (foreign tag or untagged),
// preventing a sub-agent cleanup from deleting another seat's live entry
// (incident e7746bd).
func zz4777SetupRepo(t *testing.T) (dir string, tool GitStash) {
	t.Helper()
	dir = t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	run("config", "user.email", "t@t")
	run("config", "user.name", "t")
	// TRACED baselines so a later content modification is stashable -
	// stash push only saves tracked changes (the tool's own doc says so).
	if err := os.WriteFile(filepath.Join(dir, "own.txt"), []byte("base"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("base"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "--allow-empty", "-m", "init")
	tool = GitStash{}
	return dir, tool
}

// zz4777Dirty makes the tree dirty and pushes a stash DIRECTLY via git,
// bypassing our tooling - a foreign (untagged) entry, e.g. CLI-created or
// another seat's (that seat stamps with ITS owner id; untagged covers the
// CLI case and a foreign-tagged case is exercised via message forgery
// below).
func zz4777DirectStash(t *testing.T, dir, msg string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("dirty"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "stash", "push", "-m", msg)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("direct stash: %v\n%s", err, out)
	}
}

func TestIssue4777_StashSeatIsolation(t *testing.T) {
	dir, tool := zz4777SetupRepo(t)

	// Own entry: modify the TRACKED file, push via the tool.
	if err := os.WriteFile(filepath.Join(dir, "own.txt"), []byte("own"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := tool.Execute(context.Background(), zz4777JSON(t, dir, "push", "my work", 0)); err != nil {
		t.Fatal(err)
	}
	// Foreign entry on top (index 0), own becomes index 1.
	zz4777DirectStash(t, dir, "seat-b manual work")

	// Foreign (untagged, index 0): drop refused.
	res, err := tool.Execute(context.Background(), zz4777JSON(t, dir, "drop", "", 0))
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || !strings.Contains(res.Content, "not this session") {
		t.Fatalf("untagged entry drop must be refused, got: %s", res.Content)
	}
	// pop on the foreign entry: refused too.
	res2, err2 := tool.Execute(context.Background(), zz4777JSON(t, dir, "pop", "", 0))
	if err2 != nil {
		t.Fatal(err2)
	}
	if !res2.IsError {
		t.Fatalf("untagged entry pop must be refused, got: %s", res2.Content)
	}

	// Own entry (index 1): drop succeeds.
	res3, err3 := tool.Execute(context.Background(), zz4777JSON(t, dir, "drop", "", 1))
	if err3 != nil {
		t.Fatal(err3)
	}
	if res3.IsError {
		t.Fatalf("own tagged entry must be droppable: %s", res3.Content)
	}
}

func TestIssue4777_ForeignTagRefused(t *testing.T) {
	dir, tool := zz4777SetupRepo(t)
	// A foreign seat's entry carries a DIFFERENT owner tag.
	zz4777DirectStash(t, dir, "work "+stashOwnerTagPrefix+"other-seat-999]")
	res, err := tool.Execute(context.Background(), zz4777JSON(t, dir, "drop", "", 0))
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || !strings.Contains(res.Content, "another seat/process") {
		t.Fatalf("foreign-tagged entry must be refused as another seat's, got: %s", res.Content)
	}
}

func zz4777JSON(t *testing.T, dir, action, msg string, index int) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(struct {
		Action  string `json:"action"`
		Message string `json:"message"`
		Index   int    `json:"index"`
		Path    string `json:"path"`
	}{action, msg, index, dir})
	if err != nil {
		t.Fatal(err)
	}
	return b
}
