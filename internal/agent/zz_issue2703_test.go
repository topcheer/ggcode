package agent

// #2703 regression: the regex declaration counter must not flag legal
// block-scoped declarations as duplicates.
//
// Scenario 1 (JS local const): two functions each declare a local
// `const result` — block scope, legal, must not be flagged.
// Scenario 2 (JS nested function): two functions each nest
// `function init()` — legal ES6 block scoping, must not be flagged.
// Scenario 3 (Python nested def): two methods of one class each define a
// nested `def helper()` — local functions, must not be counted twice as
// A.helper.
// Control: genuine top-level duplicates are still flagged.

import "testing"

func TestIssue2703_JSLocalConstNotFlagged(t *testing.T) {
	old := `function a() {
	const result = computeA();
	return result;
}
`
	new := `function a() {
	const result = computeA();
	return result;
}

function b() {
	const result = computeB(); // same name, different block scope
	return result;
}
`
	if dups := checkJSDuplicateDecls(old, new, ".js"); len(dups) != 0 {
		t.Fatalf("local const in separate function bodies flagged: %+v", dups)
	}
}

func TestIssue2703_JSNestedFunctionNotFlagged(t *testing.T) {
	old := `function handler() {
	function init() { setup(); }
	return init;
}
`
	new := `function handler() {
	function init() { setup(); }
	return init;
}

function other() {
	function init() { otherSetup(); }
	return init;
}
`
	if dups := checkJSDuplicateDecls(old, new, ".js"); len(dups) != 0 {
		t.Fatalf("nested block-scoped functions flagged: %+v", dups)
	}
}

func TestIssue2703_PythonNestedDefNotFlagged(t *testing.T) {
	old := `class A:
    def run(self):
        def helper():
            return 1
        return helper()
`
	new := `class A:
    def run(self):
        def helper():
            return 1
        return helper()

    def walk(self):
        def helper():  # local to walk, legal
            return 2
        return helper()
`
	if dups := checkPythonDuplicateDecls(old, new); len(dups) != 0 {
		t.Fatalf("nested local defs counted as duplicate methods: %+v", dups)
	}
}

func TestIssue2703_PythonModuleDefAfterClassNotMisattributed(t *testing.T) {
	// A module-level def AFTER a class must not be attributed to it.
	old := "class A:\n    def run(self):\n        pass\n"
	new := "class A:\n    def run(self):\n        pass\n\ndef run():\n    pass\n"
	dups := checkPythonDuplicateDecls(old, new)
	// A.run (method) and top-level run (function) are different keys: no dup.
	for _, d := range dups {
		if d.kind == "method" && d.name == "A.run" {
			t.Fatalf("module-level def after class misattributed as method: %+v", d)
		}
	}
}

func TestIssue2703_GenuineTopLevelDuplicatesStillFlagged(t *testing.T) {
	// Control: true file-scope duplicates keep firing.
	old := "const foo = () => 1;\n"
	new := "const foo = () => 1;\nconst foo = () => 2;\n"
	dups := checkJSDuplicateDecls(old, new, ".js")
	if len(dups) == 0 {
		t.Fatalf("genuine top-level duplicate const not flagged")
	}

	oldPy := "def run():\n    pass\n"
	newPy := "def run():\n    pass\n\ndef run():\n    pass\n"
	if dups := checkPythonDuplicateDecls(oldPy, newPy); len(dups) == 0 {
		t.Fatalf("genuine top-level duplicate def not flagged")
	}
}
