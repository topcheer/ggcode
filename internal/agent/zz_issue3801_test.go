package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIssue3801A_NoPhantomMakeTargets(t *testing.T) {
	dir := t.TempDir()

	makefile := "unit-test:\n\tgo test ./...\n\nintegration-test:\n\tgo test -tags integration ./...\n"
	if err := os.WriteFile(filepath.Join(dir, "Makefile"), []byte(makefile), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := buildVerifyContext(dir)
	if strings.Contains(ctx, "make test") || strings.Contains(ctx, "Available make targets") {
		t.Fatalf("phantom target advertised from long-name-only Makefile: %q", ctx)
	}

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

	got := stripCodeFence("```go test ./internal/agent/```")
	if got != "go test ./internal/agent/" {
		t.Fatalf("single-line fenced command lost: got %q", got)
	}

	got = stripCodeFence("```bash make test```")
	if got != "make test" {
		t.Fatalf("lang-tag single-line fence must strip tag: got %q", got)
	}

	if got := stripCodeFence("```bash"); got != "" {
		t.Fatalf("lang-only fence must stay empty, got %q", got)
	}
	if got := stripCodeFence("```"); got != "" {
		t.Fatalf("bare fence must stay empty, got %q", got)
	}

	got = stripCodeFence("```bash\ngo test ./x/\n```")
	if got != "go test ./x/" {
		t.Fatalf("multi-line fence regression: got %q", got)
	}
}
