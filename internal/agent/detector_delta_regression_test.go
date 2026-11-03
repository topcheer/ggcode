package agent

import "testing"

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
