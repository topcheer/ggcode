package agent

import "testing"

// #3812: verify_scope_narrow business-logic accuracy.
//   A: isNarrower must not treat a bare package-count drop as narrowing —
//      `go test ./pkg/a/ ./pkg/b/` → `go test ./pkg/c/` is a normal
//      debugging pivot (disjoint packages), not scope gaming. Only true
//      subsets (b ⊂ a) and path-prefix containment count.
//   B: isGoSubcommand / pytest classification must only match at the head of
//      a shell segment — `git commit -m "go test passed"` must NOT be
//      recorded as a passed verification; `cd /x && go test ./...` MUST.

func TestIssue3812_LenOnlyDropIsNotNarrowing(t *testing.T) {
	// Disjoint packages: a→c is a pivot, not narrowing (was true before #3812).
	if isNarrower("pkg:./pkg/c/", "pkg:./pkg/a/|pkg:./pkg/b/") {
		t.Fatal("package-count drop without containment must not count as narrowing")
	}
	// Partial overlap, still not a subset: {a,b,c} → {c,d}.
	if isNarrower("pkg:./pkg/c/|pkg:./pkg/d/", "pkg:./pkg/a/|pkg:./pkg/b/|pkg:./pkg/c/") {
		t.Fatal("intersecting-but-not-subset scope switch must not count as narrowing")
	}
}

func TestIssue3812_TrueSubsetStillNarrows(t *testing.T) {
	if !isNarrower("pkg:./pkg/a/", "pkg:./pkg/a/|pkg:./pkg/b/") {
		t.Fatal("true subset (b ⊂ a, fewer) must still count as narrowing")
	}
	// Path-prefix containment (#2810) unaffected by the len-only removal.
	if !isNarrower("pkg:./internal/agent/sub/", "pkg:./internal/agent/") {
		t.Fatal("path-prefix containment must still count as narrowing")
	}
	if !isNarrower("file:./internal/agent/x_test.go", "pkg:./internal/agent/") {
		t.Fatal("pkg→file containment must still count as narrowing")
	}
}

// goSub3812 asserts the command classifies as go-test, go-vet, or go-build.
func goSub3812(cmd string) bool {
	return isGoSubcommand(cmd, "test") || isGoSubcommand(cmd, "vet") || isGoSubcommand(cmd, "build")
}

func TestIssue3812_GoSubcommandHeadOnly(t *testing.T) {
	// Must classify.
	for _, cmd := range []string{
		"go test ./...",
		"go vet ./internal/...",
		"go build ./cmd/x",
		"time go test ./...",
		"sudo nice go test ./...",
		"env GOFLAGS=-p=1 go test ./internal/agent/",
		"timeout 60s go vet ./...",
		"cd /Volumes/new/ggai/ggcode && go test ./internal/agent/",
		"cd /tmp/x; go build ./...",
	} {
		if !goSub3812(cmd) {
			t.Fatalf("expected go subcommand classification for %q", cmd)
		}
	}
	// Must NOT classify: "go <sub>" only inside arguments/quoted text.
	for _, cmd := range []string{
		`git commit -m "go test passed"`,
		`echo go build ok`,
		`cargo test ./...`,
		`grep "go test" Makefile`,
		`pip install pytest`,
		`cat go_test_notes.txt`,
	} {
		if goSub3812(cmd) {
			t.Fatalf("mid-argument match must not classify %q", cmd)
		}
	}
}

func TestIssue3812_ClassifyNotPolluted(t *testing.T) {
	if cat, _ := classifyVerificationCommand(`git commit -m "go test passed"`); cat != "" {
		t.Fatalf("commit message must not classify as verification, got %q", cat)
	}
	if cat, _ := classifyVerificationCommand("pip install pytest"); cat != "" {
		t.Fatalf("pip install pytest must not classify as pytest run, got %q", cat)
	}
	cat, _ := classifyVerificationCommand("cd /x && go test ./internal/agent/")
	if cat != "go-test" {
		t.Fatalf("compound cd && go test must classify as go-test, got %q", cat)
	}
	cat, _ = classifyVerificationCommand("python -m pytest tests/")
	if cat != "pytest" {
		t.Fatalf("python -m pytest must classify as pytest, got %q", cat)
	}
}
