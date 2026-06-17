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

// OtherInstall represents a ggcode binary found at a different location.
type OtherInstall struct {
	Path       string // absolute path to the binary
	Version    string // version string if known, empty otherwise
	Source     string // "brew", "scoop", "winget", "winget-user", "npm", "pip", "go", "system", "path"
	Privileged bool   // true if installed in a system-wide location requiring admin/root
}

// FindOtherInstalls scans known installation paths for ggcode binaries
// other than the one at currentPath. Returns a list of all found installs.
func FindOtherInstalls(currentPath string) []OtherInstall {
	var found []OtherInstall
	currentAbs, _ := filepath.Abs(currentPath)

	// dedup by resolved path
	seen := map[string]bool{}

	check := func(path, source string, privileged bool) {
		if path == "" {
			return
		}
		abs, err := filepath.Abs(path)
		if err != nil {
			return
		}
		if abs == currentAbs {
			return // skip ourselves
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
		// Homebrew Apple Silicon
		check("/opt/homebrew/bin/ggcode", "brew", false)
		// Homebrew Intel
		check("/usr/local/bin/ggcode", "brew", false)
		// MacPorts
		check("/opt/local/bin/ggcode", "macports", true)
		// npm/pip wrapper (user-space)
		check(filepath.Join(home, ".local/bin/ggcode"), "npm", false)
		// npm versioned dir
		scanNpmDir(filepath.Join(home, ".local/share/ggcode/npm"), check)
		// go install
		gopath := os.Getenv("GOPATH")
		if gopath == "" {
			gopath = filepath.Join(home, "go")
		}
		check(filepath.Join(gopath, "bin/ggcode"), "go", false)
		// system
		check("/usr/bin/ggcode", "system", true)

	case "linux":
		// Homebrew Linux
		check("/home/linuxbrew/.linuxbrew/bin/ggcode", "brew", false)
		// npm/pip wrapper (user-space)
		check(filepath.Join(home, ".local/bin/ggcode"), "npm", false)
		// npm versioned dir
		scanNpmDir(filepath.Join(home, ".local/share/ggcode/npm"), check)
		// go install
		gopath := os.Getenv("GOPATH")
		if gopath == "" {
			gopath = filepath.Join(home, "go")
		}
		check(filepath.Join(gopath, "bin/ggcode"), "go", false)
		// system (deb/rpm/apk/ipk)
		check("/usr/bin/ggcode", "system", true)
		check("/usr/local/bin/ggcode", "direct", false)
		// Snap
		check("/snap/bin/ggcode", "snap", true)

	case "windows":
		// winget/MSI perMachine (Program Files — needs admin)
		programFiles := os.Getenv("ProgramFiles")
		check(filepath.Join(programFiles, "ggcode", "ggcode.exe"), "winget", true)
		programFiles86 := os.Getenv("ProgramFiles(x86)")
		check(filepath.Join(programFiles86, "ggcode", "ggcode.exe"), "winget", true)
		// winget/MSI perUser (LocalAppData — no admin)
		localAppData := os.Getenv("LOCALAPPDATA")
		if localAppData == "" {
			localAppData = filepath.Join(home, "AppData", "Local")
		}
		check(filepath.Join(localAppData, "ggcode", "ggcode.exe"), "winget-user", false)
		// Scoop
		check(filepath.Join(home, "scoop", "apps", "ggcode", "current", "ggcode.exe"), "scoop", false)
		check(filepath.Join(home, "scoop", "shims", "ggcode.exe"), "scoop", false)
		// npm/pip wrapper (user-space)
		check(filepath.Join(home, ".local", "bin", "ggcode.exe"), "npm", false)
		// npm versioned dir
		scanNpmDirWindows(filepath.Join(home, ".local", "share", "ggcode", "npm"), check)
		// go install
		gopath := os.Getenv("GOPATH")
		if gopath == "" {
			gopath = filepath.Join(home, "go")
		}
		check(filepath.Join(gopath, "bin", "ggcode.exe"), "go", false)
	}

	// Always check what PATH resolves to
	binaryName := "ggcode"
	if runtime.GOOS == "windows" {
		binaryName = "ggcode.exe"
	}
	if p, err := exec.LookPath(binaryName); err == nil {
		check(p, "path", false)
	}

	return found
}

// scanNpmDir scans npm versioned binary directories (macOS/Linux).
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

// scanNpmDirWindows scans npm versioned binary directories on Windows.
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

// tryGetVersion runs `ggcode version` on the binary and returns the version string.
func tryGetVersion(binaryPath string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binaryPath, "version")
	cmd.Stderr = nil
	cmd.Stdin = nil
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// FormatOtherInstalls returns a human-readable summary of other installs found.
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

// PackageManagerHint returns the package manager that likely installed the
// binary at execPath, or empty string if unknown.
func PackageManagerHint(execPath string) string {
	p := filepath.ToSlash(execPath)
	p = strings.ToLower(p)

	// Homebrew
	if strings.Contains(p, "/homebrew/cellar/") || strings.Contains(p, "/linuxbrew/.linuxbrew/cellar/") {
		return "brew"
	}
	// Homebrew symlink (not in Cellar path but in /opt/homebrew/bin)
	if strings.Contains(p, "/homebrew/bin/ggcode") || strings.Contains(p, "/linuxbrew/.linuxbrew/bin/ggcode") {
		return "brew"
	}
	// Scoop
	if strings.Contains(p, "/scoop/apps/") || strings.Contains(p, "/scoop/shims/") {
		return "scoop"
	}
	// winget perMachine (Program Files — needs admin)
	if strings.Contains(p, "c:/program files/ggcode") || strings.Contains(p, "c:\\program files\\ggcode") ||
		strings.Contains(p, "c:/program files (x86)/ggcode") {
		return "winget"
	}
	// winget perUser (LocalAppData — default, no admin needed)
	// Still show hint so winget can track the version
	if strings.Contains(p, "/appdata/local/ggcode/") {
		return "winget"
	}
	// Snap
	if strings.Contains(p, "/snap/") {
		return "snap"
	}
	return ""
}
