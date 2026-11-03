package agent

import "testing"

func TestIssue3609_JavaTestClassified(t *testing.T) {
	for _, p := range []string{"FooTest.java", "pkg/FooTest.java", "FOOTEST.JAVA"} {
		if !specGamingIsTestFile(p) {
			t.Fatalf("%s must classify as a test file", p)
		}
	}

	for _, p := range []string{"Foo.java", "pkg/Main.java"} {
		if specGamingIsTestFile(p) {
			t.Fatalf("%s must NOT classify as a test file", p)
		}
	}
}
