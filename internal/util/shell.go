package util

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

type ShellSpec struct {
	Path string
	Args []string
	Name string
}

func NewShellCommand(command string) (*exec.Cmd, ShellSpec, error) {
	spec, err := DetectShell()
	if err != nil {
		return nil, ShellSpec{}, err
	}
	return exec.Command(spec.Path, append(spec.Args, command)...), spec, nil
}

func NewShellCommandContext(ctx context.Context, command string) (*exec.Cmd, ShellSpec, error) {
	spec, err := DetectShell()
	if err != nil {
		return nil, ShellSpec{}, err
	}
	return exec.CommandContext(ctx, spec.Path, append(spec.Args, command)...), spec, nil
}

func DetectShell() (ShellSpec, error) {
	return detectShell(runtime.GOOS, exec.LookPath, os.Stat, os.Getenv)
}

type lookPathFunc func(string) (string, error)
type statFunc func(string) (os.FileInfo, error)
type getenvFunc func(string) string

func detectShell(goos string, lookPath lookPathFunc, stat statFunc, getenv getenvFunc) (ShellSpec, error) {
	if goos != "windows" {
		return ShellSpec{Path: "sh", Args: []string{"-c"}, Name: "sh"}, nil
	}

	for _, candidate := range windowsGitBashCandidates(getenv) {
		if _, err := stat(candidate); err == nil {
			return ShellSpec{Path: candidate, Args: []string{"-lc"}, Name: "git-bash"}, nil
		}
	}

	for _, name := range []string{"bash.exe", "bash"} {
		if path, err := lookPath(name); err == nil {
			return ShellSpec{Path: path, Args: []string{"-lc"}, Name: "git-bash"}, nil
		}
	}

	for _, name := range []string{"pwsh.exe", "pwsh"} {
		if path, err := lookPath(name); err == nil {
			return ShellSpec{
				Path: path,
				Args: []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command"},
				Name: "powershell",
			}, nil
		}
	}

	for _, name := range []string{"powershell.exe", "powershell"} {
		if path, err := lookPath(name); err == nil {
			return ShellSpec{
				Path: path,
				Args: []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command"},
				Name: "powershell",
			}, nil
		}
	}

	return ShellSpec{}, fmt.Errorf("no supported shell found on Windows (expected Git Bash or PowerShell)")
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
