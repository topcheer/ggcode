package mcp

// #3765 probes: greedy flag parsing is preserved for canonical interleaved
// shapes, but a flag consumed INSIDE the command region now emits a loud
// warning pointing at `--` (#3765: zero-signal double rewrite was the bug;
// changing the parse itself breaks name-flags-URL-flags ordering).

import (
	"reflect"
	"testing"
)

func TestIssue3765_WarnTriggersOnlyInCommandRegion(t *testing.T) {
	// Before the command region (only the name seen): no warning.
	if warnFlagSwallowedInCommandRegion("--env", false) {
		t.Fatal("flag while only the name is present must not warn")
	}
	// Inside the command region: every installer flag warns.
	for _, tok := range []string{"--env", "--header", "-t", "--transport"} {
		if !warnFlagSwallowedInCommandRegion(tok, true) {
			t.Fatalf("%s in command region must warn", tok)
		}
	}
	// Non-flag tokens never warn.
	if warnFlagSwallowedInCommandRegion("myimage", true) {
		t.Fatal("non-flag token must not warn")
	}
}

func TestIssue3765_CanonicalShapesUnchanged(t *testing.T) {
	// Name-first with flags around the URL: parse result must be identical
	// to the pre-fix behavior (the warning is additive only).
	positionals, opts, err := parseInstallOptions([]string{
		"http-demo", "-t", "http", "https://mcp.example.com/api",
		"--header", "Authorization: Bearer abc",
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"http-demo", "https://mcp.example.com/api"}; !reflect.DeepEqual(positionals, want) {
		t.Fatalf("positionals = %v, want %v", positionals, want)
	}
	if opts.transport != "http" || opts.headers["Authorization"] != "Bearer abc" {
		t.Fatalf("canonical parse changed: %+v", opts)
	}
	// Command-first WITHOUT `--`: the greedy parse consumes --env into
	// installer env (the #3765 behavior under complaint) - the warning now
	// fires for it; the parse result itself is the documented greedy shape.
	positionals, opts, err = parseInstallOptions([]string{"stdio", "docker", "run", "--env", "FOO=bar", "myimage"})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"stdio", "docker", "run", "myimage"}; !reflect.DeepEqual(positionals, want) {
		t.Fatalf("greedy command region shape changed, got %v", positionals)
	}
	if opts.env["FOO"] != "bar" {
		t.Fatalf("greedy env capture changed: %v", opts.env)
	}
}

func TestIssue3765_SeparatorStillWorks(t *testing.T) {
	// The `--` escape hatch is handled at the ParseInstallArgs layer
	// (separator stripping, L342) - verify end-to-end that a quoted
	// command keeps its own flags out of installer env.
	server, err := ParseInstallArgs([]string{
		"docker-demo",
		"--env", "API_KEY=secret",
		"--",
		"docker", "run", "--env", "FOO=bar", "myimage",
	})
	if err != nil {
		t.Fatal(err)
	}
	if server.Command != "docker" {
		t.Fatalf("command = %q", server.Command)
	}
	if want := []string{"run", "--env", "FOO=bar", "myimage"}; !reflect.DeepEqual(server.Args, want) {
		t.Fatalf("quoted command args = %v, want %v", server.Args, want)
	}
	if server.Env["FOO"] == "bar" {
		t.Fatalf("quoted --env leaked into installer env: %v", server.Env)
	}
	if server.Env["API_KEY"] != "secret" {
		t.Fatalf("pre-separator installer env lost: %v", server.Env)
	}
}
