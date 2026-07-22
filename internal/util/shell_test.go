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

func TestDetectShellPrefersPowerShellOnWindows(t *testing.T) {
	// PowerShell should be the first choice on Windows.
	spec, err := detectShell("windows", func(name string) (string, error) {
		switch name {
		case "powershell.exe":
			return `C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe`, nil
		default:
			return "", fmt.Errorf("missing %s", name)
		}
	}, failingStat, func(string) string { return "" })
	if err != nil {
		t.Fatalf("detectShell() error = %v", err)
	}
	if spec.Name != "powershell" {
		t.Fatalf("expected powershell as primary, got %q", spec.Name)
	}
	if len(spec.Args) == 0 || spec.Args[len(spec.Args)-1] != "-Command" {
		t.Fatalf("expected PowerShell -Command args, got %v", spec.Args)
	}
}

func TestDetectShellPrefersPwshOverPowerShell(t *testing.T) {
	// pwsh (PowerShell Core) should be preferred over powershell.exe.
	spec, err := detectShell("windows", func(name string) (string, error) {
		switch name {
		case "pwsh.exe", "powershell.exe":
			return fmt.Sprintf(`C:\%s`, name), nil
		default:
			return "", fmt.Errorf("missing %s", name)
		}
	}, failingStat, func(string) string { return "" })
	if err != nil {
		t.Fatalf("detectShell() error = %v", err)
	}
	if spec.Path != `C:\pwsh.exe` {
		t.Fatalf("expected pwsh.exe first, got %q", spec.Path)
	}
}

func TestDetectShellFallsBackToGitBashOnWindows(t *testing.T) {
	// When no PowerShell is available, fall back to Git Bash.
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
		t.Fatalf("expected Git Bash fallback path %q, got %q", bashPath, spec.Path)
	}
	if len(spec.Args) != 1 || spec.Args[0] != "-c" {
		t.Fatalf("expected bash -c, got %v", spec.Args)
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
