package agent

// verify_coverage_gap.go -- Verification Coverage Gap Detector
//
// Research basis:
//   - "Beyond the Leaderboard" (arXiv:2607.05775, 2026): synthesizes 27 papers
//     and identifies that "strong performance on individual sub-tasks does not
//     reliably translate into end-to-end success" and "failures compound
//     nonlinearly with task length." A key mechanism is that agents verify
//     edits in isolation (one package) but never verify the integration across
//     ALL edited packages.
//   - SWE-bench trajectory analyses (2025): show that agents frequently edit
//     files in multiple packages but run `go test` or `npm test` scoped to
//     only the last-touched directory, silently missing cross-package
//     regressions.
//
// Problem: AI coding agents edit files across 2+ distinct package directories,
// then run a verification command (go test, go build, npm test) scoped to a
// SINGLE package. The verification passes for that package, but files in other
// packages remain unverified. The agent declares success based on partial
// coverage -- the "sub-task success ≠ end-to-end success" trap.
//
// Example failure pattern:
//   1. Agent edits internal/agent/foo.go         (package A)
//   2. Agent edits internal/config/bar.go         (package B)
//   3. Agent edits internal/tool/baz.go           (package C)
//   4. Agent runs: go test ./internal/agent/      (covers A only)
//   5. Agent runs: go build ./internal/agent/     (covers A only)
//   6. Agent declares success
//   → Packages B and C are NEVER verified.
//
// This is distinct from existing detectors:
//   - bare_edit_streak.go: checks if NO verification was run at all.
//     This checks whether verification COVERS all edited packages.
//   - verify_scope_decay.go: tracks narrowing scope over TIME (trajectory).
//     This checks coverage at each verification point (spatial).
//   - verify_scope_narrow.go: detects command argument narrowing (gaming).
//     This checks whether verification scope matches edited scope (mismatch).
//   - change_reconcile.go: checks stated vs actual file changes.
//     This checks verified vs edited files (verification mismatch).
//   - test_impact.go: SUGGESTS which tests to run.
//     This DETECTS when verification didn't cover edited files.
//
// Detection approach (zero LLM cost, deterministic):
//   - Track all file paths edited via edit/write tools
//   - Extract package directories from edited file paths
//   - When a verification command runs, extract its scope (package path)
//   - If 2+ packages were edited but verification covers a strict subset,
//     inject a warning listing the unverified packages

import (
	"encoding/json"
	"fmt"
	"strings"
)

type editCoverageState struct {
	editedFiles   map[string]bool // set of edited file paths
	verifiedPkgs  map[string]bool // set of package dirs covered by verification
	warnCount     int             // warnings emitted this run
	lastWarnedCmd string          // dedupe: don't re-warn for same verification command
	lastEditedPkg string          // package dir of the most recent edit (#417: bare `go test` cwd heuristic)
}

const (
	coverageGapMaxWarns       = 2 // cap warnings per run
	coverageGapMinPackages    = 2 // need 2+ edited packages to trigger
	coverageGapMinEditedFiles = 2 // need 2+ edited files to trigger
)

func newEditCoverageState() *editCoverageState {
	return &editCoverageState{
		editedFiles:  make(map[string]bool),
		verifiedPkgs: make(map[string]bool),
	}
}

func (s *editCoverageState) reset() {
	s.editedFiles = make(map[string]bool)
	s.verifiedPkgs = make(map[string]bool)
	s.warnCount = 0
	s.lastWarnedCmd = ""
	s.lastEditedPkg = ""
}

// recordToolCall tracks edits and verification calls.
// Returns a warning string if a coverage gap is detected.
func (s *editCoverageState) recordToolCall(toolName string, args string) string {
	switch {
	case coverageIsEditTool(toolName):
		s.recordEditedFiles(toolName, args)
		return ""
	case coverageIsVerifyTool(toolName):
		return s.checkCoverage(toolName, args)
	default:
		return ""
	}
}

// recordEditedFiles extracts file paths from edit tool arguments.
func (s *editCoverageState) recordEditedFiles(toolName, args string) {
	paths := coverageExtractFilePaths(toolName, args)
	for _, p := range paths {
		if p != "" {
			s.editedFiles[p] = true
			if pkg := coverageFileToPackage(p); pkg != "" {
				s.lastEditedPkg = pkg
			}
		}
	}
}

// checkCoverage examines whether a verification command covers all edited packages.
func (s *editCoverageState) checkCoverage(toolName, rawArgs string) string {
	if len(s.editedFiles) < coverageGapMinEditedFiles {
		return ""
	}
	if s.warnCount >= coverageGapMaxWarns {
		return ""
	}

	// Only check for run_command-based verification (build/test commands)
	if toolName != "run_command" && toolName != "start_command" {
		return ""
	}

	cmdStr := coverageExtractCommand(rawArgs)
	if cmdStr == "" {
		return ""
	}

	// Dedupe: don't re-warn for the same command
	if cmdStr == s.lastWarnedCmd {
		return ""
	}

	// Determine verification scope(s) — multi-path commands cover ALL listed
	// packages (#417: `go test ./a/ ./b/` previously only extracted ./a/).
	scopes := coverageExtractVerifyScopes(cmdStr)
	if len(scopes) == 0 {
		return ""
	}

	// Find edited packages NOT covered by the verification scope
	editedPkgs := coveragePackagesFromFileSet(s.editedFiles)
	if len(editedPkgs) < coverageGapMinPackages {
		return ""
	}

	isAll := false
	hasDot := false
	for _, sc := range scopes {
		if sc == "ALL" {
			isAll = true
		}
		if sc == "." {
			hasDot = true
		}
	}
	if isAll {
		// ./... covers every edited package — remember them (#417).
		for _, pkg := range editedPkgs {
			s.verifiedPkgs[pkg] = true
		}
		return ""
	}
	// #417: a bare `go test`/`go build` verifies the current-directory
	// package. The cwd is unknown to the detector; the best deterministic
	// proxy is the package of the most recent edit (agents verify right
	// after editing that package).
	if hasDot && s.lastEditedPkg != "" {
		s.verifiedPkgs[s.lastEditedPkg] = true
	}

	var uncovered []string
	for _, pkg := range editedPkgs {
		// Verified by an EARLIER run — cumulative coverage (#417: sequential
		// per-package verification is a legitimate strategy and must not be
		// re-warned as UNVERIFIED on every subsequent command).
		if s.verifiedPkgs[pkg] {
			continue
		}
		covered := false
		for _, sc := range scopes {
			if sc != "." && coveragePkgInScope(pkg, sc) {
				covered = true
				break
			}
		}
		if covered {
			s.verifiedPkgs[pkg] = true // remember for subsequent runs (#417)
			continue
		}
		uncovered = append(uncovered, pkg)
	}

	if len(uncovered) == 0 {
		return "" // all packages covered
	}

	s.warnCount++
	s.lastWarnedCmd = cmdStr

	return fmt.Sprintf(
		"[verification-coverage-gap] You edited files across %d package directories "+
			"but your verification command `%s` only covers `%s`. The following edited "+
			"packages remain UNVERIFIED:\n  - %s\n\n"+
			"Re-run verification with broader scope (e.g., `go test ./...` or "+
			"`go build ./...`) to catch cross-package regressions before declaring success. "+
			"(Research: sub-task verification success does not guarantee end-to-end success -- "+
			"arXiv:2607.05775)",
		len(editedPkgs), cmdStr, strings.Join(scopes, ", "),
		strings.Join(uncovered, "\n  - "),
	)
}

// coverageIsEditTool returns true for tools that modify files.
func coverageIsEditTool(toolName string) bool {
	switch toolName {
	case "edit_file", "multi_edit_file", "write_file", "multi_file_write",
		"multi_file_edit", "file_ops", "notebook_edit":
		return true
	default:
		return false
	}
}

// coverageIsVerifyTool returns true for tools that run verification commands.
func coverageIsVerifyTool(toolName string) bool {
	switch toolName {
	case "run_command", "start_command":
		return true
	default:
		return false
	}
}

// coverageExtractFilePaths extracts file paths from edit tool JSON arguments.
func coverageExtractFilePaths(toolName, args string) []string {
	var paths []string
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(args), &raw); err != nil {
		return paths
	}

	switch toolName {
	case "edit_file", "multi_edit_file":
		if fp, ok := coverageGetStr(raw, "file_path"); ok {
			paths = append(paths, fp)
		}
	case "write_file":
		if fp, ok := coverageGetStr(raw, "path"); ok {
			paths = append(paths, fp)
		}
	case "multi_file_write", "multi_file_edit":
		if filesRaw, ok := raw["files"]; ok {
			var files []struct {
				Path string `json:"path"`
			}
			if json.Unmarshal(filesRaw, &files) == nil {
				for _, f := range files {
					if f.Path != "" {
						paths = append(paths, f.Path)
					}
				}
			}
		}
	case "file_ops":
		if opsRaw, ok := raw["operations"]; ok {
			var ops []struct {
				Source string `json:"source"`
			}
			if json.Unmarshal(opsRaw, &ops) == nil {
				for _, op := range ops {
					if op.Source != "" {
						paths = append(paths, op.Source)
					}
				}
			}
		}
	}
	return paths
}

// coverageGetStr is a helper to extract a string field from raw JSON.
func coverageGetStr(raw map[string]json.RawMessage, key string) (string, bool) {
	v, ok := raw[key]
	if !ok {
		return "", false
	}
	var s string
	if json.Unmarshal(v, &s) != nil {
		return "", false
	}
	return s, true
}

// coverageExtractCommand extracts the command string from run_command args.
func coverageExtractCommand(args string) string {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(args), &raw); err != nil {
		return ""
	}
	if cmdRaw, ok := raw["command"]; ok {
		var cmd string
		if json.Unmarshal(cmdRaw, &cmd) == nil {
			return strings.TrimSpace(cmd)
		}
	}
	return ""
}

// coverageExtractVerifyScope determines the package scope from a command string.
// Returns "" if not a verification command or scope can't be determined.
// Returns "ALL" for ./... scoped commands.
//
// Project-wide opaque runners (make/npm test/pytest/cargo test/...) return ""
// (skip): their actual coverage is unknowable from the command text — assuming
// zero coverage (".") systematically false-positived and exhausted the warning
// budget, muting real gaps (#354). Only bare `go test`/`go build`/`go vet`
// semantically mean "current directory package".
func coverageExtractVerifyScope(cmd string) string {
	scopes := coverageExtractVerifyScopes(cmd)
	if len(scopes) == 0 {
		return ""
	}
	return scopes[0]
}

// coverageExtractVerifyScopes extracts ALL package scopes from a verify
// command (#417: `go test ./internal/agent/ ./internal/config/` previously
// only yielded the first ./path — config was falsely warned UNVERIFIED).
// Returns "ALL" when ./... (or bare "./") is present, "." for bare go
// test/build/vet (current-dir package), and nil when no scope is extractable.
func coverageExtractVerifyScopes(cmd string) []string {
	lc := strings.ToLower(cmd)

	// Must be a build/test/lint command
	if !coverageIsVerifyCommand(lc) {
		return nil
	}

	fields := strings.Fields(lc)

	// Collect every ./-prefixed token as a scope.
	var scopes []string
	for _, f := range fields {
		if !strings.HasPrefix(f, "./") {
			continue
		}
		if strings.Contains(f, "...") {
			return []string{"ALL"}
		}
		r := strings.TrimRight(f, "/")
		r = strings.TrimPrefix(r, "./")
		if r != "" {
			// #550 B1: any NAMED directory is a real package scope — "./db/"
			// previously failed the len>2 test and inflated to ALL, marking
			// every edited package VERIFIED and masking the exact coverage
			// gap this detector exists to surface.
			scopes = append(scopes, r)
		} else {
			return []string{"ALL"} // "./" alone
		}
	}
	if len(scopes) > 0 {
		return scopes
	}

	// Non-Go runners with file args (pytest tests/unit/test_x.py): the .py
	// path is a file, not a package scope — skip rather than mis-scope (#354).
	for _, f := range fields {
		if strings.HasSuffix(f, ".py") || strings.HasSuffix(f, ".js") || strings.HasSuffix(f, ".ts") {
			return nil
		}
	}

	// Bare Go commands without a path cover the current directory package only.
	// Word-lexical match (first token) so "git commit -m 'make test pass'"
	// does not count as a verification run (#354).
	if len(fields) >= 2 && fields[0] == "go" {
		switch fields[1] {
		case "test", "build", "vet":
			// Fully-qualified import path args (github.com/x/y) cannot
			// be mapped to relative package dirs — skip (#354).
			for _, f := range fields[2:] {
				if strings.Contains(f, ".") && strings.Contains(f, "/") {
					return nil
				}
			}
			return []string{"."}
		}
	}

	return nil
}

// coverageIsVerifyCommand checks if the command is a build/test/lint command.
// Lexical (token-prefix) matching: substring Contains matched inside commit
// messages ("git commit -m 'make test pass'") and other noise (#354).
func coverageIsVerifyCommand(lc string) bool {
	fields := strings.Fields(lc)
	if len(fields) == 0 {
		return false
	}
	first := fields[0]
	isRunner := false
	switch first {
	case "go", "make", "npm", "yarn", "pnpm", "pytest", "cargo", "mvn", "gradle", "./gradlew", "dotnet", "tox", "nox":
		isRunner = true
	}
	if !isRunner {
		return false
	}
	// For direct invocations like "go test ..." require a verify subcommand;
	// for "make" accept any target starting with a verify-ish word (target
	// itself may be "verify-ci" — the marker list below still applies as a
	// suffix check on the whole command).
	verifyMarkers := []string{
		"test", "build", "vet", "check", "lint", "verify", "clippy", "e2e",
	}
	for _, f := range fields[1:] {
		f = strings.TrimPrefix(f, "run:") // npm/yarn "run:test"
		f = strings.TrimSuffix(f, ";")
		for _, m := range verifyMarkers {
			if f == m || strings.HasPrefix(f, m+"-") || strings.HasPrefix(f, m+"_") {
				return true
			}
		}
	}
	return false
}

// coveragePackagesFromFileSet extracts distinct package directories from file paths.
func coveragePackagesFromFileSet(files map[string]bool) []string {
	pkgSet := make(map[string]bool)
	for f := range files {
		pkg := coverageFileToPackage(f)
		if pkg != "" {
			pkgSet[pkg] = true
		}
	}
	var pkgs []string
	for p := range pkgSet {
		pkgs = append(pkgs, p)
	}
	return pkgs
}

// coverageFileToPackage converts a file path to its package directory.
// e.g., "/workspace/internal/agent/foo.go" → "internal/agent"
func coverageFileToPackage(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	dir := path
	if idx := strings.LastIndexByte(path, '/'); idx >= 0 {
		dir = path[:idx]
	}
	// Bare filename like "baz.go" -> no directory
	if dir == path {
		return ""
	}
	if dir == "" || dir == "." {
		return ""
	}
	dir = strings.TrimPrefix(dir, "./")
	return dir
}

// coveragePkgInScope checks if a package directory falls within the verification scope.
func coveragePkgInScope(pkg, scope string) bool {
	if scope == "ALL" {
		return true
	}
	if scope == "." {
		// Bare scope — handled by the caller via the lastEditedPkg cwd
		// heuristic (#417); a "." scope never matches a named package here.
		return false
	}
	// Normalize for comparison
	pkgNorm := strings.TrimPrefix(pkg, "./")
	scopeNorm := strings.TrimPrefix(scope, "./")
	scopeNorm = strings.TrimRight(scopeNorm, "/")
	pkgNorm = strings.TrimRight(pkgNorm, "/")

	// Exact match or pkg is under scope dir
	if pkgNorm == scopeNorm {
		return true
	}
	if strings.HasPrefix(pkgNorm, scopeNorm+"/") {
		return true
	}
	// #416: absolute-path edits ("/workspace/internal/agent") never matched
	// relative verify scopes ("internal/agent") — fully verified packages
	// were warned UNVERIFIED in the most common production workflow. When one
	// side is absolute and the other relative, compare on directory-segment
	// containment (scope appears as a complete sub-path of pkg).
	pkgAbs := strings.HasPrefix(pkgNorm, "/")
	scopeAbs := strings.HasPrefix(scopeNorm, "/")
	if pkgAbs != scopeAbs {
		tail := strings.TrimPrefix(pkgNorm, "/")
		head := strings.TrimPrefix(scopeNorm, "/")
		if head == "" || tail == "" {
			return false
		}
		return strings.Contains("/"+tail+"/", "/"+head+"/")
	}
	return false
}
