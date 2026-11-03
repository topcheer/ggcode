package agent

import "testing"

func TestPythonDupDecl_MethodsInDifferentClassesNotDuplicates(t *testing.T) {
	oldSrc := "class A:\n    def __init__(self): pass\n    def run(self): pass\n"
	newSrc := oldSrc + "class B:\n    def __init__(self): pass\n    def run(self): pass\n"

	if dups := checkPythonDuplicateDecls(oldSrc, newSrc); len(dups) != 0 {
		t.Fatalf("adding class B with same-name methods must not be flagged, got: %+v", dups)
	}
}

func TestPythonDupDecl_SameClassDuplicateMethodStillFlagged(t *testing.T) {
	oldSrc := "class A:\n    def run(self): pass\n"
	newSrc := "class A:\n    def run(self): pass\n    def run(self): pass\n"

	dups := checkPythonDuplicateDecls(oldSrc, newSrc)
	if len(dups) != 1 || dups[0].kind != "method" || dups[0].name != "A.run" {
		t.Fatalf("same-class duplicate method must be flagged as method:A.run, got: %+v", dups)
	}
}

func TestPythonDupDecl_TopLevelDuplicateStillFlagged(t *testing.T) {
	oldSrc := "def handler(x): pass\n"
	newSrc := "def handler(x): pass\ndef handler(y): pass\n"

	dups := checkPythonDuplicateDecls(oldSrc, newSrc)
	if len(dups) != 1 || dups[0].kind != "function" || dups[0].name != "handler" {
		t.Fatalf("top-level duplicate must be flagged, got: %+v", dups)
	}
}

func TestPythonDupDecl_AsyncMethodScoping(t *testing.T) {
	oldSrc := "class A:\n    async def run(self): pass\n"
	newSrc := oldSrc + "class B:\n    async def run(self): pass\n"

	if dups := checkPythonDuplicateDecls(oldSrc, newSrc); len(dups) != 0 {
		t.Fatalf("async same-name methods in different classes must not be flagged, got: %+v", dups)
	}
}
