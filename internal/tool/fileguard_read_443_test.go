package tool

import (
	"context"
	"encoding/json"
	"os"
	"testing"
)

// #443: read tools must never be gated by FileGuard (write-only design).
func TestReadToolsNotBlockedByFileGuard(t *testing.T) {
	dir := t.TempDir()
	reg := NewRegistry()
	// Default-protected patterns include .env / .git — the regression case.
	if err := RegisterBuiltinTools(reg, nil, dir, []string{".env*", ".git/"}); err != nil {
		t.Fatal(err)
	}
	envPath := dir + "/.env"
	writeTestFile(t, envPath, "SECRET=1\n")
	headPath := dir + "/.git/HEAD"
	if err := os.MkdirAll(dir+"/.git", 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, headPath, "ref: refs/heads/main\n")

	rf, _ := reg.Get("read_file")
	res, err := rf.Execute(context.Background(), jsonArgs(t, map[string]any{"path": envPath}))
	if err != nil || res.IsError {
		t.Errorf("read_file on .env must NOT be blocked (write-only guard), got err=%v isErr=%v content=%.80s", err, res.IsError, res.Content)
	}
	res2, _ := rf.Execute(context.Background(), jsonArgs(t, map[string]any{"path": headPath}))
	if res2.IsError {
		t.Errorf("read_file on .git/HEAD must NOT be blocked: %.80s", res2.Content)
	}
	// Write tools must STILL be blocked (regression guard for the guard).
	wf, _ := reg.Get("write_file")
	res3, _ := wf.Execute(context.Background(), jsonArgs(t, map[string]any{"path": envPath, "content": "x"}))
	if !res3.IsError {
		t.Error("write_file on .env must STILL be blocked by file guard")
	}
}

func jsonArgs(t *testing.T, m map[string]any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return json.RawMessage(b)
}

// #444: IsDestructive must include ask-level destructive commands.
func TestIsDestructiveIncludesAskRules(t *testing.T) {
	g := NewCommandGate()
	if !g.IsDestructive("rm -rf ./build") {
		t.Error("rm -rf relative path (ask-level) must be IsDestructive")
	}
	if !g.IsDestructive("git reset --hard HEAD~3") {
		t.Error("git reset --hard (ask-level) must be IsDestructive")
	}
	if g.IsDestructive("ls -la") {
		t.Error("benign command must not be IsDestructive")
	}
}
