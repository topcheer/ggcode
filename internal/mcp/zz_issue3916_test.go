package mcp

// #3916 probes: a non-runner first token that is an executable on PATH
// (docker/podman) is the COMMAND, not the server name; a genuine name
// (never on PATH) still parses as name+command; runner forms unchanged.

import (
	"os/exec"
	"testing"
)

func TestIssue3916_ExecutableFirstTokenIsCommand(t *testing.T) {
	docker, err := exec.LookPath("docker")
	if err != nil {
		t.Skip("docker unavailable - PATH heuristic path is the fix")
	}
	_ = docker
	srv, err := parseOptionTransportInstallArgs(
		[]string{"docker", "run", "-i", "--rm", "mcp/weather"}, "stdio")
	if err != nil {
		t.Fatal(err)
	}
	if srv.Command != "docker" {
		t.Fatalf("Command must be docker, got %q (name=%q)", srv.Command, srv.Name)
	}
	if len(srv.Args) != 4 || srv.Args[0] != "run" || srv.Args[3] != "mcp/weather" {
		t.Fatalf("full arg list must be the command line, got %v", srv.Args)
	}
	// The name is now INFERRED from the real command (inferCommandServerName)
	// rather than swallowing the first token while Command=run; deriving
	// "docker" from Command=docker is the correct shape.
}

func TestIssue3916_GenuineNameStillParses(t *testing.T) {
	// A real server name never resolves on PATH: name+command form intact.
	srv, err := parseOptionTransportInstallArgs(
		[]string{"my-weather-server", "/opt/weather/start.sh", "--port", "8080"}, "stdio")
	if err != nil {
		t.Fatal(err)
	}
	if srv.Name != "my-weather-server" {
		t.Fatalf("name must stay, got %q", srv.Name)
	}
	if srv.Command != "/opt/weather/start.sh" {
		t.Fatalf("command must be the second token, got %q", srv.Command)
	}
}

func TestIssue3916_RunnerFormsUnchanged(t *testing.T) {
	srv, err := parseOptionTransportInstallArgs(
		[]string{"npx", "-y", "@scope/server"}, "stdio")
	if err != nil {
		t.Fatal(err)
	}
	if srv.Command != "npx" {
		t.Fatalf("runner form regressed: command=%q", srv.Command)
	}
	// Bare command (single token) untouched.
	srv, err = parseOptionTransportInstallArgs([]string{"uvx"}, "stdio")
	if err != nil {
		t.Fatal(err)
	}
	if srv.Command != "uvx" {
		t.Fatalf("single-token form regressed: %q", srv.Command)
	}
}
