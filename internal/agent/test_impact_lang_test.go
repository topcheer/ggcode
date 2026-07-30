package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLangProfileForFile verifies that language detection works for all
// supported extensions and returns nil for unsupported types.
func TestLangProfileForFile(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"foo.go", "go"},
		{"foo_test.go", "go"},
		{"foo.ts", "typescript"},
		{"foo.tsx", "typescript"},
		{"foo.js", "typescript"},
		{"foo.jsx", "typescript"},
		{"foo.py", "python"},
		{"foo.rs", "rust"},
		{"Foo.java", "java"},
		{"foo.rb", "ruby"},
		{"Foo.swift", "swift"},
		{"foo.dart", "dart"},
		{"foo.txt", ""},
		{"foo.md", ""},
		{"README", ""},
		{"Makefile", ""},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			p := langProfileForFile(tt.path)
			if tt.want == "" {
				if p != nil {
					t.Errorf("langProfileForFile(%q) = %s, want nil", tt.path, p.Name)
				}
				return
			}
			if p == nil {
				t.Fatalf("langProfileForFile(%q) = nil, want %s", tt.path, tt.want)
			}
			if p.Name != tt.want {
				t.Errorf("langProfileForFile(%q) = %s, want %s", tt.path, p.Name, tt.want)
			}
		})
	}
}

// TestIsSupportedSourceFile checks the source file classifier.
func TestIsSupportedSourceFile(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"foo.go", true},
		{"foo.ts", true},
		{"foo.py", true},
		{"foo.rs", true},
		{"Foo.java", true},
		{"foo.rb", true},
		{"Foo.swift", true},
		{"foo.dart", true},
		// Test files should be excluded
		{"foo_test.go", false},
		{"foo.test.ts", false},
		{"test_foo.py", false},
		// Non-code files
		{"foo.txt", false},
		{"foo.md", false},
		{"README", false},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := isSupportedSourceFile(tt.path)
			if got != tt.want {
				t.Errorf("isSupportedSourceFile(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

// TestIsTestFile checks that test file detection works for all languages.
func TestIsTestFile(t *testing.T) {
	tests := []struct {
		lang string
		file string
		want bool
	}{
		// Go
		{"go", "foo_test.go", true},
		{"go", "foo.go", false},
		// TypeScript/JavaScript
		{"typescript", "foo.test.ts", true},
		{"typescript", "foo.spec.tsx", true},
		{"typescript", "foo.test.js", true},
		{"typescript", "foo.spec.jsx", true},
		{"typescript", "foo.ts", false},
		// Python
		{"python", "test_foo.py", true},
		{"python", "foo_test.py", true},
		{"python", "foo.py", false},
		// Rust
		{"rust", "foo_test.rs", true},
		{"rust", "foo.rs", false},
		// Java
		{"java", "FooTest.java", true},
		{"java", "FooTests.java", true},
		{"java", "Foo.java", false},
		// Ruby
		{"ruby", "foo_test.rb", true},
		{"ruby", "foo_spec.rb", true},
		{"ruby", "foo.rb", false},
		// Swift
		{"swift", "FooTests.swift", true},
		{"swift", "Foo.swift", false},
		// Dart
		{"dart", "foo_test.dart", true},
		{"dart", "foo.dart", false},
	}
	for _, tt := range tests {
		t.Run(tt.lang+"/"+tt.file, func(t *testing.T) {
			var profile *langProfile
			for i, p := range allLangProfiles() {
				if p.Name == tt.lang {
					profile = &allLangProfiles()[i]
					break
				}
			}
			if profile == nil {
				t.Fatalf("unknown language: %s", tt.lang)
			}
			got := profile.IsTestFile(tt.file)
			if got != tt.want {
				t.Errorf("%s.IsTestFile(%q) = %v, want %v", tt.lang, tt.file, got, tt.want)
			}
		})
	}
}

// TestHasTestFileTypeScript tests test file detection for TypeScript projects.
func TestHasTestFileTypeScript(t *testing.T) {
	dir := t.TempDir()
	srcDir := filepath.Join(dir, "src")
	os.MkdirAll(srcDir, 0o755)

	// Create foo.ts
	srcFile := filepath.Join(srcDir, "foo.ts")
	os.WriteFile(srcFile, []byte("export function foo() {}"), 0o644)

	// No test file yet
	if hasTestFile(dir, "src/foo.ts") != "" {
		t.Error("expected no test file found")
	}

	// Create foo.test.ts
	testFile := filepath.Join(srcDir, "foo.test.ts")
	os.WriteFile(testFile, []byte("test('foo', () => {})"), 0o644)

	if found := hasTestFile(dir, "src/foo.ts"); found == "" {
		t.Error("expected test file foo.test.ts to be found")
	}
}

// TestHasTestFilePython tests test file detection for Python projects.
func TestHasTestFilePython(t *testing.T) {
	dir := t.TempDir()

	// Create calculator.py
	srcFile := filepath.Join(dir, "calculator.py")
	os.WriteFile(srcFile, []byte("def add(a, b): return a + b"), 0o644)

	// No test file
	if hasTestFile(dir, "calculator.py") != "" {
		t.Error("expected no test file found")
	}

	// Create test_calculator.py
	testFile := filepath.Join(dir, "test_calculator.py")
	os.WriteFile(testFile, []byte("def test_add(): pass"), 0o644)

	if found := hasTestFile(dir, "calculator.py"); found == "" {
		t.Error("expected test file test_calculator.py to be found")
	}
}

// TestHasTestFileDart tests test file detection for Dart projects.
func TestHasTestFileDart(t *testing.T) {
	dir := t.TempDir()

	srcFile := filepath.Join(dir, "utils.dart")
	os.WriteFile(srcFile, []byte("void greet() {}"), 0o644)

	// No test
	if hasTestFile(dir, "utils.dart") != "" {
		t.Error("expected no test file found")
	}

	// Create utils_test.dart
	testFile := filepath.Join(dir, "utils_test.dart")
	os.WriteFile(testFile, []byte("test('greet', () {})"), 0o644)

	if found := hasTestFile(dir, "utils.dart"); found == "" {
		t.Error("expected test file utils_test.dart to be found")
	}
}

// TestParseExportedFuncsTypeScript tests regex-based function extraction.
func TestParseExportedFuncsTypeScript(t *testing.T) {
	dir := t.TempDir()
	srcFile := filepath.Join(dir, "service.ts")
	content := `import { Foo } from './foo'

export function getUser(id: string): User {
  return users[id]
}

export async function fetchUser(id: string): Promise<User> {
  return await api.get(id)
}

export class UserService {
  private data: User[]

  findById(id: string): User {
    return this.data.find(u => u.id === id)
  }
}

// Not exported
function internal() { return 42 }

export const VERSION = '1.0'
`
	os.WriteFile(srcFile, []byte(content), 0o644)

	funcs := parseExportedFuncsMulti(srcFile)
	// Should find: getUser, fetchUser, UserService, findById, VERSION
	names := make(map[string]bool)
	for _, f := range funcs {
		names[f.DisplayName] = true
	}
	if !names["getUser"] {
		t.Errorf("expected getUser in %v", names)
	}
	if !names["fetchUser"] {
		t.Errorf("expected fetchUser in %v", names)
	}
	// Should NOT include internal
	if names["internal"] {
		t.Error("internal should not be detected as exported")
	}
}

// TestParseExportedFuncsPython tests regex-based function extraction for Python.
func TestParseExportedFuncsPython(t *testing.T) {
	dir := t.TempDir()
	srcFile := filepath.Join(dir, "service.py")
	content := `def get_user(user_id):
    return users[user_id]

async def fetch_user(user_id):
    return await api.get(user_id)

def _internal():
    return 42

def __init__(self):
    pass

class UserService:
    def find_by_id(self, user_id):
        return None

def test_something():
    pass
`
	os.WriteFile(srcFile, []byte(content), 0o644)

	funcs := parseExportedFuncsMulti(srcFile)
	names := make(map[string]bool)
	for _, f := range funcs {
		names[f.DisplayName] = true
	}
	// Should find get_user, fetch_user, find_by_id
	if !names["get_user"] {
		t.Errorf("expected get_user in %v", names)
	}
	if !names["fetch_user"] {
		t.Errorf("expected fetch_user in %v", names)
	}
	// Should NOT include _internal, __init__, test_something
	if names["_internal"] {
		t.Error("_internal should be skipped")
	}
	if names["__init__"] {
		t.Error("__init__ should be skipped")
	}
	if names["test_something"] {
		t.Error("test_something should be skipped")
	}
}

// TestParseExportedFuncsRust tests regex-based function extraction for Rust.
func TestParseExportedFuncsRust(t *testing.T) {
	dir := t.TempDir()
	srcFile := filepath.Join(dir, "service.rs")
	content := `pub fn get_user(id: u32) -> User {
    User { id }
}

pub async fn fetch_user(id: u32) -> Result<User, Error> {
    Ok(User { id })
}

fn internal() -> u32 {
    42
}

pub struct UserService;

impl UserService {
    pub fn find_by_id(&self, id: u32) -> Option<User> {
        None
    }
}
`
	os.WriteFile(srcFile, []byte(content), 0o644)

	funcs := parseExportedFuncsMulti(srcFile)
	names := make(map[string]bool)
	for _, f := range funcs {
		names[f.DisplayName] = true
	}
	// Should find: get_user, fetch_user, find_by_id
	if !names["get_user"] {
		t.Errorf("expected get_user in %v", names)
	}
	if !names["fetch_user"] {
		t.Errorf("expected fetch_user in %v", names)
	}
	if !names["find_by_id"] {
		t.Errorf("expected find_by_id in %v", names)
	}
	// Should NOT include internal
	if names["internal"] {
		t.Error("internal should not be detected as exported")
	}
}

// TestParseExportedFuncsJava tests regex-based function extraction for Java.
func TestParseExportedFuncsJava(t *testing.T) {
	dir := t.TempDir()
	srcFile := filepath.Join(dir, "UserService.java")
	content := `public class UserService {
    public User getUser(Long id) {
        return users.get(id);
    }

    public static User findById(Long id) {
        return repository.find(id);
    }

    protected void deleteUser(Long id) {
        users.remove(id);
    }

    private void internal() {
        // private
    }

    @Override
    public String toString() {
        return "UserService";
    }
}
`
	os.WriteFile(srcFile, []byte(content), 0o644)

	funcs := parseExportedFuncsMulti(srcFile)
	names := make(map[string]bool)
	for _, f := range funcs {
		names[f.DisplayName] = true
	}
	// Should find: getUser, findById, deleteUser
	if !names["getUser"] {
		t.Errorf("expected getUser in %v", names)
	}
	if !names["findById"] {
		t.Errorf("expected findById in %v", names)
	}
	if !names["deleteUser"] {
		t.Errorf("expected deleteUser in %v", names)
	}
	// toString should be skipped
	if names["toString"] {
		t.Error("toString should be skipped")
	}
}

// TestParseExportedFuncsRuby tests regex-based function extraction for Ruby.
func TestParseExportedFuncsRuby(t *testing.T) {
	dir := t.TempDir()
	srcFile := filepath.Join(dir, "service.rb")
	content := `def get_user(id)
  users[id]
end

def self.find_user(id)
  User.find(id)
end

def initialize(opts)
  @opts = opts
end

def test_something
  assert true
end
`
	os.WriteFile(srcFile, []byte(content), 0o644)

	funcs := parseExportedFuncsMulti(srcFile)
	names := make(map[string]bool)
	for _, f := range funcs {
		names[f.DisplayName] = true
	}
	// Should find: get_user, find_user
	if !names["get_user"] {
		t.Errorf("expected get_user in %v", names)
	}
	if !names["find_user"] {
		t.Errorf("expected find_user in %v", names)
	}
	// initialize and test_something should be skipped
	if names["initialize"] {
		t.Error("initialize should be skipped")
	}
	if names["test_something"] {
		t.Error("test_something should be skipped")
	}
}

// TestParseExportedFuncsSwift tests regex-based function extraction for Swift.
func TestParseExportedFuncsSwift(t *testing.T) {
	dir := t.TempDir()
	srcFile := filepath.Join(dir, "UserService.swift")
	content := `struct UserService {
    func getUser(id: String) -> User? {
        return users[id]
    }

    public func fetchUser(id: String) async throws -> User {
        return try await api.get(id)
    }

    static func shared() -> UserService {
        UserService()
    }

    private func internalCache() -> [String: User] {
        return cache
    }
}
`
	os.WriteFile(srcFile, []byte(content), 0o644)

	funcs := parseExportedFuncsMulti(srcFile)
	names := make(map[string]bool)
	for _, f := range funcs {
		names[f.DisplayName] = true
	}
	// Should find: getUser, fetchUser, shared
	if !names["getUser"] {
		t.Errorf("expected getUser in %v", names)
	}
	if !names["fetchUser"] {
		t.Errorf("expected fetchUser in %v", names)
	}
	if !names["shared"] {
		t.Errorf("expected shared in %v", names)
	}
}

// TestParseExportedFuncsDart tests regex-based function extraction for Dart.
func TestParseExportedFuncsDart(t *testing.T) {
	dir := t.TempDir()
	srcFile := filepath.Join(dir, "service.dart")
	content := `User getUser(String id) {
  return users[id]!;
}

Future<User> fetchUser(String id) async {
  return await api.get(id);
}

void main() {
  runApp(App());
}

class Service {
  String findUser(String id) {
    return users[id]!;
  }
}
`
	os.WriteFile(srcFile, []byte(content), 0o644)

	funcs := parseExportedFuncsMulti(srcFile)
	names := make(map[string]bool)
	for _, f := range funcs {
		names[f.DisplayName] = true
	}
	// Should find: getUser, fetchUser, findUser
	if !names["getUser"] {
		t.Errorf("expected getUser in %v", names)
	}
	if !names["fetchUser"] {
		t.Errorf("expected fetchUser in %v", names)
	}
	if !names["findUser"] {
		t.Errorf("expected findUser in %v", names)
	}
	// main should be skipped
	if names["main"] {
		t.Error("main should be skipped")
	}
}

// TestUntestedExportedFuncsMultiTypeScript tests function-level coverage gap
// detection for TypeScript: a file with exported functions and no test file.
func TestUntestedExportedFuncsMultiTypeScript(t *testing.T) {
	dir := t.TempDir()
	srcFile := filepath.Join(dir, "api.ts")
	os.WriteFile(srcFile, []byte(`
export function getUsers() { return [] }
export function deleteUser(id) { return true }
`), 0o644)

	// No test file — all exported functions should be untested
	untested := untestedExportedFuncsMulti(dir, "api.ts")
	if len(untested) != 2 {
		t.Errorf("expected 2 untested functions, got %d: %v", len(untested), untested)
	}

	// Create a test file that covers one function
	testFile := filepath.Join(dir, "api.test.ts")
	os.WriteFile(testFile, []byte(`
test('getUsers returns empty', () => {})
`), 0o644)

	untested = untestedExportedFuncsMulti(dir, "api.ts")
	// deleteUser should still be untested
	found := false
	for _, name := range untested {
		if name == "deleteUser" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected deleteUser to still be untested, got: %v", untested)
	}
}

// TestUntestedExportedFuncsMultiPython tests function-level coverage for Python.
func TestUntestedExportedFuncsMultiPython(t *testing.T) {
	dir := t.TempDir()
	srcFile := filepath.Join(dir, "calc.py")
	os.WriteFile(srcFile, []byte(`
def add(a, b):
    return a + b

def multiply(a, b):
    return a * b

def _internal():
    pass
`), 0o644)

	// No test file — all exported functions should be untested
	untested := untestedExportedFuncsMulti(dir, "calc.py")
	if len(untested) < 2 {
		t.Errorf("expected at least 2 untested functions, got %d: %v", len(untested), untested)
	}

	// Create test file covering add
	testFile := filepath.Join(dir, "test_calc.py")
	os.WriteFile(testFile, []byte(`
def test_add():
    assert add(1, 2) == 3
`), 0o644)

	untested = untestedExportedFuncsMulti(dir, "calc.py")
	// multiply should still be untested
	found := false
	for _, name := range untested {
		if name == "multiply" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected multiply to be untested, got: %v", untested)
	}
}

// TestTargetedVerifyCommandMulti tests targeted test commands per language.
func TestTargetedVerifyCommandMulti(t *testing.T) {
	tests := []struct {
		name     string
		setup    func(t *testing.T, dir string)
		filePath string
		wantFn   func(cmd string) bool
	}{
		{
			name: "go file returns go test command",
			setup: func(t *testing.T, dir string) {
				os.WriteFile(filepath.Join(dir, "go.mod"),
					[]byte("module test\n\ngo 1.21\n"), 0o644)
				os.MkdirAll(filepath.Join(dir, "internal", "foo"), 0o755)
			},
			filePath: "internal/foo/bar.go",
			wantFn: func(cmd string) bool {
				return strings.Contains(cmd, "go test")
			},
		},
		{
			name: "typescript with vitest",
			setup: func(t *testing.T, dir string) {
				os.WriteFile(filepath.Join(dir, "package.json"),
					[]byte(`{"devDependencies": {"vitest": "^1.0"}}`), 0o644)
			},
			filePath: "src/foo.ts",
			wantFn: func(cmd string) bool {
				return strings.Contains(cmd, "vitest")
			},
		},
		{
			name: "typescript with jest",
			setup: func(t *testing.T, dir string) {
				os.WriteFile(filepath.Join(dir, "package.json"),
					[]byte(`{"devDependencies": {"jest": "^29.0"}}`), 0o644)
			},
			filePath: "src/foo.ts",
			wantFn: func(cmd string) bool {
				return strings.Contains(cmd, "jest")
			},
		},
		{
			name: "python with pytest",
			setup: func(t *testing.T, dir string) {
				os.WriteFile(filepath.Join(dir, "pyproject.toml"),
					[]byte("[tool.pytest]\n"), 0o644)
			},
			filePath: "src/foo.py",
			wantFn: func(cmd string) bool {
				return strings.Contains(cmd, "pytest")
			},
		},
		{
			name: "rust returns cargo test",
			setup: func(t *testing.T, dir string) {
				os.WriteFile(filepath.Join(dir, "Cargo.toml"),
					[]byte("[package]\nname = \"test\"\n"), 0o644)
			},
			filePath: "src/foo.rs",
			wantFn: func(cmd string) bool {
				return cmd == "cargo test"
			},
		},
		{
			name: "java with maven",
			setup: func(t *testing.T, dir string) {
				os.WriteFile(filepath.Join(dir, "pom.xml"),
					[]byte("<project></project>"), 0o644)
			},
			filePath: "src/main/java/Foo.java",
			wantFn: func(cmd string) bool {
				return strings.Contains(cmd, "mvn")
			},
		},
		{
			name: "java with gradle",
			setup: func(t *testing.T, dir string) {
				os.WriteFile(filepath.Join(dir, "build.gradle"),
					[]byte("apply plugin: 'java'\n"), 0o644)
			},
			filePath: "src/main/java/Foo.java",
			wantFn: func(cmd string) bool {
				return strings.Contains(cmd, "gradle")
			},
		},
		{
			name: "dart with flutter",
			setup: func(t *testing.T, dir string) {
				os.WriteFile(filepath.Join(dir, "pubspec.yaml"),
					[]byte("name: test\ndependencies:\n  flutter:\n    sdk: flutter\n"), 0o644)
			},
			filePath: "lib/foo.dart",
			wantFn: func(cmd string) bool {
				return strings.Contains(cmd, "flutter test")
			},
		},
		{
			name: "swift package",
			setup: func(t *testing.T, dir string) {
				os.WriteFile(filepath.Join(dir, "Package.swift"),
					[]byte("// swift-tools-version:5.0\n"), 0o644)
			},
			filePath: "Sources/Foo/foo.swift",
			wantFn: func(cmd string) bool {
				return cmd == "swift test"
			},
		},
		{
			name:     "unsupported language returns empty",
			setup:    func(t *testing.T, dir string) {},
			filePath: "docs/readme.md",
			wantFn: func(cmd string) bool {
				return cmd == ""
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			tt.setup(t, dir)
			cmd := targetedVerifyCommandMulti(dir, tt.filePath)
			if !tt.wantFn(cmd) {
				t.Errorf("targetedVerifyCommandMulti(%q) = %q, assertion failed", tt.filePath, cmd)
			}
		})
	}
}

// TestDetectJSRunner verifies JavaScript test runner detection.
func TestDetectJSRunner(t *testing.T) {
	tests := []struct {
		name    string
		pkgJSON string
		want    string
	}{
		{
			name:    "vitest detected",
			pkgJSON: `{"devDependencies": {"vitest": "^1.0.0"}}`,
			want:    "npx vitest run",
		},
		{
			name:    "jest detected",
			pkgJSON: `{"devDependencies": {"jest": "^29.0.0"}}`,
			want:    "npx jest",
		},
		{
			name:    "npm test fallback",
			pkgJSON: `{"scripts": {"test": "node --test"}}`,
			want:    "npm test",
		},
		{
			name:    "no test runner",
			pkgJSON: `{"dependencies": {"express": "^4.0"}}`,
			want:    "",
		},
		{
			name:    "no package.json",
			pkgJSON: "",
			want:    "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			if tt.pkgJSON != "" {
				os.WriteFile(filepath.Join(dir, "package.json"), []byte(tt.pkgJSON), 0o644)
			}
			got := detectJSRunner(dir)
			if got != tt.want {
				t.Errorf("detectJSRunner() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestChangedDirsForLang tests the directory grouping helper.
func TestChangedDirsForLang(t *testing.T) {
	files := []string{
		"src/api/user.ts",
		"src/api/order.ts",
		"src/utils/format.ts",
		"internal/agent/agent.go",
		"internal/tool/tool.go",
	}
	dirs := changedDirsForLang(files, "typescript")
	want := []string{"src/api", "src/utils"}
	if len(dirs) != len(want) {
		t.Fatalf("expected %d dirs, got %d: %v", len(want), len(dirs), dirs)
	}
	for i, d := range dirs {
		if d != want[i] {
			t.Errorf("dirs[%d] = %q, want %q", i, d, want[i])
		}
	}

	// Go files should return empty for typescript
	goDirs := changedDirsForLang(files, "go")
	wantGo := []string{"internal/agent", "internal/tool"}
	if len(goDirs) != len(wantGo) {
		t.Fatalf("expected %d go dirs, got %d: %v", len(wantGo), len(goDirs), goDirs)
	}
}

// TestParseTestFuncNamesMultiTypeScript tests test name extraction from TS.
func TestParseTestFuncNamesMultiTypeScript(t *testing.T) {
	dir := t.TempDir()
	testFile := filepath.Join(dir, "foo.test.ts")
	content := `import { test, it } from 'vitest'

test('should create user', () => {})
it('should delete user', () => {})
it('handles edge case', () => {})

// Not a test
function helper() {}
`
	os.WriteFile(testFile, []byte(content), 0o644)

	names := parseTestFuncNamesMulti(testFile, &tsLangProfile)
	if len(names) < 3 {
		t.Errorf("expected at least 3 test names, got %d: %v", len(names), names)
	}
	if !names["should create user"] {
		t.Error("missing 'should create user'")
	}
	if !names["should delete user"] {
		t.Error("missing 'should delete user'")
	}
}

// TestParseTestFuncNamesMultiPython tests test name extraction from Python.
func TestParseTestFuncNamesMultiPython(t *testing.T) {
	dir := t.TempDir()
	testFile := filepath.Join(dir, "test_foo.py")
	content := `import pytest

def test_add():
    assert 1 + 1 == 2

def test_multiply():
    assert 2 * 3 == 6

def helper():
    pass
`
	os.WriteFile(testFile, []byte(content), 0o644)

	names := parseTestFuncNamesMulti(testFile, &pyLangProfile)
	if len(names) != 2 {
		t.Fatalf("expected 2 test names, got %d: %v", len(names), names)
	}
	if !names["test_add"] {
		t.Error("missing test_add")
	}
	if !names["test_multiply"] {
		t.Error("missing test_multiply")
	}
}

// TestParseTestFuncNamesMultiRust tests test name extraction from Rust.
func TestParseTestFuncNamesMultiRust(t *testing.T) {
	dir := t.TempDir()
	testFile := filepath.Join(dir, "foo_test.rs")
	content := `#[test]
fn test_add() {
    assert_eq!(1 + 1, 2);
}

#[tokio::test]
async fn test_async_fetch() {
    assert!(true);
}

#[test]
fn validates_input() {
    assert!(true);
}
`
	os.WriteFile(testFile, []byte(content), 0o644)

	names := parseTestFuncNamesMulti(testFile, &rustLangProfile)
	if len(names) < 3 {
		t.Errorf("expected at least 3 test names, got %d: %v", len(names), names)
	}
	if !names["test_add"] {
		t.Error("missing test_add")
	}
	if !names["validates_input"] {
		t.Error("missing validates_input")
	}
}

// TestParseTestFuncNamesMultiJava tests test name extraction from Java.
func TestParseTestFuncNamesMultiJava(t *testing.T) {
	dir := t.TempDir()
	testFile := filepath.Join(dir, "FooTest.java")
	content := `public class FooTest {
    @Test
    public void testCreateUser() {
        assert true;
    }

    @Test
    void testDeleteUser() {
        assert true;
    }

    @Test
    public static void testStatic() {
        assert true;
    }

    public void helper() {}
}
`
	os.WriteFile(testFile, []byte(content), 0o644)

	names := parseTestFuncNamesMulti(testFile, &javaLangProfile)
	if len(names) < 3 {
		t.Errorf("expected at least 3 test names, got %d: %v", len(names), names)
	}
	if !names["testCreateUser"] {
		t.Error("missing testCreateUser")
	}
	if !names["testDeleteUser"] {
		t.Error("missing testDeleteUser")
	}
}
