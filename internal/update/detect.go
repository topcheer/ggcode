package update

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

type OtherInstall struct {
	Path       string
	Version    string
	Source     string
	Privileged bool
}

type DualScopeInstall struct {
	UserPath    string
	MachinePath string
}

func DetectDualScopeWindows() *DualScopeInstall {
	if runtime.GOOS != "windows" {
		return nil
	}
	home, _ := os.UserHomeDir()
	localAppData := os.Getenv("LOCALAPPDATA")
	if localAppData == "" {
		localAppData = filepath.Join(home, "AppData", "Local")
	}
	userPath := filepath.Join(localAppData, "ggcode", "ggcode.exe")
	machinePath := filepath.Join(os.Getenv("ProgramFiles"), "ggcode", "ggcode.exe")

	userExists := fileExists(userPath)
	machineExists := fileExists(machinePath)

	if userExists && machineExists {
		return &DualScopeInstall{
			UserPath:    userPath,
			MachinePath: machinePath,
		}
	}
	return nil
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// resolvePath returns the fully-resolved form of path: EvalSymlinks when it
// succeeds (following symlink chains to the real file), falling back to Abs.
// #2806: on Linux os.Executable() (/proc/self/exe) returns the RESOLVED real
// path (e.g. the Cellar inner path) while the known-install candidates are
// symlink paths (linuxbrew bin/ggcode) - comparing bare Abs forms made the
// currently-running binary report itself as an "other install" with a
// misleading warning plus a wasted self-subprocess version probe. macOS
// KERN_PROCARGS keeps the launch symlink path, which masked the bug there.

func resolvePath(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		if abs, err := filepath.Abs(resolved); err == nil {
			return abs
		}
		return resolved
	}
	abs, _ := filepath.Abs(path)
	return abs
}

func FindOtherInstalls(currentPath string) []OtherInstall {
	var found []OtherInstall
	currentAbs := resolvePath(currentPath)

	seen := map[string]bool{}

	check := func(path, source string, privileged bool) {
		if path == "" {
			return
		}
		abs := resolvePath(path)
		if abs == currentAbs {
			return
		}
		if seen[abs] {
			return
		}
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			seen[abs] = true
			ver := tryGetVersion(path)
			found = append(found, OtherInstall{
				Path:       path,
				Version:    ver,
				Source:     source,
				Privileged: privileged,
			})
		}
	}

	home, _ := os.UserHomeDir()

	switch runtime.GOOS {
	case "darwin":

		check("/opt/homebrew/bin/ggcode", "brew", false)

		check("/usr/local/bin/ggcode", "brew", false)

		check("/opt/local/bin/ggcode", "macports", true)

		check(filepath.Join(home, ".local/bin/ggcode"), "npm", false)

		scanNpmDir(filepath.Join(home, ".local/share/ggcode/npm"), check)

		gopath := os.Getenv("GOPATH")
		if gopath == "" {
			gopath = filepath.Join(home, "go")
		}
		check(filepath.Join(gopath, "bin/ggcode"), "go", false)

		check("/usr/bin/ggcode", "system", true)

	case "linux":

		check("/home/linuxbrew/.linuxbrew/bin/ggcode", "brew", false)

		check(filepath.Join(home, ".local/bin/ggcode"), "npm", false)

		scanNpmDir(filepath.Join(home, ".local/share/ggcode/npm"), check)

		gopath := os.Getenv("GOPATH")
		if gopath == "" {
			gopath = filepath.Join(home, "go")
		}
		check(filepath.Join(gopath, "bin/ggcode"), "go", false)

		check("/usr/bin/ggcode", "system", true)
		check("/usr/local/bin/ggcode", "direct", false)

		check("/snap/bin/ggcode", "snap", true)

	case "windows":

		programFiles := os.Getenv("ProgramFiles")
		check(filepath.Join(programFiles, "ggcode", "ggcode.exe"), "winget-machine", true)
		programFiles86 := os.Getenv("ProgramFiles(x86)")
		check(filepath.Join(programFiles86, "ggcode", "ggcode.exe"), "winget-machine", true)

		localAppData := os.Getenv("LOCALAPPDATA")
		if localAppData == "" {
			localAppData = filepath.Join(home, "AppData", "Local")
		}
		check(filepath.Join(localAppData, "ggcode", "ggcode.exe"), "winget-user", false)

		check(filepath.Join(home, "scoop", "apps", "ggcode", "current", "ggcode.exe"), "scoop", false)
		check(filepath.Join(home, "scoop", "shims", "ggcode.exe"), "scoop", false)

		check(filepath.Join(home, ".local", "bin", "ggcode.exe"), "npm", false)

		scanNpmDirWindows(filepath.Join(home, ".local", "share", "ggcode", "npm"), check)

		gopath := os.Getenv("GOPATH")
		if gopath == "" {
			gopath = filepath.Join(home, "go")
		}
		check(filepath.Join(gopath, "bin", "ggcode.exe"), "go", false)
	}

	binaryName := "ggcode"
	if runtime.GOOS == "windows" {
		binaryName = "ggcode.exe"
	}
	if p, err := exec.LookPath(binaryName); err == nil {
		check(p, "path", false)
	}

	return found
}

func scanNpmDir(npmDir string, check func(string, string, bool)) {
	entries, err := os.ReadDir(npmDir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		binary := filepath.Join(npmDir, entry.Name(), runtime.GOOS+"_"+runtime.GOARCH, "ggcode")
		check(binary, "npm", false)
	}
}

func scanNpmDirWindows(npmDir string, check func(string, string, bool)) {
	entries, err := os.ReadDir(npmDir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		binary := filepath.Join(npmDir, entry.Name(), "windows_"+runtime.GOARCH, "ggcode.exe")
		check(binary, "npm", false)
	}
}

func tryGetVersion(binaryPath string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binaryPath, "version")
	cmd.Stderr = nil
	cmd.Stdin = strings.NewReader("")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func FormatOtherInstalls(installs []OtherInstall) string {
	if len(installs) == 0 {
		return ""
	}
	var lines []string
	for _, inst := range installs {
		ver := inst.Version
		if ver == "" {
			ver = "(unknown version)"
		}
		sourceLabel := inst.Source
		if inst.Privileged {
			sourceLabel += " (admin)"
		}
		lines = append(lines, fmt.Sprintf("  - %s [%s] at %s", ver, sourceLabel, inst.Path))
	}
	return strings.Join(lines, "\n")
}

func PackageManagerHint(execPath string) string {
	p := filepath.ToSlash(execPath)
	p = strings.ToLower(p)

	if strings.Contains(p, "/homebrew/cellar/") || strings.Contains(p, "/linuxbrew/.linuxbrew/cellar/") {
		return "brew"
	}

	if strings.Contains(p, "/homebrew/bin/ggcode") || strings.Contains(p, "/linuxbrew/.linuxbrew/bin/ggcode") {
		return "brew"
	}

	if strings.Contains(p, "/scoop/apps/") || strings.Contains(p, "/scoop/shims/") {
		return "scoop"
	}

	if strings.Contains(p, "c:/program files/ggcode") || strings.Contains(p, "c:\\program files\\ggcode") ||
		strings.Contains(p, "c:/program files (x86)/ggcode") {
		return "winget"
	}

	if strings.Contains(p, "/appdata/local/ggcode/") {
		return "winget"
	}

	if strings.Contains(p, "/snap/") {
		return "snap"
	}
	return ""
}
