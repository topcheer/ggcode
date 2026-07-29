package agentruntime

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// overviewSkipDirs are directory names excluded from the project layout.
// Hidden entries (starting with ".") are always skipped as well.
var overviewSkipDirs = map[string]bool{
	"node_modules": true,
	"vendor":       true,
	"dist":         true,
	"build":        true,
	"out":          true,
	"target":       true,
	"__pycache__":  true,
	"coverage":     true,
	"bin":          true,
}

// overviewSkipFileExts are file extensions excluded from the root listing —
// build artifacts, media, and archives carry no structural signal.
var overviewSkipFileExts = map[string]bool{
	".exe": true, ".test": true, ".out": true,
	".jpg": true, ".jpeg": true, ".png": true, ".gif": true,
	".webp": true, ".bmp": true, ".ico": true, ".svg": true,
	".pdf": true, ".zip": true, ".gz": true, ".dmg": true,
	".msi": true, ".pkg": true, ".deb": true, ".rpm": true,
	".mp4": true, ".mov": true, ".pen": true, ".html": true,
}

const (
	// overviewMaxDepth limits how deep the layout walk descends (root = depth 0).
	overviewMaxDepth = 2
	// overviewMaxEntries caps the total number of lines in the layout so the
	// system prompt stays small even in huge repositories.
	overviewMaxEntries = 60
)

// BuildProjectOverview returns a compact, depth-limited directory layout of the
// workspace root for injection into the system prompt. It gives the model
// upfront codebase awareness (similar in spirit to Aider's repo map) without
// any tool calls. Returns an empty string if the directory cannot be read or
// contains nothing worth showing.
func BuildProjectOverview(root string) string {
	if _, err := os.ReadDir(root); err != nil {
		return ""
	}

	var sb strings.Builder
	remaining := overviewMaxEntries
	truncated := false

	var walk func(dir string, depth int)
	walk = func(dir string, depth int) {
		if remaining <= 0 {
			truncated = true
			return
		}
		items, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		names := make([]os.DirEntry, 0, len(items))
		for _, item := range items {
			name := item.Name()
			if strings.HasPrefix(name, ".") {
				continue
			}
			if item.IsDir() && overviewSkipDirs[name] {
				continue
			}
			// Files are only listed at the root (depth 0) — they are usually
			// the few important manifests (go.mod, package.json, README).
			// Deeper levels show directory names only, so the line budget
			// covers the whole directory tree instead of being consumed by
			// file listings in a single large subdirectory.
			if !item.IsDir() {
				if depth > 0 || overviewSkipFileExts[strings.ToLower(filepath.Ext(name))] {
					continue
				}
				// Skip extension-less executables (built binaries like
				// `ggcode`) but keep Makefile/LICENSE-style files.
				if filepath.Ext(name) == "" {
					if info, err := item.Info(); err == nil && info.Mode()&0o111 != 0 {
						continue
					}
				}
			}
			names = append(names, item)
		}
		sort.Slice(names, func(i, j int) bool {
			if names[i].IsDir() != names[j].IsDir() {
				// At the root, files come first: they are the few key
				// manifests (go.mod, README, Makefile) and would otherwise
				// be truncated away by subdirectory listings. Deeper levels
				// contain directories only (see filter above).
				if depth == 0 {
					return !names[i].IsDir()
				}
				return names[i].IsDir()
			}
			return names[i].Name() < names[j].Name()
		})
		for _, item := range names {
			if remaining <= 0 {
				truncated = true
				return
			}
			name := item.Name()
			if item.IsDir() {
				name += "/"
			}
			sb.WriteString(strings.Repeat("  ", depth) + name + "\n")
			remaining--
			if item.IsDir() && depth+1 < overviewMaxDepth {
				walk(filepath.Join(dir, item.Name()), depth+1)
			}
		}
	}
	walk(root, 0)

	out := strings.TrimRight(sb.String(), "\n")
	if out == "" {
		return ""
	}
	if truncated {
		out += "\n... (truncated; use glob/list_directory to explore deeper)"
	}
	return out
}

// projectOverviewSection wraps BuildProjectOverview output as a prompt section.
func projectOverviewSection(workingDir string) string {
	if strings.TrimSpace(workingDir) == "" {
		return ""
	}
	overview := BuildProjectOverview(workingDir)
	if overview == "" {
		return ""
	}
	return fmt.Sprintf("\n\n## Project layout\nWorkspace directory structure (depth-limited, noise directories omitted, files shown at root only). Use glob, search_files, or list_directory to explore deeper.\n```\n%s\n```", overview)
}
