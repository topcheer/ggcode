package memory

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// GGCODEFilename is the project memory filename.
const GGCODEFilename = "GGCODE.md"

// LoadProjectMemory reads GGCODE.md files from global, project root, and subdirectories.
// Merge order: global → project root → subdirectories (latter overrides former).
func LoadProjectMemory(workingDir string) (content string, files []string, err error) {
	seen := make(map[string]bool)

	// 1. Global: ~/.ggcode/GGCODE.md
	globalDir, err := os.UserHomeDir()
	if err == nil {
		globalPath := filepath.Join(globalDir, ".ggcode", GGCODEFilename)
		if data, err := readFileSafe(globalPath); err == nil && data != "" {
			content += data + "\n"
			files = append(files, globalPath)
			seen[globalPath] = true
		}
	}

	// 2. Walk up from workingDir to find project root GGCODE.md
	absDir, err := filepath.Abs(workingDir)
	if err != nil {
		absDir = workingDir
	}
	projectFiles := walkUpGGCODE(absDir)
	for _, p := range projectFiles {
		if seen[p] {
			continue
		}
		data, err := readFileSafe(p)
		if err == nil && data != "" {
			content += data + "\n"
			files = append(files, p)
			seen[p] = true
		}
	}

	// 3. Recursively scan subdirectories for nested GGCODE.md
	subFiles := scanSubdirs(absDir, seen)
	for _, p := range subFiles {
		data, err := readFileSafe(p)
		if err == nil && data != "" {
			content += data + "\n"
			files = append(files, p)
		}
	}

	return strings.TrimSpace(content), files, nil
}

// walkUpGGCODE walks from dir upward looking for GGCODE.md files.
func walkUpGGCODE(dir string) []string {
	var found []string
	visited := make(map[string]bool)
	for {
		if dir == "" || dir == "/" {
			break
		}
		if visited[dir] {
			break
		}
		visited[dir] = true
		p := filepath.Join(dir, GGCODEFilename)
		if _, err := os.Stat(p); err == nil {
			found = append([]string{p}, found...) // prepend so root comes first
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return found
}

// scanSubdirs recursively scans for GGCODE.md in subdirectories.
func scanSubdirs(root string, exclude map[string]bool) []string {
	var found []string
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if d.Name() == GGCODEFilename && !exclude[path] {
			found = append(found, path)
		}
		return nil
	})
	return found
}

func readFileSafe(p string) (string, error) {
	data, err := os.ReadFile(p)
	if err != nil {
		return "", err
	}
	if len(data) == 0 {
		return "", fmt.Errorf("empty file")
	}
	return string(data), nil
}
