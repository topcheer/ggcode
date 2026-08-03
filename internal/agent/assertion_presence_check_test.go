package agent

import (
	"testing"
)

func TestCheckAssertionPresence_NotTestFile(t *testing.T) {
	result := checkAssertionPresence("internal/agent/main.go", "", "package main\nfunc main() {}\n")
	if result != "" {
		t.Errorf("expected no warning for non-test file, got: %s", result)
	}
}

func TestCheckAssertionPresence_HollowTest(t *testing.T) {
	newContent := `package agent

import "testing"

func TestSomething(t *testing.T) {
	x := 1 + 1
	_ = x
}
`
	result := checkAssertionPresence("foo_test.go", "", newContent)
	if result == "" {
		t.Fatal("expected warning for hollow test function")
	}
	if !contains(result, "TestSomething") {
		t.Errorf("expected warning to mention TestSomething, got: %s", result)
	}
}

func TestCheckAssertionPresence_TestWithAssertion(t *testing.T) {
	newContent := `package agent

import "testing"

func TestSomething(t *testing.T) {
	if 1+1 != 2 {
		t.Errorf("expected 2, got %d", 1+1)
	}
}
`
	result := checkAssertionPresence("foo_test.go", "", newContent)
	if result != "" {
		t.Errorf("expected no warning for test with assertion, got: %s", result)
	}
}

func TestCheckAssertionPresence_TestWithFatal(t *testing.T) {
	newContent := `package agent

import "testing"

func TestFatal(t *testing.T) {
	t.Fatal("immediate failure")
}
`
	result := checkAssertionPresence("foo_test.go", "", newContent)
	if result != "" {
		t.Errorf("expected no warning for test with t.Fatal, got: %s", result)
	}
}

func TestCheckAssertionPresence_TestWithRequire(t *testing.T) {
	newContent := `package agent

import (
	"testing"
	"github.com/stretchr/testify/require"
)

func TestRequire(t *testing.T) {
	require.Equal(t, 1, 1)
}
`
	result := checkAssertionPresence("foo_test.go", "", newContent)
	if result != "" {
		t.Errorf("expected no warning for test with require.Equal, got: %s", result)
	}
}

func TestCheckAssertionPresence_TestWithAssert(t *testing.T) {
	newContent := `package agent

import (
	"testing"
	"github.com/stretchr/testify/assert"
)

func TestAssert(t *testing.T) {
	assert.True(t, true)
}
`
	result := checkAssertionPresence("foo_test.go", "", newContent)
	if result != "" {
		t.Errorf("expected no warning for test with assert.True, got: %s", result)
	}
}

func TestCheckAssertionPresence_TestMainExcluded(t *testing.T) {
	newContent := `package agent

import (
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	os.Exit(m.Run())
}
`
	result := checkAssertionPresence("foo_test.go", "", newContent)
	if result != "" {
		t.Errorf("expected no warning for TestMain (excluded), got: %s", result)
	}
}

func TestCheckAssertionPresence_PreExistingHollowNotFlagged(t *testing.T) {
	// A hollow test that existed before should not be flagged (delta-aware).
	oldContent := `package agent

import "testing"

func TestHollow(t *testing.T) {
	_ = 1
}
`
	newContent := oldContent // no change
	result := checkAssertionPresence("foo_test.go", oldContent, newContent)
	if result != "" {
		t.Errorf("expected no warning for pre-existing hollow test, got: %s", result)
	}
}

func TestCheckAssertionPresence_AssertionRemovedRegression(t *testing.T) {
	oldContent := `package agent

import "testing"

func TestReg(t *testing.T) {
	t.Errorf("fail")
}
`
	newContent := `package agent

import "testing"

func TestReg(t *testing.T) {
	_ = 1
}
`
	result := checkAssertionPresence("foo_test.go", oldContent, newContent)
	if result == "" {
		t.Fatal("expected warning for regression (assertions removed)")
	}
	if !contains(result, "TestReg") {
		t.Errorf("expected warning to mention TestReg, got: %s", result)
	}
}

func TestCheckAssertionPresence_MultipleHollowTests(t *testing.T) {
	newContent := `package agent

import "testing"

func TestOne(t *testing.T) {
	_ = 1
}

func TestTwo(t *testing.T) {
	_ = 2
}

func TestThree(t *testing.T) {
	_ = 3
}
`
	result := checkAssertionPresence("foo_test.go", "", newContent)
	if result == "" {
		t.Fatal("expected warning for multiple hollow tests")
	}
	if !contains(result, "TestOne") {
		t.Errorf("expected TestOne in warning, got: %s", result)
	}
}

func TestCheckAssertionPresence_MixedHollowAndValid(t *testing.T) {
	newContent := `package agent

import "testing"

func TestGood(t *testing.T) {
	if true != true {
		t.Error("fail")
	}
}

func TestBad(t *testing.T) {
	_ = 42
}
`
	result := checkAssertionPresence("foo_test.go", "", newContent)
	if result == "" {
		t.Fatal("expected warning for hollow TestBad")
	}
	if contains(result, "TestGood") {
		t.Errorf("TestGood should not be flagged (has assertion), got: %s", result)
	}
	if !contains(result, "TestBad") {
		t.Errorf("expected TestBad in warning, got: %s", result)
	}
}

func TestCheckAssertionPresence_SubTestWithRunExcluded(t *testing.T) {
	// A test that calls t.Run (sub-tests) should not be flagged because
	// assertions may be in the sub-test closures.
	newContent := `package agent

import "testing"

func TestParent(t *testing.T) {
	t.Run("sub1", func(t *testing.T) {
		if 1 != 1 {
			t.Error("fail")
		}
	})
}
`
	// This test DOES have assertions in the sub-test, but our AST walker
	// will find the t.Error inside the closure. Let's verify it doesn't flag.
	result := checkAssertionPresence("foo_test.go", "", newContent)
	if result != "" {
		t.Errorf("expected no warning for test with sub-test assertions, got: %s", result)
	}
}
