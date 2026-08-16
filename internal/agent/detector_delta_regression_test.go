package agent

import "testing"

// Regression tests for #185 (content fingerprint delta) and
// #186 (multiset delta): line shifts and fix-one-add-one must be handled.
// (The error-sentinel line-shift test was removed with its host detector in
// #510; the two empty-error-body tests below are the live regression pair.)

func TestEmptyError_FixOneAddOneIsDetected(t *testing.T) {
	oldSrc := `package main
func f() error {
	err := g()
	if err != nil {
	}
	return nil
}
func h() error {
	err := i()
	if err != nil {
	}
	return nil
}`
	// Fix the FIRST empty block, introduce a NEW one with a different
	// condition variable: counts stay equal (2 vs 2) but the new one must
	// be reported.
	newSrc := `package main
func f() error {
	err := g()
	if err != nil {
		return err
	}
	return nil
}
func h() error {
	err := i()
	if err != nil {
	}
	return nil
}
func k() error {
	err := j()
	if err != nil {
	}
	return nil
}`
	w := checkEmptyErrorBody("e.go", oldSrc, newSrc)
	if len(w) != 1 {
		t.Fatalf("expected exactly 1 warning for newly introduced empty body (fix-1-add-1), got %d: %v", len(w), w)
	}
}

func TestEmptyError_LineShiftDoesNotRereport(t *testing.T) {
	oldSrc := `package main
func f() error {
	err := g()
	if err != nil {
	}
	return nil
}`
	newSrc := `package main
// new comment line
func f() error {
	err := g()
	if err != nil {
	}
	return nil
}`
	w := checkEmptyErrorBody("e.go", oldSrc, newSrc)
	if len(w) != 0 {
		t.Fatalf("expected no warnings for line-shifted pre-existing empty body, got %v", w)
	}
}
