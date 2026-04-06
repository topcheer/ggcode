package mcp

import "testing"

func TestParseInstallArgsInfersNPXServerName(t *testing.T) {
	server, err := ParseInstallArgs([]string{"stdio", "npx", "-y", "12306-mcp", "stdio"})
	if err != nil {
		t.Fatal(err)
	}
	if server.Name != "12306-mcp" {
		t.Fatalf("expected inferred name 12306-mcp, got %q", server.Name)
	}
	if server.Type != "stdio" || server.Command != "npx" {
		t.Fatalf("unexpected stdio server config: %+v", server)
	}
	if len(server.Args) != 3 || server.Args[2] != "stdio" {
		t.Fatalf("unexpected stdio args: %+v", server.Args)
	}
}

func TestParseInstallArgsSupportsCommandFirstStdioSyntax(t *testing.T) {
	server, err := ParseInstallArgs([]string{"npx", "-y", "12306-mcp", "stdio"})
	if err != nil {
		t.Fatal(err)
	}
	if server.Name != "12306-mcp" || server.Type != "stdio" || server.Command != "npx" {
		t.Fatalf("unexpected command-first stdio config: %+v", server)
	}
}

func TestParseInstallArgsSupportsExplicitName(t *testing.T) {
	server, err := ParseInstallArgs([]string{"train", "stdio", "npx", "-y", "12306-mcp", "stdio"})
	if err != nil {
		t.Fatal(err)
	}
	if server.Name != "train" {
		t.Fatalf("expected explicit name train, got %q", server.Name)
	}
}

func TestParseInstallArgsSupportsHTTPURL(t *testing.T) {
	server, err := ParseInstallArgs([]string{"http", "https://mcp.example.com/api"})
	if err != nil {
		t.Fatal(err)
	}
	if server.Name != "mcp" {
		t.Fatalf("expected inferred host-based name mcp, got %q", server.Name)
	}
	if server.URL != "https://mcp.example.com/api" {
		t.Fatalf("unexpected URL: %q", server.URL)
	}
}

func TestParseInstallArgsInfersUVXServerName(t *testing.T) {
	server, err := ParseInstallArgs([]string{"stdio", "uvx", "wikipedia-mcp-server@latest"})
	if err != nil {
		t.Fatal(err)
	}
	if server.Name != "wikipedia-mcp" {
		t.Fatalf("expected inferred uvx name wikipedia-mcp, got %q", server.Name)
	}
	if server.Command != "uvx" || len(server.Args) != 1 || server.Args[0] != "wikipedia-mcp-server@latest" {
		t.Fatalf("unexpected uvx server config: %+v", server)
	}
}

func TestParseInstallArgsRejectsMissingTarget(t *testing.T) {
	if _, err := ParseInstallArgs([]string{"stdio"}); err == nil {
		t.Fatal("expected missing target error")
	}
}

func TestParseInstallArgsSupportsEnvWithDelimitedCommand(t *testing.T) {
	server, err := ParseInstallArgs([]string{
		"mcp-name",
		"--env", "ZAI_AI_API_KEY=xxxx",
		"--env", "DEBUG=1",
		"--",
		"npx", "-y", "@z_ai/mcp-server",
	})
	if err != nil {
		t.Fatal(err)
	}
	if server.Name != "mcp-name" || server.Type != "stdio" || server.Command != "npx" {
		t.Fatalf("unexpected delimited stdio config: %+v", server)
	}
	if len(server.Args) != 2 || server.Args[1] != "@z_ai/mcp-server" {
		t.Fatalf("unexpected delimited stdio args: %+v", server.Args)
	}
	if server.Env["ZAI_AI_API_KEY"] != "xxxx" || server.Env["DEBUG"] != "1" {
		t.Fatalf("unexpected env map: %+v", server.Env)
	}
}

func TestParseInstallArgsSupportsDelimitedHTTPEnv(t *testing.T) {
	server, err := ParseInstallArgs([]string{
		"http",
		"--env", "TOKEN=secret",
		"--",
		"https://mcp.example.com/api",
	})
	if err != nil {
		t.Fatal(err)
	}
	if server.Name != "mcp" || server.Type != "http" || server.URL != "https://mcp.example.com/api" {
		t.Fatalf("unexpected delimited http config: %+v", server)
	}
	if server.Env["TOKEN"] != "secret" {
		t.Fatalf("unexpected env map: %+v", server.Env)
	}
}

func TestParseInstallArgsRejectsBadEnvAssignment(t *testing.T) {
	if _, err := ParseInstallArgs([]string{"demo", "--env", "BROKEN", "--", "npx", "-y", "pkg"}); err == nil {
		t.Fatal("expected invalid env assignment error")
	}
}

func TestParseInstallArgsSupportsTransportFlagAndHeaders(t *testing.T) {
	server, err := ParseInstallArgs([]string{
		"http-demo",
		"-t", "http",
		"https://mcp.example.com/api",
		"--header", "Authorization: Bearer abc",
		"--header", "X-Api-Key=xyz",
	})
	if err != nil {
		t.Fatal(err)
	}
	if server.Name != "http-demo" || server.Type != "http" || server.URL != "https://mcp.example.com/api" {
		t.Fatalf("unexpected http config: %+v", server)
	}
	if server.Headers["Authorization"] != "Bearer abc" || server.Headers["X-Api-Key"] != "xyz" {
		t.Fatalf("unexpected headers: %+v", server.Headers)
	}
}

func TestParseInstallArgsRejectsHeadersForStdio(t *testing.T) {
	if _, err := ParseInstallArgs([]string{"demo", "stdio", "npx", "-y", "pkg", "--header", "Authorization: nope"}); err == nil {
		t.Fatal("expected stdio header rejection")
	}
}
