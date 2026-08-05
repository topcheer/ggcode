package agent

import (
	"testing"
)

func TestErrorSentinel_ErrEqualsSentinel(t *testing.T) {
	src := `package main
import "database/sql"
func query() error {
	err := db.Query()
	if err == sql.ErrNoRows {
		return nil
	}
	return err
}`
	w := checkErrorSentinelCmp("sentinel.go", "", src)
	if !hasWarning(w, "errors.Is") {
		t.Fatalf("expected errors.Is warning, got %v", w)
	}
}

func TestErrorSentinel_ErrNotEqualSentinel(t *testing.T) {
	src := `package main
import "io"
func read() error {
	err := reader.Read()
	if err != io.EOF {
		return err
	}
	return nil
}`
	w := checkErrorSentinelCmp("sentinel.go", "", src)
	if !hasWarning(w, "errors.Is") {
		t.Fatalf("expected errors.Is warning for != comparison, got %v", w)
	}
}

func TestErrorSentinel_ReversedOrder(t *testing.T) {
	src := `package main
import "database/sql"
func query() error {
	err := db.Query()
	if sql.ErrNoRows == err {
		return nil
	}
	return err
}`
	w := checkErrorSentinelCmp("sentinel.go", "", src)
	if !hasWarning(w, "errors.Is") {
		t.Fatalf("expected errors.Is warning for reversed order, got %v", w)
	}
}

func TestErrorSentinel_CustomSentinelVar(t *testing.T) {
	src := `package main
var ErrNotFound = errors.New("not found")
func lookup() error {
	err := search()
	if err == ErrNotFound {
		return nil
	}
	return err
}`
	w := checkErrorSentinelCmp("sentinel.go", "", src)
	if !hasWarning(w, "errors.Is") {
		t.Fatalf("expected errors.Is warning for custom Err-prefixed sentinel, got %v", w)
	}
}

func TestErrorSentinel_ErrVarSuffix(t *testing.T) {
	src := `package main
import "database/sql"
func query() error {
	dbErr := db.Query()
	if dbErr == sql.ErrNoRows {
		return nil
	}
	return dbErr
}`
	w := checkErrorSentinelCmp("sentinel.go", "", src)
	if !hasWarning(w, "errors.Is") {
		t.Fatalf("expected errors.Is warning for dbErr variable, got %v", w)
	}
}

func TestErrorSentinel_ErrorsIs_OK(t *testing.T) {
	src := `package main
import ("database/sql"; "errors")
func query() error {
	err := db.Query()
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	return err
}`
	if w := checkErrorSentinelCmp("sentinel.go", "", src); len(w) != 0 {
		t.Fatalf("expected no warnings when using errors.Is, got %v", w)
	}
}

func TestErrorSentinel_NilComparison_OK(t *testing.T) {
	src := `package main
func work() error {
	err := doSomething()
	if err != nil {
		return err
	}
	return nil
}`
	if w := checkErrorSentinelCmp("sentinel.go", "", src); len(w) != 0 {
		t.Fatalf("expected no warnings for nil comparison, got %v", w)
	}
}

func TestErrorSentinel_NonErrorVarComparison_OK(t *testing.T) {
	src := `package main
import "database/sql"
func work() bool {
	result := query()
	if result == sql.ErrNoRows {
		return true
	}
	return false
}`
	// result is not an error variable name, should not trigger
	if w := checkErrorSentinelCmp("sentinel.go", "", src); len(w) != 0 {
		t.Fatalf("expected no warnings for non-error variable, got %v", w)
	}
}

func TestErrorSentinel_DeltaAware(t *testing.T) {
	oldSrc := `package main
import "database/sql"
func query() error {
	err := db.Query()
	if err == sql.ErrNoRows {
		return nil
	}
	return err
}`
	// Same pre-existing comparison: should NOT trigger (delta-aware).
	if w := checkErrorSentinelCmp("sentinel.go", oldSrc, oldSrc); len(w) != 0 {
		t.Fatalf("expected no warnings for unchanged pre-existing comparison, got %v", w)
	}
}

func TestErrorSentinel_DeltaAware_NewComparisonTriggered(t *testing.T) {
	oldSrc := `package main
import "database/sql"
func query() error {
	err := db.Query()
	return err
}`
	newSrc := `package main
import "database/sql"
func query() error {
	err := db.Query()
	if err == sql.ErrNoRows {
		return nil
	}
	return err
}`
	w := checkErrorSentinelCmp("sentinel.go", oldSrc, newSrc)
	if !hasWarning(w, "errors.Is") {
		t.Fatalf("expected errors.Is warning for newly added comparison, got %v", w)
	}
}

func TestErrorSentinel_MaxWarningsCap(t *testing.T) {
	src := `package main
import ("database/sql"; "io")
func f1() error {
	err := db.Query()
	if err == sql.ErrNoRows { return nil }
	if err != io.EOF { return err }
	if err == ErrTimeout { return nil }
	if err == ErrNotFound { return nil }
	return err
}`
	w := checkErrorSentinelCmp("sentinel.go", "", src)
	if len(w) > maxErrorSentinelWarnings {
		t.Fatalf("expected at most %d warnings, got %d", maxErrorSentinelWarnings, len(w))
	}
}

func TestErrorSentinel_NonGoFile_OK(t *testing.T) {
	if w := checkErrorSentinelCmp("test.py", "", "if err == io.EOF"); len(w) != 0 {
		t.Fatalf("expected no warnings for non-Go file, got %v", w)
	}
}

func TestErrorSentinel_SyntaxError_OK(t *testing.T) {
	src := `package main
import "database/sql"
func query() {
	if err == sql.ErrNoRows {`
	if w := checkErrorSentinelCmp("sentinel.go", "", src); len(w) != 0 {
		t.Fatalf("expected no warnings for unparseable file, got %v", w)
	}
}

func TestErrorSentinel_ContextCanceled(t *testing.T) {
	src := `package main
import "context"
func work(ctx context.Context) error {
	err := doWork(ctx)
	if err == context.Canceled {
		return nil
	}
	return err
}`
	w := checkErrorSentinelCmp("sentinel.go", "", src)
	if !hasWarning(w, "errors.Is") {
		t.Fatalf("expected errors.Is warning for context.Canceled, got %v", w)
	}
}

func TestErrorSentinel_NonErrorSelector_OK(t *testing.T) {
	src := `package main
import "database/sql"
func work() int {
	code := getCode()
	if code == sql.ErrNoRows {
		return 1
	}
	return 0
}`
	// code is not an error variable, should not trigger
	if w := checkErrorSentinelCmp("sentinel.go", "", src); len(w) != 0 {
		t.Fatalf("expected no warnings for non-error variable, got %v", w)
	}
}

func TestErrorSentinel_LessGreaterOp_OK(t *testing.T) {
	src := `package main
import "database/sql"
func work() error {
	count := db.Count()
	if count > sql.ErrNoRows {
		return nil
	}
	return nil
}`
	// > operator should not trigger
	if w := checkErrorSentinelCmp("sentinel.go", "", src); len(w) != 0 {
		t.Fatalf("expected no warnings for > operator, got %v", w)
	}
}
