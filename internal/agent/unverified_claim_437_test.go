package agent

import "testing"

// #437: make clean/tidy are NOT verification; test-runner names must match
// on word boundaries.
func TestIsBuildTestCommandTightened(t *testing.T) {
	for _, c := range []string{"make clean", "make tidy", "cat jestfile.txt", "ls pytest_dir"} {
		if isBuildTestCommand(c) {
			t.Errorf("%q must NOT count as verification", c)
		}
	}
	for _, c := range []string{"make test", "make verify-ci", "make check", "go test ./...", "npx jest", "pytest -x"} {
		if !isBuildTestCommand(c) {
			t.Errorf("%q must count as verification", c)
		}
	}
}
