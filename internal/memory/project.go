package memory

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// ProjectMemoryFilenames lists the supported project bootstrap documents in
// priority order.
var ProjectMemoryFilenames = []string{
	"GGCODE.md",
	"AGENTS.md",
	"CLAUDE.md",
	"COPILOT.md",
}

const DefaultProjectMemoryFilename = "GGCODE.md"

// LoadProjectMemory reads supported project bootstrap documents from global,
// project root, and subdirectories.
// Merge order: global -> walked parents -> nested subdirectories.
func LoadProjectMemory(workingDir string) (content string, files []string, err error) {
	seen := make(map[string]bool)

	globalDir, err := os.UserHomeDir()
	if err == nil {
		for _, name := range ProjectMemoryFilenames {
			globalPath := filepath.Join(globalDir, ".ggcode", name)
			if data, err := readFileSafe(globalPath); err == nil && data != "" {
				content += data + "\n"
				files = append(files, globalPath)
				seen[globalPath] = true
			}
		}
	}

	absDir, err := filepath.Abs(workingDir)
	if err != nil {
		absDir = workingDir
	}

	projectFiles := walkUpProjectMemory(absDir)
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

// walkUpProjectMemory walks from dir upward looking for supported project docs.
func walkUpProjectMemory(dir string) []string {
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
		var currentDir []string
		for _, name := range ProjectMemoryFilenames {
			p := filepath.Join(dir, name)
			if _, err := os.Stat(p); err == nil {
				currentDir = append(currentDir, p)
			}
		}
		found = append(currentDir, found...)
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return found
}

// scanSubdirs recursively scans for supported project docs in subdirectories.
func scanSubdirs(root string, exclude map[string]bool) []string {
	var found []string
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if !isProjectMemoryFile(d.Name()) || exclude[path] {
			return nil
		}
		found = append(found, path)
		return nil
	})
	slices.Sort(found)
	return found
}

func isProjectMemoryFile(name string) bool {
	for _, candidate := range ProjectMemoryFilenames {
		if name == candidate {
			return true
		}
	}
	return false
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

// ResolveProjectMemoryInitTarget returns the preferred directory and file path
// for creating a new project memory file from the current working directory.
func ResolveProjectMemoryInitTarget(workingDir string) (targetPath string, existingFiles []string, err error) {
	absDir, err := filepath.Abs(workingDir)
	if err != nil {
		absDir = workingDir
	}
	root := findProjectMemoryRoot(absDir)
	return filepath.Join(root, DefaultProjectMemoryFilename), listProjectMemoryFiles(root), nil
}

func findProjectMemoryRoot(dir string) string {
	current := dir
	for {
		if current == "" || current == string(filepath.Separator) {
			break
		}
		if hasProjectMemoryFiles(current) || hasGitRootMarker(current) {
			return current
		}
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		current = parent
	}
	return dir
}

func hasProjectMemoryFiles(dir string) bool {
	return len(listProjectMemoryFiles(dir)) > 0
}

func listProjectMemoryFiles(dir string) []string {
	var files []string
	for _, name := range ProjectMemoryFilenames {
		path := filepath.Join(dir, name)
		if _, err := os.Stat(path); err == nil {
			files = append(files, path)
		}
	}
	return files
}

func hasGitRootMarker(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil
}
