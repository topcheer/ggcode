package agent

import (
	"testing"
)

func TestCheckTestGaming_NewFile(t *testing.T) {
	// New files (empty oldContent) should not trigger test gaming warnings.
	result := checkTestGaming("foo_test.go", "", "package foo\n")
	if result != "" {
		t.Errorf("expected empty result for new file, got: %s", result)
	}
}

func TestCheckTestGaming_NonTestFile(t *testing.T) {
	// Non-test files should not trigger test gaming warnings.
	result := checkTestGaming("main.go", "package main\n", "package main\n")
	if result != "" {
		t.Errorf("expected empty result for non-test file, got: %s", result)
	}
}

func TestCheckTestGaming_NoChange(t *testing.T) {
	// No changes to test assertions/skips/functions = no warning.
	old := `package foo
import "testing"
func TestA(t *testing.T) {
	if 1+1 != 2 {
		t.Errorf("math broken")
	}
}
`
	result := checkTestGaming("foo_test.go", old, old)
	if result != "" {
		t.Errorf("expected empty result for unchanged content, got: %s", result)
	}
}

func TestCheckTestGaming_DeletedTestFunc(t *testing.T) {
	old := `package foo
import "testing"
func TestA(t *testing.T) {
	t.Errorf("fail")
}
func TestB(t *testing.T) {
	t.Errorf("fail")
}
`
	newSrc := `package foo
import "testing"
// Tests removed
`
	result := checkTestGaming("foo_test.go", old, newSrc)
	if result == "" {
		t.Fatal("expected warning for deleted test functions, got empty")
	}
	if !contains(result, "removed 2 test function") {
		t.Errorf("expected 'removed 2 test function' in warning, got: %s", result)
	}
}

func TestCheckTestGaming_AddedSkipDirective(t *testing.T) {
	old := `package foo
import "testing"
func TestA(t *testing.T) {
	if 1+1 != 2 {
		t.Errorf("math broken")
	}
}
`
	newSrc := `package foo
import "testing"
func TestA(t *testing.T) {
	t.Skip("skipping broken test")
}
`
	result := checkTestGaming("foo_test.go", old, newSrc)
	if result == "" {
		t.Fatal("expected warning for added skip directive, got empty")
	}
	if !contains(result, "skip") {
		t.Errorf("expected 'skip' in warning, got: %s", result)
	}
}

func TestCheckTestGaming_PythonSkipDirective(t *testing.T) {
	old := `import unittest
class MyTest(unittest.TestCase):
    def test_foo(self):
        self.assertEqual(1, 1)
    def test_bar(self):
        self.assertEqual(2, 2)
`
	newSrc := `import unittest
class MyTest(unittest.TestCase):
    def test_foo(self):
        self.assertEqual(1, 1)
    def test_bar(self):
        self.skipTest("not working")
`
	result := checkTestGaming("test_foo.py", old, newSrc)
	if result == "" {
		t.Fatal("expected warning for Python skip directive, got empty")
	}
}

func TestCheckTestGaming_AssertionRemoval(t *testing.T) {
	old := `package foo
import "testing"
func TestA(t *testing.T) {
	t.Errorf("error 1")
	t.Errorf("error 2")
	t.Errorf("error 3")
}
`
	newSrc := `package foo
import "testing"
func TestA(t *testing.T) {
	// all assertions removed
}
`
	result := checkTestGaming("foo_test.go", old, newSrc)
	if result == "" {
		t.Fatal("expected warning for removed assertions, got empty")
	}
}

func TestCheckTestGaming_SingleAssertionRemovalNotFlagged(t *testing.T) {
	// Removing a single assertion is legitimate refactoring - should NOT flag.
	old := `package foo
import "testing"
func TestA(t *testing.T) {
	t.Errorf("error")
}
`
	newSrc := `package foo
import "testing"
func TestA(t *testing.T) {
	// refactored
}
`
	result := checkTestGaming("foo_test.go", old, newSrc)
	// Single assertion removal should only trigger if skip or func deletion, not assertion count.
	// The assertion removal threshold is 2+, so this should not trigger from assertion removal alone.
	// But t.Errorf -> nothing might still trigger... check it's not about assertion removal.
	if result != "" && contains(result, "removed 1 assertion") {
		t.Errorf("should not flag removal of single assertion, got: %s", result)
	}
}

func TestCheckTestGaming_JSSkipDirective(t *testing.T) {
	old := `describe('suite', () => {
  it('test1', () => {
    expect(1).toBe(1);
  });
  it('test2', () => {
    expect(2).toBe(2);
  });
});
`
	newSrc := `describe('suite', () => {
  it('test1', () => {
    expect(1).toBe(1);
  });
  it.skip('test2', () => {
    expect(2).toBe(2);
  });
});
`
	result := checkTestGaming("app.test.js", old, newSrc)
	if result == "" {
		t.Fatal("expected warning for JS .skip(), got empty")
	}
}

func TestGoTestFuncNames_ParseError(t *testing.T) {
	result := goTestFuncNames("test.go", "this is not valid go code {{{")
	if result != nil {
		t.Errorf("expected nil for parse error, got: %v", result)
	}
}

func TestGoTestFuncNames_Normal(t *testing.T) {
	src := `package foo
import "testing"
func TestA(t *testing.T) {}
func TestB(t *testing.T) {}
func HelperFunc() {}
func BenchmarkX(b *testing.B) {}
`
	result := goTestFuncNames("test.go", src)
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !result["TestA"] {
		t.Error("expected TestA in result")
	}
	if !result["TestB"] {
		t.Error("expected TestB in result")
	}
	if !result["BenchmarkX"] {
		t.Error("expected BenchmarkX in result")
	}
	if result["HelperFunc"] {
		t.Error("HelperFunc should not be in result")
	}
}

func TestCountSkipDirectives(t *testing.T) {
	content := `package foo
import "testing"
func TestA(t *testing.T) {
	t.Skip("skip")
	t.Skipf("skip %s", "fmt")
}
`
	count := countSkipDirectives("foo_test.go", content)
	if count != 2 {
		t.Errorf("expected 2 skip directives, got %d", count)
	}
}

func TestCountActiveAssertions(t *testing.T) {
	content := `package foo
import "testing"
func TestA(t *testing.T) {
	t.Errorf("active")
	// t.Errorf("commented")
	/* t.Errorf("block commented") */
	assert.Equal(t, 1, 1)
}
`
	count := countActiveAssertions(content)
	if count != 2 {
		t.Errorf("expected 2 active assertions, got %d", count)
	}
}

func TestCheckTestGaming_AddedFuncNotFlagged(t *testing.T) {
	// Adding a new test function should NOT trigger deleted-func warning.
	old := `package foo
import "testing"
func TestA(t *testing.T) {
	t.Errorf("error")
}
`
	newSrc := `package foo
import "testing"
func TestA(t *testing.T) {
	t.Errorf("error")
}
func TestB(t *testing.T) {
	t.Errorf("error")
}
`
	result := checkTestGaming("foo_test.go", old, newSrc)
	// Adding a test function with assertion should not trigger any warning.
	if result != "" {
		t.Errorf("expected no warning when adding test, got: %s", result)
	}
}

// contains is defined in reflection_test.go
