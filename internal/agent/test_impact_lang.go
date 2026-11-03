package agent

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

type langProfile struct {
	Name            string
	Extensions      []string
	IsTestFile      func(string) bool
	TestFilePaths   func(workingDir, srcFile string) []string
	TargetedTestCmd func(workingDir, srcFile string) string
	FuncRegex       *regexp.Regexp
	SkipFuncNames   *regexp.Regexp
}

var goLangProfile = langProfile{
	Name:       "go",
	Extensions: []string{".go"},
	IsTestFile: func(name string) bool {
		return strings.HasSuffix(name, "_test.go")
	},
	TestFilePaths: func(workingDir, srcFile string) []string {
		base := filepath.Base(srcFile)
		name := strings.TrimSuffix(base, ".go")
		dir := filepath.Dir(srcFile)
		return []string{filepath.Join(dir, name+"_test.go")}
	},
	TargetedTestCmd: func(workingDir, srcFile string) string {
		return goTargetedTestCommand(workingDir, srcFile)
	},
}

var tsFuncRegex = regexp.MustCompile(

	`(?:export\s+(?:async\s+)?(?:function|const)\s+(\w+))|(?:export\s+class\s+(\w+))|(?:^|\n)\s*(?:public|private|protected)?\s*(\w+)\s*\([^)]*\)\s*\{`,
)

var tsSkipFunc = regexp.MustCompile(`^(test|it|describe|beforeEach|afterEach|beforeAll|afterAll|constructor|render|main|default)$`)

var tsLangProfile = langProfile{
	Name:       "typescript",
	Extensions: []string{".ts", ".tsx", ".js", ".jsx"},
	IsTestFile: func(name string) bool {
		lower := strings.ToLower(name)
		return strings.HasSuffix(lower, ".test.ts") ||
			strings.HasSuffix(lower, ".test.tsx") ||
			strings.HasSuffix(lower, ".test.js") ||
			strings.HasSuffix(lower, ".test.jsx") ||
			strings.HasSuffix(lower, ".spec.ts") ||
			strings.HasSuffix(lower, ".spec.tsx") ||
			strings.HasSuffix(lower, ".spec.js") ||
			strings.HasSuffix(lower, ".spec.jsx") ||
			strings.HasSuffix(name, "__tests__.ts") ||
			strings.HasSuffix(name, "__tests__.js")
	},
	TestFilePaths: func(workingDir, srcFile string) []string {
		dir := filepath.Dir(srcFile)
		base := filepath.Base(srcFile)
		ext := filepath.Ext(base)
		nameNoExt := strings.TrimSuffix(base, ext)
		return []string{
			filepath.Join(dir, nameNoExt+".test"+ext),
			filepath.Join(dir, nameNoExt+".spec"+ext),
			filepath.Join(dir, "__tests__", nameNoExt+".test"+ext),
		}
	},
	TargetedTestCmd: func(workingDir, srcFile string) string {
		runner := detectJSRunner(workingDir)
		if runner == "" {
			return ""
		}
		dir := filepath.Dir(srcFile)

		return runner + " " + filepath.ToSlash(dir)
	},
	FuncRegex:     tsFuncRegex,
	SkipFuncNames: tsSkipFunc,
}

func detectJSRunner(workingDir string) string {
	data, err := os.ReadFile(filepath.Join(workingDir, "package.json"))
	if err != nil {
		return ""
	}
	content := string(data)

	if strings.Contains(content, "vitest") {
		return "npx vitest run"
	}

	if strings.Contains(content, "jest") {
		return "npx jest"
	}

	if strings.Contains(content, "\"test\"") {
		return "npm test"
	}
	return ""
}

var pyFuncRegex = regexp.MustCompile(

	`(?:^|\n)\s*(?:async\s+)?def\s+(\w+)`,
)

var pySkipFunc = regexp.MustCompile(`^(__init__|__main__|setUp|tearDown|setUpClass|tearDownClass|main|_.*|test_.*)$`)

var pyLangProfile = langProfile{
	Name:       "python",
	Extensions: []string{".py"},
	IsTestFile: func(name string) bool {
		lower := strings.ToLower(name)
		return strings.HasPrefix(lower, "test_") ||
			strings.HasSuffix(lower, "_test.py") ||
			strings.HasSuffix(lower, "_spec.py")
	},
	TestFilePaths: func(workingDir, srcFile string) []string {
		dir := filepath.Dir(srcFile)
		base := filepath.Base(srcFile)
		name := strings.TrimSuffix(base, ".py")
		testsDir := filepath.Join(dir, "tests")
		return []string{
			filepath.Join(dir, "test_"+name+".py"),
			filepath.Join(dir, name+"_test.py"),
			filepath.Join(testsDir, "test_"+name+".py"),
			filepath.Join(testsDir, name+"_test.py"),
		}
	},
	TargetedTestCmd: func(workingDir, srcFile string) string {

		if fileExists(filepath.Join(workingDir, "pyproject.toml")) ||
			fileExists(filepath.Join(workingDir, "pytest.ini")) ||
			fileExists(filepath.Join(workingDir, "setup.py")) ||
			fileExists(filepath.Join(workingDir, "tox.ini")) {
			dir := filepath.Dir(srcFile)
			return "python -m pytest " + filepath.ToSlash(dir)
		}
		return ""
	},
	FuncRegex:     pyFuncRegex,
	SkipFuncNames: pySkipFunc,
}

var rustFuncRegex = regexp.MustCompile(

	`(?:^|\n)\s*pub\s+(?:async\s+)?fn\s+(\w+)`,
)

var rustSkipFunc = regexp.MustCompile(`^(main|test_.*)$`)

var rustLangProfile = langProfile{
	Name:       "rust",
	Extensions: []string{".rs"},
	IsTestFile: func(name string) bool {

		return strings.HasSuffix(name, "_test.rs") || strings.HasSuffix(name, "test.rs")
	},
	TestFilePaths: func(workingDir, srcFile string) []string {
		dir := filepath.Dir(srcFile)
		base := filepath.Base(srcFile)
		name := strings.TrimSuffix(base, ".rs")
		return []string{
			filepath.Join(dir, name+"_test.rs"),
			filepath.Join(dir, "tests", name+"_test.rs"),
			filepath.Join(filepath.Dir(dir), "tests", name+".rs"),
		}
	},
	TargetedTestCmd: func(workingDir, srcFile string) string {

		if fileExists(filepath.Join(workingDir, "Cargo.toml")) {
			return "cargo test"
		}
		return ""
	},
	FuncRegex:     rustFuncRegex,
	SkipFuncNames: rustSkipFunc,
}

var javaFuncRegex = regexp.MustCompile(

	`(?:public|protected)\s+(?:static\s+)?(?:\w+(?:<[^>]*>)?\s+)+(\w+)\s*\(`,
)

var javaSkipFunc = regexp.MustCompile(`^(main|toString|equals|hashCode|compareTo|setUp|tearDown)$`)

var javaLangProfile = langProfile{
	Name:       "java",
	Extensions: []string{".java"},
	IsTestFile: func(name string) bool {
		return strings.HasSuffix(name, "Test.java") ||
			strings.HasSuffix(name, "Tests.java") ||
			strings.HasSuffix(name, "IT.java") ||
			strings.HasSuffix(name, "Spec.java")
	},
	TestFilePaths: func(workingDir, srcFile string) []string {
		dir := filepath.Dir(srcFile)
		base := filepath.Base(srcFile)
		name := strings.TrimSuffix(base, ".java")

		testDir := dir
		if i := strings.Index(dir, filepath.Join("src", "main", "java")); i >= 0 {
			testDir = dir[:i] + filepath.Join("src", "test", "java") + dir[i+len(filepath.Join("src", "main", "java")):]
		}
		return []string{
			filepath.Join(testDir, name+"Test.java"),
			filepath.Join(testDir, name+"Tests.java"),
			filepath.Join(testDir, name+"IT.java"),
		}
	},
	TargetedTestCmd: func(workingDir, srcFile string) string {
		base := filepath.Base(srcFile)
		name := strings.TrimSuffix(base, ".java")
		if fileExists(filepath.Join(workingDir, "pom.xml")) {
			return "mvn test -Dtest=" + name + "Test"
		}
		if fileExists(filepath.Join(workingDir, "build.gradle")) ||
			fileExists(filepath.Join(workingDir, "build.gradle.kts")) {
			return "gradle test --tests *" + name + "Test"
		}
		return ""
	},
	FuncRegex:     javaFuncRegex,
	SkipFuncNames: javaSkipFunc,
}

var rubyFuncRegex = regexp.MustCompile(

	`(?:^|\n)\s*def\s+(?:self\.)?(\w+)`,
)

var rubySkipFunc = regexp.MustCompile(`^(initialize|test_.*)$`)

var rubyLangProfile = langProfile{
	Name:       "ruby",
	Extensions: []string{".rb"},
	IsTestFile: func(name string) bool {
		return strings.HasSuffix(name, "_test.rb") || strings.HasSuffix(name, "_spec.rb")
	},
	TestFilePaths: func(workingDir, srcFile string) []string {
		dir := filepath.Dir(srcFile)
		base := filepath.Base(srcFile)
		name := strings.TrimSuffix(base, ".rb")
		return []string{
			filepath.Join(dir, name+"_test.rb"),
			filepath.Join(dir, name+"_spec.rb"),
			filepath.Join(dir, "test", name+"_test.rb"),
			filepath.Join(dir, "spec", name+"_spec.rb"),
		}
	},
	TargetedTestCmd: func(workingDir, srcFile string) string {
		dir := filepath.Dir(srcFile)

		isRSpecProject := fileExists(filepath.Join(workingDir, ".rspec")) ||
			fileExists(filepath.Join(workingDir, "spec"))
		for _, seg := range strings.Split(filepath.ToSlash(dir), "/") {
			if seg == "spec" {
				isRSpecProject = true
			}
		}
		if isRSpecProject && strings.HasSuffix(srcFile, "_spec.rb") {
			return "rspec " + filepath.ToSlash(srcFile)
		}
		return "bundle exec rake test"
	},
	FuncRegex:     rubyFuncRegex,
	SkipFuncNames: rubySkipFunc,
}

var swiftFuncRegex = regexp.MustCompile(

	`(?:^|\n)\s*(?:public\s+|static\s+|class\s+)*func\s+(\w+)`,
)

var swiftSkipFunc = regexp.MustCompile(`^(main|setUp|tearDown|test.*)$`)

var swiftLangProfile = langProfile{
	Name:       "swift",
	Extensions: []string{".swift"},
	IsTestFile: func(name string) bool {
		return strings.HasSuffix(name, "Tests.swift") || strings.HasSuffix(name, "Test.swift")
	},
	TestFilePaths: func(workingDir, srcFile string) []string {
		dir := filepath.Dir(srcFile)
		base := filepath.Base(srcFile)
		name := strings.TrimSuffix(base, ".swift")
		return []string{
			filepath.Join(dir, name+"Tests.swift"),
			filepath.Join(dir, "Tests", name+"Tests.swift"),
		}
	},
	TargetedTestCmd: func(workingDir, srcFile string) string {

		if fileExists(filepath.Join(workingDir, "Package.swift")) {
			return "swift test"
		}

		return ""
	},
	FuncRegex:     swiftFuncRegex,
	SkipFuncNames: swiftSkipFunc,
}

var dartFuncRegex = regexp.MustCompile(

	`(?:^|\n)\s*(?:static\s+)?(?:\w+(?:<[^>]*>)?\s+)?(\w+)\s*\([^)]*\)\s*(?:async\s*)?\{`,
)

var dartSkipFunc = regexp.MustCompile(`^(main|setUp|tearDown|test.*)$`)

var dartLangProfile = langProfile{
	Name:       "dart",
	Extensions: []string{".dart"},
	IsTestFile: func(name string) bool {
		return strings.HasSuffix(name, "_test.dart")
	},
	TestFilePaths: func(workingDir, srcFile string) []string {
		dir := filepath.Dir(srcFile)
		base := filepath.Base(srcFile)
		name := strings.TrimSuffix(base, ".dart")

		abs := srcFile
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(workingDir, abs)
		}
		if rel, err := filepath.Rel(workingDir, abs); err == nil && strings.HasPrefix(rel, "lib"+string(filepath.Separator)) {

			sub := strings.TrimSuffix(strings.TrimPrefix(rel, "lib"+string(filepath.Separator)), ".dart")
			return []string{
				filepath.Join("test", sub+"_test.dart"),
				filepath.Join(dir, name+"_test.dart"),
			}
		}
		return []string{
			filepath.Join(dir, name+"_test.dart"),
			filepath.Join(dir, "test", name+"_test.dart"),
		}
	},
	TargetedTestCmd: func(workingDir, srcFile string) string {
		dir := filepath.Dir(srcFile)
		if fileExists(filepath.Join(workingDir, "pubspec.yaml")) {

			data, err := os.ReadFile(filepath.Join(workingDir, "pubspec.yaml"))
			if err == nil && strings.Contains(string(data), "flutter") {
				return "flutter test " + filepath.ToSlash(dir)
			}
			return "dart test " + filepath.ToSlash(dir)
		}
		return ""
	},
	FuncRegex:     dartFuncRegex,
	SkipFuncNames: dartSkipFunc,
}

var controlFlowKeywords = map[string]bool{
	"if": true, "for": true, "while": true, "switch": true,
	"catch": true, "else": true, "do": true, "try": true,
	"return": true, "new": true, "delete": true, "await": true,
	"yield": true, "match": true, "loop": true, "when": true,
	"case": true, "break": true, "continue": true, "throw": true,
	"finally": true, "async": true, "class": true, "interface": true,
	"type": true, "enum": true, "import": true, "export": true,
	"from": true, "const": true, "let": true, "var": true,
	"function": true, "void": true, "bool": true, "int": true,
	"double": true, "String": true, "dynamic": true, "Object": true,
}

func isControlFlowKeyword(name string) bool {
	return controlFlowKeywords[name]
}

func allLangProfiles() []langProfile {
	return []langProfile{
		goLangProfile,
		tsLangProfile,
		pyLangProfile,
		rustLangProfile,
		javaLangProfile,
		rubyLangProfile,
		swiftLangProfile,
		dartLangProfile,
	}
}

func langProfileForFile(filePath string) *langProfile {
	ext := strings.ToLower(filepath.Ext(filePath))
	if ext == "" {
		return nil
	}
	for i, p := range allLangProfiles() {
		for _, e := range p.Extensions {
			if ext == e {
				return &allLangProfiles()[i]
			}
		}
	}
	return nil
}

func isSupportedSourceFile(path string) bool {
	p := langProfileForFile(path)
	if p == nil {
		return false
	}

	if p.IsTestFile(filepath.Base(path)) {
		return false
	}
	return true
}

func changedSourceFilesFromGit(workingDir string) []string {
	files := changedFilesFromGitRaw(workingDir)
	var result []string
	for _, f := range files {
		if isSupportedSourceFile(f) {
			result = append(result, f)
		}
	}
	return result
}

func changedFilesFromGitRaw(workingDir string) []string {
	if workingDir == "" {
		return nil
	}
	out, err := runGitStatusShort(workingDir)
	if err != nil || len(out) == 0 {
		return nil
	}
	var files []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || len(line) < 4 {
			continue
		}
		path := strings.TrimSpace(line[3:])
		if idx := strings.LastIndex(path, " -> "); idx >= 0 {
			path = strings.TrimSpace(path[idx+4:])
		}
		if path != "" {
			files = append(files, filepath.ToSlash(path))
		}
	}
	return files
}

func runGitStatusShort(workingDir string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "status", "--short", "--untracked-files=all")
	cmd.Dir = workingDir
	return cmd.CombinedOutput()
}

func hasTestFile(workingDir, srcFile string) string {
	profile := langProfileForFile(srcFile)
	if profile == nil {
		return ""
	}
	candidates := profile.TestFilePaths(workingDir, srcFile)
	for _, candidate := range candidates {
		abs := candidate
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(workingDir, candidate)
		}
		if fileExists(abs) {
			return candidate
		}
	}

	if strings.HasSuffix(srcFile, ".go") {
		dir := filepath.Dir(srcFile)
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(workingDir, dir)
		}
		if entries, err := os.ReadDir(dir); err == nil {
			for _, e := range entries {
				if !e.IsDir() && strings.HasSuffix(e.Name(), "_test.go") {
					return filepath.Join(dir, e.Name())
				}
			}
		}
	}

	if strings.HasSuffix(srcFile, ".rs") {
		abs := srcFile
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(workingDir, abs)
		}
		if data, err := os.ReadFile(abs); err == nil {
			if bytes.Contains(data, []byte("#[test]")) || bytes.Contains(data, []byte("#[tokio::test]")) {
				return abs
			}
		}
	}
	return ""
}

func untestedChangedFilesMulti(workingDir string) []string {
	files := changedSourceFilesFromGit(workingDir)
	if len(files) == 0 {
		return nil
	}
	var untested []string
	for _, f := range files {
		if hasTestFile(workingDir, f) == "" {
			untested = append(untested, f)
		}
	}
	return untested
}

func testCoverageNudgeMulti(workingDir string) string {
	untested := untestedChangedFilesMulti(workingDir)
	if len(untested) == 0 {
		return ""
	}
	display := untested
	if len(display) > 5 {
		display = display[:5]
	}
	var b strings.Builder
	b.WriteString("[Test coverage gap: ")
	if len(untested) == 1 {
		b.WriteString("1 changed file has no test")
	} else {
		b.WriteString(fmt.Sprintf("%d changed files have no tests", len(untested)))
	}
	b.WriteString(": ")
	b.WriteString(strings.Join(display, ", "))
	if len(untested) > 5 {
		b.WriteString(fmt.Sprintf(", … (+%d more)", len(untested)-5))
	}
	b.WriteString(". Consider writing tests for these files.]")
	return b.String()
}

func parseExportedFuncsMulti(filePath string) []exportedFuncInfo {
	profile := langProfileForFile(filePath)
	if profile == nil {
		return nil
	}

	if profile.Name == "go" {
		return parseExportedFuncs(filePath)
	}

	if profile.FuncRegex == nil {
		return nil
	}
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil
	}
	content := string(data)
	matches := profile.FuncRegex.FindAllStringSubmatch(content, -1)
	if len(matches) == 0 {
		return nil
	}
	var funcs []exportedFuncInfo
	for _, m := range matches {

		name := ""
		for _, g := range m[1:] {
			if g != "" {
				name = g
				break
			}
		}
		if name == "" {
			continue
		}

		if profile.SkipFuncNames != nil && profile.SkipFuncNames.MatchString(name) {
			continue
		}

		if (profile.Name == "typescript" || profile.Name == "dart") && isControlFlowKeyword(name) {
			continue
		}
		funcs = append(funcs, exportedFuncInfo{
			DisplayName: name,
			IsMethod:    false,
		})
	}
	return funcs
}

func parseTestFuncNamesMulti(filePath string, profile *langProfile) map[string]bool {
	if profile == nil {
		return nil
	}

	if profile.Name == "go" {
		names, _ := parseTestFuncNames(filePath)
		return names
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil
	}
	content := string(data)
	names := make(map[string]bool)

	switch profile.Name {
	case "typescript":

		testRe := regexp.MustCompile(`(?:it|test)\s*\(\s*['"]([^'"]+)['"]`)
		for _, m := range testRe.FindAllStringSubmatch(content, -1) {
			if len(m) > 1 {
				names[m[1]] = true
			}
		}
	case "python":

		testRe := regexp.MustCompile(`def\s+(test_\w+)`)
		for _, m := range testRe.FindAllStringSubmatch(content, -1) {
			if len(m) > 1 {
				names[m[1]] = true
			}
		}
	case "rust":

		testRe := regexp.MustCompile(`#\[(?:\w+::)*test\]\s*(?:async\s+)?fn\s+(\w+)`)
		for _, m := range testRe.FindAllStringSubmatch(content, -1) {
			if len(m) > 1 {
				names[m[1]] = true
			}
		}
	case "ruby":

		testRe := regexp.MustCompile(`def\s+(test_\w+)`)
		for _, m := range testRe.FindAllStringSubmatch(content, -1) {
			if len(m) > 1 {
				names[m[1]] = true
			}
		}

		itRe := regexp.MustCompile(`it\s+['"]([^'"]+)['"]`)
		for _, m := range itRe.FindAllStringSubmatch(content, -1) {
			if len(m) > 1 {
				names[m[1]] = true
			}
		}
	case "swift":

		testRe := regexp.MustCompile(`func\s+(test\w+)`)
		for _, m := range testRe.FindAllStringSubmatch(content, -1) {
			if len(m) > 1 {
				names[m[1]] = true
			}
		}
	case "dart":

		testRe := regexp.MustCompile(`test\s*\(\s*['"]([^'"]+)['"]`)
		for _, m := range testRe.FindAllStringSubmatch(content, -1) {
			if len(m) > 1 {
				names[m[1]] = true
			}
		}
	case "java":

		testRe := regexp.MustCompile(`@Test\s*(?:public\s+)?(?:static\s+)?void\s+(\w+)`)
		for _, m := range testRe.FindAllStringSubmatch(content, -1) {
			if len(m) > 1 {
				names[m[1]] = true
			}
		}
	}
	return names
}

func untestedExportedFuncsMulti(workingDir, srcFile string) []string {
	profile := langProfileForFile(srcFile)
	if profile == nil {
		return nil
	}

	if profile.Name == "go" {
		return untestedExportedFuncs(workingDir, srcFile)
	}

	abs := srcFile
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(workingDir, srcFile)
	}
	funcs := parseExportedFuncsMulti(abs)
	if len(funcs) == 0 {
		return nil
	}

	candidates := profile.TestFilePaths(workingDir, srcFile)
	var testFuncs map[string]bool
	for _, c := range candidates {
		absTest := c
		if !filepath.IsAbs(absTest) {
			absTest = filepath.Join(workingDir, c)
		}
		if fileExists(absTest) {
			testFuncs = parseTestFuncNamesMulti(absTest, profile)
			break
		}
	}

	if len(testFuncs) == 0 {

		result := make([]string, 0, len(funcs))
		for _, f := range funcs {
			result = append(result, f.DisplayName)
		}
		return result
	}

	testSeqs := make([]string, 0, len(testFuncs))
	for name := range testFuncs {
		tokens := tokenizeHintName(name)
		if len(tokens) > 0 {
			testSeqs = append(testSeqs, " "+strings.Join(tokens, " ")+" ")
		}
	}

	var untested []string
	for _, f := range funcs {
		funcTokens := tokenizeHintName(f.DisplayName)
		matched := false
		if len(funcTokens) > 0 {
			want := " " + strings.Join(funcTokens, " ") + " "
			for _, seq := range testSeqs {
				if strings.Contains(seq, want) {
					matched = true
					break
				}
			}
		}
		if !matched {
			untested = append(untested, f.DisplayName)
		}
	}
	return untested
}

func camelToSnakeLower(s string) string {
	var b strings.Builder
	runes := []rune(s)
	for i, r := range runes {
		if r >= 'A' && r <= 'Z' {

			if i > 0 {
				prev := runes[i-1]
				nextLower := i+1 < len(runes) && runes[i+1] >= 'a' && runes[i+1] <= 'z'
				if !(prev >= 'A' && prev <= 'Z') || (prev >= 'A' && prev <= 'Z' && nextLower) {
					b.WriteByte('_')
				}
			}
			b.WriteRune(r + ('a' - 'A'))
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func tokenizeHintName(name string) []string {
	normalized := strings.ToLower(camelToSnakeLower(name))
	normalized = strings.ReplaceAll(normalized, "-", "_")
	normalized = strings.ReplaceAll(normalized, " ", "_")
	normalized = strings.ReplaceAll(normalized, ".", "_")
	normalized = strings.ReplaceAll(normalized, ":", "_")
	var tokens []string
	for _, tok := range strings.Split(normalized, "_") {
		if tok != "" {
			tokens = append(tokens, tok)
		}
	}
	return tokens
}

func funcLevelCoverageGapsMulti(workingDir string, maxFiles, maxFuncsPerFile int) []funcCoverageGap {
	files := changedSourceFilesFromGit(workingDir)
	if len(files) == 0 {
		return nil
	}
	var gaps []funcCoverageGap
	for _, f := range files {
		untested := untestedExportedFuncsMulti(workingDir, f)
		if len(untested) == 0 {
			continue
		}
		if len(untested) > maxFuncsPerFile {
			untested = untested[:maxFuncsPerFile]
		}
		gaps = append(gaps, funcCoverageGap{
			File:  filepath.Base(f),
			Funcs: untested,
		})
		if len(gaps) >= maxFiles {
			break
		}
	}
	return gaps
}

func targetedVerifyCommandMulti(workingDir, filePath string) string {
	profile := langProfileForFile(filePath)
	if profile == nil {
		return ""
	}
	return profile.TargetedTestCmd(workingDir, filePath)
}

func impactScopedTestCommandMulti(workingDir string) string {

	if fileExists(filepath.Join(workingDir, "go.mod")) {
		if cmd := impactScopedTestCommandWithDeps(workingDir); cmd != "" {
			return cmd
		}
		if cmd := impactScopedTestCommand(workingDir); cmd != "" {
			return cmd
		}
	}

	files := changedSourceFilesFromGit(workingDir)
	if len(files) == 0 {
		return ""
	}

	langCounts := make(map[string]int)
	for _, f := range files {
		if p := langProfileForFile(f); p != nil {
			langCounts[p.Name]++
		}
	}
	if len(langCounts) == 0 {
		return ""
	}

	dominantLang := ""
	maxCount := 0
	for lang, count := range langCounts {
		if count > maxCount {
			maxCount = count
			dominantLang = lang
		}
	}

	switch dominantLang {
	case "typescript":
		runner := detectJSRunner(workingDir)
		if runner == "" {
			return ""
		}

		dirs := changedDirsForLang(files, "typescript")
		if len(dirs) == 0 {
			return runner
		}
		return runner + " " + strings.Join(dirs, " ")

	case "python":
		dirs := changedDirsForLang(files, "python")
		if len(dirs) == 0 {
			return "python -m pytest"
		}
		return "python -m pytest " + strings.Join(dirs, " ")

	case "rust":
		return "cargo test"

	case "java":
		if fileExists(filepath.Join(workingDir, "pom.xml")) {
			return "mvn test"
		}
		if fileExists(filepath.Join(workingDir, "build.gradle")) ||
			fileExists(filepath.Join(workingDir, "build.gradle.kts")) {
			return "gradle test"
		}
		return ""

	case "ruby":
		if fileExists(filepath.Join(workingDir, ".rspec")) {
			return "rspec"
		}
		return "bundle exec rake test"

	case "swift":
		if fileExists(filepath.Join(workingDir, "Package.swift")) {
			return "swift test"
		}
		return ""

	case "dart":
		if fileExists(filepath.Join(workingDir, "pubspec.yaml")) {
			data, err := os.ReadFile(filepath.Join(workingDir, "pubspec.yaml"))
			if err == nil && strings.Contains(string(data), "flutter") {
				return "flutter test"
			}
			return "dart test"
		}
		return ""
	}

	return ""
}

func changedDirsForLang(files []string, lang string) []string {
	seen := make(map[string]bool)
	var dirs []string
	for _, f := range files {
		p := langProfileForFile(f)
		if p == nil || p.Name != lang {
			continue
		}
		dir := filepath.ToSlash(filepath.Dir(f))

		if dir == "" {
			continue
		}
		if !seen[dir] {
			seen[dir] = true
			dirs = append(dirs, dir)
		}
	}
	sort.Strings(dirs)
	return dirs
}
