package agent

import (
	"strings"
	"testing"
)

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
