package main

import (
	"context"
	"os"
	"testing"

	"github.com/topcheer/ggcode/internal/permission"
)

// withEnv sets env[k]=v for the duration of fn, restoring the prior value
// (or absence) afterwards.
func withEnv(t *testing.T, k, v string, fn func()) {
	t.Helper()
	old, had := os.LookupEnv(k)
	if err := os.Setenv(k, v); err != nil {
		t.Skipf("cannot set %s: %v", k, err)
	}
	defer func() {
		if had {
			_ = os.Setenv(k, old)
		} else {
			_ = os.Unsetenv(k)
		}
	}()
	fn()
}

// TestIsDumbTerminal verifies the entry-level environment probe that routes
// --plain / TERM=dumb / blank TERM away from the Bubble Tea frontend (#4604).
func TestIsDumbTerminal(t *testing.T) {
	cases := []struct {
		term string
		want bool
	}{
		{"dumb", true},
		{"DUMB", true},   // case-insensitive
		{" dumb ", true}, // surrounding whitespace
		{"", true},       // blank TERM on restricted environments
		{"xterm-256color", false},
		{"screen", false}, // screen sessions keep the TUI path
	}
	old, had := os.LookupEnv("TERM")
	if !had {
		t.Skipf("TERM not set in test environment")
	}
	defer func() { _ = os.Setenv("TERM", old) }()
	for _, c := range cases {
		setTerm := c.term
		if err := os.Setenv("TERM", setTerm); err != nil {
			t.Skipf("cannot set TERM: %v", err)
		}
		if got := isDumbTerminal(); got != c.want {
			t.Errorf("isDumbTerminal(TERM=%q) = %v, want %v", c.term, got, c.want)
		}
	}
}

// TestPlainApprovalDefaultAllow verifies the #4604 design decision: the
// linear frontend approves tools by default (no dialog exists) and only
// GGCODE_PLAIN_APPROVE=deny switches to rejection.
func TestPlainApprovalDefaultAllow(t *testing.T) {
	old, had := os.LookupEnv("GGCODE_PLAIN_APPROVE")
	defer func() {
		if had {
			_ = os.Setenv("GGCODE_PLAIN_APPROVE", old)
		} else {
			_ = os.Unsetenv("GGCODE_PLAIN_APPROVE")
		}
	}()
	_ = os.Unsetenv("GGCODE_PLAIN_APPROVE")
	if d := plainApproval(context.Background(), "run_command", "{}"); d != permission.Allow {
		t.Errorf("default approval = %v, want Allow", d)
	}

	os.Setenv("GGCODE_PLAIN_APPROVE", "deny")
	if d := plainApproval(context.Background(), "run_command", "{}"); d != permission.Deny {
		t.Errorf("GGCODE_PLAIN_APPROVE=deny approval = %v, want Deny", d)
	}

	os.Setenv("GGCODE_PLAIN_APPROVE", "DENY")
	if d := plainApproval(context.Background(), "run_command", "{}"); d != permission.Deny {
		t.Errorf("GGCODE_PLAIN_APPROVE=DENY (case-insensitive) = %v, want Deny", d)
	}
}

// TestHandlePlainCommandLang verifies /lang switching semantics on the nil-cfg
// path (pure in-memory, no persistence): valid targets switch plainLang and
// keep the loop up; unsupported languages and missing args are refused
// without changing the active language.
func TestHandlePlainCommandLang(t *testing.T) {
	orig := plainLang
	defer func() { plainLang = orig }()

	plainLang = "en"
	if handlePlainCommand("/lang fr", nil, nil) {
		t.Fatal("/lang must not exit the loop")
	}
	if plainLang != "en" {
		t.Errorf("unsupported language must not change plainLang, got %q", plainLang)
	}

	if handlePlainCommand("/lang", nil, nil) {
		t.Fatal("missing arg must not exit the loop")
	}
	if plainLang != "en" {
		t.Errorf("missing arg must not change plainLang, got %q", plainLang)
	}

	if handlePlainCommand("/lang zh-CN", nil, nil) {
		t.Fatal("valid switch must not exit the loop")
	}
	if plainLang != "zh-CN" {
		t.Errorf("plainLang = %q, want zh-CN", plainLang)
	}

	if handlePlainCommand("/lang en", nil, nil) {
		t.Fatal("switch back must not exit the loop")
	}
	if plainLang != "en" {
		t.Errorf("plainLang = %q, want en", plainLang)
	}
}

// TestHandlePlainCommandExit verifies only the exit-family commands terminate
// the linear loop; every other command (including unknown ones) keeps it up.
func TestHandlePlainCommandExit(t *testing.T) {
	old, had := os.LookupEnv("GGCODE_PLAIN_APPROVE")
	defer func() {
		if had {
			_ = os.Setenv("GGCODE_PLAIN_APPROVE", old)
		} else {
			_ = os.Unsetenv("GGCODE_PLAIN_APPROVE")
		}
	}()
	_ = os.Unsetenv("GGCODE_PLAIN_APPROVE")

	for _, cmd := range []string{"/exit", "/quit", "/q"} {
		if !handlePlainCommand(cmd, nil, nil) {
			t.Errorf("%s should exit the loop", cmd)
		}
	}
	for _, cmd := range []string{
		"/help", "/?", "/status", // nil cfg must not panic
		"/nosuchcommand",
		"/model",           // missing arg: usage, no exit
		"/provider",        // missing arg: usage, no exit
		"/endpoint",        // missing arg: usage, no exit
		"/model glm-5.3",   // nil agent/cfg: graceful message, no exit
		"/provider z ai-1", // nil agent/cfg: graceful message, no exit
	} {
		if handlePlainCommand(cmd, nil, nil) {
			t.Errorf("%s must NOT exit the loop", cmd)
		}
	}
}
