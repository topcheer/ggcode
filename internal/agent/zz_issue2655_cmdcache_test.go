package agent

import (
	"testing"

	"github.com/topcheer/ggcode/internal/tool"
)

func TestIssue2655_MissedMutatorsDetected(t *testing.T) {
	missed := []string{

		"git checkout feature-x",
		"git checkout main",
		"git switch feature-x",

		"git pull",
		"git pull --ff-only",
		"git merge feature-x",

		"curl -o main.go https://example.com/payload",
		"curl -fsSL https://example.com/x -o gen.go",
		"wget https://example.com/blob.go",
		"wget -O gen2.go https://example.com/x",

		"rsync -a src/ dst/",
		"scp host:gen.go gen.go",
		"tar -xzf bundle.tgz",
		"tar xzf bundle.tgz",
		"unzip bundle.zip",
	}
	for _, cmd := range missed {
		if !shellMutatesSources(cmd) {
			t.Errorf("shellMutatesSources(%q) = false, want true (#2655 missed mutator)", cmd)
		}
	}
}

func TestIssue2655_StaleHitAfterBranchSwitch(t *testing.T) {
	cc := newCommandCache()
	res := tool.Result{Content: "PASS old branch"}
	cmd := "# verify\ngo test ./..."
	cc.put(cmd, "/repo", res)

	if shellMutatesSources("git checkout feature-x") {
		cc.invalidate()
	}
	if _, hit := cc.get(cmd, "/repo"); hit {
		t.Fatal("cache still serves pre-branch-switch `go test` result: stale replay (#2655); invalidation predicate missed the branch switch")
	}
}

func TestIssue2655_GoFmtNotCacheable(t *testing.T) {
	if isCacheableCommand("go fmt ./...") {
		t.Fatal("isCacheableCommand(\"go fmt ./...\") = true: go fmt rewrites files in place (#1028), caching its result is contract-inconsistent and the entry can never hit (#2655)")
	}
}
