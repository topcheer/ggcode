package agent

import "testing"

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

func TestIssue4638_PytestFiveStillAmnesty(t *testing.T) {
	if !isNonFailureExit("pytest tests/", 5) {
		t.Fatal("plain pytest exit 5 must keep the amnesty (#4402)")
	}
}
