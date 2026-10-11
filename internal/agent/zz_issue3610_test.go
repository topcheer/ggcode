package agent

// #3610 probes: three legitimate Makefile shapes were misreported as
// tampering, each burning the run's single spec-gaming budget:
//   1. delegated targets ("test: unit" + unit carries the recipe)
//   2. one-line recipes ("test: ; go test ./...")
//   3. pure-build Makefiles that never had a test target
// Real tampering shapes must keep firing (no-over-block).

import "testing"

func TestIssue3610_Form1_DelegatedTargetNotTampering(t *testing.T) {
	content := ".PHONY: test unit\ntest: unit\nunit:\n\tgo test ./...\n"
	if hasMakefileTamperingContent(content) {
		t.Fatal("delegated target (test: unit + unit recipe) must not report tampering")
	}
	// Delegation to a no-op delegate IS still gaming: make test runs echo.
	gaming := ".PHONY: test unit\ntest: unit\nunit:\n\techo ok\n"
	if !hasMakefileTamperingContent(gaming) {
		t.Fatal("delegation chain resolving to all no-ops must still report")
	}
}

func TestIssue3610_Form2_OneLineRecipeNotTampering(t *testing.T) {
	content := ".PHONY: test\ntest: ; go test ./...\n"
	if hasMakefileTamperingContent(content) {
		t.Fatal("one-line recipe (test: ; go test) must not report tampering")
	}
	gaming := ".PHONY: test\ntest: ; echo ok\n"
	if !hasMakefileTamperingContent(gaming) {
		t.Fatal("one-line no-op recipe must still report")
	}
}

func TestIssue3610_Form3_PureBuildNotTampering(t *testing.T) {
	// Library project: build/all only, no test target ever existed.
	if hasMakefileTamperingContent("build:\n\tgo build ./...\n") {
		t.Fatal("pure-build Makefile with no test trace must not report tampering")
	}
	// .PHONY still listing test = the target was deleted out from under
	// it - that trace keeps the deletion-detection firing.
	deleted := ".PHONY: build test\nbuild:\n\tgo build ./...\n"
	if !hasMakefileTamperingContent(deleted) {
		t.Fatal(".PHONY trace of deleted test target must still report")
	}
	// Deliberate disable via comment stays exempt.
	commented := "# test:\nbuild:\n\tgo build ./...\n"
	if hasMakefileTamperingContent(commented) {
		t.Fatal("commented-out test target stays exempt (deliberate disable)")
	}
}

func TestIssue3610_RealTamperingUnchanged(t *testing.T) {
	// Hollowed recipe: real target, all no-op commands.
	if !hasMakefileTamperingContent("test:\n\techo ok\n") {
		t.Fatal("all-no-op test target must still report")
	}
	// Empty test target with no delegation at all.
	if !hasMakefileTamperingContent("test:\nbuild:\n\tgo build ./...\n") {
		t.Fatal("commandless test target with no deps must still report")
	}
	// Delegation cycle must not hang or misfire.
	cycle := ".PHONY: test unit\ntest: unit\nunit: test\n\tgo test ./...\n"
	if hasMakefileTamperingContent(cycle) {
		t.Fatal("delegation cycle resolving to real commands must not report")
	}
}
