package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/topcheer/ggcode/internal/debug"
)

// Orphaned New File Integration Detector
//
// Research basis:
//   - "Studying the Effectiveness of LLMs in Code Generation" (FSE 2025):
//     AI agents create new files that are never imported or referenced by
//     existing code in 8-12% of SWE-bench trajectories, producing orphaned
//     modules that compile in isolation but are never wired into the build.
//   - SICA (arXiv:2504.15228, NeurIPS 2025): "incomplete integration" is
//     a top-3 failure mode -- the agent creates a solution file but never
//     connects it to the entry point, then declares success.
//   - GitHub Octoverse 2025: "phantom modules" (files that exist but have
//     zero importers) are the leading cause of silent build failures in
//     AI-assisted repositories.
//
// Problem: AI coding agents sometimes create new source files (write_file
// on a non-existent path) and then move on to other work or declare success
// without ever integrating the new file into the codebase. The file becomes
// an orphan -- it exists on disk but no existing file imports, references,
// or calls anything from it. The build may pass (the orphan compiles on
// its own) but the feature is completely non-functional because nothing
// invokes the new code.
//
// What it detects: When the agent creates a new source file and then makes
// N or more subsequent tool calls WITHOUT any edit to existing files (which
// would typically be needed to add an import/reference), it warns that the
// new file may be orphaned and un-integrated.
//
// Distinct from existing detectors:
//   - missing_test_check.go: detects missing test companions for edited
//     files. Orphan detection catches new source files with zero importers.
//   - companion_guard.go: ensures test/source pairs. Orphan detection
//     catches source files never wired into the build.
//   - edit_propagation.go: tracks cross-file edit cascades. Orphan detection
//     tracks the ABSENCE of cascades after file creation.
//   - dead_code_check.go: finds unused code WITHIN files. Orphan detection
//     finds unused FILES (zero importers across the codebase).

const (
	orphanFileMaxWarnings  = 2 // cap warnings per run
	orphanCallThreshold    = 3 // calls after new-file creation before warning
	orphanRecentFilesLimit = 8 // how many new files to track
)

// orphanFileState tracks newly created source files and whether they've
// been integrated by subsequent edits to existing files.
type orphanFileState struct {
	mu         sync.Mutex
	newFiles   []string // paths of new source files created this run
	callsSince int      // tool calls since the last new-file creation
	integrated bool     // whether any edit to existing file occurred since creation
	warnings   int      // warnings emitted this run
	// preExecExisted snapshots write-target existence BEFORE execution
	// (#1587-A) - post-execution stat'ing is blind to success.
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
}

// isOrphanSourceFile returns true for file extensions that are typically
// imported/compiled as part of a build.
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

// recordToolCall processes a completed tool call. For write_file creating
// new source files, it starts tracking. For edit_file/multi_edit_file on
// existing files, it marks integration. Returns a warning string if the
// orphan threshold is reached.
// recordPreExec snapshots path existence BEFORE the tool runs (#1587-A):
// the detector's write path consumes this snapshot post-execution, where
// stat'ing the live filesystem is blind (success implies existence).
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
		// Key by the SAME raw path recordToolCall extracts.
		o.preExecExisted[path] = err == nil
	}
}

func (o *orphanFileState) recordToolCall(toolName, argsJSON string, iteration int) string {
	o.mu.Lock()
	defer o.mu.Unlock()

	switch toolName {
	case "write_file", "multi_file_write":
		// #1473-B: extract ALL paths (the old first-"path"-only match tracked
		// just files[0]; editing any OTHER file of the batch set integrated
		// and cleared the whole list, so the remaining real orphans never
		// warned).
		paths := extractFilePathsOrArg(argsJSON, toolName)
		for _, path := range paths {
			// #1473-A / #1587-A: this runs AFTER execution - a successful
			// write_file means the file IS on disk now, so the old os.Stat
			// 'err == nil -> overwrite' skipped EVERY real creation (the
			// detector never fired on its core scenario), while cwd
			// misalignment stat'd the RAW relative path and failed into
			// tracking everything (the #542 false positive the fix claimed
			// to close). Decide from the PRE-EXECUTION existence snapshot
			// recorded in recordPreExec; absent snapshot entries default to
			// not-existed (creation).
			if o.preExecExisted[path] {
				continue // existed BEFORE the call: an overwrite, not a creation
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
		// Note: write_file on a non-source file (e.g., config) does not
		// mark as integrated. Only edits to source files or builds do.

	case "edit_file", "multi_edit_file", "multi_file_edit", "batch_replace":
		o.recordEditIntegration(argsJSON)

	case "run_command":
		low := strings.ToLower(argsJSON)
		// Build commands may discover/import new files
		for _, kw := range []string{"go build", "go test", "make build", "make test",
			"npm build", "npm test", "cargo build", "cargo test", "tsc"} {
			if strings.Contains(low, kw) {
				o.integrated = true
				break
			}
		}
	}

	// Only warn if we have untracked new files
	if len(o.newFiles) == 0 {
		return ""
	}

	o.callsSince++

	// If integrated, we're fine -- reset tracking
	if o.integrated {
		o.newFiles = nil
		o.callsSince = 0
		o.integrated = false
		return ""
	}

	// Warn after threshold calls without integration
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

// extractFilePathOrArg extracts the file path from tool call arguments.
// For write_file it's "path", for multi_file_write it's in "files" array.
func extractFilePathsOrArg(argsJSON, toolName string) []string {
	if toolName == "multi_file_write" {
		// Every "path":"..." entry in the files array (#1473-B).
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
	// Standard write_file: single "path":"..." entry.
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

// recordEditIntegration decides whether an edit-style tool call counts as
// integration of tracked new files (#1138). An edit whose target is one of
// the newly created files themselves is iterative refinement (write ->
// edit -> edit polish loop), NOT integration. Integration must come from
// edits to OTHER (existing) files that add imports/references/build wiring.
// When target paths cannot be parsed from the arguments, the call falls
// back to legacy behavior and marks integration.
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

// isTrackedNewFile reports whether path refers to a newly created source
// file currently tracked by this state (#1138). Paths are compared exactly
// and by base name to tolerate relative/absolute variation.
func (o *orphanFileState) isTrackedNewFile(path string) bool {
	for _, f := range o.newFiles {
		if f == path || filepath.Base(f) == filepath.Base(path) {
			return true
		}
	}
	return false
}

// extractEditPathsOrArg extracts candidate target file paths from edit-style
// tool call arguments (#1138). It covers edit_file/multi_edit_file
// ("file_path"), multi_file_edit/batch_replace ("files": [{"path": ...}]),
// and falls back to plain "path" keys.
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
