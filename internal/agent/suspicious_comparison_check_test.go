package agent

import (
	"strings"
	"testing"
)

func TestCheckSuspiciousComparison_SentinelError(t *testing.T) {
	code := `package main
import (
	"database/sql"
	"errors"
)
func findUser(id int) error {
	var name string
	err := mockQuery(id, &name)
	if err == sql.ErrNoRows {
		return errors.New("not found")
	}
	return err
}
func mockQuery(id int, name *string) error { return nil }
`
	result := checkSuspiciousComparison("test.go", "", code)
	if result == "" {
		t.Fatal("expected detection of err == sql.ErrNoRows")
	}
	if !strings.Contains(result, "sql.ErrNoRows") {
		t.Errorf("expected mention of sql.ErrNoRows, got: %s", result)
	}
	if !strings.Contains(result, "errors.Is") {
		t.Errorf("expected suggestion to use errors.Is(), got: %s", result)
	}
}

func TestCheckSuspiciousComparison_IOEOF(t *testing.T) {
	code := `package main
import "io"
func readAll(r io.Reader) error {
	_, err := r.Read(nil)
	if err == io.EOF {
		return nil
	}
	return err
}
`
	result := checkSuspiciousComparison("reader.go", "", code)
	if result == "" {
		t.Fatal("expected detection of err == io.EOF")
	}
	if !strings.Contains(result, "io.EOF") {
		t.Errorf("expected mention of io.EOF, got: %s", result)
	}
}

func TestCheckSuspiciousComparison_NilComparison(t *testing.T) {
	code := `package main
func process() error {
	err := doSomething()
	if err != nil {
		return err
	}
	return nil
}
func doSomething() error { return nil }
`
	result := checkSuspiciousComparison("test.go", "", code)
	if result != "" {
		t.Errorf("expected no detection for err != nil, got: %s", result)
	}
}

func TestCheckSuspiciousComparison_TwoErrors(t *testing.T) {
	code := `package main
var ErrSentinel = newErr("sentinel")
func compare(err1, err2 error) bool {
	return err1 == err2
}
func newErr(msg string) error { return nil }
`
	result := checkSuspiciousComparison("test.go", "", code)
	if result == "" {
		t.Fatal("expected detection of err1 == err2 comparison")
	}
	if !strings.Contains(result, "consider errors.Is") {
		t.Errorf("expected suggestion to consider errors.Is, got: %s", result)
	}
}

func TestCheckSuspiciousComparison_DeltaAware(t *testing.T) {
	oldCode := `package main
import "database/sql"
func findUser() error {
	err := mockErr()
	if err == sql.ErrNoRows {
		return nil
	}
	return err
}
func mockErr() error { return nil }
`
	newCode := `package main
import "database/sql"
func findUser() error {
	err := mockErr()
	if err == sql.ErrNoRows {
		return nil
	}
	return err
}
func mockErr() error { return nil }
func cleanup() {}
`
	result := checkSuspiciousComparison("test.go", oldCode, newCode)
	if result != "" {
		t.Errorf("delta-aware check should not re-flag pre-existing comparisons, got: %s", result)
	}
}

func TestCheckSuspiciousComparison_NonGoFile(t *testing.T) {
	result := checkSuspiciousComparison("test.py", "", "if err == nil:\n  pass")
	if result != "" {
		t.Errorf("expected no detection for non-Go file, got: %s", result)
	}
}

func TestCheckSuspiciousComparison_TestFileSkipped(t *testing.T) {
	code := `package main
import "database/sql"
func TestFoo(t *testing.T) {
	err := mockErr()
	if err == sql.ErrNoRows {
		t.Skip("no rows")
	}
}
func mockErr() error { return nil }
`
	result := checkSuspiciousComparison("foo_test.go", "", code)
	if result != "" {
		t.Errorf("expected no detection for test file, got: %s", result)
	}
}

func TestCheckSuspiciousComparison_NotEqualSentinel(t *testing.T) {
	code := `package main
import "database/sql"
func findUser() error {
	err := mockErr()
	if err != sql.ErrNoRows {
		return err
	}
	return nil
}
func mockErr() error { return nil }
`
	result := checkSuspiciousComparison("test.go", "", code)
	if result == "" {
		t.Fatal("expected detection of err != sql.ErrNoRows")
	}
	if !strings.Contains(result, "sql.ErrNoRows") {
		t.Errorf("expected mention of sql.ErrNoRows, got: %s", result)
	}
}

func TestCheckSuspiciousComparison_OsErrNotExist(t *testing.T) {
	code := `package main
import "os"
func checkFile(err error) bool {
	return err == os.ErrNotExist
}
`
	result := checkSuspiciousComparison("test.go", "", code)
	if result == "" {
		t.Fatal("expected detection of err == os.ErrNotExist")
	}
}

func TestCheckSuspiciousComparison_NonErrorComparison(t *testing.T) {
	code := `package main
func compare(a, b int) bool {
	return a == b
}
`
	result := checkSuspiciousComparison("test.go", "", code)
	if result != "" {
		t.Errorf("expected no detection for int comparison, got: %s", result)
	}
}

func TestCheckSuspiciousComparison_SyntaxError(t *testing.T) {
	code := `package main
func broken(`
	result := checkSuspiciousComparison("test.go", "", code)
	if result != "" {
		t.Errorf("expected no detection for syntax error, got: %s", result)
	}
}

func TestCheckSuspiciousComparison_FloatEquality(t *testing.T) {
	code := `package main
func check(ratio float64) bool {
	if ratio == 0.1 {
		return true
	}
	return false
}
`
	result := checkSuspiciousComparison("test.go", "", code)
	if result == "" {
		t.Fatal("expected detection of float equality comparison")
	}
	if !strings.Contains(result, "float") {
		t.Errorf("expected mention of float, got: %s", result)
	}
}

func TestCheckSuspiciousComparison_SelfComparison(t *testing.T) {
	code := `package main
func check(x int) bool {
	if x == x {
		return true
	}
	return false
}
`
	result := checkSuspiciousComparison("test.go", "", code)
	if result == "" {
		t.Fatal("expected detection of self-comparison x == x")
	}
	if !strings.Contains(result, "self-comparison") {
		t.Errorf("expected mention of self-comparison, got: %s", result)
	}
}

func TestCheckSuspiciousComparison_ConstantBoolCondition(t *testing.T) {
	code := `package main
func check() {
	if true {
		_ = 1
	}
	for false {
		_ = 2
	}
}
`
	result := checkSuspiciousComparison("test.go", "", code)
	if result == "" {
		t.Fatal("expected detection of constant boolean condition")
	}
	if !strings.Contains(result, "constant boolean") {
		t.Errorf("expected mention of constant boolean, got: %s", result)
	}
}

func TestCheckSuspiciousComparison_FloatNotFlaggedForInt(t *testing.T) {
	code := `package main
func check(x int) bool {
	return x == 42
}
`
	result := checkSuspiciousComparison("test.go", "", code)
	if result != "" {
		t.Errorf("expected no detection for int literal comparison, got: %s", result)
	}
}

func TestCheckSuspiciousComparison_DeltaAwareFloat(t *testing.T) {
	oldCode := `package main
func check(r float64) bool {
	if r == 0.1 { return true }
	return false
}
`
	newCode := `package main
func check(r float64) bool {
	if r == 0.1 { return true }
	if r == 0.2 { return false }
	return false
}
`
	result := checkSuspiciousComparison("test.go", oldCode, newCode)
	// Should only flag the NEW float comparison (0.2), not the existing one (0.1)
	if !strings.Contains(result, "0.2") {
		t.Errorf("expected detection of new 0.2 comparison, got: %s", result)
	}
	if strings.Contains(result, "0.1") {
		t.Errorf("should not re-flag existing 0.1 comparison, got: %s", result)
	}
}
