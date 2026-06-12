package util

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestDetectShellUsesShOnUnixLikePlatforms(t *testing.T) {
	spec, err := detectShell("darwin", failingLookPath, failingStat, func(string) string { return "" })
	if err != nil {
		t.Fatalf("detectShell() error = %v", err)
	}
	if spec.Path != "sh" {
		t.Fatalf("expected sh, got %q", spec.Path)
	}
	if len(spec.Args) != 1 || spec.Args[0] != "-c" {
		t.Fatalf("expected sh -c, got %v", spec.Args)
	}
}

func TestDetectShellPrefersGitBashOnWindows(t *testing.T) {
	root := t.TempDir()
	bashPath := filepath.Join(root, "Git", "bin", "bash.exe")
	if err := os.MkdirAll(filepath.Dir(bashPath), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(bashPath, []byte(""), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	spec, err := detectShell("windows", failingLookPath, os.Stat, func(key string) string {
		if key == "ProgramFiles" {
			return root
		}
		return ""
	})
	if err != nil {
		t.Fatalf("detectShell() error = %v", err)
	}
	if spec.Path != bashPath {
		t.Fatalf("expected Git Bash path %q, got %q", bashPath, spec.Path)
	}
	if len(spec.Args) != 1 || spec.Args[0] != "-lc" {
		t.Fatalf("expected bash -lc, got %v", spec.Args)
	}
}

func TestDetectShellFallsBackToPowerShellOnWindows(t *testing.T) {
	spec, err := detectShell("windows", func(name string) (string, error) {
		switch name {
		case "pwsh.exe":
			return `C:\Program Files\PowerShell\7\pwsh.exe`, nil
		default:
			return "", fmt.Errorf("missing %s", name)
		}
	}, failingStat, func(string) string { return "" })
	if err != nil {
		t.Fatalf("detectShell() error = %v", err)
	}
	if spec.Name != "powershell" {
		t.Fatalf("expected powershell fallback, got %q", spec.Name)
	}
	if len(spec.Args) == 0 || spec.Args[len(spec.Args)-1] != "-Command" {
		t.Fatalf("expected PowerShell -Command args, got %v", spec.Args)
	}
}

func TestDetectShellReturnsErrorWithoutWindowsShell(t *testing.T) {
	_, err := detectShell("windows", failingLookPath, failingStat, func(string) string { return "" })
	if err == nil {
		t.Fatal("expected error when no supported Windows shell exists")
	}
}

func failingLookPath(name string) (string, error) {
	return "", fmt.Errorf("missing %s", name)
}

func failingStat(string) (os.FileInfo, error) {
	return nil, os.ErrNotExist
}
