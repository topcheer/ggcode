package wailskit

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// FileInfo describes a file or directory entry.
type FileInfo struct {
	Name     string `json:"name"`
	IsDir    bool   `json:"isDir"`
	Size     int64  `json:"size"`
	Modified int64  `json:"modified"` // Unix timestamp
	Path     string `json:"path"`     // Full path
}

// ListDirectory returns entries in the given directory.
// If recursive is true, it walks subdirectories recursively.
func ListDirectory(dir string, recursive bool) ([]FileInfo, error) {
	if dir == "" {
		dir, _ = os.Getwd()
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("resolve path: %w", err)
	}

	// Security: verify the path exists and is a directory
	info, err := os.Stat(abs)
	if err != nil {
		return nil, fmt.Errorf("stat path: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%q is not a directory", abs)
	}

	var result []FileInfo

	if recursive {
		err = filepath.WalkDir(abs, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return nil // skip errors for individual entries
			}
			if path == abs {
				return nil // skip root
			}
			fi, fiErr := d.Info()
			if fiErr != nil {
				return nil
			}
			relPath, _ := filepath.Rel(abs, path)
			result = append(result, FileInfo{
				Name:     d.Name(),
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
	} else {
		entries, err := os.ReadDir(abs)
		if err != nil {
			return nil, fmt.Errorf("read directory: %w", err)
		}
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
	}

	return result, nil
}

// ReadFileContent reads a text file and returns its content.
// For security, it restricts file access to the working directory.
func ReadFileContent(path string) (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("get working directory: %w", err)
	}

	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve path: %w", err)
	}

	// Security: verify the resolved path is within the working directory.
	// filepath.Clean + Abs already resolved all ".." sequences, so checking
	// for ".." after Clean is useless. Instead, check that the resolved
	// path is within the working directory.
	rel, err := filepath.Rel(wd, abs)
	if err != nil {
		return "", fmt.Errorf("resolve relative path: %w", err)
	}
	if strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("access denied: path outside working directory")
	}

	data, err := os.ReadFile(abs)
	if err != nil {
		return "", fmt.Errorf("read file: %w", err)
	}
	return string(data), nil
}

// GetWorkingDir returns the current working directory.
func GetWorkingDir() string {
	wd, err := os.Getwd()
	if err != nil {
		return ""
	}
	return wd
}
