package agent

import "testing"

// #4638: the compound-command amnesty guard used ContainsAny("||;&&") - a
// char SET, so any single | & ; lost the not-found/pytest amnesty. The most
// common verification shape (`go test ./... 2>&1`) exited 127 on hosts
// without the toolchain and was judged FAILED instead of skipped, firing
// doomed auto-repair loops.
func TestIssue4638_RedirectKeepsAmnesty(t *testing.T) {
	if !isNonFailureExit("go test ./... 2>&1", 127) {
		t.Fatal("2>&1 redirect must keep the not-found amnesty (#4638)")
	}
	if !isNonFailureExit("cat log &> /dev/null", 127) {
		t.Fatal("bash &> redirect must keep the amnesty (#4638)")
	}
	if !isNonFailureExit("cat log | grep err", 127) {
		t.Fatal("single pipe must keep the amnesty (#4638)")
	}
}

// Control: true compound commands still lose the amnesty (#1522/#4402).
func TestIssue4638_CompoundStillLosesAmnesty(t *testing.T) {
	for _, cmd := range []string{
		"go test ./... || npm run build",
		"go test ./... && npm run build",
		"go test ./pkg; pytest",
	} {
		if isNonFailureExit(cmd, 127) {
			t.Fatalf("compound command %q must lose the not-found amnesty (#1522)", cmd)
		}
		if isNonFailureExit(cmd, 5) {
			t.Fatalf("compound command %q must lose the pytest amnesty (#4402)", cmd)
		}
	}
}

// Control: pytest exit 5 amnesty unchanged for simple commands.
func TestIssue4638_PytestFiveStillAmnesty(t *testing.T) {
	if !isNonFailureExit("pytest tests/", 5) {
		t.Fatal("plain pytest exit 5 must keep the amnesty (#4402)")
	}
}
