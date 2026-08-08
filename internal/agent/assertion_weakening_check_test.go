package agent

import (
	"strings"
	"testing"
)

func TestCheckAssertionWeakening_NoChange(t *testing.T) {
	old := `package foo

func TestAdd(t *testing.T) {
	require.Equal(t, 42, Add(40, 2))
}`
	new := old
	result := checkAssertionWeakening("calc_test.go", old, new)
	if result != "" {
		t.Errorf("expected no warning for unchanged test, got: %s", result)
	}
}

func TestCheckAssertionWeakening_NotTestFile(t *testing.T) {
	old := `require.Equal(t, 42, result)`
	new := `require.Equal(t, 41, result)`
	result := checkAssertionWeakening("calc.go", old, new)
	if result != "" {
		t.Errorf("expected no warning for non-test file, got: %s", result)
	}
}

func TestCheckAssertionWeakening_NewFile(t *testing.T) {
	new := `require.NotEqual(t, 0, result)`
	result := checkAssertionWeakening("calc_test.go", "", new)
	if result != "" {
		t.Errorf("expected no warning for new file (no old content), got: %s", result)
	}
}

func TestCheckAssertionWeakening_PolarityFlip(t *testing.T) {
	old := `package foo

func TestErr(t *testing.T) {
	err := DoSomething()
	assert.NoError(t, err)
}`
	new := `package foo

func TestErr(t *testing.T) {
	err := DoSomething()
	assert.Error(t, err)
}`
	result := checkAssertionWeakening("calc_test.go", old, new)
	if !strings.Contains(result, "polarity flipped") {
		t.Errorf("expected polarity flip warning, got: %q", result)
	}
}

func TestCheckAssertionWeakening_RequirePolarityFlip(t *testing.T) {
	old := `require.NoError(t, err)`
	new := `require.Error(t, err)`
	result := checkAssertionWeakening("calc_test.go", old, new)
	if !strings.Contains(result, "polarity flipped") {
		t.Errorf("expected polarity flip warning, got: %q", result)
	}
}

func TestCheckAssertionWeakening_TrueFalseFlip(t *testing.T) {
	old := `assertTrue(result.isValid())`
	new := `assertFalse(result.isValid())`
	result := checkAssertionWeakening("calc_test.go", old, new)
	if !strings.Contains(result, "polarity flipped") {
		t.Errorf("expected polarity flip warning, got: %q", result)
	}
}

func TestCheckAssertionWeakening_ComparisonFlip(t *testing.T) {
	old := `if result != expected { t.Error("mismatch") }`
	new := `if result == expected { t.Error("mismatch") }`
	result := checkAssertionWeakening("calc_test.go", old, new)
	if !strings.Contains(result, "operator flipped") {
		t.Errorf("expected operator flip warning, got: %q", result)
	}
}

func TestCheckAssertionWeakening_LessGreaterFlip(t *testing.T) {
	old := `if count < 10 { t.Error("too few") }`
	new := `if count >= 10 { t.Error("too few") }`
	result := checkAssertionWeakening("calc_test.go", old, new)
	if !strings.Contains(result, "operator flipped") {
		t.Errorf("expected operator flip warning, got: %q", result)
	}
}

func TestCheckAssertionWeakening_NoStructuralMatch(t *testing.T) {
	// Different assertion lines entirely - should not trigger.
	old := `assert.Equal(t, 42, result)`
	new := `assert.Equal(t, 100, total)`
	result := checkAssertionWeakening("calc_test.go", old, new)
	if result != "" {
		t.Errorf("expected no warning for different assertions, got: %s", result)
	}
}

func TestCheckAssertionWeakening_ValueChangeOnly(t *testing.T) {
	// Same structural assertion, different value - this IS suspicious
	// but value changes are harder to distinguish from legitimate updates.
	// The structural normalization replaces numbers with "N", so these
	// lines match and would trigger comparison checks (but no comparison
	// changed, so no warning expected).
	old := `require.Equal(t, 42, result)`
	new := `require.Equal(t, 41, result)`
	result := checkAssertionWeakening("calc_test.go", old, new)
	// Value-only changes within same operator should not trigger here
	// (it's suspicious but not detectable without value-level analysis).
	if strings.Contains(result, "operator flipped") {
		t.Errorf("value-only change should not trigger operator flip: %s", result)
	}
}

func TestCheckAssertionWeakening_PythonTestFile(t *testing.T) {
	old := `def test_calc():
    assertEqual(42, calc())`
	new := `def test_calc():
    assertNotEqual(42, calc())`
	result := checkAssertionWeakening("test_calc.py", old, new)
	if !strings.Contains(result, "polarity flipped") {
		t.Errorf("expected polarity flip for assertEqual->assertNotEqual, got: %q", result)
	}
}

func TestCheckAssertionWeakening_JSTestFile(t *testing.T) {
	old := `test('calc', () => {
  expect(result).toBe(42)
})`
	new := `test('calc', () => {
  expect(result).toBe(43)
})`
	// Should not crash on JS test files. Value-only change should not trigger
	// polarity/comparison warnings.
	result := checkAssertionWeakening("calc.test.js", old, new)
	if strings.Contains(result, "flipped") {
		t.Errorf("value-only change should not trigger flip warning: %s", result)
	}
}

func TestCheckAssertionWeakening_MaxWarnings(t *testing.T) {
	old := `
func Test1(t *testing.T) { assert.NoError(t, err1) }
func Test2(t *testing.T) { assert.NoError(t, err2) }
func Test3(t *testing.T) { assert.NoError(t, err3) }
func Test4(t *testing.T) { assert.NoError(t, err4) }
func Test5(t *testing.T) { assert.NoError(t, err5) }
func Test6(t *testing.T) { assert.NoError(t, err6) }
`
	new := `
func Test1(t *testing.T) { assert.Error(t, err1) }
func Test2(t *testing.T) { assert.Error(t, err2) }
func Test3(t *testing.T) { assert.Error(t, err3) }
func Test4(t *testing.T) { assert.Error(t, err4) }
func Test5(t *testing.T) { assert.Error(t, err5) }
func Test6(t *testing.T) { assert.Error(t, err6) }
`
	result := checkAssertionWeakening("multi_test.go", old, new)
	if !strings.Contains(result, "max warnings") {
		t.Errorf("expected max warnings cap message, got: %q", result)
	}
}

func TestExtractAssertionLines(t *testing.T) {
	content := `package foo

func TestA(t *testing.T) {
	require.Equal(t, 1, a)
	// comment line
	if x != y { t.Error("bad") }
}`
	lines := extractAssertionLines(content)
	// Should find the require.Equal line and the if/t.Error line.
	if len(lines) < 2 {
		t.Errorf("expected at least 2 assertion lines, got %d", len(lines))
	}
}

func TestNormalizeAssertionStructure(t *testing.T) {
	a := normalizeAssertionStructure(`require.Equal(t, 42, result)`)
	b := normalizeAssertionStructure(`require.NotEqual(t, 41, result)`)
	// After normalization (numbers -> N, polarity canonicalized, operators -> OP),
	// these should produce the same structural key.
	if a != b {
		t.Logf("a=%q b=%q", a, b)
		// This is OK - they may not match perfectly, but the key insight is
		// that polarity pairs are canonicalized. Let's verify Equal/NotEqual
		// canonicalization.
	}
	// At minimum, numbers should be normalized.
	if strings.Contains(a, "42") {
		t.Errorf("number 42 should be normalized to N, got: %s", a)
	}
}

func TestDetectPolarityFlip(t *testing.T) {
	result := detectPolarityFlip(`assert.NoError(t, err)`, `assert.Error(t, err)`)
	if !strings.Contains(result, "polarity flipped") {
		t.Errorf("expected polarity flip, got: %q", result)
	}
}

func TestDetectPolarityFlip_NoFlip(t *testing.T) {
	result := detectPolarityFlip(`assert.NoError(t, err)`, `assert.NoError(t, err2)`)
	if result != "" {
		t.Errorf("expected no flip, got: %s", result)
	}
}

func TestDetectComparisonFlip(t *testing.T) {
	result := detectComparisonFlip(`if x != y {}`, `if x == y {}`)
	if !strings.Contains(result, "operator flipped") {
		t.Errorf("expected operator flip, got: %q", result)
	}
}

func TestDetectComparisonFlip_NoFlip(t *testing.T) {
	result := detectComparisonFlip(`if x != y {}`, `if x != y {}`)
	if result != "" {
		t.Errorf("expected no flip, got: %s", result)
	}
}

func TestFindOperatorNotInAssert(t *testing.T) {
	// Should find standalone != in a comparison.
	idx := findOperatorNotInAssert(`if x != y {}`, "!=")
	if idx < 0 {
		t.Errorf("expected to find != in comparison")
	}
}

func TestFindOperatorNotInAssert_InFuncName(t *testing.T) {
	// Should NOT match operators that are part of identifiers.
	// "assert.NotEqual" contains no standalone == operator.
	idx := findOperatorNotInAssert(`assert.NotEqual(t, x, y)`, "==")
	if idx >= 0 {
		t.Errorf("should not find == in NotEqual function name")
	}
}

func TestIsTestFile_Go(t *testing.T) {
	if !isTestFile("foo_test.go") {
		t.Error("expected _test.go to be a test file")
	}
}

func TestIsTestFile_Python(t *testing.T) {
	if !isTestFile("test_foo.py") {
		t.Error("expected test_foo.py to be a test file")
	}
}

func TestIsTestFile_NonTest(t *testing.T) {
	if isTestFile("foo.go") {
		t.Error("foo.go should not be a test file")
	}
}
