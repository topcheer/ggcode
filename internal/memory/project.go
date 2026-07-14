package memory

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/topcheer/ggcode/internal/config"
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

// LoadProjectMemory reads supported project bootstrap documents from the
// global config dir (~/.ggcode/) and the current working directory only.
// No parent directory traversal is performed — this prevents loading memory
// from unrelated ancestor workspaces (e.g. modelmeta loading ggcode's memory
// just because they share a parent directory).
func LoadProjectMemory(workingDir string) (content string, files []string, err error) {
	absDir, err := filepath.Abs(workingDir)
	if err != nil {
		absDir = workingDir
	}
	paths := append(globalProjectMemoryFiles(), currentDirProjectMemoryFiles(absDir)...)
	return ReadProjectMemoryFiles(paths)
}

// currentDirProjectMemoryFiles returns project memory files that exist in the
// given directory itself (no parent walk).
func currentDirProjectMemoryFiles(dir string) []string {
	return listProjectMemoryFiles(dir)
}

// ProjectMemoryFilesForPath returns the supported project memory files that
// exist in the directory containing the given path (no parent walk).
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
	return listProjectMemoryFiles(dir), nil
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

// ResolveProjectMemoryInitTarget returns the preferred file path for creating
// a new project memory file in the current working directory.
func ResolveProjectMemoryInitTarget(workingDir string) (targetPath string, existingFiles []string, err error) {
	absDir, err := filepath.Abs(workingDir)
	if err != nil {
		absDir = workingDir
	}
	return filepath.Join(absDir, DefaultProjectMemoryFilename), listProjectMemoryFiles(absDir), nil
}

func globalProjectMemoryFiles() []string {
	globalDir := config.HomeDir()
	var files []string
	for _, name := range ProjectMemoryFilenames {
		files = append(files, filepath.Join(globalDir, ".ggcode", name))
	}
	return files
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
