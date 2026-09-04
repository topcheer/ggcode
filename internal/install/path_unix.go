//go:build !windows

package install

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

const (
	pathMarkerStart = "# >>> ggcode PATH >>>"
	pathMarkerEnd   = "# <<< ggcode PATH <<<"
)

// EnsureOnPath appends dir to the user's PATH via a marked block in their
// shell profile (#1573-A). The python installer has always done this; the
// Go installer only printed a Note, so a fresh %USERPROFILE%\go\bin (or
// ~/go/bin) install left `ggcode` unusable until the user hand-edited
// their profile - the installer's core acceptance is "it works after
// install".
func EnsureOnPath(dir string) (bool, error) {
	changed := false
	for _, target := range pathProfileTargets() {
		before, err := os.ReadFile(target)
		if err != nil && !os.IsNotExist(err) {
			debug.Log("install", "path: reading %s: %v", target, err)
			continue
		}
		after, err := upsertPathBlock(string(before), dir)
		if err != nil {
			continue
		}
		if after != string(before) {
			if err := os.WriteFile(target, []byte(after), 0o644); err != nil {
				debug.Log("install", "path: writing %s: %v", target, err)
				continue
			}
			changed = true
		}
	}
	return changed, nil
}

// pathProfileTargets mirrors the python installer: preferred rc files for
// the current shell first, then any of the well-known profiles that exist.
func pathProfileTargets() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	shell := filepath.Base(os.Getenv("SHELL"))
	var preferred []string
	switch shell {
	case "zsh":
		preferred = []string{".zshrc", ".zprofile"}
	case "bash":
		preferred = []string{".bashrc", ".bash_profile"}
	}
	existing := []string{".zshrc", ".zprofile", ".bashrc", ".bash_profile", ".profile"}

	seen := map[string]bool{}
	var out []string
	add := func(name string) {
		if name == "" || seen[name] {
			return
		}
		p := filepath.Join(home, name)
		if _, err := os.Stat(p); err != nil {
			// Only skip non-preferred names that don't exist; preferred
			// ones are created on demand like the python version.
			isPreferred := false
			for _, pn := range preferred {
				if pn == name {
					isPreferred = true
				}
			}
			if !isPreferred {
				return
			}
		}
		seen[name] = true
		out = append(out, p)
	}
	for _, n := range preferred {
		add(n)
	}
	for _, n := range existing {
		add(n)
	}
	return out
}

var pathBlockPattern = regexp.MustCompile(`(?s)` + regexp.QuoteMeta(pathMarkerStart) + `.*?` + regexp.QuoteMeta(pathMarkerEnd) + `\n?`)

func upsertPathBlock(content, dir string) (string, error) {
	block := fmt.Sprintf("%s\nexport PATH=%q:$PATH\n%s\n", pathMarkerStart, dir, pathMarkerEnd)
	if pathBlockPattern.MatchString(content) {
		return pathBlockPattern.ReplaceAllString(content, block), nil
	}
	suffix := ""
	if content != "" && !strings.HasSuffix(content, "\n") {
		suffix = "\n"
	}
	return content + suffix + block, nil
}
