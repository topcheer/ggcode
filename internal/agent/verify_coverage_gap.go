package agent

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
)

type editCoverageState struct {
	editedFiles   map[string]bool
	verifiedPkgs  map[string]bool
	warnCount     int
	lastWarnedCmd string
	lastEditedPkg string
}

const (
	coverageGapMaxWarns       = 2
	coverageGapMinPackages    = 2
	coverageGapMinEditedFiles = 2
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

func (s *editCoverageState) checkCoverage(toolName, rawArgs string) string {
	if len(s.editedFiles) < coverageGapMinEditedFiles {
		return ""
	}
	if s.warnCount >= coverageGapMaxWarns {
		return ""
	}

	if toolName != "run_command" && toolName != "start_command" {
		return ""
	}

	cmdStr := coverageExtractCommand(rawArgs)
	if cmdStr == "" {
		return ""
	}

	if cmdStr == s.lastWarnedCmd {
		return ""
	}

	scopes := coverageExtractVerifyScopes(cmdStr)
	if len(scopes) == 0 {
		return ""
	}

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

		for _, pkg := range editedPkgs {
			s.verifiedPkgs[pkg] = true
		}
		return ""
	}

	if hasDot && s.lastEditedPkg != "" {
		s.verifiedPkgs[s.lastEditedPkg] = true
	}

	var uncovered []string
	for _, pkg := range editedPkgs {

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

			if toolName == "run_command" {
				s.verifiedPkgs[pkg] = true
			}
			continue
		}
		uncovered = append(uncovered, pkg)
	}

	if len(uncovered) == 0 {
		return ""
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

func coverageIsEditTool(toolName string) bool {
	return sourceMutatingTools[toolName]
}

func coverageIsVerifyTool(toolName string) bool {
	switch toolName {
	case "run_command", "start_command":
		return true
	default:
		return false
	}
}

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
	case "batch_replace":

		if filesRaw, ok := raw["files"]; ok {
			var files []string
			if json.Unmarshal(filesRaw, &files) == nil {
				for _, f := range files {
					if f != "" {
						paths = append(paths, f)
					}
				}
			}
		}
	case "lsp_rename":

		if fp, ok := coverageGetStr(raw, "path"); ok {
			paths = append(paths, fp)
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

func coverageExtractCommand(args string) string {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(args), &raw); err != nil {
		return ""
	}
	if cmdRaw, ok := raw["command"]; ok {
		var cmd string
		if json.Unmarshal(cmdRaw, &cmd) == nil {

			return strings.TrimSpace(stripLeadingShellComment(cmd))
		}
	}
	return ""
}

func coverageExtractVerifyScope(cmd string) string {
	scopes := coverageExtractVerifyScopes(cmd)
	if len(scopes) == 0 {
		return ""
	}
	return scopes[0]
}

func coverageExtractVerifyScopes(cmd string) []string {
	lc := strings.ToLower(cmd)

	if !coverageIsVerifyCommand(lc) {
		return nil
	}

	fields := strings.Fields(stripEnvAssignments(lc))

	var scopes []string
	for _, f := range fields {
		if strings.Contains(f, "...") && (strings.HasPrefix(f, "./") || strings.HasPrefix(f, "../")) {

			prefix := strings.TrimSuffix(f, "...")
			prefix = strings.TrimSuffix(prefix, "/")
			prefix = strings.TrimPrefix(prefix, "./")
			if prefix == "" || prefix == "." {
				return []string{"ALL"}
			}
			scopes = append(scopes, prefix)
			continue
		}

		if strings.HasSuffix(f, ".go") {
			if idx := strings.LastIndex(f, "/"); idx > 0 {
				f = f[:idx]
			} else {
				continue
			}
		}
		isRel := strings.HasPrefix(f, "./") || strings.HasPrefix(f, "../")
		if !isRel {

			if strings.HasPrefix(f, "-") || !strings.Contains(f, "/") ||
				strings.Contains(f, ":") || !isASCIIPathStart(f) ||
				firstSegHasDot(f) {
				continue
			}
		}
		if !isRel && !strings.HasSuffix(f, "/") && !strings.Contains(f, "/") {
			continue
		}

		if strings.HasSuffix(f, "...") {
			f = strings.TrimSuffix(strings.TrimSuffix(f, "..."), "/")
		}
		r := strings.TrimRight(f, "/")
		r = strings.TrimPrefix(r, "./")
		if r != "" && r != "." {

			scopes = append(scopes, r)
		} else if isRel {

			scopes = append(scopes, ".")
		}
	}
	if len(scopes) > 0 {
		return scopes
	}

	for _, f := range fields {
		if strings.HasSuffix(f, ".py") || strings.HasSuffix(f, ".js") || strings.HasSuffix(f, ".ts") {
			return nil
		}
	}

	if len(fields) >= 2 && fields[0] == "go" {
		switch fields[1] {
		case "test", "build", "vet":

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

func coverageIsVerifyCommand(lc string) bool {

	lc = stripEnvAssignments(lc)
	fields := strings.Fields(lc)
	if len(fields) == 0 {
		return false
	}
	first := fields[0]
	isRunner := false
	switch first {
	case "go", "make", "npm", "yarn", "pnpm", "pytest", "cargo", "mvn", "mvnw", "./mvnw", "gradle", "./gradlew", "dotnet", "tox", "nox":
		isRunner = true
	}
	if !isRunner {
		return false
	}

	verifyMarkers := []string{
		"test", "build", "vet", "check", "lint", "verify", "clippy", "e2e",
	}
	for _, f := range fields[1:] {
		f = strings.TrimPrefix(f, "run:")
		f = strings.TrimSuffix(f, ";")
		for _, m := range verifyMarkers {
			if f == m || strings.HasPrefix(f, m+"-") || strings.HasPrefix(f, m+"_") {
				return true
			}
		}
	}
	return false
}

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

func coverageFileToPackage(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}

	if filepath.Ext(path) != ".go" {
		return ""
	}
	dir := path
	if idx := strings.LastIndexByte(path, '/'); idx >= 0 {
		dir = path[:idx]
	}

	if dir == path {
		return ""
	}
	if dir == "" || dir == "." {
		return ""
	}
	dir = strings.TrimPrefix(dir, "./")
	return dir
}

func coveragePkgInScope(pkg, scope string) bool {
	if scope == "ALL" {
		return true
	}
	if scope == "." {

		return false
	}

	pkgNorm := strings.TrimPrefix(pkg, "./")
	scopeNorm := strings.TrimPrefix(scope, "./")
	scopeNorm = strings.TrimRight(scopeNorm, "/")
	pkgNorm = strings.TrimRight(pkgNorm, "/")

	if pkgNorm == scopeNorm {
		return true
	}
	if strings.HasPrefix(pkgNorm, scopeNorm+"/") {
		return true
	}

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

func firstSegHasDot(f string) bool {
	i := strings.IndexByte(f, '/')
	if i < 0 {
		return false
	}
	return strings.Contains(f[:i], ".")
}
