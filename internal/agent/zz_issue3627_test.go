package agent

import "testing"

func TestIssue3627_JavaStripReattachesExtension(t *testing.T) {
	if got := stripTestSuffix("FooTest.java"); got != "Foo.java" {
		t.Fatalf("stripTestSuffix(FooTest.java) = %q, want %q", got, "Foo.java")
	}
	if got := stripTestSuffix("pkg/FooTest.java"); got != "pkg/Foo.java" {
		t.Fatalf("stripTestSuffix(pkg/FooTest.java) = %q, want %q", got, "pkg/Foo.java")
	}

	if got := stripTestSuffix("test.java"); got != "test.java" {
		t.Fatalf("whole-name guard regressed: %q", got)
	}

	if got := stripTestSuffix("foo_test.go"); got != "foo.go" {
		t.Fatalf("go family regressed: %q", got)
	}
}
