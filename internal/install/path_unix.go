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
	var firstFailure error
	attempted := 0
	for _, target := range pathProfileTargets() {
		before, err := os.ReadFile(target)
		if err != nil && !os.IsNotExist(err) {
			debug.Log("install", "path: reading %s: %v", target, err)
			if firstFailure == nil {
				firstFailure = fmt.Errorf("reading %s: %w", target, err)
			}
			continue
		}
		after, err := upsertPathBlockFor(target, string(before), dir)
		if err != nil {
			continue
		}
		if after != string(before) {
			attempted++
			// #1648-1: preferred-on-demand targets may live in a directory
			// that doesn't exist yet (~/.config/fish/ on a fresh fish box).
			if d := filepath.Dir(target); d != "." && d != "" {
				_ = os.MkdirAll(d, 0o755)
			}
			if err := os.WriteFile(target, []byte(after), 0o644); err != nil {
				debug.Log("install", "path: writing %s: %v", target, err)
				if firstFailure == nil {
					firstFailure = fmt.Errorf("writing %s: %w", target, err)
				}
				continue
			}
			changed = true
		}
	}
	// #1606-A: swallowing every failure and returning nil made the caller
	// promise "available in new terminals" with ZERO bytes written to ANY
	// profile (root-owned rc, read-only home, full disk) - the python
	// reference fails loudly here. If a write was needed and none landed,
	// surface the first failure.
	if !changed && attempted > 0 && firstFailure != nil {
		return false, firstFailure
	}
	return changed, firstFailure
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
	case "fish":
		// #1648-1: fish sources ONLY ~/.config/fish/config.fish - the POSIX
		// fallback wrote rc files fish never reads, and with none existing
		// the installer returned (false, nil) yet still promised "will be
		// available in new terminals" with zero bytes written.
		preferred = []string{".config/fish/config.fish"}
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
	return upsertBlockWith(content, block)
}

// upsertPathBlockFor picks fish syntax for config.fish targets (#1648-1):
// fish does not read POSIX rc files and cannot parse `export PATH=...`.
func upsertPathBlockFor(target, content, dir string) (string, error) {
	if filepath.Base(target) == "config.fish" {
		block := fmt.Sprintf("%s\nfish_add_path %q\n%s\n", pathMarkerStart, dir, pathMarkerEnd)
		return upsertBlockWith(content, block)
	}
	return upsertPathBlock(content, dir)
}

func upsertBlockWith(content, block string) (string, error) {
	if pathBlockPattern.MatchString(content) {
		// #1827 case 1: ReplaceAllString would interpret $PATH (and any
		// other $-prefixed text) in the block as capture-group references;
		// the pattern has no groups, so the marker-exists branch of a
		// re-run (version upgrade - the normal case) expanded $PATH to an
		// empty string and truncated the user's shell PATH to a single
		// directory. The replacement is a literal block, never a template.
		return pathBlockPattern.ReplaceAllLiteralString(content, block), nil
	}
	suffix := ""
	if content != "" && !strings.HasSuffix(content, "\n") {
		suffix = "\n"
	}
	return content + suffix + block, nil
}
