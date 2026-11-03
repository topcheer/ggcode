package mcp

import (
	"reflect"
	"testing"
)

func TestIssue3765_WarnTriggersOnlyInCommandRegion(t *testing.T) {

	if warnFlagSwallowedInCommandRegion("--env", false) {
		t.Fatal("flag while only the name is present must not warn")
	}

	for _, tok := range []string{"--env", "--header", "-t", "--transport"} {
		if !warnFlagSwallowedInCommandRegion(tok, true) {
			t.Fatalf("%s in command region must warn", tok)
		}
	}

	if warnFlagSwallowedInCommandRegion("myimage", true) {
		t.Fatal("non-flag token must not warn")
	}
}

func TestIssue3765_CanonicalShapesUnchanged(t *testing.T) {

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

func TestIssue3765_IPHostInferredFullName(t *testing.T) {

	if got := inferURLServerName("http://192.168.1.5:8080/mcp"); got != "192.168.1.5" {
		t.Fatalf("IP host inferred name = %q, want full IP", got)
	}
	if got := inferURLServerName("http://192.168.1.6:8080/mcp"); got != "192.168.1.6" {
		t.Fatalf("neighbor IP inferred name = %q, want full IP (no collision)", got)
	}

	if got := inferURLServerName("https://api.example.com/mcp"); got != "api" {
		t.Fatalf("domain inference changed: %q", got)
	}
}
