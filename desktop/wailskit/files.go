package wailskit

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// maxReadFileTextBytes caps ReadFileContent so a huge text file cannot be
// fully loaded into memory and shipped across the Wails bridge (#287).
// Text previews render the whole string, so this is lower than the 150MB
// binary (base64) preview cap from #253.
const maxReadFileTextBytes = 20 * 1024 * 1024 // 20MB

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
	wd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("get working directory: %w", err)
	}
	abs, err := ResolveContainedPath(wd, dir)
	if err != nil {
		return nil, err
	}

	// Security: verify the path exists and is a directory
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

// walkDirectoryEntries walks dir recursively and returns one FileInfo per
// entry below the root (the root itself is skipped), with paths relative to dir.
func walkDirectoryEntries(abs string) ([]FileInfo, error) {
	var result []FileInfo
	err := filepath.WalkDir(abs, func(path string, d os.DirEntry, err error) error {
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
	return result, nil
}

// readDirectoryEntries lists the immediate children of dir (non-recursive).
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

// MaxReadFileTextBytes exposes the text preview size cap so callers that
// share this package's containment logic (e.g. the Wails App file APIs) can
// enforce the same limit (#329).
const MaxReadFileTextBytes = maxReadFileTextBytes

// ResolveContainedPath resolves path (relative to root when applicable),
// follows symlinks, and verifies the fully-resolved path stays within root.
// It returns the resolved absolute path on success.
//
// Security notes (#146/#285/#329):
//   - filepath.Abs only lexically cleans the path — it does NOT resolve
//     symlinks. A symlink located inside root but pointing outside would
//     pass a naive containment check while os.ReadFile follows it to an
//     arbitrary target. Symlinks are resolved FIRST, then containment is
//     verified against the resolved path.
//   - ".." only escapes root when it is a complete path element; a leading
//     ".." prefix must not falsely deny names like "..cfg" (#285).
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
	// Resolve symlinks in the root itself too: on macOS t.TempDir()/TMPDIR
	// lives under /var which is itself a symlink to /private/var, so without
	// this the resolved target would never "be inside" the unresolved root.
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("resolve root: %w", err)
	}
	if !filepath.IsAbs(path) {
		// Relative paths are anchored at the containment root, not the
		// process working directory.
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

// ReadFileContent reads a text file and returns its content.
// For security, it restricts file access to the working directory.
func ReadFileContent(path string) (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("get working directory: %w", err)
	}
	resolved, err := ResolveContainedPath(wd, path)
	if err != nil {
		return "", err
	}

	// #287: cap text preview size BEFORE reading. os.ReadFile loads the
	// whole file into memory and ships it across the Wails bridge; without
	// this check, clicking a multi-GB log file in the browser would hang the
	// backend. 20MB is a deliberately lower cap than the 150MB binary preview
	// limit (#253) because text content is passed as an unencoded string and
	// is always rendered, not just offered for download.
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

// GetWorkingDir returns the current working directory.
func GetWorkingDir() string {
	wd, err := os.Getwd()
	if err != nil {
		return ""
	}
	return wd
}
