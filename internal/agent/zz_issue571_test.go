package agent

// Feature test for #571: write_integrity registry full reconciliation.
// Verifies that newly registered checks appear in allChecks.

import (
	"strings"
	"testing"
)

// TestIssue571_AllRegisteredDetectors verifies all detectors from the issue
// are registered in allChecks.
func TestIssue571_AllRegisteredDetectors(t *testing.T) {
	ensureRegistryInited()

	// Discovery 1 (HIGH): race-verify-hint
	checkRegistered(t, "race-verify-hint")

	// Discovery 2 (MEDIUM-HIGH): reward-hacking detectors
	checkRegistered(t, "hardcoded-output")
	checkRegistered(t, "suppression-directives")
	checkRegistered(t, "placeholder-code")
	checkRegistered(t, "assertion-presence")

	// Discovery 3 (MEDIUM): silent wrong behavior detectors (priority order)
	checkRegistered(t, "value-recv-mutation")
	checkRegistered(t, "unsafe-usage")
	checkRegistered(t, "unreachable-code")
	checkRegistered(t, "test-isolation")
	checkRegistered(t, "init-sideeffect")
	checkRegistered(t, "constant-conditional")
	checkRegistered(t, "unkeyed-struct")
	checkRegistered(t, "unicode-check")

	// Discovery 3 (MEDIUM): http-plaintext (security)
	checkRegistered(t, "http-plaintext")

	// #567: typosquat (should already be registered)
	checkRegistered(t, "typosquat")

	// #570: content-growth and edit-blast-radius (should already be registered)
	checkRegistered(t, "content-growth")
	checkRegistered(t, "edit-blast-radius")
}

// checkRegistered is a helper that verifies a check is in allChecks.
func checkRegistered(t *testing.T, name string) {
	t.Helper()
	found := false
	for _, c := range allChecks {
		if c.Name == name {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("%s not found in allChecks (registry incomplete)", name)
	}
}

// TestIssue571_RaceVerifyHintFires verifies race-verify-hint fires on new concurrency.
func TestIssue571_RaceVerifyHintFires(t *testing.T) {
	ensureRegistryInited()
	c := findCheck("race-verify-hint")
	if c == nil {
		t.Fatal("race-verify-hint not registered")
	}
	warnings := c.Run(CheckContext{
		FilePath:   "foo.go",
		OldContent: "func f() {}",
		NewContent: "func f() { go func() {}() }",
		Lang:       LangGo,
	})
	if len(warnings) == 0 {
		t.Error("race-verify-hint should fire on new goroutine")
	}
	if !strings.Contains(strings.Join(warnings, " "), "go test -race") {
		t.Error("race-verify-hint warning should mention 'go test -race'")
	}
}

// TestIssue571_HardcodedOutputFires verifies hardcoded-output fires on large map.
func TestIssue571_HardcodedOutputFires(t *testing.T) {
	ensureRegistryInited()
	c := findCheck("hardcoded-output")
	if c == nil {
		t.Fatal("hardcoded-output not registered")
	}
	warnings := c.Run(CheckContext{
		FilePath:   "foo.go",
		OldContent: "",
		NewContent: `func f() {
	m := map[string]string{
		"a": "1",
		"b": "2",
		"c": "3",
		"d": "4",
		"e": "5",
	}
}`,
		Lang: LangGo,
	})
	if len(warnings) == 0 {
		t.Error("hardcoded-output should fire on large map literal")
	}
}

// TestIssue571_SuppressionDirectivesFires verifies suppression-directives fires.
func TestIssue571_SuppressionDirectivesFires(t *testing.T) {
	ensureRegistryInited()
	c := findCheck("suppression-directives")
	if c == nil {
		t.Fatal("suppression-directives not registered")
	}
	warnings := c.Run(CheckContext{
		FilePath:   "foo.go",
		OldContent: "func f() {}",
		NewContent: "func f() {} //nolint:all",
		Lang:       LangGo,
	})
	if len(warnings) == 0 {
		t.Error("suppression-directives should fire on new //nolint")
	}
}

// TestIssue571_PlaceholderCodeFires verifies placeholder-code fires.
func TestIssue571_PlaceholderCodeFires(t *testing.T) {
	ensureRegistryInited()
	c := findCheck("placeholder-code")
	if c == nil {
		t.Fatal("placeholder-code not registered")
	}
	warnings := c.Run(CheckContext{
		FilePath:   "foo.go",
		OldContent: "",
		NewContent: "func f() { panic(\"not implemented\") }",
		Lang:       LangGo,
	})
	if len(warnings) == 0 {
		t.Error("placeholder-code should fire on panic(\"not implemented\")")
	}
}

// TestIssue571_AssertionPresenceFires verifies assertion-presence fires on hollow test.
func TestIssue571_AssertionPresenceFires(t *testing.T) {
	ensureRegistryInited()
	c := findCheck("assertion-presence")
	if c == nil {
		t.Fatal("assertion-presence not registered")
	}
	// Use valid Go code - empty function body with no assertions
	warnings := c.Run(CheckContext{
		FilePath:   "foo_test.go",
		OldContent: "",
		NewContent: `package foo

import "testing"

func TestFoo(t *testing.T) {
	// No assertions - this is a hollow test
}`,
		Lang: LangGo,
	})
	if len(warnings) == 0 {
		t.Error("assertion-presence should fire on hollow test")
	}
}

// findCheck returns the IntegrityCheck with the given name, or nil.
func findCheck(name string) *IntegrityCheck {
	for i := range allChecks {
		if allChecks[i].Name == name {
			return &allChecks[i]
		}
	}
	return nil
}

// ensureRegistryInited forces the check registry to initialize.
func ensureRegistryInited() {
	if allChecks == nil {
		registerAllChecks()
	}
}
