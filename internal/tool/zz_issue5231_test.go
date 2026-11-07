package tool

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/topcheer/ggcode/internal/debug"
)

func issue5231ResetState() {
	rgPath.Store((*string)(nil))
	rgLastCheck.Store(0)
	rgTrying.Store(false)
}

func issue5231CaptureLogs(t *testing.T, run func()) []string {
	t.Helper()
	var mu sync.Mutex
	var got []string
	debug.SetLiveSink(func(category, msg string) {
		mu.Lock()
		got = append(got, msg)
		mu.Unlock()
	})
	defer debug.SetLiveSink(nil)
	run()
	mu.Lock()
	defer mu.Unlock()
	return got
}

func issue5231AssertGuidance(t *testing.T, logs []string) {
	t.Helper()
	joined := strings.Join(logs, "\n")
	if !strings.Contains(joined, "third-party wrapper") || !strings.Contains(joined, "BurntSushi/ripgrep/releases") {
		t.Errorf("expected actionable install-guidance log (third-party wrapper + official releases URL) after installRG, got: %v", logs)
	}
}

func issue5231WriteShim(t *testing.T, dir, name string) {
	t.Helper()
	path := filepath.Join(dir, name)
	body := "exit 0\n"
	if name == "brew" || name == "pip" || name == "pip3" || name == "cargo" {
		body = "/usr/bin/touch \"" + filepath.Join(dir, "invoked-"+name) + "\"\nexit 0\n"
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestIssue5231_PipBranchRemovedNoManagerGuidance(t *testing.T) {
	issue5231ResetState()

	emptyDir := t.TempDir()
	t.Setenv("PATH", emptyDir)

	logs := issue5231CaptureLogs(t, installRG)

	if rgTrying.Load() {
		t.Error("expected rgTrying to be reset after installRG")
	}
	issue5231AssertGuidance(t, logs)
}

func TestIssue5231_PipOnlyEnvNeverInvokesPip(t *testing.T) {
	issue5231ResetState()

	dir := t.TempDir()
	issue5231WriteShim(t, dir, "pip")
	issue5231WriteShim(t, dir, "pip3")
	t.Setenv("PATH", dir)

	logs := issue5231CaptureLogs(t, installRG)

	for _, mgr := range []string{"pip", "pip3"} {
		if _, err := os.Stat(filepath.Join(dir, "invoked-"+mgr)); err == nil {
			t.Errorf("%s must never be invoked by installRG (third-party PyPI ripgrep wrapper risk, issue #5231)", mgr)
		}
	}
	if rgTrying.Load() {
		t.Error("expected rgTrying to be reset after installRG")
	}
	issue5231AssertGuidance(t, logs)
}

func TestIssue5231_BrewStillUsedAsOfficialChannel(t *testing.T) {
	issue5231ResetState()

	dir := t.TempDir()
	issue5231WriteShim(t, dir, "brew")
	issue5231WriteShim(t, dir, "pip")
	issue5231WriteShim(t, dir, "pip3")
	t.Setenv("PATH", dir)

	installRG()

	if _, err := os.Stat(filepath.Join(dir, "invoked-brew")); err != nil {
		t.Fatal("expected brew install ripgrep to still be attempted (official channel)")
	}
	for _, mgr := range []string{"pip", "pip3"} {
		if _, err := os.Stat(filepath.Join(dir, "invoked-"+mgr)); err == nil {
			t.Errorf("%s must not be invoked when brew is available", mgr)
		}
	}
	if rgTrying.Load() {
		t.Error("expected rgTrying to be reset after installRG")
	}
}

func TestIssue5231_SourceAnchorNoPipInstallBranch(t *testing.T) {
	src, err := os.ReadFile("grep.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)
	for _, banned := range []string{
		"exec.Command(\"pip\"",
		"exec.Command(\"pip3\"",
		"\"pip\", \"install\"",
		"\"pip3\", \"install\"",
	} {
		if strings.Contains(s, banned) {
			t.Errorf("grep.go must not auto-install via pip (issue #5231): found %s", banned)
		}
	}
	for _, want := range []string{
		"BurntSushi/ripgrep/releases",
		"third-party wrapper",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("grep.go installRG guidance missing %q", want)
		}
	}
}
