package tool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// #1325: our conflict guidance tells the agent to "run 'git revert
// --continue'" - the natural mapping is calling this tool again with
// commit="--continue", which git parses as an option and commits/discards
// the sequencer's staged changes. Option-looking values must be rejected.
func TestGitRevertRejectsOptionLikeCommit(t *testing.T) {
	tool := GitRevert{WorkingDir: t.TempDir()}

	for _, hostile := range []string{"--continue", "--abort", "--quit", "-"} {
		input, _ := json.Marshal(map[string]string{"commit": hostile})
		res, err := tool.Execute(context.Background(), input)
		if err != nil {
			t.Fatalf("execute error for %q: %v", hostile, err)
		}
		if !res.IsError {
			t.Errorf("commit %q should be rejected", hostile)
			continue
		}
		if !strings.Contains(res.Content, "shell tool") {
			t.Errorf("rejection for %q should point at the shell tool, got: %s", hostile, res.Content)
		}
	}

	// A normal-looking ref is NOT rejected by this guard (downstream git
	// handles rev resolution).
	input, _ := json.Marshal(map[string]string{"commit": "HEAD"})
	res, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError && strings.Contains(res.Content, "looks like a git option") {
		t.Error("HEAD must not be rejected by the option guard")
	}
}
