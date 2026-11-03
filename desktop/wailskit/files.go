package wailskit

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

const maxReadFileTextBytes = 20 * 1024 * 1024

const maxRecursiveEntries = 10000

type FileInfo struct {
	Name     string `json:"name"`
	IsDir    bool   `json:"isDir"`
	Size     int64  `json:"size"`
	Modified int64  `json:"modified"`
	Path     string `json:"path"`
}

func ListDirectory(dir string, recursive bool) ([]FileInfo, error) {
	if dir == "" {
		dir, _ = os.Getwd()
	}
	wd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("get working directory: %w", err)
	}
	abs, err := ResolveContainedPath(wd, dir)
	if err != nil {
		return nil, err
	}

	info, err := os.Stat(abs)
	if err != nil {
		return nil, fmt.Errorf("stat path: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%q is not a directory", abs)
	}

	if recursive {
		return walkDirectoryEntries(abs)
	}
	return readDirectoryEntries(abs)
}

func walkDirectoryEntries(abs string) ([]FileInfo, error) {
	var result []FileInfo
	err := filepath.WalkDir(abs, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			debug.Log("files", "walk error at %q: %v", path, err)
			return nil
		}
		if path == abs {
			return nil
		}

		name := d.Name()
		if name == "node_modules" || name == ".git" || name == ".vscode" || name == ".idea" {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		fi, fiErr := d.Info()
		if fiErr != nil {
			debug.Log("files", "stat error at %q: %v", path, fiErr)
			return nil
		}
		if len(result) >= maxRecursiveEntries {

			debug.Log("files", "recursive walk truncated at %d entries", maxRecursiveEntries)
			return filepath.SkipAll
		}
		relPath, _ := filepath.Rel(abs, path)
		result = append(result, FileInfo{
			Name:     name,
			IsDir:    d.IsDir(),
			Size:     fi.Size(),
			Modified: fi.ModTime().Unix(),
			Path:     relPath,
		})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk directory: %w", err)
	}
	return result, nil
}

func readDirectoryEntries(abs string) ([]FileInfo, error) {
	entries, err := os.ReadDir(abs)
	if err != nil {
		return nil, fmt.Errorf("read directory: %w", err)
	}
	var result []FileInfo
	for _, e := range entries {
		fi, fiErr := e.Info()
		if fiErr != nil {
			continue
		}
		result = append(result, FileInfo{
			Name:     e.Name(),
			IsDir:    e.IsDir(),
			Size:     fi.Size(),
			Modified: fi.ModTime().Unix(),
			Path:     e.Name(),
		})
	}
	return result, nil
}

const MaxReadFileTextBytes = maxReadFileTextBytes

func ResolveContainedPath(root, path string) (string, error) {
	if root == "" {
		var err error
		root, err = os.Getwd()
		if err != nil {
			return "", fmt.Errorf("get working directory: %w", err)
		}
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve root: %w", err)
	}

	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("resolve root: %w", err)
	}
	if !filepath.IsAbs(path) {

		path = filepath.Join(root, path)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve path: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("resolve path: %w", err)
	}
	rel, err := filepath.Rel(root, resolved)
	if err != nil {
		return "", fmt.Errorf("resolve relative path: %w", err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("access denied: path outside working directory")
	}
	return resolved, nil
}

func ReadFileContent(path string) (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("get working directory: %w", err)
	}
	resolved, err := ResolveContainedPath(wd, path)
	if err != nil {
		return "", err
	}

	info, err := os.Stat(resolved)
	if err != nil {
		return "", fmt.Errorf("stat file: %w", err)
	}
	if info.Size() > maxReadFileTextBytes {
		return "", fmt.Errorf("file too large to preview: %s is %d bytes (limit 20MB)", resolved, info.Size())
	}

	data, err := os.ReadFile(resolved)
	if err != nil {
		return "", fmt.Errorf("read file: %w", err)
	}
	return string(data), nil
}

func GetWorkingDir() string {
	wd, err := os.Getwd()
	if err != nil {
		return ""
	}
	return wd
}
