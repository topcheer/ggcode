package agent

import (
	"strings"
	"testing"
)

func TestCheckFlakyTestPatterns_NotTestFile(t *testing.T) {
	old := ""
	new := "time.Now()\nrand.Intn(10)\ntime.Sleep(1 * time.Second)"
	result := checkFlakyTestPatterns("main.go", old, new)
	if result != "" {
		t.Errorf("non-test file should not be checked, got: %s", result)
	}
}

func TestCheckFlakyTestPatterns_EmptyNew(t *testing.T) {
	result := checkFlakyTestPatterns("foo_test.go", "old", "")
	if result != "" {
		t.Errorf("empty new content should return empty string")
	}
}

func TestCheckFlakyTestPatterns_TimeNow(t *testing.T) {
	old := "package foo_test\n\nfunc init() {}\n"
	new := `package foo_test

import (
	"testing"
	"time"
)

func TestSomething(t *testing.T) {
	ts := time.Now()
	if ts.IsZero() {
		t.Fatal("zero time")
	}
}`
	result := checkFlakyTestPatterns("foo_test.go", old, new)
	if result == "" {
		t.Errorf("should detect time.Now() in test file")
	}
	if !strings.Contains(result, "time-dependent") {
		t.Errorf("warning should mention time-dependence, got: %s", result)
	}
}

func TestCheckFlakyTestPatterns_TimeSleep(t *testing.T) {
	old := "package foo_test\n\nfunc init() {}\n"
	new := `package foo_test

import (
	"testing"
	"time"
)

func TestWithSleep(t *testing.T) {
	time.Sleep(100 * time.Millisecond)
}`
	result := checkFlakyTestPatterns("foo_test.go", old, new)
	if result == "" {
		t.Errorf("should detect time.Sleep() in test file")
	}
	if !strings.Contains(result, "timing-dependent") {
		t.Errorf("warning should mention timing-dependence")
	}
}

func TestCheckFlakyTestPatterns_UnseededRand(t *testing.T) {
	old := "package foo_test\n\nfunc init() {}\n"
	new := `package foo_test

import (
	"math/rand"
	"testing"
)

func TestRandom(t *testing.T) {
	n := rand.Intn(100)
	if n < 0 {
		t.Fatal("negative")
	}
}`
	result := checkFlakyTestPatterns("foo_test.go", old, new)
	if result == "" {
		t.Errorf("should detect rand.Intn without seed")
	}
	if !strings.Contains(result, "non-deterministic") {
		t.Errorf("warning should mention non-determinism")
	}
}

func TestCheckFlakyTestPatterns_GoroutineWithoutSync(t *testing.T) {
	old := "package foo_test\n\nfunc init() {}\n"
	new := `package foo_test

import "testing"

func TestAsync(t *testing.T) {
	go func() {
		// do something
		_ = 1
	}()
}`
	result := checkFlakyTestPatterns("foo_test.go", old, new)
	if result == "" {
		t.Errorf("should detect goroutine without WaitGroup")
	}
	if !strings.Contains(result, "WaitGroup") {
		t.Errorf("warning should mention WaitGroup")
	}
}

func TestCheckFlakyTestPatterns_GoroutineWithWaitGroup(t *testing.T) {
	old := "package foo_test\n\nfunc init() {}\n"
	new := `package foo_test

import (
	"sync"
	"testing"
)

func TestAsync(t *testing.T) {
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = 1
	}()
	wg.Wait()
}`
	result := checkFlakyTestPatterns("foo_test.go", old, new)
	if result != "" {
		t.Errorf("goroutine with WaitGroup should not trigger warning, got: %s", result)
	}
}

func TestCheckFlakyTestPatterns_DeltaAware(t *testing.T) {
	// time.Now() already in the old content should NOT trigger.
	old := `package foo_test

import "time"

func TestOld(t *testing.T) {
	_ = time.Now()
}`
	new := old
	result := checkFlakyTestPatterns("foo_test.go", old, new)
	if result != "" {
		t.Errorf("unchanged content should not trigger (delta-aware), got: %s", result)
	}
}

func TestCheckFlakyTestPatterns_PythonTimeSleep(t *testing.T) {
	old := "import unittest\n\n\nclass OldTest(unittest.TestCase):\n    pass\n"
	new := `import unittest
import time

class MyTest(unittest.TestCase):
    def test_wait(self):
        time.sleep(2)
        self.assertTrue(True)`
	result := checkFlakyTestPatterns("test_foo.py", old, new)
	if result == "" {
		t.Errorf("should detect time.sleep() in Python test")
	}
	if !strings.Contains(result, "timing-dependent") {
		t.Errorf("warning should mention timing-dependence")
	}
}

func TestCheckFlakyTestPatterns_JavaScriptMathRandom(t *testing.T) {
	old := "describe('old', () => {\n  it('noop', () => {});\n});\n"
	new := `describe('MyTest', () => {
  it('should be random', () => {
    const val = Math.random();
    expect(val).toBeGreaterThan(0);
  });
});`
	result := checkFlakyTestPatterns("foo.test.js", old, new)
	if result == "" {
		t.Errorf("should detect Math.random() in JS test")
	}
	if !strings.Contains(result, "non-deterministic") {
		t.Errorf("warning should mention non-determinism")
	}
}

func TestCheckFlakyTestPatterns_MaxWarnings(t *testing.T) {
	old := "package foo_test\n\nfunc init() {}\n"
	// Introduce multiple flaky patterns in one edit.
	new := `package foo_test

import (
	"math/rand"
	"testing"
	"time"
)

func TestA(t *testing.T) {
	time.Sleep(1 * time.Second)
	n := rand.Intn(10)
	_ = n
	_ = time.Now()
}`
	result := checkFlakyTestPatterns("foo_test.go", old, new)
	// Count how many warnings were emitted.
	count := strings.Count(result, "\n") + 1
	if count > flakyTestMaxWarnings+1 { // +1 for the header line
		t.Errorf("should cap at %d warnings, got ~%d", flakyTestMaxWarnings, count-1)
	}
}

func TestCheckFlakyTestPatterns_NewFile(t *testing.T) {
	// New files (oldContent="") should still be checked for flaky patterns.
	new := `package foo_test

import (
	"testing"
	"time"
)

func TestNew(t *testing.T) {
	time.Sleep(1 * time.Second)
}`
	result := checkFlakyTestPatterns("foo_test.go", "", new)
	// Should still detect patterns even in new files.
	if result == "" {
		t.Errorf("new test files should still be checked for flaky patterns")
	}
}

func TestCheckFlakyTestPatterns_NoFalsePositiveCleanTest(t *testing.T) {
	old := "package foo_test\n\nfunc init() {}\n"
	new := `package foo_test

import "testing"

func TestClean(t *testing.T) {
	got := Add(1, 2)
	if got != 3 {
		t.Fatalf("expected 3, got %d", got)
	}
}`
	result := checkFlakyTestPatterns("foo_test.go", old, new)
	if result != "" {
		t.Errorf("clean deterministic test should not trigger, got: %s", result)
	}
}

func TestCheckFlakyTestPatterns_MapRangeEqualNotFlagged(t *testing.T) {
	// #119: assert.Equal in a map range loop should NOT be flagged as flaky
	// because Equal matches any equality assertion regardless of iteration order.
	old := "package foo_test\n\nfunc init() {}\n"
	new := `package foo_test

import (
	"testing"
)

func TestConfigProcessing(t *testing.T) {
	configs := map[string]int{
		"default": 30,
		"fast":    5,
	}
	for name, cfg := range configs {
		result := processConfig(cfg)
		if result != cfg {
			t.Fatalf("%s: expected %d, got %d", name, cfg, result)
		}
	}
}`
	result := checkFlakyTestPatterns("config_test.go", old, new)
	if result != "" {
		t.Errorf("map range with order-independent assertion should not trigger, got: %s", result)
	}
}

func TestCheckFlakyTestPatterns_SliceRangeNotFlagged(t *testing.T) {
	// #119: range over slice should NOT be flagged (deterministic order in Go)
	old := "package foo_test\n\nfunc init() {}\n"
	new := `package foo_test

import "testing"

func TestSliceOrder(t *testing.T) {
	items := []string{"a", "b", "c"}
	for i, v := range items {
		if items[i] != v {
			t.Fatalf("mismatch at %d", i)
		}
	}
}`
	result := checkFlakyTestPatterns("slice_test.go", old, new)
	if result != "" {
		t.Errorf("range over slice should not be flagged as flaky, got: %s", result)
	}
}
