package util

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

type ShellSpec struct {
	Path string
	Args []string
	Name string
}

var (
	shellMu       sync.Mutex
	shellCache    ShellSpec
	shellCacheErr error
	shellDetected bool
)

func NewShellCommand(command string) (*exec.Cmd, ShellSpec, error) {
	return NewShellCommandContext(context.Background(), command)
}

func NewShellCommandContext(ctx context.Context, command string) (*exec.Cmd, ShellSpec, error) {
	spec, err := DetectShell()
	if err != nil {
		return nil, ShellSpec{}, err
	}

	if spec.Name == "powershell" {
		command = "try{[Console]::OutputEncoding=[Text.Encoding]::UTF8;[Console]::InputEncoding=[Text.Encoding]::UTF8}catch{};" +
			"$OutputEncoding=[Text.Encoding]::UTF8;" +
			"$PSDefaultParameterValues['*:Encoding']='utf8';" +
			"$ErrorActionPreference='Continue'; $PSNativeCommandUseErrorActionPreference=$false; " + command
	}
	return exec.CommandContext(ctx, spec.Path, append(spec.Args, command)...), spec, nil
}

var debugLogFn func(category, format string, args ...interface{})

func SetDebugLogFn(fn func(category, format string, args ...interface{})) {
	debugLogFn = fn
}

func DetectShell() (ShellSpec, error) {
	shellMu.Lock()
	defer shellMu.Unlock()
	if !shellDetected {
		shellCache, shellCacheErr = detectShell(runtime.GOOS, exec.LookPath, os.Stat, os.Getenv)
		shellDetected = true
		if debugLogFn != nil {
			if shellCacheErr == nil {
				debugLogFn("shell", "detected shell: %s path=%s args=%v", shellCache.Name, shellCache.Path, shellCache.Args)
			} else {
				debugLogFn("shell", "shell detection failed: %v", shellCacheErr)
			}
		}
	}
	return shellCache, shellCacheErr
}

func ResetShellCache() {
	shellMu.Lock()
	defer shellMu.Unlock()
	shellDetected = false
	shellCache = ShellSpec{}
	shellCacheErr = nil
}

const ShellHealthProbeCommand = "echo ggcode-shell-health-probe-ok"

func ShellAvailable() bool {
	if _, err := DetectShell(); err != nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	probe, _, err := NewShellCommandContext(ctx, "true")
	if err != nil {
		return false
	}
	return probe.Run() == nil
}

type lookPathFunc func(string) (string, error)
type statFunc func(string) (os.FileInfo, error)
type getenvFunc func(string) string

func detectShell(goos string, lookPath lookPathFunc, stat statFunc, getenv getenvFunc) (ShellSpec, error) {
	if goos != "windows" {
		return ShellSpec{Path: "sh", Args: []string{"-c"}, Name: "sh"}, nil
	}

	for _, name := range []string{"pwsh.exe", "pwsh", "powershell.exe", "powershell"} {
		if path, err := lookPath(name); err == nil {
			return ShellSpec{
				Path: path,
				Args: []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command"},
				Name: "powershell",
			}, nil
		}
	}

	for _, candidate := range windowsGitBashCandidates(getenv) {
		if _, err := stat(candidate); err == nil {
			return ShellSpec{Path: candidate, Args: []string{"-c"}, Name: "git-bash"}, nil
		}
	}

	for _, name := range []string{"bash.exe", "bash"} {
		if path, err := lookPath(name); err == nil {

			if isWSLBashPath(path) {
				continue
			}
			return ShellSpec{Path: path, Args: []string{"-c"}, Name: "git-bash"}, nil
		}
	}

	return ShellSpec{}, fmt.Errorf("no supported shell found on Windows (expected PowerShell or Git Bash; note WSL bash is not a Windows shell)")
}

func isWSLBashPath(path string) bool {
	p := strings.ReplaceAll(strings.ToLower(path), "\\", "/")
	return strings.Contains(p, "/windows/system32/") || strings.HasSuffix(p, "/windows/system32/bash.exe")
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
