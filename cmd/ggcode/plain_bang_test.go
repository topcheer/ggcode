package main

import (
	"bytes"
	"strings"
	"testing"
)

// The ! shell passthrough (shell-mode input on the linear REPL): input is
// only read while the agent is idle, and !cmd executes directly in the
// user's shell without an agent round-trip.

func TestRunPlainBang_EchoStreamsOutput(t *testing.T) {
	var buf bytes.Buffer
	runPlainBang(&buf, "echo bang-hello")
	if out := buf.String(); !strings.Contains(out, "bang-hello") {
		t.Fatalf("echo output must stream to the writer, got: %q", out)
	}
}

func TestRunPlainBang_NonzeroExitReported(t *testing.T) {
	var buf bytes.Buffer
	runPlainBang(&buf, "exit 3")
	out := buf.String()
	if !strings.Contains(out, "3") {
		t.Fatalf("nonzero exit code must be surfaced, got: %q", out)
	}
}

func TestRunPlainBang_EmptyShowsUsage(t *testing.T) {
	var buf bytes.Buffer
	runPlainBang(&buf, "   ")
	if out := buf.String(); !strings.Contains(out, "!") {
		t.Fatalf("empty command must print usage mentioning !, got: %q", out)
	}
}

func TestHandlePlainCommand_BangRoutesAndContinues(t *testing.T) {
	// A ! line is a command-family line: it must not exit the REPL and must
	// not fall through to the agent path.
	if exit := handlePlainCommand("!echo routed-ok", nil, nil); exit {
		t.Fatal("bang passthrough must never exit the REPL")
	}
}

func TestBangShellCmd_PlatformShell(t *testing.T) {
	c := bangShellCmd("echo x")
	if c.Path == "" && c.Err != nil {
		t.Fatalf("expected a resolvable shell command, got err: %v", c.Err)
	}
}
