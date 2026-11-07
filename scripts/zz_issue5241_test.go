package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// #5241: pre-commit/pre-push/verify-ci gofmt gates used the default
// toolchain's gofmt (e.g. 1.27.3) while go.mod pins 1.26.10 for CI, so the
// hooks silently re-formatted CI-clean files and the gates false-red'd on
// files the branch never touched. The gates now route through
// scripts/dev/gofmt-pinned.sh, which pins gofmt to the go.mod toolchain.
// These tests pin that contract: the wrapper exists and is executable, it
// runs the pinned path (no fallback WARNING on stderr) on a fixture whose
// formatting is identical across gofmt versions, and it agrees with the
// toolchain that go.mod declares.
func TestIssue5241PinnedGofmtWrapperRunsPinnedToolchain(t *testing.T) {
	root, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		t.Skipf("not in a git repo: %v", err)
	}
	repo := strings.TrimSpace(string(root))
	wrapper := filepath.Join(repo, "scripts", "dev", "gofmt-pinned.sh")

	st, err := os.Stat(wrapper)
	if err != nil {
		t.Fatalf("gofmt-pinned.sh missing: %v", err)
	}
	if st.Mode()&0111 == 0 {
		t.Fatalf("gofmt-pinned.sh not executable: mode %v", st.Mode())
	}

	// Fixture formatted identically by every gofmt version.
	dir := t.TempDir()
	fixture := filepath.Join(dir, "fixture.go")
	if err := os.WriteFile(fixture, []byte("package p\n\nfunc f() {}\n"), 0644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(wrapper, "-l", fixture)
	var out, errb strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		t.Fatalf("wrapper run failed: %v; stderr: %s", err, errb.String())
	}
	if got := out.String(); got != "" {
		t.Errorf("wrapper -l on a clean fixture must be empty, got %q", got)
	}
	// A fallback to the default gofmt emits a WARNING on stderr (#5241's
	// failure mode). Empty stderr proves the pinned path actually ran.
	if warn := errb.String(); warn != "" {
		t.Errorf("wrapper emitted stderr (fallback or toolchain problem): %q", warn)
	}
}

func TestIssue5241GoModPinnedVersionResolvable(t *testing.T) {
	root, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		t.Skipf("not in a git repo: %v", err)
	}
	repo := strings.TrimSpace(string(root))

	mod, err := os.ReadFile(filepath.Join(repo, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	pinned := ""
	for _, line := range strings.Split(string(mod), "\n") {
		if strings.HasPrefix(line, "go ") {
			pinned = strings.TrimSpace(strings.TrimPrefix(line, "go "))
			break
		}
	}
	if pinned == "" || !strings.Contains(pinned, ".") {
		t.Fatalf("go.mod has no parseable pinned go version: %q", pinned)
	}

	// The toolchain the wrapper pins to must actually resolve here
	// (module cache or download); this is what keeps the wrapper off its
	// fallback path in CI-matching environments.
	cmd := exec.Command("go", "env", "GOVERSION")
	cmd.Env = append(os.Environ(), "GOTOOLCHAIN=go"+pinned)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("GOTOOLCHAIN=go%s go env GOVERSION failed: %v", pinned, err)
	}
	if got := strings.TrimSpace(string(out)); got != "go"+pinned {
		t.Errorf("pinned toolchain resolved to %q, want go%s", got, pinned)
	}
}

// #5241 class guard: no repo gate may invoke a bare `gofmt` command anymore.
// A bare gofmt resolves from PATH to the machine's default toolchain (e.g.
// 1.27.3) while go.mod pins 1.26.10 for CI, reintroducing exactly the
// format/verify skew this issue fixed (pre-commit silently reformatting
// CI-clean files, pre-push false-reds, regen scripts stamping 1.27 style).
// The pinned wrapper is the only sanctioned gofmt path. Detected shapes:
// command-substitution invocations and command-position invocations;
// messages and comments merely mentioning gofmt stay allowed.
func TestIssue5241NoBareGofmtInRepoGates(t *testing.T) {
	root, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		t.Skipf("not in a git repo: %v", err)
	}
	repo := strings.TrimSpace(string(root))

	gates := []string{
		".githooks/pre-commit",
		".githooks/pre-push",
		"scripts/dev/verify-ci.sh",
		"Makefile",
	}
	for _, gate := range gates {
		body, err := os.ReadFile(filepath.Join(repo, filepath.FromSlash(gate)))
		if err != nil {
			t.Errorf("gate %s missing: %v", gate, err)
			continue
		}
		for i, line := range strings.Split(string(body), "\n") {
			if strings.Contains(line, "gofmt-pinned.sh") {
				continue
			}
			bare := strings.Contains(line, "$(gofmt ") ||
				strings.HasPrefix(strings.TrimLeft(line, " \t"), "gofmt ")
			if bare {
				t.Errorf("%s:%d uses bare gofmt: %q", gate, i+1, strings.TrimSpace(line))
			}
		}
	}
}
