package agent

// #3627 probe: stripTestSuffix must re-attach the .java extension for Java
// test files. FooTest.java stripped to the bare base "Foo" (every other
// suffix family re-attaches its extension), so the Pattern 1 sourceExists
// check looked for an extension-less file and never matched the real
// Foo.java source.

import "testing"

func TestIssue3627_JavaStripReattachesExtension(t *testing.T) {
	if got := stripTestSuffix("FooTest.java"); got != "Foo.java" {
		t.Fatalf("stripTestSuffix(FooTest.java) = %q, want %q", got, "Foo.java")
	}
	if got := stripTestSuffix("pkg/FooTest.java"); got != "pkg/Foo.java" {
		t.Fatalf("stripTestSuffix(pkg/FooTest.java) = %q, want %q", got, "pkg/Foo.java")
	}
	// Whole-name edge from #588/#3609 keeps its guard.
	if got := stripTestSuffix("test.java"); got != "test.java" {
		t.Fatalf("whole-name guard regressed: %q", got)
	}
	// Other families unchanged.
	if got := stripTestSuffix("foo_test.go"); got != "foo.go" {
		t.Fatalf("go family regressed: %q", got)
	}
}
