package agent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIsTestFilePath(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"foo_test.go", true},
		{"internal/agent/agent_test.go", true},
		{"test_foo.py", true},
		{"foo_test.py", true},
		{"foo.test.js", true},
		{"foo.spec.ts", true},
		{"FooTest.java", true},
		{"FooTests.java", true},
		{"FooIT.java", true},
		// Not test files
		{"foo.go", false},
		{"agent.go", false},
		{"foo.py", false},
		{"main.js", false},
		{"README.md", false},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			if got := isTestFilePath(tt.path); got != tt.want {
				t.Errorf("isTestFile(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestCompanionTestPaths(t *testing.T) {
	tests := []struct {
		name    string
		srcPath string
		want    []string
	}{
		{
			name:    "go source",
			srcPath: "internal/agent/agent.go",
			want:    []string{"internal/agent/agent_test.go"},
		},
		{
			name:    "python source",
			srcPath: "src/app.py",
			want: []string{
				"src/test_app.py",
				"src/app_test.py",
				"tests/test_app.py",
				"test/test_app.py",
			},
		},
		{
			name:    "javascript source",
			srcPath: "src/utils.js",
			want: []string{
				"src/utils.test.js",
				"src/utils.spec.js",
			},
		},
		{
			name:    "typescript source",
			srcPath: "src/index.ts",
			want: []string{
				"src/index.test.ts",
				"src/index.spec.ts",
			},
		},
		{
			name:    "java source",
			srcPath: "src/Foo.java",
			want: []string{
				"src/FooTest.java",
				"src/FooTests.java",
			},
		},
		{
			name:    "ruby source",
			srcPath: "lib/helper.rb",
			want: []string{
				"lib/helper_spec.rb",
				"lib/helper_test.rb",
			},
		},
		{
			name:    "config file returns nil",
			srcPath: "config.yaml",
			want:    nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := companionTestPaths(tt.srcPath)
			if len(got) != len(tt.want) {
				t.Errorf("companionTestPaths(%q) returned %d paths, want %d.\ngot:  %v\nwant: %v",
					tt.srcPath, len(got), len(tt.want), got, tt.want)
				return
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("companionTestPaths(%q)[%d] = %q, want %q", tt.srcPath, i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestShouldSkipCompanionCheck(t *testing.T) {
	skip := []string{
		"vendor/foo/foo.go",
		"node_modules/bar.js",
		"third_party/lib.c",
		"foo.pb.go",
		"zz_generated.go",
		"config.json",
		"README.md",
		"testdata/input.txt",
		"internal/agent/testdata/mock.go",
	}
	for _, p := range skip {
		if !shouldSkipCompanionCheck(p) {
			t.Errorf("shouldSkipCompanionCheck(%q) = false, want true", p)
		}
	}

	noSkip := []string{
		"internal/agent/agent.go",
		"cmd/main.py",
		"src/app.ts",
	}
	for _, p := range noSkip {
		if shouldSkipCompanionCheck(p) {
			t.Errorf("shouldSkipCompanionCheck(%q) = true, want false", p)
		}
	}
}

func TestCheckCompanionFiles_NoEdits(t *testing.T) {
	g := newCompanionGuardState()
	stats := newRunStats("test")
	msg := g.checkCompanionFiles(stats, "")
	if msg != "" {
		t.Errorf("expected empty message for no edits, got: %s", msg)
	}
}

func TestCheckCompanionFiles_AlreadyFired(t *testing.T) {
	g := newCompanionGuardState()
	g.fired = true
	stats := newRunStats("test")
	stats.recordFileEdit("foo.go")
	msg := g.checkCompanionFiles(stats, "")
	if msg != "" {
		t.Errorf("expected empty message when already fired, got: %s", msg)
	}
}

func TestCheckCompanionFiles_TestFileEditedOnly(t *testing.T) {
	tmpDir := t.TempDir()
	// Only a test file is edited — no companion check needed.
	testFile := filepath.Join(tmpDir, "foo_test.go")
	if err := os.WriteFile(testFile, []byte("package test\n"), 0644); err != nil {
		t.Fatal(err)
	}

	g := newCompanionGuardState()
	stats := newRunStats("test")
	stats.recordFileEdit(testFile)
	msg := g.checkCompanionFiles(stats, tmpDir)
	if msg != "" {
		t.Errorf("expected empty message for test-file-only edits, got: %s", msg)
	}
}

func TestCheckCompanionFiles_SourceWithoutTestCompanion(t *testing.T) {
	tmpDir := t.TempDir()
	// Source file exists, but NO test companion.
	srcFile := filepath.Join(tmpDir, "bar.go")
	if err := os.WriteFile(srcFile, []byte("package bar\n"), 0644); err != nil {
		t.Fatal(err)
	}

	g := newCompanionGuardState()
	stats := newRunStats("test")
	stats.recordFileEdit(srcFile)
	msg := g.checkCompanionFiles(stats, tmpDir)
	if msg != "" {
		t.Errorf("expected empty message when no test companion exists, got: %s", msg)
	}
}

func TestCheckCompanionFiles_SourceWithTestCompanionNotEdited(t *testing.T) {
	tmpDir := t.TempDir()
	// Source file and test companion both exist on disk.
	srcFile := filepath.Join(tmpDir, "handler.go")
	testCompanion := filepath.Join(tmpDir, "handler_test.go")
	if err := os.WriteFile(srcFile, []byte("package handler\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(testCompanion, []byte("package handler\n"), 0644); err != nil {
		t.Fatal(err)
	}

	g := newCompanionGuardState()
	stats := newRunStats("test")
	stats.recordFileEdit(srcFile) // Only edit the source, not the test
	msg := g.checkCompanionFiles(stats, tmpDir)
	if msg == "" {
		t.Fatal("expected non-empty warning message, got empty")
	}
	if !contains(msg, "handler_test.go") {
		t.Errorf("expected message to mention handler_test.go, got: %s", msg)
	}
	if !contains(msg, "Companion File Check") {
		t.Errorf("expected message to contain guard label, got: %s", msg)
	}
}

func TestCheckCompanionFiles_BothEdited(t *testing.T) {
	tmpDir := t.TempDir()
	srcFile := filepath.Join(tmpDir, "service.go")
	testFile := filepath.Join(tmpDir, "service_test.go")
	if err := os.WriteFile(srcFile, []byte("package service\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(testFile, []byte("package service\n"), 0644); err != nil {
		t.Fatal(err)
	}

	g := newCompanionGuardState()
	stats := newRunStats("test")
	stats.recordFileEdit(srcFile)
	stats.recordFileEdit(testFile) // Both edited — no warning
	msg := g.checkCompanionFiles(stats, tmpDir)
	if msg != "" {
		t.Errorf("expected empty message when both source and test edited, got: %s", msg)
	}
}

func TestCheckCompanionFiles_FiresOnlyOnce(t *testing.T) {
	tmpDir := t.TempDir()
	srcFile := filepath.Join(tmpDir, "api.go")
	testFile := filepath.Join(tmpDir, "api_test.go")
	os.WriteFile(srcFile, []byte("package api\n"), 0644)
	os.WriteFile(testFile, []byte("package api\n"), 0644)

	g := newCompanionGuardState()
	stats := newRunStats("test")
	stats.recordFileEdit(srcFile)

	msg1 := g.checkCompanionFiles(stats, tmpDir)
	if msg1 == "" {
		t.Fatal("expected first call to produce warning")
	}

	// Second call should be empty (already fired).
	msg2 := g.checkCompanionFiles(stats, tmpDir)
	if msg2 != "" {
		t.Errorf("expected second call to be empty (already fired), got: %s", msg2)
	}
}

func TestCheckCompanionFiles_MultipleMissingCompanions(t *testing.T) {
	tmpDir := t.TempDir()
	files := []struct {
		src, test string
	}{
		{"a.go", "a_test.go"},
		{"b.go", "b_test.go"},
		{"c.go", "c_test.go"},
	}
	for _, f := range files {
		os.WriteFile(filepath.Join(tmpDir, f.src), []byte("package x\n"), 0644)
		os.WriteFile(filepath.Join(tmpDir, f.test), []byte("package x\n"), 0644)
	}

	g := newCompanionGuardState()
	stats := newRunStats("test")
	for _, f := range files {
		stats.recordFileEdit(filepath.Join(tmpDir, f.src))
	}
	msg := g.checkCompanionFiles(stats, tmpDir)
	if msg == "" {
		t.Fatal("expected warning for multiple missing companions")
	}
	// All three should be mentioned.
	for _, f := range files {
		if !contains(msg, f.test) {
			t.Errorf("expected message to mention %s, got: %s", f.test, msg)
		}
	}
}

func TestCheckCompanionFiles_RelativePath(t *testing.T) {
	tmpDir := t.TempDir()
	srcFile := filepath.Join(tmpDir, "mod.go")
	testFile := filepath.Join(tmpDir, "mod_test.go")
	os.WriteFile(srcFile, []byte("package mod\n"), 0644)
	os.WriteFile(testFile, []byte("package mod\n"), 0644)

	g := newCompanionGuardState()
	stats := newRunStats("test")
	// Edit recorded with relative path.
	stats.recordFileEdit("mod.go")
	msg := g.checkCompanionFiles(stats, tmpDir)
	if msg == "" {
		t.Fatal("expected warning for relative path with existing test companion")
	}
}

func TestCheckCompanionFiles_PythonConvention(t *testing.T) {
	tmpDir := t.TempDir()
	srcFile := filepath.Join(tmpDir, "app.py")
	testFile := filepath.Join(tmpDir, "test_app.py")
	os.WriteFile(srcFile, []byte("print('hi')\n"), 0644)
	os.WriteFile(testFile, []byte("import app\n"), 0644)

	g := newCompanionGuardState()
	stats := newRunStats("test")
	stats.recordFileEdit(srcFile)
	msg := g.checkCompanionFiles(stats, tmpDir)
	if msg == "" {
		t.Fatal("expected warning for Python file with test_app.py companion")
	}
	if !contains(msg, "test_app.py") {
		t.Errorf("expected message to mention test_app.py, got: %s", msg)
	}
}

func TestCheckCompanionFiles_SkipsVendorAndGenerated(t *testing.T) {
	tmpDir := t.TempDir()
	// Vendor file — should be skipped even if test companion exists.
	vendorSrc := filepath.Join(tmpDir, "vendor", "lib", "foo.go")
	vendorTest := filepath.Join(tmpDir, "vendor", "lib", "foo_test.go")
	os.MkdirAll(filepath.Dir(vendorSrc), 0755)
	os.WriteFile(vendorSrc, []byte("package lib\n"), 0644)
	os.WriteFile(vendorTest, []byte("package lib\n"), 0644)

	g := newCompanionGuardState()
	stats := newRunStats("test")
	stats.recordFileEdit(vendorSrc)
	msg := g.checkCompanionFiles(stats, tmpDir)
	if msg != "" {
		t.Errorf("expected empty message for vendor file, got: %s", msg)
	}
}

func TestCompanionGuardReset(t *testing.T) {
	g := newCompanionGuardState()
	g.fired = true
	g.reset()
	if g.fired {
		t.Error("expected fired=false after reset")
	}
}

func TestNormalizeCompanionPath(t *testing.T) {
	tests := []struct {
		input, want string
	}{
		{"foo.go", "foo.go"},
		{"./foo.go", "foo.go"},
		{"dir/../foo.go", "foo.go"},
		{"/abs/path/foo.go", "/abs/path/foo.go"},
	}
	for _, tt := range tests {
		if got := normalizeCompanionPath(tt.input); got != tt.want {
			t.Errorf("normalizePath(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
