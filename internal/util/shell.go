package util

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

type ShellSpec struct {
	Path string
	Args []string
	Name string
}

// shellCache caches the detected shell to avoid repeated filesystem probes
// (exec.LookPath / os.Stat) on every command execution. On Windows with a
// long PATH (WSL + Git Bash + tools), each LookPath call can take 100-500ms.
var (
	shellCache     ShellSpec
	shellCacheErr  error
	shellCacheOnce sync.Once
)

func NewShellCommand(command string) (*exec.Cmd, ShellSpec, error) {
	return NewShellCommandContext(context.Background(), command)
}

func NewShellCommandContext(ctx context.Context, command string) (*exec.Cmd, ShellSpec, error) {
	spec, err := DetectShell()
	if err != nil {
		return nil, ShellSpec{}, err
	}
	return exec.CommandContext(ctx, spec.Path, append(spec.Args, command)...), spec, nil
}

func DetectShell() (ShellSpec, error) {
	shellCacheOnce.Do(func() {
		shellCache, shellCacheErr = detectShell(runtime.GOOS, exec.LookPath, os.Stat, os.Getenv)
		if shellCacheErr == nil {
			log.Printf("[shell] detected shell: %s path=%s args=%v", shellCache.Name, shellCache.Path, shellCache.Args)
		} else {
			log.Printf("[shell] shell detection failed: %v", shellCacheErr)
		}
	})
	return shellCache, shellCacheErr
}

type lookPathFunc func(string) (string, error)
type statFunc func(string) (os.FileInfo, error)
type getenvFunc func(string) string

func detectShell(goos string, lookPath lookPathFunc, stat statFunc, getenv getenvFunc) (ShellSpec, error) {
	if goos != "windows" {
		return ShellSpec{Path: "sh", Args: []string{"-c"}, Name: "sh"}, nil
	}

	// PowerShell is the primary shell on Windows. Prefer pwsh (PowerShell
	// Core / cross-platform) over powershell.exe (Windows PowerShell).
	for _, name := range []string{"pwsh.exe", "pwsh", "powershell.exe", "powershell"} {
		if path, err := lookPath(name); err == nil {
			return ShellSpec{
				Path: path,
				Args: []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command"},
				Name: "powershell",
			}, nil
		}
	}

	// Fall back to Git Bash if PowerShell is not available.
	for _, candidate := range windowsGitBashCandidates(getenv) {
		if _, err := stat(candidate); err == nil {
			return ShellSpec{Path: candidate, Args: []string{"-c"}, Name: "git-bash"}, nil
		}
	}

	for _, name := range []string{"bash.exe", "bash"} {
		if path, err := lookPath(name); err == nil {
			return ShellSpec{Path: path, Args: []string{"-c"}, Name: "git-bash"}, nil
		}
	}

	return ShellSpec{}, fmt.Errorf("no supported shell found on Windows (expected PowerShell or Git Bash)")
}

func windowsGitBashCandidates(getenv getenvFunc) []string {
	var candidates []string
	add := func(root string, elems ...string) {
		root = strings.TrimSpace(root)
		if root == "" {
			return
		}
		candidates = append(candidates,
			filepath.Join(append([]string{root}, elems...)...),
		)
	}

	for _, root := range []string{
		getenv("ProgramW6432"),
		getenv("ProgramFiles"),
		getenv("ProgramFiles(x86)"),
		getenv("LocalAppData"),
	} {
		add(root, "Git", "bin", "bash.exe")
		add(root, "Git", "usr", "bin", "bash.exe")
		add(root, "Programs", "Git", "bin", "bash.exe")
		add(root, "Programs", "Git", "usr", "bin", "bash.exe")
	}

	seen := make(map[string]struct{}, len(candidates))
	out := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		if _, ok := seen[candidate]; ok {
			continue
		}
		seen[candidate] = struct{}{}
		out = append(out, candidate)
	}
	return out
}
