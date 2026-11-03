package agent

import "testing"

func TestIssue3610_Form1_DelegatedTargetNotTampering(t *testing.T) {
	content := ".PHONY: test unit\ntest: unit\nunit:\n\tgo test ./...\n"
	if hasMakefileTamperingContent(content) {
		t.Fatal("delegated target (test: unit + unit recipe) must not report tampering")
	}

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

	if hasMakefileTamperingContent("build:\n\tgo build ./...\n") {
		t.Fatal("pure-build Makefile with no test trace must not report tampering")
	}

	deleted := ".PHONY: build test\nbuild:\n\tgo build ./...\n"
	if !hasMakefileTamperingContent(deleted) {
		t.Fatal(".PHONY trace of deleted test target must still report")
	}

	commented := "# test:\nbuild:\n\tgo build ./...\n"
	if hasMakefileTamperingContent(commented) {
		t.Fatal("commented-out test target stays exempt (deliberate disable)")
	}
}

func TestIssue3610_RealTamperingUnchanged(t *testing.T) {

	if !hasMakefileTamperingContent("test:\n\techo ok\n") {
		t.Fatal("all-no-op test target must still report")
	}

	if !hasMakefileTamperingContent("test:\nbuild:\n\tgo build ./...\n") {
		t.Fatal("commandless test target with no deps must still report")
	}

	cycle := ".PHONY: test unit\ntest: unit\nunit: test\n\tgo test ./...\n"
	if hasMakefileTamperingContent(cycle) {
		t.Fatal("delegation cycle resolving to real commands must not report")
	}
}
