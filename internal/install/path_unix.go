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

	if !changed && attempted > 0 && firstFailure != nil {
		return false, firstFailure
	}
	return changed, firstFailure
}

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

func upsertPathBlockFor(target, content, dir string) (string, error) {
	if filepath.Base(target) == "config.fish" {
		block := fmt.Sprintf("%s\nfish_add_path %q\n%s\n", pathMarkerStart, dir, pathMarkerEnd)
		return upsertBlockWith(content, block)
	}
	return upsertPathBlock(content, dir)
}

func upsertBlockWith(content, block string) (string, error) {
	if pathBlockPattern.MatchString(content) {

		return pathBlockPattern.ReplaceAllLiteralString(content, block), nil
	}
	suffix := ""
	if content != "" && !strings.HasSuffix(content, "\n") {
		suffix = "\n"
	}
	return content + suffix + block, nil
}
