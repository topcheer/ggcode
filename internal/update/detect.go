package update

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// OtherInstall represents a ggcode binary found at a different location.
type OtherInstall struct {
	Path    string // absolute path to the binary
	Version string // version string if known, empty otherwise
	Source  string // "brew", "scoop", "winget", "npm", "pip", "direct"
}

// FindOtherInstalls scans known installation paths for ggcode binaries
// other than the one at currentPath. Returns a list of all found installs.
func FindOtherInstalls(currentPath string) []OtherInstall {
	var found []OtherInstall
	currentAbs, _ := filepath.Abs(currentPath)

	check := func(path, source string) {
		if path == "" {
			return
		}
		abs, _ := filepath.Abs(path)
		if abs == currentAbs {
			return // skip ourselves
		}
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			ver := tryGetVersion(path)
			found = append(found, OtherInstall{
				Path:    path,
				Version: ver,
				Source:  source,
			})
		}
	}

	switch runtime.GOOS {
	case "darwin", "linux":
		// Homebrew
		for _, brewBin := range []string{
			"/opt/homebrew/bin/ggcode",
			"/usr/local/bin/ggcode",
			"/home/linuxbrew/.linuxbrew/bin/ggcode",
		} {
			check(brewBin, "brew")
		}
		// npm wrapper
		home, _ := os.UserHomeDir()
		for _, npmBin := range []string{
			filepath.Join(home, ".local/bin/ggcode"),
			"/usr/local/bin/ggcode",
			filepath.Join(home, ".npm-global/bin/ggcode"),
		} {
			check(npmBin, "npm")
		}
		// npm versioned directory structure
		npmDir := filepath.Join(home, ".local/share/ggcode/npm")
		if entries, err := os.ReadDir(npmDir); err == nil {
			for _, entry := range entries {
				if !entry.IsDir() {
					continue
				}
				binary := filepath.Join(npmDir, entry.Name(), runtime.GOOS+"_"+runtime.GOARCH, "ggcode")
				check(binary, "npm")
			}
		}
		// Direct / go install / pip
		gopath := os.Getenv("GOPATH")
		if gopath == "" {
			gopath = filepath.Join(home, "go")
		}
		check(filepath.Join(gopath, "bin/ggcode"), "go")
		check("/usr/bin/ggcode", "system")

		// Also check which ggcode resolves to in PATH
		if p, err := exec.LookPath("ggcode"); err == nil {
			check(p, "PATH")
		}

	case "windows":
		home, _ := os.UserHomeDir()
		// winget/MSI
		check(filepath.Join(os.Getenv("ProgramFiles"), "ggcode", "ggcode.exe"), "winget")
		// scoop
		check(filepath.Join(home, "scoop", "apps", "ggcode", "current", "bin", "ggcode.exe"), "scoop")
		check(filepath.Join(home, "scoop", "shims", "ggcode.exe"), "scoop")
		// npm
		check(filepath.Join(home, ".local", "bin", "ggcode.exe"), "npm")
		// npm versioned directory
		npmDir := filepath.Join(home, ".local", "share", "ggcode", "npm")
		if entries, err := os.ReadDir(npmDir); err == nil {
			for _, entry := range entries {
				if !entry.IsDir() {
					continue
				}
				binary := filepath.Join(npmDir, entry.Name(), "windows_"+runtime.GOARCH, "ggcode.exe")
				check(binary, "npm")
			}
		}
		// Also check which ggcode resolves to in PATH
		if p, err := exec.LookPath("ggcode.exe"); err == nil {
			check(p, "PATH")
		}
	}

	return found
}

// tryGetVersion runs `ggcode version` on the binary and returns the version string.
func tryGetVersion(binaryPath string) string {
	cmd := exec.Command(binaryPath, "version")
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
		lines = append(lines, fmt.Sprintf("  - %s at %s", ver, inst.Path))
	}
	return strings.Join(lines, "\n")
}
