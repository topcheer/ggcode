package agent

import (
	"strings"
	"testing"
)

func TestIssue571_AllRegisteredDetectors(t *testing.T) {
	ensureRegistryInited()

	checkRegistered(t, "race-verify-hint")

	checkRegistered(t, "hardcoded-output")
	checkRegistered(t, "suppression-directives")
	checkRegistered(t, "placeholder-code")
	checkRegistered(t, "assertion-presence")

	checkRegistered(t, "value-recv-mutation")
	checkRegistered(t, "unsafe-usage")
	checkRegistered(t, "unreachable-code")
	checkRegistered(t, "test-isolation")
	checkRegistered(t, "init-sideeffect")
	checkRegistered(t, "constant-conditional")
	checkRegistered(t, "unkeyed-struct")
	checkRegistered(t, "unicode-check")

	checkRegistered(t, "http-plaintext")

	checkRegistered(t, "typosquat")

	checkRegistered(t, "content-growth")
	checkRegistered(t, "edit-blast-radius")
}

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

func TestIssue571_AssertionPresenceFires(t *testing.T) {
	ensureRegistryInited()
	c := findCheck("assertion-presence")
	if c == nil {
		t.Fatal("assertion-presence not registered")
	}

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

func findCheck(name string) *IntegrityCheck {
	for i := range allChecks {
		if allChecks[i].Name == name {
			return &allChecks[i]
		}
	}
	return nil
}

func ensureRegistryInited() {
	if allChecks == nil {
		registerAllChecks()
	}
}
