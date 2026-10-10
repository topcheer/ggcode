package agent

// #3749 + #3750 probes.
//
// #3749: a subtree wildcard (`go test ./internal/agent/...`) must yield the
// SUBTREE prefix as the verification scope - not ALL, which permanently
// marked every edited package verified and swallowed cross-package gaps.
// Bare-root ./... keeps ALL semantics.
//
// #3750: only Go source files map to package directories; docs/YAML/python
// edits must not manufacture "packages" that then warn UNVERIFIED and burn
// the warning budget.

import (
	"encoding/json"
	"testing"
)

func jsonCmd(t *testing.T, cmd string) string {
	t.Helper()
	return jsonStr(t, map[string]string{"command": cmd})
}

func TestIssue3749_SubtreeWildcardIsSubtreeNotAll(t *testing.T) {
	scopes := coverageExtractVerifyScopes("go test ./internal/agent/...")
	if len(scopes) != 1 || scopes[0] != "internal/agent" {
		t.Fatalf("subtree wildcard scopes = %v, want [internal/agent]", scopes)
	}
	// Root wildcard keeps ALL.
	if scopes := coverageExtractVerifyScopes("go test ./..."); len(scopes) != 1 || scopes[0] != "ALL" {
		t.Fatalf("root ./... scopes = %v, want [ALL]", scopes)
	}
}

func TestIssue3749_CrossPackageGapStillWarned(t *testing.T) {
	s := newEditCoverageState()
	s.recordToolCall("edit_file", jsonStr(t, map[string]string{"file_path": "/workspace/internal/agent/foo.go"}))
	s.recordToolCall("edit_file", jsonStr(t, map[string]string{"file_path": "/workspace/cmd/ggcode/bar.go"}))

	// Verifying only the internal/agent subtree must NOT mark cmd/ggcode
	// verified - the cross-package gap is the detector's whole point.
	warn := s.recordToolCall("run_command", jsonCmd(t, "go test ./internal/agent/..."))
	if warn == "" {
		t.Fatal("cross-package gap after subtree-only verify must warn")
	}

	// And verifying the actual subtree of a single-package edit stays quiet.
	s2 := newEditCoverageState()
	s2.recordToolCall("edit_file", jsonStr(t, map[string]string{"file_path": "/workspace/internal/agent/foo.go"}))
	if warn := s2.recordToolCall("run_command", jsonCmd(t, "go test ./internal/agent/...")); warn != "" {
		t.Fatalf("subtree verify covering all edits must stay quiet, got: %s", warn)
	}
}

func TestIssue3750_NonSourceFilesMakeNoPackages(t *testing.T) {
	for _, f := range []string{
		"docs/design/bar.md",
		"desktop/foo.yaml",
		"scripts/eval/baz.py",
		"internal/agent/noext",
	} {
		if got := coverageFileToPackage(f); got != "" {
			t.Fatalf("coverageFileToPackage(%q) = %q, want empty", f, got)
		}
	}
	if got := coverageFileToPackage("internal/agent/real.go"); got != "internal/agent" {
		t.Fatalf("coverageFileToPackage(.go) = %q, want internal/agent", got)
	}
}

func TestIssue3750_DocsEditDoesNotWarnOrBurnBudget(t *testing.T) {
	s := newEditCoverageState()
	s.recordToolCall("edit_file", jsonStr(t, map[string]string{"file_path": "/workspace/internal/agent/foo.go"}))
	s.recordToolCall("edit_file", jsonStr(t, map[string]string{"file_path": "/workspace/docs/design/bar.md"}))

	// Verifying the only (Go) package must be quiet: the .md edit
	// manufactures no docs/design package entry.
	if warn := s.recordToolCall("run_command", jsonCmd(t, "go test ./internal/agent/")); warn != "" {
		t.Fatalf("doc edit must not warn, got: %s", warn)
	}
	_ = json.Marshal // keep encoding/json import exercised alongside jsonStr
}
