package agent

// #3801 companion tests.
// A: buildVerifyContext must not advertise phantom make targets - a
//    Makefile with only "unit-test:" has no bare "test" target, but the
//    old bare substring check ("test:" in content) claimed one, the
//    oracle emitted `make test`, and "No rule to make target 'test'"
//    pushed the agent into a phantom-error repair loop.
// B: a single-LINE fenced command (```go test ./x/```) must keep its
//    command - the old no-newline branch blanked the block and the scoped
//    verification silently fell back to whole-repo commands.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIssue3801A_NoPhantomMakeTargets(t *testing.T) {
	dir := t.TempDir()
	// Long-named targets only: bare `test`/`verify`/`check` do NOT exist.
	makefile := "unit-test:\n\tgo test ./...\n\nintegration-test:\n\tgo test -tags integration ./...\n"
	if err := os.WriteFile(filepath.Join(dir, "Makefile"), []byte(makefile), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := buildVerifyContext(dir)
	if strings.Contains(ctx, "make test") || strings.Contains(ctx, "Available make targets") {
		t.Fatalf("phantom target advertised from long-name-only Makefile: %q", ctx)
	}

	// A real bare target must still be advertised (and anchored: comment
	// mentions and `:=` assignments still excluded by hasMakeTarget).
	real := "# run make test\nTEST := foo\n\ntest:\n\tgo test ./...\n"
	if err := os.WriteFile(filepath.Join(dir, "Makefile"), []byte(real), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx = buildVerifyContext(dir)
	if !strings.Contains(ctx, "make test") {
		t.Fatalf("real test target must be advertised: %q", ctx)
	}
}

func TestIssue3801B_SingleLineFenceKeepsCommand(t *testing.T) {
	// "go" is a command verb here, NOT the Go lang tag - kept intact.
	got := stripCodeFence("```go test ./internal/agent/```")
	if got != "go test ./internal/agent/" {
		t.Fatalf("single-line fenced command lost: got %q", got)
	}
	// Known lang tag as first token is stripped, command kept.
	got = stripCodeFence("```bash make test```")
	if got != "make test" {
		t.Fatalf("lang-tag single-line fence must strip tag: got %q", got)
	}
	// Lang-tag-only single-line fence (no command) still falls back empty.
	if got := stripCodeFence("```bash"); got != "" {
		t.Fatalf("lang-only fence must stay empty, got %q", got)
	}
	if got := stripCodeFence("```"); got != "" {
		t.Fatalf("bare fence must stay empty, got %q", got)
	}
	// Multi-line shape is unchanged: tag line dropped, command kept.
	got = stripCodeFence("```bash\ngo test ./x/\n```")
	if got != "go test ./x/" {
		t.Fatalf("multi-line fence regression: got %q", got)
	}
}
