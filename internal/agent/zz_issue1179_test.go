package agent

import (
	"strings"
	"testing"
)

// #1179: nil_deref_check.go delta keys embed the enclosing function name
// (fnName|path|var). A rename or extract-function refactoring changes that
// component while the finding itself is unchanged, so pre-existing instances
// were re-reported as "new" - the third-generation regression surface of the
// #1069/#1128 suppression series. The delta filter now falls back to a
// name-independent suffix key (path|var): renamed code stays suppressed while
// a pattern absent from the old content still reports as genuinely new.

const issue1179RenameOld = `package main

import "fmt"

func fetch() (*Item, error) {
	return nil, fmt.Errorf("fail")
}

func oldHandler() {
	it, err := fetch()
	fmt.Println(it.Name)
	_ = err
}
`

const issue1179RenameNew = `package main

import "fmt"

func fetch() (*Item, error) {
	return nil, fmt.Errorf("fail")
}

func handleRequest() {
	it, err := fetch()
	fmt.Println(it.Name)
	_ = err
}
`

// TestNilDerefDeltaKeySurvivesFunctionRename_Issue1179 reproduces #1179: the
// identical finding moved from oldHandler to handleRequest must stay
// suppressed, exactly as #1128 required it to survive line shifts.
func TestNilDerefDeltaKeySurvivesFunctionRename_Issue1179(t *testing.T) {
	first := checkNilDerefAfterError("test.go", "", issue1179RenameNew)
	if first == "" {
		t.Fatal("#1179: baseline content must produce a warning first")
	}

	got := checkNilDerefAfterError("test.go", issue1179RenameOld, issue1179RenameNew)
	if got != "" {
		t.Fatalf("#1179: renaming the enclosing function must not resurrect a suppressed warning, got: %s", got)
	}
}

// TestNilDerefDeltaKeyStillReportsGenuinelyNewPattern_Issue1179 guards the
// fallback against over-suppression: a dereference pattern whose suffix key
// (path|var) is absent from the old content must still report.
func TestNilDerefDeltaKeyStillReportsGenuinelyNewPattern_Issue1179(t *testing.T) {
	codeNew := issue1179RenameNew + `
func extra() {
	it2, err := fetch()
	fmt.Println(it2.Name)
	_ = err
}
`
	got := checkNilDerefAfterError("test.go", issue1179RenameOld, codeNew)
	if got == "" || !strings.Contains(got, "it2") {
		t.Fatalf("#1179: a dereference pattern absent from old content must still report, got: %q", got)
	}
}

// TestNilDerefDeltaKeySurvivesExtractFunction_Issue1179 covers the
// extract-function refactoring named by the issue: the dereference moves from
// run() into a helper render() whose name the old content never contained.
func TestNilDerefDeltaKeySurvivesExtractFunction_Issue1179(t *testing.T) {
	codeOld := `package main

import "fmt"

func fetch() (*Item, error) {
	return nil, fmt.Errorf("fail")
}

func run() {
	it, err := fetch()
	fmt.Println(it.Name)
	_ = err
}
`
	codeNew := `package main

import "fmt"

func fetch() (*Item, error) {
	return nil, fmt.Errorf("fail")
}

func run() {
	render()
}

func render() {
	it, err := fetch()
	fmt.Println(it.Name)
	_ = err
}
`
	first := checkNilDerefAfterError("test.go", "", codeNew)
	if first == "" {
		t.Fatal("#1179: baseline content must produce a warning first")
	}

	got := checkNilDerefAfterError("test.go", codeOld, codeNew)
	if got != "" {
		t.Fatalf("#1179: extract-function must keep the moved dereference suppressed, got: %s", got)
	}
}
