package agent

import (
	"testing"

	"github.com/topcheer/ggcode/internal/tool"
)

// #2655: shellMutatesSources misses known source-rewriting shell commands
// (git checkout <branch>, git switch/pull/merge, curl -o, wget, rsync, scp,
// tar -x, unzip). The commandCache invalidation chain (agent.go #750 branch)
// keys solely on this predicate, so executing one of them leaves cached
// `go test` results alive for the full 10-minute TTL - a later identical
// `go test` replays the OLD tree's PASS with a literally false
// "no source files have changed" annotation.
func TestIssue2655_MissedMutatorsDetected(t *testing.T) {
	missed := []string{
		// Branch switches rewrite potentially every tracked file.
		"git checkout feature-x", // old patterns only matched "--" and "." forms
		"git checkout main",
		"git switch feature-x",
		// History-advancing commands fast-forward/merge the worktree.
		"git pull",
		"git pull --ff-only",
		"git merge feature-x",
		// Downloaders that write files into the tree.
		"curl -o main.go https://example.com/payload",
		"curl -fsSL https://example.com/x -o gen.go",
		"wget https://example.com/blob.go",
		"wget -O gen2.go https://example.com/x",
		// Copy/extract surfaces.
		"rsync -a src/ dst/",
		"scp host:gen.go gen.go",
		"tar -xzf bundle.tgz",
		"tar xzf bundle.tgz", // old-style flags, no dash
		"unzip bundle.zip",
	}
	for _, cmd := range missed {
		if !shellMutatesSources(cmd) {
			t.Errorf("shellMutatesSources(%q) = false, want true (#2655 missed mutator)", cmd)
		}
	}
}

// The #2655 trigger scenario, composed exactly like agent.go's post-execution
// branch: put a `go test` result, then run a branch-switching shell command;
// the invalidation the agent performs is gated on shellMutatesSources, so a
// false negative serves the stale entry. Post-fix the same sequence must miss.
func TestIssue2655_StaleHitAfterBranchSwitch(t *testing.T) {
	cc := newCommandCache()
	res := tool.Result{Content: "PASS old branch"}
	cmd := "# verify\ngo test ./..."
	cc.put(cmd, "/repo", res)

	// Mirror agent.go: after run_command executes, invalidate ONLY if the
	// predicate fires (same predicate, same command shape).
	if shellMutatesSources("git checkout feature-x") {
		cc.invalidate()
	}
	if _, hit := cc.get(cmd, "/repo"); hit {
		t.Fatal("cache still serves pre-branch-switch `go test` result: stale replay (#2655); invalidation predicate missed the branch switch")
	}
}

// #2655 companion: `go fmt` in cacheablePrefixes is a dead entry - #1028 made
// every `go fmt` run a source mutation, so its own put is invalidated in the
// same iteration (agent.go put L3354 -> invalidate L3441) and can never hit.
// A file-writing command does not belong in a "deterministic build/test/lint"
// whitelist anyway (cf. excluded npm install / go generate).
func TestIssue2655_GoFmtNotCacheable(t *testing.T) {
	if isCacheableCommand("go fmt ./...") {
		t.Fatal("isCacheableCommand(\"go fmt ./...\") = true: go fmt rewrites files in place (#1028), caching its result is contract-inconsistent and the entry can never hit (#2655)")
	}
}
