package memory

import (
	"fmt"
	"os"
	"path/filepath"
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

// LoadProjectMemory reads supported project bootstrap documents from the global
// config dir and the current working directory's ancestor chain.
// Merge order: global -> walked parents/current directory.
func LoadProjectMemory(workingDir string) (content string, files []string, err error) {
	absDir, err := filepath.Abs(workingDir)
	if err != nil {
		absDir = workingDir
	}
	paths := append(globalProjectMemoryFiles(), walkUpProjectMemory(absDir)...)
	return ReadProjectMemoryFiles(paths)
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

// ProjectMemoryFilesForPath returns the supported project memory files that
// apply to a specific path by walking that path's ancestor chain.
func ProjectMemoryFilesForPath(targetPath string) ([]string, error) {
	absPath, err := filepath.Abs(targetPath)
	if err != nil {
		absPath = targetPath
	}
	dir := absPath
	if info, err := os.Stat(absPath); err == nil {
		if !info.IsDir() {
			dir = filepath.Dir(absPath)
		}
	} else {
		dir = filepath.Dir(absPath)
	}
	return walkUpProjectMemory(dir), nil
}

// ReadProjectMemoryFiles reads project memory files in order and returns their
// merged content plus the subset that had non-empty readable contents.
func ReadProjectMemoryFiles(paths []string) (content string, files []string, err error) {
	seen := make(map[string]bool)
	for _, p := range paths {
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		data, err := readFileSafe(p)
		if err == nil && data != "" {
			content += data + "\n"
			files = append(files, p)
		}
	}
	return strings.TrimSpace(content), files, nil
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

func globalProjectMemoryFiles() []string {
	globalDir, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	var files []string
	for _, name := range ProjectMemoryFilenames {
		files = append(files, filepath.Join(globalDir, ".ggcode", name))
	}
	return files
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
