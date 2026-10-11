package agent

// zz_issue3752_lint_tags_test.go -- companion tests for #3752: go vet must
// carry the detected build tags, and compile-error lines must not be
// extracted as lint warnings.
import (
	"os"
	"path/filepath"
	"testing"
)

func TestIssue3752VetCarriesBuildTags(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n\ngo 1.27\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Makefile with tags but NO lint target - exactly the #3752 shape where
	// detectLintCommand used to fall through to a bare `go vet ./...`.
	makefile := "TAGS := mytag3752\n\nbuild:\n\tgo build -tags \"$(TAGS)\" ./...\n"
	if err := os.WriteFile(filepath.Join(dir, "Makefile"), []byte(makefile), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := detectLintCommand(dir)
	if cmd != "go vet -tags mytag3752 ./..." {
		t.Fatalf("detectLintCommand = %q, want %q", cmd, "go vet -tags mytag3752 ./...")
	}
}

func TestIssue3752VetPlainWithoutTags(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n\ngo 1.27\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if cmd := detectLintCommand(dir); cmd != "go vet ./..." {
		t.Fatalf("detectLintCommand = %q, want plain go vet when no tags are known", cmd)
	}
}

func TestIssue3752MakeLintStillWins(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n\ngo 1.27\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	makefile := "TAGS := mytag3752\n\nlint:\n\tgo vet -tags \"$(TAGS)\" ./...\n"
	if err := os.WriteFile(filepath.Join(dir, "Makefile"), []byte(makefile), 0o644); err != nil {
		t.Fatal(err)
	}
	if cmd := detectLintCommand(dir); cmd != "make lint" {
		t.Fatalf("detectLintCommand = %q, want make lint (authoritative project config)", cmd)
	}
}

func TestIssue3752CompileErrorsNotLintWarnings(t *testing.T) {
	// The exact #3752 repro: vet without tags on a tag-gated repo emits
	// undefined-symbol compile errors; they must NOT become lint warnings.
	output := "# github.com/topcheer/ggcode/internal/agent [build failed]\n" +
		"internal/agent/agent.go:381: undefined: attemptBriefState\n" +
		"internal/agent/tool.go:12: imported and not used: \"fmt\"\n" +
		"internal/agent/x.go:8:2: undeclared name: missingThing\n" +
		"internal/agent/y.go:3:1: missing return\n"
	warnings := extractLintWarnings(output)
	if len(warnings) != 0 {
		t.Fatalf("compile errors leaked as lint warnings: %v", warnings)
	}
}

func TestIssue3752RealVetFindingsStillExtracted(t *testing.T) {
	// Guard against over-filtering: genuine vet diagnostics must survive.
	output := "internal/agent/verify.go:120: Printf-style function has dynamic arg list\n" +
		"internal/util/x.go:42: copylocks: assignment copies lock value\n"
	warnings := extractLintWarnings(output)
	if len(warnings) != 2 {
		t.Fatalf("real vet findings must be kept, got %v", warnings)
	}
}
