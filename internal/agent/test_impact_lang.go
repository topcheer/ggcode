package agent

// test_impact_lang.go implements multi-language Test Impact Analysis (TIA).
// It extends the Go-only TIA (test_impact.go, test_impact_ast.go) to support
// TypeScript/JavaScript, Python, Rust, Java, Ruby, Swift, and Dart — covering
// the most popular programming languages used in real-world projects.
//
// For each language we define:
//   - Source file extensions and test file naming conventions
//   - The default test runner command
//   - A regex-based function/method extractor for coverage gap detection
//
// Go still uses the superior AST-based approach (go/parser). Other languages
// use lightweight regex extraction — accurate enough for coverage nudges
// without introducing tree-sitter or language-specific parser dependencies.
//
// Competitor mapping:
//   - GitHub Copilot: AST/regex function analysis across languages
//   - Cursor: language-aware test suggestions
//   - JetBrains AI: per-language coverage gap detection

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// langProfile defines the test conventions for a programming language.
type langProfile struct {
	Name       string            // "go", "typescript", "python", etc.
	Extensions []string          // source file extensions including the dot
	IsTestFile func(string) bool // returns true if filename is a test file
	// testFilePath returns the candidate test file path(s) for a source file.
	// Multiple candidates may exist (e.g., foo.test.ts and foo.spec.ts).
	TestFilePaths func(workingDir, srcFile string) []string
	// targetedTestCmd returns a scope-limited test command for a single file/dir.
	TargetedTestCmd func(workingDir, srcFile string) string
	// funcRegex extracts exported/public function names from source.
	// For Go this is unused (AST is used instead).
	FuncRegex *regexp.Regexp
	// skipFuncNames matches function names that should not be flagged as untested.
	SkipFuncNames *regexp.Regexp
}

// --- Go ---
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
	// Go uses AST-based extraction; regex not used.
}

// --- TypeScript / JavaScript ---
var tsFuncRegex = regexp.MustCompile(
	// Matches: export function foo, export async function foo,
	//          export const foo =, export class Foo
	//          public foo(, private foo( (class methods)
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
		// vitest/jest can run a directory
		return runner + " " + filepath.ToSlash(dir)
	},
	FuncRegex:     tsFuncRegex,
	SkipFuncNames: tsSkipFunc,
}

// detectJSRunner checks package.json to determine the test runner.
// Returns "npx vitest run", "npx jest", "npm test", or "" if no runner found.
func detectJSRunner(workingDir string) string {
	data, err := os.ReadFile(filepath.Join(workingDir, "package.json"))
	if err != nil {
		return ""
	}
	content := string(data)
	// Check for vitest in dependencies
	if strings.Contains(content, "vitest") {
		return "npx vitest run"
	}
	// Check for jest
	if strings.Contains(content, "jest") {
		return "npx jest"
	}
	// Fall back to npm test
	if strings.Contains(content, "\"test\"") {
		return "npm test"
	}
	return ""
}

// --- Python ---
var pyFuncRegex = regexp.MustCompile(
	// Matches: def foo(, async def foo(
	// Captures only the function name
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
		// Check if pytest is available
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

// --- Rust ---
var rustFuncRegex = regexp.MustCompile(
	// Matches: pub fn foo, pub async fn foo, fn foo (only pub is exported)
	`(?:^|\n)\s*pub\s+(?:async\s+)?fn\s+(\w+)`,
)

var rustSkipFunc = regexp.MustCompile(`^(main|test_.*)$`)

var rustLangProfile = langProfile{
	Name:       "rust",
	Extensions: []string{".rs"},
	IsTestFile: func(name string) bool {
		// Rust tests are usually inline (#[test]) or in tests/ directory
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
		// Rust can't easily scope to a single source file without knowing
		// the binary/lib name. Just suggest `cargo test`.
		if fileExists(filepath.Join(workingDir, "Cargo.toml")) {
			return "cargo test"
		}
		return ""
	},
	FuncRegex:     rustFuncRegex,
	SkipFuncNames: rustSkipFunc,
}

// --- Java ---
var javaFuncRegex = regexp.MustCompile(
	// Matches: public ReturnType foo(, public static void main(,
	//          protected void foo(, @Test public void foo(
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
		// Java tests live in src/test/java/... mirroring src/main/java/...
		testDir := strings.Replace(filepath.Join(dir, "..", "..", "test", "java"),
			string(filepath.Separator)+string(filepath.Separator), string(filepath.Separator), -1)
		return []string{
			filepath.Join(dir, name+"Test.java"),
			filepath.Join(dir, name+"Tests.java"),
			filepath.Join(testDir, name+"Test.java"),
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

// --- Ruby ---
var rubyFuncRegex = regexp.MustCompile(
	// Matches: def foo, def self.foo, def Foo.bar
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
		// Check for rspec
		if fileExists(filepath.Join(workingDir, ".rspec")) ||
			strings.Contains(dir, "spec") {
			return "rspec " + filepath.ToSlash(srcFile)
		}
		return "bundle exec rake test"
	},
	FuncRegex:     rubyFuncRegex,
	SkipFuncNames: rubySkipFunc,
}

// --- Swift ---
var swiftFuncRegex = regexp.MustCompile(
	// Matches: func foo(, public func foo(, static func foo(
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
		// For Swift Package Manager projects
		if fileExists(filepath.Join(workingDir, "Package.swift")) {
			return "swift test"
		}
		// Xcode projects can't easily be scoped
		return ""
	},
	FuncRegex:     swiftFuncRegex,
	SkipFuncNames: swiftSkipFunc,
}

// --- Dart ---
var dartFuncRegex = regexp.MustCompile(
	// Matches: void foo(, Future<void> foo(, foo() {, static foo(
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
		return []string{
			filepath.Join(dir, name+"_test.dart"),
			filepath.Join(dir, "test", name+"_test.dart"),
		}
	},
	TargetedTestCmd: func(workingDir, srcFile string) string {
		dir := filepath.Dir(srcFile)
		if fileExists(filepath.Join(workingDir, "pubspec.yaml")) {
			// Check if it's a Flutter project
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

// allLangProfiles returns all supported language profiles in priority order
// (most popular first for detection).
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

// langProfileForFile returns the language profile matching a file's extension.
// Returns nil for unsupported file types.
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

// langProfileForExt returns the language profile matching a file extension
// (including the dot, e.g. ".py"). Returns nil for unsupported extensions.
func langProfileForExt(ext string) *langProfile {
	return langProfileForFile("test" + ext)
}

// isSourceFile checks whether a path is a source file in a supported language
// (not a test file, not a vendored/generated file).
func isSupportedSourceFile(path string) bool {
	p := langProfileForFile(path)
	if p == nil {
		return false
	}
	// Exclude test files themselves
	if p.IsTestFile(filepath.Base(path)) {
		return false
	}
	return true
}

// --- Multi-language changed file detection ---

// changedSourceFilesFromGit returns all changed source files (modified, staged,
// newly added) across all supported languages, excluding test files. Uses
// `git status --short --untracked-files=all`. Returns slash-separated paths.
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

// changedFilesFromGitRaw returns all changed files from git status without
// language filtering. Shared infrastructure for both Go-specific and
// multi-language callers.
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

// runGitStatusShort executes `git status --short --untracked-files=all` and
// returns its output.
func runGitStatusShort(workingDir string) ([]byte, error) {
	cmd := exec.Command("git", "status", "--short", "--untracked-files=all")
	cmd.Dir = workingDir
	return cmd.CombinedOutput()
}

// --- Multi-language test file existence check ---

// hasTestFile checks whether a source file has a corresponding test file in
// any of the language's conventional test file locations. Returns the found
// test file path, or "" if none exists.
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
	return ""
}

// --- Multi-language untested files ---

// untestedChangedFilesMulti returns changed source files (any language) that
// lack a corresponding test file. Candidates for test generation suggestions.
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

// --- Multi-language test coverage nudge ---

// testCoverageNudgeMulti generates a hint string about changed files (any
// language) that lack test coverage. Caps at 5 files to avoid noise.
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

// --- Multi-language function-level coverage ---

// parseExportedFuncsMulti extracts exported/public function names from source
// files using language-appropriate parsing:
//   - Go: AST-based (go/parser) for precision
//   - Others: regex-based for zero-dependency portability
//
// Returns nil if the file can't be parsed or has no exported functions.
func parseExportedFuncsMulti(filePath string) []exportedFuncInfo {
	profile := langProfileForFile(filePath)
	if profile == nil {
		return nil
	}
	// Go uses the superior AST approach
	if profile.Name == "go" {
		return parseExportedFuncs(filePath)
	}
	// Other languages use regex
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
		// regex has multiple capture groups for different patterns;
		// find the first non-empty group
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
		// Skip names that match the skip pattern
		if profile.SkipFuncNames != nil && profile.SkipFuncNames.MatchString(name) {
			continue
		}
		funcs = append(funcs, exportedFuncInfo{
			DisplayName: name,
			IsMethod:    false,
		})
	}
	return funcs
}

// parseTestFuncNamesMulti extracts test function names from a test file.
// For Go this uses AST; for other languages it scans for test patterns.
func parseTestFuncNamesMulti(filePath string, profile *langProfile) map[string]bool {
	if profile == nil {
		return nil
	}
	// Go uses AST
	if profile.Name == "go" {
		return parseTestFuncNames(filePath)
	}
	// For other languages, read and scan for test patterns
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil
	}
	content := string(data)
	names := make(map[string]bool)
	// Generic test function detection:
	//   - Python: def test_foo( → "test_foo"
	//   - TypeScript: it("foo" / test("foo" → "foo"
	//   - Rust: #[test] fn foo( → "foo"
	//   - Ruby: def test_foo / it "foo"
	//   - Swift: func testFoo()
	//   - Dart: test("foo" / testFoo()
	//   - Java: @Test public void testFoo()
	// We collect the identifier after test/it keywords.
	switch profile.Name {
	case "typescript":
		// vitest/jest: test("name") or it("name")
		testRe := regexp.MustCompile(`(?:it|test)\s*\(\s*['"]([^'"]+)['"]`)
		for _, m := range testRe.FindAllStringSubmatch(content, -1) {
			if len(m) > 1 {
				names[m[1]] = true
			}
		}
	case "python":
		// pytest/unittest: def test_foo(
		testRe := regexp.MustCompile(`def\s+(test_\w+)`)
		for _, m := range testRe.FindAllStringSubmatch(content, -1) {
			if len(m) > 1 {
				names[m[1]] = true
			}
		}
	case "rust":
		// #[test] fn foo or #[tokio::test] async fn foo
		testRe := regexp.MustCompile(`#\[(?:\w+::)*test\]\s*(?:async\s+)?fn\s+(\w+)`)
		for _, m := range testRe.FindAllStringSubmatch(content, -1) {
			if len(m) > 1 {
				names[m[1]] = true
			}
		}
	case "ruby":
		// Minitest: def test_foo / RSpec: it "does something"
		testRe := regexp.MustCompile(`def\s+(test_\w+)`)
		for _, m := range testRe.FindAllStringSubmatch(content, -1) {
			if len(m) > 1 {
				names[m[1]] = true
			}
		}
		// RSpec it blocks
		itRe := regexp.MustCompile(`it\s+['"]([^'"]+)['"]`)
		for _, m := range itRe.FindAllStringSubmatch(content, -1) {
			if len(m) > 1 {
				names[m[1]] = true
			}
		}
	case "swift":
		// XCTest: func testFoo()
		testRe := regexp.MustCompile(`func\s+(test\w+)`)
		for _, m := range testRe.FindAllStringSubmatch(content, -1) {
			if len(m) > 1 {
				names[m[1]] = true
			}
		}
	case "dart":
		// test("description") or testFoo()
		testRe := regexp.MustCompile(`test\s*\(\s*['"]([^'"]+)['"]`)
		for _, m := range testRe.FindAllStringSubmatch(content, -1) {
			if len(m) > 1 {
				names[m[1]] = true
			}
		}
	case "java":
		// @Test public void testFoo() or @Test void testFoo()
		testRe := regexp.MustCompile(`@Test\s*(?:public\s+)?(?:static\s+)?void\s+(\w+)`)
		for _, m := range testRe.FindAllStringSubmatch(content, -1) {
			if len(m) > 1 {
				names[m[1]] = true
			}
		}
	}
	return names
}

// untestedExportedFuncsMulti returns the display names of exported functions
// in a source file (any language) that have no corresponding test function.
// For Go it uses the precise AST approach; for others it uses regex matching.
func untestedExportedFuncsMulti(workingDir, srcFile string) []string {
	profile := langProfileForFile(srcFile)
	if profile == nil {
		return nil
	}
	// Go uses the existing AST-based implementation
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

	// Find test file candidates
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
		// No test file — all exported functions are untested
		result := make([]string, 0, len(funcs))
		for _, f := range funcs {
			result = append(result, f.DisplayName)
		}
		return result
	}

	// For non-Go languages, matching test names to function names is
	// heuristic. We check if the function name appears in any test name
	// (case-insensitive substring match), since naming conventions vary.
	testNameLowers := make(map[string]bool, len(testFuncs))
	for name := range testFuncs {
		testNameLowers[strings.ToLower(name)] = true
	}

	var untested []string
	for _, f := range funcs {
		funcLower := strings.ToLower(f.DisplayName)
		matched := false
		for testName := range testNameLowers {
			if strings.Contains(testName, funcLower) {
				matched = true
				break
			}
		}
		if !matched {
			untested = append(untested, f.DisplayName)
		}
	}
	return untested
}

// funcLevelCoverageGapsMulti analyzes changed files (any language) and returns
// per-file lists of untested exported functions.
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

// --- Multi-language targeted test command ---

// targetedVerifyCommandMulti returns a scope-limited test command for any
// supported language. Delegates to the language profile's TargetedTestCmd.
func targetedVerifyCommandMulti(workingDir, filePath string) string {
	profile := langProfileForFile(filePath)
	if profile == nil {
		return ""
	}
	return profile.TargetedTestCmd(workingDir, filePath)
}

// --- Multi-language impact-scoped test command ---

// impactScopedTestCommandMulti builds a test command covering all changed
// packages/directories. For Go it uses the existing enhanced approach with
// import graph. For other languages it falls back to language-appropriate
// commands.
func impactScopedTestCommandMulti(workingDir string) string {
	// Try Go first (most sophisticated: import graph + transitive deps)
	if fileExists(filepath.Join(workingDir, "go.mod")) {
		if cmd := impactScopedTestCommandWithDeps(workingDir); cmd != "" {
			return cmd
		}
		if cmd := impactScopedTestCommand(workingDir); cmd != "" {
			return cmd
		}
	}

	// Group changed files by language and directory
	files := changedSourceFilesFromGit(workingDir)
	if len(files) == 0 {
		return ""
	}

	// Detect the dominant language in changed files
	langCounts := make(map[string]int)
	for _, f := range files {
		if p := langProfileForFile(f); p != nil {
			langCounts[p.Name]++
		}
	}
	if len(langCounts) == 0 {
		return ""
	}

	// Find the dominant language
	dominantLang := ""
	maxCount := 0
	for lang, count := range langCounts {
		if count > maxCount {
			maxCount = count
			dominantLang = lang
		}
	}

	// Build commands per dominant language
	switch dominantLang {
	case "typescript":
		runner := detectJSRunner(workingDir)
		if runner == "" {
			return ""
		}
		// Group by unique directories
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

// changedDirsForLang returns unique directory paths for changed files of the
// given language, slash-separated and sorted.
func changedDirsForLang(files []string, lang string) []string {
	seen := make(map[string]bool)
	var dirs []string
	for _, f := range files {
		p := langProfileForFile(f)
		if p == nil || p.Name != lang {
			continue
		}
		dir := filepath.ToSlash(filepath.Dir(f))
		if dir == "." || dir == "" {
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
