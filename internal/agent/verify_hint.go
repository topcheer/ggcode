package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

// postEditVerifyState tracks file edits to inject periodic verification hints.
// This implements the "generate-verify-fix loop" pattern: after editing source
// code, prompt the agent to verify (build/test) before making more changes.
//
// The hint fires once every postEditVerifyInterval source-code edits, not
// after every single edit, to avoid noise. Non-source files (docs, JSON,
// markdown) don't count toward the threshold.
//
// Smart detection: if the agent runs a build/test/verify command between edits,
// the counter resets - the agent already verified, no need to nag.
type postEditVerifyState struct {
	sourceEditsSinceHint int    // consecutive source-code edits since last hint or build
	buildCmd             string // cached build command (detected lazily, empty = not yet checked)
	buildCmdChecked      bool   // whether we've attempted detection
	lastBuildFailed      bool   // true if the agent's last build command failed
}

// postEditVerifyInterval is how many source-code edits between hints.
const postEditVerifyInterval = 3

// sourceMutatingTools is the CANONICAL set of tools that write to disk and
// change source files (#153/#154). All per-purpose gates (fileEditingTools,
// reverifyEditTools, build idempotency) must be derived from or kept in sync
// with this superset. Includes every registered tool that persists edits:
// edit_file, write_file, multi_edit_file, multi_file_edit, multi_file_write,
// batch_replace, lsp_rename (applies LSP workspace edits), file_ops, notebook_edit.
var sourceMutatingTools = map[string]bool{
	"edit_file":        true,
	"write_file":       true,
	"multi_edit_file":  true,
	"multi_file_edit":  true,
	"multi_file_write": true,
	"batch_replace":    true,
	"lsp_rename":       true,
	"file_ops":         true,
	"notebook_edit":    true,
}

// fileEditingTools is the set of tool names that modify files on disk.
// Derived from the canonical sourceMutatingTools superset (#154) so it can
// never drift out of sync.
var fileEditingTools = sourceMutatingTools

// assertEditToolMapsInSync is referenced by TestSourceMutatingToolsSuperset
// to guarantee the canonical map stays complete and every per-purpose gate
// derived in #738 still covers the full canonical superset (aliases share the
// same map object; wider-semantics gates are built with derivedEditTools).
func assertEditToolMapsInSync() bool {
	if !(len(sourceMutatingTools) == 9 && fileEditingTools["batch_replace"] && reverifyEditTools["lsp_rename"] &&
		strategyFixationIsMutation("file_ops") && strategyFixationIsMutation("multi_file_write")) {
		return false
	}
	// Aliased gates (same canonical map, all 9 members by construction).
	for _, m := range []map[string]bool{
		editTools,               // adaptive_effort
		causalEditTools,         // causal_attribution
		psEditTools,             // premature_success
		outcomeCorrectiveTools,  // outcome_misattrib
		productiveEditTools,     // scope_drift
		editToolSet,             // iter_pressure
		reproducerEditToolNames, // reproducer_lifecycle
	} {
		if len(m) != len(sourceMutatingTools) {
			return false
		}
		for t := range sourceMutatingTools {
			if !m[t] {
				return false
			}
		}
	}
	// Superset gates (canonical members + declared extras).
	for _, m := range []map[string]bool{
		privilegedSinkTools,      // taint_influence_check
		mutatingToolNamesFrag,    // exploration_frag
		qcActionTools,            // query_converge
		integrationMutatingTools, // tool_integration_monitor
	} {
		for t := range sourceMutatingTools {
			if !m[t] {
				return false
			}
		}
	}
	// Predicate gates (derived lookups).
	for t := range sourceMutatingTools {
		if !isEditTool(t) || !isEditingTool(t) || !coverageIsEditTool(t) ||
			!bareStreakIsMutation(t) || !csIsEditTool(t) || !ecIsEditTool(t) ||
			!recklessIsEditTool(t) || !undoBlindIsMutation(t) || !eaIsEditTool(t) {
			return false
		}
	}
	return true
}

// fileReadingTools is the set of tools that read file contents.
var fileReadingTools = map[string]bool{
	"read_file":       true,
	"multi_file_read": true,
}

// extractToolFilePath was removed (#500): the read-tracking path now uses
// extractFilePathsFromArgs directly (batch-aware, multi_file_read records ALL
// files instead of files[0]).

// gitFileModifyingTools are git operations that change file contents on
// disk (checkout, stash pop/apply, etc.). These must invalidate tool caches
// just like direct file edits, otherwise stale cached results (grep, LSP)
// will be served after the working tree changes.
var gitFileModifyingTools = map[string]bool{
	"git_stash":      true, // pop/apply restores changed files
	"enter_worktree": true, // switches working directory + files
	"exit_worktree":  true, // switches back, files may differ
}

// gitWholeTreeTools are git operations that can change the ENTIRE working tree
// (branch switches, hard resets, reverts). Unlike single-file edits, these
// operations may silently change dozens of files at once. When they run,
// ALL caches must be fully invalidated - including mtime-based entries -
// because the agent's read cache, command results, and LSP diagnostics are
// all potentially stale relative to the new working tree state.
//
// Competitive analysis:
//   - Claude Code: re-reads files after git operations
//   - Cursor: IDE detects branch switches and refreshes file state
//   - Aider: operates on a single commit, catches changes via git diff
//   - OpenHands: full file refresh on git operations
//
// The gap: without this, the agent may edit files based on cached content
// from the previous branch, or claim "build passes" from a cached result
// that predates the branch switch. Especially dangerous in autonomous mode.
var gitWholeTreeTools = map[string]bool{
	"git_checkout": true, // branch switch changes potentially all files
	"git_reset":    true, // hard mode discards all changes to tracked files
	"git_revert":   true, // creates new commit undoing prior changes
}

// mutatesSourceTree reports whether executing toolName may rewrite
// working-tree files and therefore must invalidate the speculative,
// memoize, and command caches.
//
// #1104: undo_edit restores prior file content from checkpoints (a real
// disk write) yet lived outside every existing mutation set, so stale
// grep/LSP/git_diff/command entries survived an undo. It is extended here
// rather than added to the canonical sourceMutatingTools superset, whose
// exact 9-tool membership is pinned by the #737/#153 sync assertions;
// cache gating only needs "may touch the tree", which this predicate owns.
// Notebook edits flow through the canonical superset.
func mutatesSourceTree(toolName string) bool {
	if fileEditingTools[toolName] || gitFileModifyingTools[toolName] || gitWholeTreeTools[toolName] {
		return true
	}
	return toolName == "undo_edit"
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

	// 1. Makefile - the project's authoritative build configuration.
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
			// language detection - bare "make" might run the wrong thing.
			break
		}
	}

	// 2. Justfile - modern command runner (just).
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

	// 3. Taskfile - modern task runner (task).
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
				// #940: task names are YAML keys at line start (after indentation);
				// anchor per-line like the Makefile/Justfile branches above so that
				// "integration-test:" / "docker-build:" / "image: node:latest" no
				// longer satisfy the bare-contains check for "test:" / "build:".
				for _, task := range []string{"verify-ci", "ci", "verify", "test", "build"} {
					taskKey := task + ":"
					for _, line := range strings.Split(content, "\n") {
						trimmed := strings.TrimLeft(line, " \t")
						if strings.HasPrefix(trimmed, "#") {
							continue
						}
						if strings.HasPrefix(trimmed, taskKey) {
							return "task " + task
						}
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

	// 5. Language-specific defaults (lower confidence - may miss build tags).
	// Only suggest commands for tools that are actually available on the host.
	if fileExists(filepath.Join(workingDir, "go.mod")) && commandAvailable("go") {
		return "go build ./..."
	}
	if fileExists(filepath.Join(workingDir, "Cargo.toml")) && commandAvailable("cargo") {
		return "cargo build"
	}
	if cmd := detectNpmCommand(workingDir); cmd != "" {
		return cmd
	}
	if fileExists(filepath.Join(workingDir, "CMakeLists.txt")) && commandAvailable("cmake") {
		return "cmake --build build"
	}
	if (fileExists(filepath.Join(workingDir, "pyproject.toml")) ||
		fileExists(filepath.Join(workingDir, "setup.py"))) && commandAvailable("python") {
		return "python -m pytest"
	}

	return ""
}

// commandAvailable checks if the given binary is available in PATH.
// Used by detectBuildSystem to avoid suggesting verification commands
// for tools that are not installed on the host system.
func commandAvailable(binary string) bool {
	_, err := exec.LookPath(binary)
	return err == nil
}

// detectNpmCommand returns the npm verification command for workingDir, or ""
// when npm is unavailable or package.json defines neither a "build" nor a
// "test" script. package.json without a "build" script (docs sites, library
// packages) makes "npm run build" fail with "Missing script" (exit 1) - that
// is NOT a code failure but was captured as one, triggering pointless
// auto-repair loops. Only offer commands whose script actually exists;
// otherwise fall through so the (now gated) LLM oracle can decide.
func detectNpmCommand(workingDir string) string {
	if !fileExists(filepath.Join(workingDir, "package.json")) || !commandAvailable("npm") {
		return ""
	}
	if packageJSONHasScript(workingDir, "build") {
		return "npm run build"
	}
	if packageJSONHasScript(workingDir, "test") {
		return "npm test"
	}
	return ""
}

// packageJSONHasScript reports whether package.json in workingDir defines the
// named script. npm exits 1 with "Missing script: <name>" when the script is
// absent, which the verifier must not misread as a code failure.
func packageJSONHasScript(workingDir, script string) bool {
	data, err := os.ReadFile(filepath.Join(workingDir, "package.json"))
	if err != nil {
		return false
	}
	var pkg struct {
		Scripts map[string]string `json:"scripts"`
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		return false
	}
	_, ok := pkg.Scripts[script]
	return ok
}

// codeFileExtensions lists file suffixes that indicate real source code.
var codeFileExtensions = map[string]bool{
	".go": true, ".rs": true, ".py": true, ".js": true, ".mjs": true, ".cjs": true,
	".ts": true, ".tsx": true, ".jsx": true, ".java": true, ".rb": true, ".c": true,
	".cc": true, ".cpp": true, ".h": true, ".hpp": true, ".swift": true, ".kt": true,
	".cs": true, ".php": true, ".lua": true, ".dart": true, ".zig": true,
	".ex": true, ".exs": true, ".scala": true, ".m": true, ".mm": true,
}

// codeFileBasenames lists build-manifest filenames that count as "code" for
// verification gating purposes.
var codeFileBasenames = map[string]bool{
	"Makefile": true, "makefile": true, "GNUmakefile": true,
	"go.mod": true, "go.sum": true, "package.json": true,
	"Cargo.toml": true, "CMakeLists.txt": true, "pyproject.toml": true,
	"setup.py": true, "Taskfile.yml": true, "Taskfile.yaml": true,
	"Justfile": true, "justfile": true, "mix.exs": true, "Gemfile": true,
}

// isCodeFilePath reports whether a path looks like source code or a build
// manifest rather than prose/docs/notes.
func isCodeFilePath(path string) bool {
	if codeFileExtensions[strings.ToLower(filepath.Ext(path))] {
		return true
	}
	return codeFileBasenames[filepath.Base(path)]
}

// runTouchedCode reports whether the run edited any file that looks like
// source code. It gates the LLM verify-command fallback: in workspaces with
// no build system where only docs/notes were edited, the oracle has nothing
// to anchor on and hallucinates commands whose failures trigger pointless
// auto-repair loops. Unknown stats (nil or empty FilesEdited) default to
// true so verification is never silently skipped.
func runTouchedCode(runStats *RunStats) bool {
	if runStats == nil || len(runStats.FilesEdited) == 0 {
		return true
	}
	for _, f := range runStats.FilesEdited {
		if isCodeFilePath(f) {
			return true
		}
	}
	return false
}

// verifyCommandAvailable checks if the command's primary binary is available.
// Handles compound commands like "make verify-ci" or "python -m pytest".
// Returns true for shell builtins and commands that bypass PATH (e.g. "bash /path/script.sh").
func verifyCommandAvailable(command string) bool {
	parts := strings.Fields(command)
	if len(parts) == 0 {
		return false
	}
	primary := parts[0]

	// #941: for scripts run via bash/sh, check the script path instead — must
	// run BEFORE the unconditional-true switch below, which previously made
	// this branch unreachable dead code. Shell flags (e.g. "sh -c 'cmd'")
	// are not script paths, so only non-flag arguments get the fileExists check.
	if primary == "bash" || primary == "sh" {
		if len(parts) > 1 && !strings.HasPrefix(parts[1], "-") {
			return fileExists(parts[1])
		}
		return true // bare shell or flag-only invocation, always available
	}

	// Shell builtins and wrappers that are always available.
	switch primary {
	case "source":
		return true
	}

	return commandAvailable(primary)
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
			// #941: "test:=foo" / "test::=foo" are variable assignments, not
			// rules — the comment above promised this exclusion but the check
			// was missing, creating phantom targets → make exits 2 (not 127) →
			// false verification failure.
			rest := trimmed[len(targetPrefix):]
			if strings.HasPrefix(rest, "=") || strings.HasPrefix(rest, ":=") {
				continue
			}
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

// sfExtractMutationPaths returns EVERY file path referenced by a mutation
// tool call for strategy-fixation tracking (#485): multi_file_edit's files[]
// entries must all count as edits, and notebook_edit carries its path in
// notebook_path (previously never extracted, so notebook edits were silently
// untracked). Distinct from extractFilePathFromArgs (first path only) and
// from wasted_explore's extractFilePathsFromArgs (read-tool oriented, would
// admit url/directory noise).
func sfExtractMutationPaths(args json.RawMessage) []string {
	if len(args) == 0 {
		return nil
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(args, &raw); err != nil {
		return nil
	}

	var out []string
	seen := make(map[string]bool)
	add := func(s string) {
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}

	sfAddStringFields(add, raw, "file_path", "path", "notebook_path")

	// files[] has two shapes: []map (multi_file_edit/multi_file_write) and
	// plain []string (batch_replace schema) -- both must yield every path (#737).
	if filesRaw, ok := raw["files"]; ok {
		sfAddMapListStringFields(add, filesRaw, "file_path", "path")
		var plainFiles []string
		if json.Unmarshal(filesRaw, &plainFiles) == nil {
			for _, s := range plainFiles {
				add(s)
			}
		}
	}

	// file_ops carries its targets in operations[].source/destination;
	// previously never extracted, so file_ops mutations were untracked (#737).
	// source is always a touched path; destination matters for move/rename.
	if opsRaw, ok := raw["operations"]; ok {
		sfAddMapListStringFields(add, opsRaw, "source", "destination")
	}

	return out
}

// sfAddStringFields unmarshals each named top-level field of raw as a string
// and passes the values to add (empty strings are dropped by add).
func sfAddStringFields(add func(string), raw map[string]json.RawMessage, fields ...string) {
	for _, field := range fields {
		if v, ok := raw[field]; ok {
			var s string
			if json.Unmarshal(v, &s) == nil {
				add(s)
			}
		}
	}
}

// sfAddMapListStringFields unmarshals rawJSON as a list of string-keyed maps
// (e.g. files[] or operations[]) and passes each named field's string value
// from every element to add.
func sfAddMapListStringFields(add func(string), rawJSON json.RawMessage, fields ...string) {
	var list []map[string]json.RawMessage
	if json.Unmarshal(rawJSON, &list) != nil {
		return
	}
	for _, entry := range list {
		sfAddStringFields(add, entry, fields...)
	}
}

// isSourceCodeFile returns true if the path has a source-code extension.
func isSourceCodeFile(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return sourceCodeExtensions[ext]
}

// postEditVerifyHint checks if we should inject a verification hint after
// a successful file edit. Returns the hint text, or "" if no hint needed.
//
// Context-aware: if the agent has already run a build since the last hint
// fire, the counter was reset (via maybeResetVerifyOnCommand). If the last
// build FAILED, the hint message includes urgency.
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

	// Affected-test detection: derive a fast, scope-limited verification
	// command for the package just edited (e.g. `go test ./internal/agent/`).
	// This is far cheaper than the full project suite for a mid-session check
	// and gives the agent faster compile/test feedback on its latest change.
	// The full-suite command (cmd) is still surfaced for final verification.
	targeted := targetedVerifyCommand(a.workingDir, filePath)
	fileName := filepath.Base(filePath)

	// Test impact analysis with transitive dependencies: if multiple
	// packages/directories changed (detected via git status), suggest an
	// impact-scoped test command that covers all affected packages AND their
	// downstream importers - not just the single file being edited.
	// Multi-language: works for Go, TypeScript, Python, Rust, Java, etc.
	impact := impactScopedTestCommandMulti(a.workingDir)

	// Test coverage gap: surface changed files that lack test counterparts
	// (any language), nudging the agent toward test generation.
	coverageNudge := funcLevelCoverageNudgeMulti(a.workingDir)
	if coverageNudge == "" {
		coverageNudge = testCoverageNudgeMulti(a.workingDir)
	}

	debug.Log("agent", "post-edit verify hint: targeted=%q impact=%q full=%q after %d source-code edits", targeted, impact, cmd, postEditVerifyInterval)

	// Prefer the impact-scoped command when it covers more than the targeted
	// package alone; fall back to the single-package targeted command.
	fastCmd := targeted
	if impact != "" && impact != targeted {
		fastCmd = impact
	}

	// Context-aware: if last build failed, make it urgent.
	if a.postEditVerify.lastBuildFailed {
		var hint string
		if fastCmd != "" {
			hint = fmt.Sprintf(
				"[Verification reminder: you've edited %d source files since the last build check (which FAILED). "+
					"Run `%s` for a fast check of the affected packages (`%s`), or `%s` for the full suite before finishing.]",
				postEditVerifyInterval, fastCmd, fileName, cmd,
			)
		} else {
			hint = fmt.Sprintf(
				"[Verification reminder: you've edited %d source files since the last build check (which FAILED). "+
					"Run `%s` to verify your fixes compile before making further edits.]",
				postEditVerifyInterval, cmd,
			)
		}
		if coverageNudge != "" {
			hint += " " + coverageNudge
		}
		return hint
	}

	var hint string
	if fastCmd != "" {
		hint = fmt.Sprintf(
			"[Verification reminder: you've edited %d source files since the last build check. "+
				"Run `%s` for a fast check of the affected packages (`%s`), or `%s` for the full suite before finishing.]",
			postEditVerifyInterval, fastCmd, fileName, cmd,
		)
	} else {
		hint = fmt.Sprintf(
			"[Verification reminder: you've edited %d source files since the last build check. "+
				"Run `%s` to verify your changes compile before making further edits.]",
			postEditVerifyInterval, cmd,
		)
	}
	if coverageNudge != "" {
		hint += " " + coverageNudge
	}
	return hint
}

// verifyCommands is a set of command substrings that indicate a build/test/verify
// command. Used by maybeResetVerifyOnCommand to detect when the agent has
// proactively run verification (so the hint counter can be reset).
var verifyCommands = map[string]bool{
	"go build":      true,
	"go test":       true,
	"go vet":        true,
	"make":          true,
	"cargo build":   true,
	"cargo test":    true,
	"npm run build": true,
	"npm test":      true,
	"npm run test":  true,
	"just":          true,
	"task":          true,
	"pytest":        true,
	"flutter test":  true,
	"cmake":         true,
	"ctest":         true,
	"rake test":     true,
	// Lint / format commands also count as verification - running them
	// resets the post-edit verify hint counter.
	"golangci-lint": true,
	"gofmt":         true,
	"goimports":     true,
	"eslint":        true,
	"prettier":      true,
	"ruff":          true,
	"rubocop":       true,
	"rustfmt":       true,
	"cargo fmt":     true,
	"cargo clippy":  true,
	"flake8":        true,
	"mypy":          true,
	"pylint":        true,
}

// isVerifyCommand checks whether a command string looks like a build/test/verify
// command by matching against known verify command substrings.
// #950: handles env-var assignment prefixes ("GOFLAGS=-p=1 make ...") and
// compound commands ("cd /app && go test ./...") - any matching segment counts.
func isVerifyCommand(cmd string) bool {
	cmdLower := strings.ToLower(strings.TrimSpace(cmd))
	if cmdLower == "" {
		return false
	}
	if isVerifyCommandSegment(cmdLower) {
		return true
	}
	// Compound command: check each &&-separated, then ; / |-separated segment.
	for _, andSeg := range strings.Split(cmdLower, "&&") {
		for _, seg := range splitCompoundCommand(andSeg) {
			if isVerifyCommandSegment(seg) {
				return true
			}
		}
	}
	return false
}

// maybeResetVerifyOnCommand checks whether a run_command tool call was a
// build/test/verify command and, if so, resets the edit counter and records
// the result. This prevents redundant verify hints when the agent has already
// proactively verified.
//
// Thread-safety: caller must NOT hold a.mu (this method acquires it).
func (a *Agent) maybeResetVerifyOnCommand(toolName string, args json.RawMessage, resultErr bool) {
	if toolName != "run_command" {
		return
	}

	cmd := extractCommandFromArgs(args)
	if cmd == "" || !isVerifyCommand(cmd) {
		return
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	a.postEditVerify.sourceEditsSinceHint = 0
	a.postEditVerify.lastBuildFailed = resultErr

	debug.Log("agent", "verify hint counter reset: agent ran build command %q (failed=%v)", cmd, resultErr)
}

// extractCommandFromArgs extracts the "command" field from run_command args.
// #471: the run_command tool schema mandates a leading '# ' comment line
// (shown as the activity label). Strip it so prefix-anchored matchers
// (isVerifyCommand / isConvergenceVerifyCommand / isStrictVerifyCommand)
// see the actual command, not the comment.
func extractCommandFromArgs(args json.RawMessage) string {
	if len(args) == 0 {
		return ""
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(args, &raw); err != nil {
		return ""
	}
	if v, ok := raw["command"]; ok {
		var s string
		if json.Unmarshal(v, &s) == nil {
			return stripLeadingShellComment(s)
		}
	}
	return ""
}

// stripLeadingShellComment removes leading '#' comment line(s) from a
// shell command string (#471).
func stripLeadingShellComment(s string) string {
	for {
		trimmed := strings.TrimLeft(s, " \t\n\r")
		if !strings.HasPrefix(trimmed, "#") {
			return trimmed
		}
		if idx := strings.IndexByte(trimmed, '\n'); idx >= 0 {
			s = trimmed[idx+1:]
		} else {
			return ""
		}
	}
}

// envAssignPrefix matches a leading environment-variable assignment such as
// "GOFLAGS=-p=1 " or "CGO_ENABLED=0 " (#950).
var envAssignPrefix = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=\S*\s+`)

// stripEnvAssignments removes leading env-var assignment prefixes from a
// command string so prefix-anchored verify matchers see the actual command
// (#950: "GOFLAGS=\"-p=1\" make verify-ci" must be recognized as make).
func stripEnvAssignments(cmd string) string {
	for {
		next := envAssignPrefix.ReplaceAllString(cmd, "")
		if next == cmd {
			return cmd
		}
		cmd = next
	}
}

// splitCompoundCommand splits a shell command on && / ; / | separators
// (quote context is not tracked - build/verify commands in practice don't
// quote these operators; worst case a segment is checked and mismatches).
func splitCompoundCommand(cmd string) []string {
	return strings.FieldsFunc(cmd, func(r rune) bool {
		return r == ';' || r == '|'
	})
}

// isVerifyCommandSegment reports whether a single shell segment (env
// assignments stripped) is a verify command. Splitting on '&' is handled by
// the caller via strings.Split on "&&".
func isVerifyCommandSegment(seg string) bool {
	seg = strings.ToLower(strings.TrimSpace(stripEnvAssignments(seg)))
	if seg == "" {
		return false
	}
	for prefix := range verifyCommands {
		if strings.HasPrefix(seg, prefix+" ") || seg == prefix {
			return true
		}
	}
	words := strings.Fields(seg)
	if len(words) > 0 {
		if words[0] == "make" || words[0] == "just" || words[0] == "task" {
			return true
		}
	}
	return false
}

// funcLevelCoverageNudge generates a function-level coverage hint showing
// which specific exported functions in changed files lack tests. This is
// richer than the file-level testCoverageNudge - it mirrors GitHub Copilot's
// per-function test generation suggestions.
//
// Returns "" when no function-level gaps are found.
func funcLevelCoverageNudge(workingDir string) string {
	gaps := funcLevelCoverageGaps(workingDir, 3, 4)
	if len(gaps) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("[Untested functions: ")
	for i, gap := range gaps {
		if i > 0 {
			b.WriteString("; ")
		}
		b.WriteString(gap.File)
		b.WriteString(": ")
		b.WriteString(strings.Join(gap.Funcs, ", "))
	}
	b.WriteString(". Consider generating tests for these functions.]")
	return b.String()
}

// funcLevelCoverageNudgeMulti is the multi-language version of
// funcLevelCoverageNudge. It uses funcLevelCoverageGapsMulti to analyze
// changed files across all supported languages (not just Go).
func funcLevelCoverageNudgeMulti(workingDir string) string {
	gaps := funcLevelCoverageGapsMulti(workingDir, 3, 4)
	if len(gaps) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("[Untested functions: ")
	for i, gap := range gaps {
		if i > 0 {
			b.WriteString("; ")
		}
		b.WriteString(gap.File)
		b.WriteString(": ")
		b.WriteString(strings.Join(gap.Funcs, ", "))
	}
	b.WriteString(". Consider generating tests for these functions.]")
	return b.String()
}

// resetPostEditVerify clears edit tracking state. Called at the start of
// each new RunStreamWithContent (new user turn).
func (a *Agent) resetPostEditVerify() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.postEditVerify = postEditVerifyState{}
}

// targetedVerifyCommand returns a fast, scope-limited verification command for
// the package containing filePath, when one can be derived. This implements
// multi-language test-impact analysis: instead of nudging the agent to run the
// entire project suite (e.g. `make verify-ci`) after every few edits, we scope
// the suggestion to the package/module that actually changed - `go test
// ./internal/agent/` for a Go edit, `npx vitest run src/foo` for TypeScript,
// `python -m pytest src/foo` for Python, etc.
//
// Returns "" when no targeted command applies.
func targetedVerifyCommand(workingDir, filePath string) string {
	if filePath == "" {
		return ""
	}
	// Use multi-language dispatcher; Go files are handled by
	// goTargetedTestCommand via the Go language profile.
	return targetedVerifyCommandMulti(workingDir, filePath)
}

// goTargetedTestCommand computes `go test ./<pkg-dir>/` for an edited Go file,
// scoped to the Go module that contains it. It walks up from the file's
// directory to locate go.mod, then expresses the package directory relative to
// the module root. Returns "" if no module root is found or the file escapes
// the module (e.g. an absolute path outside the workspace).
func goTargetedTestCommand(workingDir, filePath string) string {
	abs := filePath
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(workingDir, abs)
	}
	abs = filepath.Clean(abs)

	// Walk up from the file's directory to find the module root (go.mod).
	dir := filepath.Dir(abs)
	modRoot := ""
	for {
		if fileExists(filepath.Join(dir, "go.mod")) {
			modRoot = dir
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break // reached filesystem root
		}
		dir = parent
	}
	if modRoot == "" {
		return ""
	}

	rel, err := filepath.Rel(modRoot, filepath.Dir(abs))
	if err != nil {
		return ""
	}
	rel = filepath.ToSlash(rel)
	// Guard against a relative path that escapes the module.
	if strings.HasPrefix(rel, "..") {
		return ""
	}
	if rel == "." {
		return "go test ./"
	}
	return "go test ./" + rel + "/"
}
