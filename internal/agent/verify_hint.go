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

type postEditVerifyState struct {
	sourceEditsSinceHint int
	buildCmd             string
	buildCmdChecked      bool
	lastBuildFailed      bool

	sourceEditsThisRun        int
	lastSourceFileThisRun     string
	buildOrTestRunThisRun     bool
	realBuildOrTestRunThisRun bool
	finalGateFiredThisRun     bool
}

const postEditVerifyInterval = 3

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

var fileEditingTools = sourceMutatingTools

func assertEditToolMapsInSync() bool {
	if !(len(sourceMutatingTools) == 9 && fileEditingTools["batch_replace"] && reverifyEditTools["lsp_rename"] &&
		strategyFixationIsMutation("file_ops") && strategyFixationIsMutation("multi_file_write")) {
		return false
	}

	for _, m := range []map[string]bool{
		editTools,
		causalEditTools,
		psEditTools,
		outcomeCorrectiveTools,
		productiveEditTools,
		editToolSet,
		reproducerEditToolNames,
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

	for _, m := range []map[string]bool{
		privilegedSinkTools,
		mutatingToolNamesFrag,
		qcActionTools,
		integrationMutatingTools,
	} {
		for t := range sourceMutatingTools {
			if !m[t] {
				return false
			}
		}
	}

	for t := range sourceMutatingTools {
		if !isEditTool(t) || !isEditingTool(t) || !coverageIsEditTool(t) ||
			!bareStreakIsMutation(t) || !csIsEditTool(t) || !ecIsEditTool(t) ||
			!recklessIsEditTool(t) || !undoBlindIsMutation(t) || !eaIsEditTool(t) {
			return false
		}
	}
	return true
}

var fileReadingTools = map[string]bool{
	"read_file":       true,
	"multi_file_read": true,
}

var gitFileModifyingTools = map[string]bool{
	"git_stash":      true,
	"enter_worktree": true,
	"exit_worktree":  true,
}

var gitWholeTreeTools = map[string]bool{
	"git_checkout": true,
	"git_reset":    true,
	"git_revert":   true,
}

func mutatesSourceTree(toolName string) bool {
	if fileEditingTools[toolName] || gitFileModifyingTools[toolName] || gitWholeTreeTools[toolName] {
		return true
	}
	switch toolName {
	case "undo_edit", "apply_patch", "scaffold_project":
		return true
	}
	return false
}

func partialEditWroteDespiteError(toolName, resultContent string, isError bool) bool {
	return isError && toolName == "multi_file_edit" &&
		len(extractWrittenPaths(resultContent)) > 0
}

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

func detectBuildSystem(workingDir string) string {
	if workingDir == "" {
		return ""
	}

	makefiles := []string{
		filepath.Join(workingDir, "Makefile"),
		filepath.Join(workingDir, "makefile"),
		filepath.Join(workingDir, "GNUmakefile"),
	}
	for _, mf := range makefiles {
		if data, err := os.ReadFile(mf); err == nil {
			content := string(data)

			for _, target := range []string{"verify-ci", "ci", "verify", "test", "build"} {

				if hasMakeTarget(content, target) {
					return "make " + target
				}
			}

			break
		}
	}

	justfiles := []string{
		filepath.Join(workingDir, "Justfile"),
		filepath.Join(workingDir, "justfile"),
		filepath.Join(workingDir, ".justfile"),
	}
	for _, jf := range justfiles {
		if fileExists(jf) {

			if data, err := os.ReadFile(jf); err == nil {
				content := string(data)
				for _, recipe := range []string{"verify-ci", "ci", "verify", "test", "build"} {

					if strings.Contains(content, "\n"+recipe+":") || strings.HasPrefix(content, recipe+":") {
						return "just " + recipe
					}
				}
			}

			continue
		}
	}

	taskfiles := []string{
		filepath.Join(workingDir, "Taskfile.yml"),
		filepath.Join(workingDir, "Taskfile.yaml"),
		filepath.Join(workingDir, ".taskfile.yml"),
	}
	for _, tf := range taskfiles {
		if fileExists(tf) {

			if data, err := os.ReadFile(tf); err == nil {
				content := string(data)

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

			continue
		}
	}

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

func commandAvailable(binary string) bool {
	_, err := exec.LookPath(binary)
	return err == nil
}

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

var codeFileExtensions = map[string]bool{
	".go": true, ".rs": true, ".py": true, ".js": true, ".mjs": true, ".cjs": true,
	".ts": true, ".tsx": true, ".jsx": true, ".java": true, ".rb": true, ".c": true,
	".cc": true, ".cpp": true, ".h": true, ".hpp": true, ".swift": true, ".kt": true,
	".cs": true, ".php": true, ".lua": true, ".dart": true, ".zig": true,
	".ex": true, ".exs": true, ".scala": true, ".m": true, ".mm": true,
}

var codeFileBasenames = map[string]bool{
	"Makefile": true, "makefile": true, "GNUmakefile": true,
	"go.mod": true, "go.sum": true, "package.json": true,
	"Cargo.toml": true, "CMakeLists.txt": true, "pyproject.toml": true,
	"setup.py": true, "Taskfile.yml": true, "Taskfile.yaml": true,
	"Justfile": true, "justfile": true, "mix.exs": true, "Gemfile": true,
}

func isCodeFilePath(path string) bool {
	if codeFileExtensions[strings.ToLower(filepath.Ext(path))] {
		return true
	}
	return codeFileBasenames[filepath.Base(path)]
}

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

func verifyCommandAvailable(command string) bool {

	command = stripEnvAssignments(command)
	parts := strings.Fields(command)
	if len(parts) == 0 {
		return false
	}

	for _, seg := range splitCompoundCommand(command) {

		seg = stripEnvAssignments(strings.TrimSpace(seg))
		segFields := strings.Fields(seg)
		if len(segFields) == 0 || segFields[0] == "cd" {
			continue
		}
		return verifyPrimaryAvailable(segFields)
	}
	return verifyPrimaryAvailable(parts)
}

func verifyPrimaryAvailable(parts []string) bool {
	primary := parts[0]

	if primary == "bash" || primary == "sh" {
		if len(parts) > 1 && !strings.HasPrefix(parts[1], "-") {
			return fileExists(parts[1])
		}
		return true
	}

	switch primary {
	case "source":
		return true
	}

	return commandAvailable(primary)
}

func hasMakeTarget(makefileContent, target string) bool {
	targetPrefix := target + ":"
	for _, line := range strings.Split(makefileContent, "\n") {
		trimmed := strings.TrimLeft(line, " \t")
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.HasPrefix(trimmed, targetPrefix) {

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

func extractFilePathFromArgs(toolName string, args json.RawMessage) string {
	if len(args) == 0 {
		return ""
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(args, &raw); err != nil {
		return ""
	}

	for _, field := range []string{"file_path", "path"} {
		if v, ok := raw[field]; ok {
			var s string
			if json.Unmarshal(v, &s) == nil && s != "" {
				return s
			}
		}
	}

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
		} else {
			var list []string
			if json.Unmarshal(filesRaw, &list) == nil {
				for _, s := range list {
					if s != "" {
						return s
					}
				}
			}
		}
	}

	if v, ok := raw["notebook_path"]; ok {
		var s string
		if json.Unmarshal(v, &s) == nil && s != "" {
			return s
		}
	}
	if v, ok := raw["source"]; ok {
		var s string
		if json.Unmarshal(v, &s) == nil && s != "" {
			return s
		}
	}

	if opsRaw, ok := raw["operations"]; ok {
		var ops []map[string]json.RawMessage
		if json.Unmarshal(opsRaw, &ops) == nil {
			for _, op := range ops {
				if v, ok := op["source"]; ok {
					var s string
					if json.Unmarshal(v, &s) == nil && s != "" {
						return s
					}
				}
			}
		}
	}

	return ""
}

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

	if filesRaw, ok := raw["files"]; ok {
		sfAddMapListStringFields(add, filesRaw, "file_path", "path")
		var plainFiles []string
		if json.Unmarshal(filesRaw, &plainFiles) == nil {
			for _, s := range plainFiles {
				add(s)
			}
		}
	}

	if opsRaw, ok := raw["operations"]; ok {
		sfAddMapListStringFields(add, opsRaw, "source", "destination")
	}

	return out
}

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

func sfAddMapListStringFields(add func(string), rawJSON json.RawMessage, fields ...string) {
	var list []map[string]json.RawMessage
	if json.Unmarshal(rawJSON, &list) != nil {
		return
	}
	for _, entry := range list {
		sfAddStringFields(add, entry, fields...)
	}
}

func isSourceCodeFile(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return sourceCodeExtensions[ext]
}

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
	a.postEditVerify.sourceEditsThisRun++
	a.postEditVerify.lastSourceFileThisRun = filePath

	if a.postEditVerify.sourceEditsSinceHint < postEditVerifyInterval {
		return ""
	}

	if !a.postEditVerify.buildCmdChecked {
		a.postEditVerify.buildCmd = detectBuildSystem(a.workingDir)
		a.postEditVerify.buildCmdChecked = true
	}

	cmd := a.postEditVerify.buildCmd
	if cmd == "" {

		a.postEditVerify.sourceEditsSinceHint = 0
		return ""
	}

	a.postEditVerify.sourceEditsSinceHint = 0

	targeted := targetedVerifyCommand(a.workingDir, filePath)
	fileName := filepath.Base(filePath)

	impact := impactScopedTestCommandMulti(a.workingDir)

	coverageNudge := funcLevelCoverageNudgeMulti(a.workingDir)
	if coverageNudge == "" {
		coverageNudge = testCoverageNudgeMulti(a.workingDir)
	}

	debug.Log("agent", "post-edit verify hint: targeted=%q impact=%q full=%q after %d source-code edits", targeted, impact, cmd, postEditVerifyInterval)

	fastCmd := targeted
	if impact != "" && impact != targeted {
		fastCmd = impact
	}

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

var verifyCommands = map[string]bool{
	"go build": true,
	"go test":  true,
	"go vet":   true,

	"cargo build":   true,
	"cargo test":    true,
	"npm run build": true,
	"npm test":      true,
	"npm run test":  true,
	"pytest":        true,
	"flutter test":  true,
	"cmake":         true,
	"ctest":         true,
	"rake test":     true,

	"bazel test":       true,
	"bzl test":         true,
	"gradle test":      true,
	"gradlew test":     true,
	"mvn test":         true,
	"dotnet test":      true,
	"yarn test":        true,
	"pnpm test":        true,
	"vitest":           true,
	"jest":             true,
	"dart test":        true,
	"composer test":    true,
	"go test -race":    true,
	"python -m pytest": true,

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

func isVerifyCommand(cmd string) bool {
	cmdLower := strings.ToLower(strings.TrimSpace(cmd))
	if cmdLower == "" {
		return false
	}
	if isVerifyCommandSegment(cmdLower) {
		return true
	}

	for _, andSeg := range strings.Split(cmdLower, "&&") {
		for _, seg := range splitCompoundCommand(andSeg) {
			if isVerifyCommandSegment(seg) {
				return true
			}
		}
	}
	return false
}

func (a *Agent) maybeResetVerifyOnCommand(toolName string, args json.RawMessage, resultErr bool) {

	if toolName != "run_command" && toolName != "start_command" {
		return
	}

	cmd := extractCommandFromArgs(args)
	if cmd == "" || !isVerifyCommand(cmd) {
		return
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	a.postEditVerify.sourceEditsSinceHint = 0
	a.postEditVerify.buildOrTestRunThisRun = true

	if isRealTestExecution(cmd) {
		a.postEditVerify.realBuildOrTestRunThisRun = true
	}

	if isRealTestExecution(cmd) && toolName == "run_command" {
		a.postEditVerify.lastBuildFailed = resultErr
	}

	debug.Log("agent", "verify hint counter reset: agent ran build command %q (failed=%v)", cmd, resultErr)
}

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

var envAssignPrefix = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=(?:"[^"]*"|'[^']*'|\S*)\s+`)

func stripEnvAssignments(cmd string) string {
	for {
		next := envAssignPrefix.ReplaceAllString(cmd, "")
		if next == cmd {
			return cmd
		}
		cmd = next
	}
}

func splitCompoundCommand(cmd string) []string {
	var segs []string
	var cur []rune
	quote := rune(0)
	for _, r := range cmd {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			}
			cur = append(cur, r)
		case r == '\'' || r == '"':
			quote = r
			cur = append(cur, r)
		case r == ';' || r == '|' || r == '&':
			if len(cur) > 0 {
				segs = append(segs, string(cur))
				cur = nil
			}
		default:
			cur = append(cur, r)
		}
	}
	if len(cur) > 0 {
		segs = append(segs, string(cur))
	}
	return segs
}

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

			if len(words) >= 2 && makeRunnerNoopTargets[words[1]] {
				return false
			}
			return true
		}
	}
	return false
}

var realTestCommands = map[string]bool{
	"go test": true, "go build": true, "go vet": true,
	"cargo test": true, "cargo build": true,
	"npm test": true, "npm run test": true, "npm run build": true,
	"pytest": true, "python -m pytest": true,
	"flutter test": true, "dart test": true,
	"ctest": true, "rake test": true,
	"bazel test": true, "bzl test": true,
	"gradle test": true, "gradlew test": true,
	"mvn test": true, "dotnet test": true,
	"yarn test": true, "pnpm test": true,
	"vitest": true, "jest": true, "composer test": true,
}

func isRealTestExecution(cmd string) bool {
	for _, seg := range splitCompoundCommand(stripEnvAssignments(cmd)) {
		if isRealTestSegment(strings.ToLower(strings.TrimSpace(seg))) {
			return true
		}
	}
	return false
}

var makeRunnerNoopTargets = map[string]bool{
	"help": true, "list": true, "clean": true, "install": true,
	"fmt": true, "format": true, "tidy": true,
	"deploy": true, "info": true,
}

func isRealTestSegment(seg string) bool {

	if strings.Contains(seg, "--help") || strings.Contains(seg, " -list") || strings.Contains(seg, " --list") ||
		strings.Contains(seg, "help") && strings.HasPrefix(seg, "make ") {
		return false
	}
	for prefix := range realTestCommands {
		if strings.HasPrefix(seg, prefix+" ") || seg == prefix {
			return true
		}
	}
	words := strings.Fields(seg)
	if len(words) >= 2 && (words[0] == "make" || words[0] == "just" || words[0] == "task") {

		t := words[1]
		if strings.HasPrefix(t, "-") || makeRunnerNoopTargets[t] {
			return false
		}
		return true
	}
	return false
}

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

func (a *Agent) resetPostEditVerify() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.postEditVerify = postEditVerifyState{}
}

func targetedVerifyCommand(workingDir, filePath string) string {
	if filePath == "" {
		return ""
	}

	return targetedVerifyCommandMulti(workingDir, filePath)
}

func goTargetedTestCommand(workingDir, filePath string) string {
	abs := filePath
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(workingDir, abs)
	}
	abs = filepath.Clean(abs)

	dir := filepath.Dir(abs)
	modRoot := ""
	for {
		if fileExists(filepath.Join(dir, "go.mod")) {
			modRoot = dir
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
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

	if strings.HasPrefix(rel, "..") {
		return ""
	}
	if rel == "." {
		return "go test ./"
	}
	return "go test ./" + rel + "/"
}
