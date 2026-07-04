package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

// postEditVerifyState tracks file edits to inject periodic verification hints.
// This implements the "generate-verify-fix loop" pattern from the Iterative
// Refinement Loop research: after editing source code, prompt the agent to
// verify (build/test) before making more changes.
//
// The hint fires once every postEditVerifyInterval source-code edits, not
// after every single edit, to avoid noise. Non-source files (docs, JSON,
// markdown) don't count toward the threshold.
type postEditVerifyState struct {
	sourceEditsSinceHint int    // consecutive source-code edits since last hint
	buildCmd             string // cached build command (detected lazily, empty = not yet checked)
	buildCmdChecked      bool   // whether we've attempted detection
}

// postEditVerifyInterval is how many source-code edits between hints.
const postEditVerifyInterval = 3

// fileEditingTools is the set of tool names that modify files on disk.
var fileEditingTools = map[string]bool{
	"edit_file":        true,
	"write_file":       true,
	"multi_edit_file":  true,
	"multi_file_edit":  true,
	"multi_file_write": true,
}

// sourceCodeExtensions maps file extensions to whether they're compiled/interpreted code.
var sourceCodeExtensions = map[string]bool{
	".go":    true,
	".rs":    true,
	".ts":    true,
	".tsx":   true,
	".js":    true,
	".jsx":   true,
	".py":    true,
	".java":  true,
	".kt":    true,
	".c":     true,
	".cpp":   true,
	".cc":    true,
	".h":     true,
	".hpp":   true,
	".swift": true,
	".rb":    true,
	".php":   true,
	".cs":    true,
	".scala": true,
	".dart":  true,
	".zig":   true,
	".lua":   true,
	".sh":    true,
	".bash":  true,
	".zsh":   true,
}

// detectBuildSystem checks the working directory for build system markers
// and returns the appropriate verify command, or "" if none found.
func detectBuildSystem(workingDir string) string {
	if workingDir == "" {
		return ""
	}

	// Check for Go module first (most relevant for this project).
	if fileExists(filepath.Join(workingDir, "go.mod")) {
		return "go build ./..."
	}

	// Makefile covers many projects (C/C++, Go, etc.).
	if fileExists(filepath.Join(workingDir, "Makefile")) ||
		fileExists(filepath.Join(workingDir, "makefile")) ||
		fileExists(filepath.Join(workingDir, "GNUmakefile")) {
		return "make"
	}

	// Rust.
	if fileExists(filepath.Join(workingDir, "Cargo.toml")) {
		return "cargo build"
	}

	// Node.js.
	if fileExists(filepath.Join(workingDir, "package.json")) {
		return "npm run build"
	}

	// CMake.
	if fileExists(filepath.Join(workingDir, "CMakeLists.txt")) {
		return "cmake --build build"
	}

	// Python (test rather than build — Python isn't compiled).
	if fileExists(filepath.Join(workingDir, "pyproject.toml")) ||
		fileExists(filepath.Join(workingDir, "setup.py")) {
		return "python -m pytest"
	}

	return ""
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// extractFilePathFromArgs parses tool call arguments to extract the edited file path.
// Different tools use different JSON field names for the path.
func extractFilePathFromArgs(toolName string, args json.RawMessage) string {
	if len(args) == 0 {
		return ""
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(args, &raw); err != nil {
		return ""
	}

	// Try common field names: "file_path" (edit_file), "path" (write_file).
	for _, field := range []string{"file_path", "path"} {
		if v, ok := raw[field]; ok {
			var s string
			if json.Unmarshal(v, &s) == nil && s != "" {
				return s
			}
		}
	}

	// For multi-file tools, check "files" array for the first path.
	if filesRaw, ok := raw["files"]; ok {
		var files []map[string]json.RawMessage
		if json.Unmarshal(filesRaw, &files) == nil {
			for _, f := range files {
				for _, field := range []string{"file_path", "path"} {
					if v, ok := f[field]; ok {
						var s string
						if json.Unmarshal(v, &s) == nil && s != "" {
							return s
						}
					}
				}
			}
		}
	}

	return ""
}

// isSourceCodeFile returns true if the path has a source-code extension.
func isSourceCodeFile(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return sourceCodeExtensions[ext]
}

// postEditVerifyHint checks if we should inject a verification hint after
// a successful file edit. Returns the hint text, or "" if no hint needed.
//
// Thread-safety: caller must NOT hold a.mu (this method acquires it).
func (a *Agent) postEditVerifyHint(toolName string, args json.RawMessage) string {
	if !fileEditingTools[toolName] {
		return ""
	}

	filePath := extractFilePathFromArgs(toolName, args)
	if filePath == "" || !isSourceCodeFile(filePath) {
		return ""
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	a.postEditVerify.sourceEditsSinceHint++

	if a.postEditVerify.sourceEditsSinceHint < postEditVerifyInterval {
		return ""
	}

	// Detect build system lazily and cache.
	if !a.postEditVerify.buildCmdChecked {
		a.postEditVerify.buildCmd = detectBuildSystem(a.workingDir)
		a.postEditVerify.buildCmdChecked = true
	}

	cmd := a.postEditVerify.buildCmd
	if cmd == "" {
		// No build system detected; reset counter so we don't keep checking.
		a.postEditVerify.sourceEditsSinceHint = 0
		return ""
	}

	a.postEditVerify.sourceEditsSinceHint = 0
	debug.Log("agent", "post-edit verify hint: suggesting %q after %d source-code edits", cmd, postEditVerifyInterval)

	return fmt.Sprintf(
		"[Verification reminder: you've edited %d source files since the last build check. "+
			"Run `%s` to verify your changes compile before making further edits.]",
		postEditVerifyInterval, cmd,
	)
}

// resetPostEditVerify clears edit tracking state. Called at the start of
// each new RunStreamWithContent (new user turn).
func (a *Agent) resetPostEditVerify() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.postEditVerify = postEditVerifyState{}
}
