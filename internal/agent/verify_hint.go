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
// Priority: Makefile targets > verification scripts > language-specific defaults.
// Makefile is preferred over go.mod because it includes build tags, env vars,
// and other project-specific configuration that language defaults miss.
func detectBuildSystem(workingDir string) string {
	if workingDir == "" {
		return ""
	}

	// 1. Makefile — the project's authoritative build configuration.
	// Check for specific high-value targets in priority order.
	makefiles := []string{
		filepath.Join(workingDir, "Makefile"),
		filepath.Join(workingDir, "makefile"),
		filepath.Join(workingDir, "GNUmakefile"),
	}
	for _, mf := range makefiles {
		if data, err := os.ReadFile(mf); err == nil {
			content := string(data)
			// Look for the most useful verification target.
			for _, target := range []string{"verify-ci", "ci", "verify", "test", "build"} {
				// Match "target:" or "target :" at start of a line (not in a comment or variable)
				if hasMakeTarget(content, target) {
					return "make " + target
				}
			}
			// Makefile exists but no recognized target. Fall through to
			// language detection — bare "make" might run the wrong thing.
			break
		}
	}

	// 2. Justfile — modern command runner (just).
	justfiles := []string{
		filepath.Join(workingDir, "Justfile"),
		filepath.Join(workingDir, "justfile"),
		filepath.Join(workingDir, ".justfile"),
	}
	for _, jf := range justfiles {
		if fileExists(jf) {
			// Check for a verify/ci/test recipe
			if data, err := os.ReadFile(jf); err == nil {
				content := string(data)
				for _, recipe := range []string{"verify-ci", "ci", "verify", "test", "build"} {
					// Just recipes can be defined as "recipe:" or "recipe:"
					if strings.Contains(content, "\n"+recipe+":") || strings.HasPrefix(content, recipe+":") {
						return "just " + recipe
					}
				}
			}
			return "just"
		}
	}

	// 3. Taskfile — modern task runner (task).
	taskfiles := []string{
		filepath.Join(workingDir, "Taskfile.yml"),
		filepath.Join(workingDir, "Taskfile.yaml"),
		filepath.Join(workingDir, ".taskfile.yml"),
	}
	for _, tf := range taskfiles {
		if fileExists(tf) {
			// Check for a verify/ci/test task
			if data, err := os.ReadFile(tf); err == nil {
				content := string(data)
				for _, task := range []string{"verify-ci", "ci", "verify", "test", "build"} {
					// YAML task names appear as keys under "tasks:" section
					if strings.Contains(content, task+":") {
						return "task " + task
					}
				}
			}
			return "task"
		}
	}

	// 4. Project-specific verification scripts.
	scriptChecks := []string{
		filepath.Join(workingDir, "scripts", "dev", "verify-ci.sh"),
		filepath.Join(workingDir, "scripts", "verify.sh"),
		filepath.Join(workingDir, "scripts", "ci.sh"),
	}
	for _, script := range scriptChecks {
		if fileExists(script) {
			return "bash " + script
		}
	}

	// 5. Language-specific defaults (lower confidence — may miss build tags).
	if fileExists(filepath.Join(workingDir, "go.mod")) {
		return "go build ./..."
	}
	if fileExists(filepath.Join(workingDir, "Cargo.toml")) {
		return "cargo build"
	}
	if fileExists(filepath.Join(workingDir, "package.json")) {
		return "npm run build"
	}
	if fileExists(filepath.Join(workingDir, "CMakeLists.txt")) {
		return "cmake --build build"
	}
	if fileExists(filepath.Join(workingDir, "pyproject.toml")) ||
		fileExists(filepath.Join(workingDir, "setup.py")) {
		return "python -m pytest"
	}

	return ""
}

// hasMakeTarget checks if a Makefile defines a target with the given name.
// Matches "target:" at the beginning of a line (after optional whitespace),
// but not in comments (lines starting with #) or variable assignments (=).
func hasMakeTarget(makefileContent, target string) bool {
	targetPrefix := target + ":"
	for _, line := range strings.Split(makefileContent, "\n") {
		trimmed := strings.TrimLeft(line, " \t")
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.HasPrefix(trimmed, targetPrefix) {
			return true
		}
	}
	return false
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
