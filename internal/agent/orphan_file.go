package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/topcheer/ggcode/internal/debug"
)

const (
	orphanFileMaxWarnings  = 2
	orphanCallThreshold    = 3
	orphanRecentFilesLimit = 8
)

type orphanFileState struct {
	mu             sync.Mutex
	newFiles       []string
	callsSince     int
	integrated     bool
	warnings       int
	preExecExisted map[string]bool
}

func newOrphanFileState() *orphanFileState {
	return &orphanFileState{}
}

func (o *orphanFileState) reset() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.newFiles = nil
	o.callsSince = 0
	o.integrated = false
	o.warnings = 0

	o.preExecExisted = nil
}

func isOrphanSourceFile(path string) bool {
	lower := strings.ToLower(path)
	for _, ext := range []string{".go", ".py", ".ts", ".tsx", ".js", ".jsx",
		".rs", ".java", ".kt", ".rb", ".c", ".cpp", ".h", ".hpp", ".swift"} {
		if strings.HasSuffix(lower, ext) {
			return true
		}
	}
	return false
}

func (o *orphanFileState) recordPreExec(toolName, argsJSON, workingDir string) {
	if toolName != "write_file" && toolName != "multi_file_write" {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.preExecExisted == nil {
		o.preExecExisted = make(map[string]bool)
	}
	for _, path := range extractFilePathsOrArg(argsJSON, toolName) {
		resolved := path
		if !filepath.IsAbs(resolved) && workingDir != "" {
			resolved = filepath.Join(workingDir, resolved)
		}
		_, err := os.Stat(resolved)

		o.preExecExisted[path] = err == nil
	}
}

func (o *orphanFileState) recordToolCall(toolName, argsJSON string, iteration int) string {
	o.mu.Lock()
	defer o.mu.Unlock()

	switch toolName {
	case "write_file", "multi_file_write":

		paths := extractFilePathsOrArg(argsJSON, toolName)
		for _, path := range paths {

			if o.preExecExisted[path] {
				continue
			}
			if isOrphanSourceFile(path) {
				o.newFiles = append(o.newFiles, path)
				if len(o.newFiles) > orphanRecentFilesLimit {
					o.newFiles = o.newFiles[len(o.newFiles)-orphanRecentFilesLimit:]
				}
				o.callsSince = 0
				o.integrated = false
				debug.Log("agent", "orphanFile: tracking new source file %s (iteration %d)", path, iteration)
			}
		}

	case "edit_file", "multi_edit_file", "multi_file_edit", "batch_replace":
		o.recordEditIntegration(argsJSON)

	case "run_command":
		low := strings.ToLower(argsJSON)

		for _, kw := range []string{"go build", "go test", "make build", "make test",
			"npm build", "npm test", "cargo build", "cargo test", "tsc"} {
			if strings.Contains(low, kw) {
				o.integrated = true
				break
			}
		}
	}

	if len(o.newFiles) == 0 {
		return ""
	}

	o.callsSince++

	if o.integrated {
		o.newFiles = nil
		o.callsSince = 0
		o.integrated = false
		return ""
	}

	if o.callsSince < orphanCallThreshold {
		return ""
	}
	if o.warnings >= orphanFileMaxWarnings {
		return ""
	}

	o.warnings++
	filesList := strings.Join(o.newFiles, ", ")
	return fmt.Sprintf("[Orphaned File] New source file(s) created but not yet integrated: %s. "+
		"%d subsequent tool calls have occurred without any edit to existing files "+
		"or a build to verify integration. If these files are meant to be used by "+
		"existing code, add the necessary imports/references now. Otherwise the "+
		"files may be orphaned -- compiling in isolation but never invoked.",
		filesList, o.callsSince)
}

func extractFilePathsOrArg(argsJSON, toolName string) []string {
	if toolName == "multi_file_write" {
		var out []string
		rest := argsJSON
		for {
			idx := strings.Index(rest, `"path":"`)
			if idx < 0 {
				break
			}
			start := idx + len(`"path":"`)
			end := strings.Index(rest[start:], `"`)
			if end <= 0 {
				break
			}
			out = append(out, rest[start:start+end])
			rest = rest[start+end:]
		}
		return out
	}

	idx := strings.Index(argsJSON, `"path":"`)
	if idx < 0 {
		return nil
	}
	start := idx + len(`"path":"`)
	end := strings.Index(argsJSON[start:], `"`)
	if end > 0 {
		return []string{argsJSON[start : start+end]}
	}
	return nil
}

func (o *orphanFileState) recordEditIntegration(argsJSON string) {
	paths := extractEditPathsOrArg(argsJSON)
	if len(paths) == 0 {
		o.integrated = true
		return
	}
	for _, p := range paths {
		if !o.isTrackedNewFile(p) {
			o.integrated = true
			return
		}
	}
}

func (o *orphanFileState) isTrackedNewFile(path string) bool {
	for _, f := range o.newFiles {
		if f == path || filepath.Base(f) == filepath.Base(path) {
			return true
		}
	}
	return false
}

func extractEditPathsOrArg(argsJSON string) []string {
	var out []string
	seen := map[string]bool{}
	for _, key := range []string{"file_path", "path"} {
		needle := `"` + key + `":"`
		pos := 0
		for {
			idx := strings.Index(argsJSON[pos:], needle)
			if idx < 0 {
				break
			}
			start := pos + idx + len(needle)
			endRel := strings.Index(argsJSON[start:], `"`)
			if endRel <= 0 {
				break
			}
			path := argsJSON[start : start+endRel]
			if path != "" && !seen[path] {
				seen[path] = true
				out = append(out, path)
			}
			pos = start + endRel
		}
	}
	return out
}
