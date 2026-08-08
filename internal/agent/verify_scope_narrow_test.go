package agent

import (
	"strings"
	"testing"
)

func TestClassifyVerificationCommand(t *testing.T) {
	tests := []struct {
		cmd     string
		wantCat string
	}{
		{"go test ./...", "go-test"},
		{"go test -tags goolm ./internal/agent/", "go-test"},
		{"go vet ./...", "go-test"},
		{"go build ./...", "go-build"},
		{"pytest", "pytest"},
		{"python -m pytest tests/", "pytest"},
		{"npm test", "npm-test"},
		{"yarn test", "npm-test"},
		{"cargo test", "cargo-test"},
		{"make test", "make-test"},
		{"make verify-ci", "make-test"},
		{"echo hello", ""},
		{"cat file.txt", ""},
	}
	for _, tt := range tests {
		cat, _ := classifyVerificationCommand(tt.cmd)
		if cat != tt.wantCat {
			t.Errorf("classifyVerificationCommand(%q) category = %q, want %q", tt.cmd, cat, tt.wantCat)
		}
	}
}

func TestExtractGoTestScope(t *testing.T) {
	// Broad scope (./...)
	scope := extractGoTestScope("go test ./...")
	if !strings.Contains(scope, "scope:broad") {
		t.Errorf("expected scope:broad, got %q", scope)
	}

	// With package
	scope = extractGoTestScope("go test ./internal/agent/")
	if !strings.Contains(scope, "pkg:./internal/agent/") {
		t.Errorf("expected pkg:./internal/agent/, got %q", scope)
	}

	// With -run filter
	scope = extractGoTestScope("go test -run TestFoo ./internal/agent/")
	if !strings.Contains(scope, "run:TestFoo") {
		t.Errorf("expected run:TestFoo, got %q", scope)
	}
}

func TestIsNarrower(t *testing.T) {
	// Broad -> specific package
	if !isNarrower("pkg:./internal/agent/|", "scope:broad") {
		t.Error("broad -> specific package should be narrower")
	}

	// Specific package -> same package + run filter
	if !isNarrower("run:TestFoo|pkg:./internal/agent/", "pkg:./internal/agent/") {
		t.Error("adding -run filter should be narrower")
	}

	// Same scope should not be narrower
	if isNarrower("scope:broad", "scope:broad") {
		t.Error("same scope should not be narrower")
	}

	// Fewer packages
	if !isNarrower("pkg:./internal/agent/", "pkg:./internal/agent/|pkg:./internal/config/") {
		t.Error("fewer packages should be narrower")
	}
}

func TestScopeNarrow_DetectionFires(t *testing.T) {
	s := newScopeNarrowState()

	// 1. Broad go test -> fails
	msg := s.recordVerificationCommand("run_command", "go test ./...",
		"FAIL  pkg1 [build failed]\nFAIL", true)
	if msg != "" {
		t.Fatal("first call should not fire")
	}

	// 2. Narrowed to one package -> still fails
	msg = s.recordVerificationCommand("run_command", "go test ./internal/agent/",
		"FAIL  TestFoo [run failed]", true)
	if msg != "" {
		t.Fatal("second call should not fire")
	}

	// 3. Narrowed further with -run filter -> passes
	msg = s.recordVerificationCommand("run_command", "go test -run TestBar ./internal/agent/",
		"ok  internal/agent/", false)
	if msg == "" {
		t.Fatal("third call should fire warning")
	}
	if !strings.Contains(msg, "Verification Scope Narrowing") {
		t.Errorf("warning should mention scope narrowing, got: %q", msg)
	}
}

func TestScopeNarrow_NoDetectionWhenExpanding(t *testing.T) {
	s := newScopeNarrowState()

	// Narrow first, then expand - should NOT fire
	s.recordVerificationCommand("run_command", "go test -run TestFoo ./internal/agent/",
		"FAIL", true)
	s.recordVerificationCommand("run_command", "go test ./internal/agent/",
		"FAIL", true)
	msg := s.recordVerificationCommand("run_command", "go test ./...",
		"ok", false)
	if msg != "" {
		t.Fatal("should not fire when scope is expanding")
	}
}

func TestScopeNarrow_NoDetectionWhenAllPass(t *testing.T) {
	s := newScopeNarrowState()

	// All pass - no fail->pass transition, should not fire
	s.recordVerificationCommand("run_command", "go test ./...", "ok", false)
	s.recordVerificationCommand("run_command", "go test ./internal/agent/", "ok", false)
	msg := s.recordVerificationCommand("run_command", "go test -run TestFoo ./internal/agent/", "ok", false)
	if msg != "" {
		t.Fatal("should not fire when all commands passed (no fail->pass)")
	}
}

func TestScopeNarrow_FiresOnlyOnce(t *testing.T) {
	s := newScopeNarrowState()

	s.recordVerificationCommand("run_command", "go test ./...", "FAIL", true)
	s.recordVerificationCommand("run_command", "go test ./internal/agent/", "FAIL", true)
	msg1 := s.recordVerificationCommand("run_command", "go test -run TestFoo ./internal/agent/", "ok", false)
	if msg1 == "" {
		t.Fatal("should fire first time")
	}

	// More narrowing - should not fire again
	msg2 := s.recordVerificationCommand("run_command", "go test -run TestBar ./internal/agent/", "ok", false)
	if msg2 != "" {
		t.Fatal("should not fire second time")
	}
}

func TestScopeNarrow_Reset(t *testing.T) {
	s := newScopeNarrowState()

	s.recordVerificationCommand("run_command", "go test ./...", "FAIL", true)
	s.recordVerificationCommand("run_command", "go test ./internal/agent/", "FAIL", true)
	_ = s.recordVerificationCommand("run_command", "go test -run TestFoo ./internal/agent/", "ok", false)

	s.reset()
	if len(s.history) != 0 {
		t.Error("reset should clear history")
	}
	if s.fired {
		t.Error("reset should clear fired flag")
	}
}

func TestScopeNarrow_PytestDetection(t *testing.T) {
	s := newScopeNarrowState()

	s.recordVerificationCommand("run_command", "pytest", "2 failed, 3 passed", true)
	s.recordVerificationCommand("run_command", "pytest tests/test_foo.py", "1 failed", true)
	msg := s.recordVerificationCommand("run_command", "pytest -k test_specific tests/test_foo.py", "3 passed", false)
	if msg == "" {
		t.Fatal("should fire for pytest scope narrowing")
	}
}

func TestScopeNarrow_NonVerificationCommand(t *testing.T) {
	s := newScopeNarrowState()
	msg := s.recordVerificationCommand("run_command", "echo hello", "hello", false)
	if msg != "" {
		t.Fatal("should not fire for non-verification commands")
	}
	if len(s.history) != 0 {
		t.Fatal("non-verification commands should not be tracked")
	}
}

func TestLooksLikeTestFailure(t *testing.T) {
	tests := []struct {
		output string
		want   bool
	}{
		{"ok  pkg/internal/agent  0.5s", false},
		{"PASS", false},
		{"FAIL  TestFoo [bad output]", true},
		{"--- FAIL: TestBar", true},
		{"panic: runtime error", true},
		{"2 failed, 3 passed", true},
		{"Error: something broke", true},
	}
	for _, tt := range tests {
		got := looksLikeTestFailure(tt.output)
		if got != tt.want {
			t.Errorf("looksLikeTestFailure(%q) = %v, want %v", tt.output, got, tt.want)
		}
	}
}

func TestScopeNarrow_MixedCategoriesDontCrossFire(t *testing.T) {
	s := newScopeNarrowState()

	// go test fails, then pytest fails, then go test narrowed passes
	// Only 2 go-test entries, should not fire (need 3 same category)
	s.recordVerificationCommand("run_command", "go test ./...", "FAIL", true)
	s.recordVerificationCommand("run_command", "pytest", "FAIL", true)
	msg := s.recordVerificationCommand("run_command", "go test -run TestFoo ./internal/agent/", "ok", false)
	if msg != "" {
		t.Fatal("should not fire with fewer than 3 same-category entries")
	}
}
