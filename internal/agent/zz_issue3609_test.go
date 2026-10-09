package agent

// #3609 probe: specGamingIsTestFile lowercases the path before HasSuffix,
// so the mixed-case "Test.java" suffix entry could never match - Java test
// files (FooTest.java) were classified as SOURCE files, and Pattern 1
// (test-weakening detection) missed edits to them entirely.

import "testing"

func TestIssue3609_JavaTestClassified(t *testing.T) {
	for _, p := range []string{"FooTest.java", "pkg/FooTest.java", "FOOTEST.JAVA"} {
		if !specGamingIsTestFile(p) {
			t.Fatalf("%s must classify as a test file", p)
		}
	}
	// Plain sources stay out.
	for _, p := range []string{"Foo.java", "pkg/Main.java"} {
		if specGamingIsTestFile(p) {
			t.Fatalf("%s must NOT classify as a test file", p)
		}
	}
}
