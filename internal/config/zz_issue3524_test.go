package config

// #3524 probe: migrateMCPFinding must resolve server identity the same way
// detectPlaintextAPIKeysFromRaw does (name, or mcp_%d for anonymous) and
// must migrate duplicate-named servers, not just the first match.

import (
	"strings"
	"testing"
)

func rawWithServers(servers ...map[string]interface{}) map[string]interface{} {
	list := make([]interface{}, len(servers))
	for i, s := range servers {
		list[i] = s
	}
	return map[string]interface{}{"mcp_servers": list}
}

func srvEnv(name string, env map[string]interface{}) map[string]interface{} {
	m := map[string]interface{}{"env": env}
	if name != "" {
		m["name"] = name
	}
	return m
}

func TestIssue3524_AnonymousServerMigrates(t *testing.T) {
	// Anonymous server at index 0: detect names it "mcp_0".
	raw := rawWithServers(srvEnv("", map[string]interface{}{"TOKEN": "sk-plain-123456"}))
	f := APIKeyFinding{Section: "mcp_env", KeyPath: "mcp_servers[mcp_0].env.TOKEN", EnvVar: "MCP_0_TOKEN"}
	entries := map[string]string{}
	migrateMCPFinding(raw, f, entries)

	env, _ := raw["mcp_servers"].([]interface{})[0].(map[string]interface{})["env"].(map[string]interface{})
	got, _ := env["TOKEN"].(string)
	if !strings.HasPrefix(got, "${MCP_0_TOKEN}") {
		t.Fatalf("anonymous server env must migrate to a reference, got %q", got)
	}
	if entries["MCP_0_TOKEN"] != "sk-plain-123456" {
		t.Fatalf("secret must be captured in envEntries, got %v", entries)
	}
}

func TestIssue3524_DuplicateNamesBothMigrate(t *testing.T) {
	raw := rawWithServers(
		srvEnv("dup", map[string]interface{}{"TOKEN": "sk-first-aaa111"}),
		srvEnv("dup", map[string]interface{}{"TOKEN": "sk-second-bbb222"}),
	)
	f := APIKeyFinding{Section: "mcp_env", KeyPath: "mcp_servers[dup].env.TOKEN", EnvVar: "MCP_DUP_TOKEN"}
	entries := map[string]string{}
	migrateMCPFinding(raw, f, entries)

	for i := 0; i < 2; i++ {
		env, _ := raw["mcp_servers"].([]interface{})[i].(map[string]interface{})["env"].(map[string]interface{})
		got, _ := env["TOKEN"].(string)
		if !strings.HasPrefix(got, "${MCP_DUP_TOKEN}") {
			t.Fatalf("server %d with duplicate name must also migrate, got %q", i, got)
		}
	}
}

func TestIssue3524_NamedServerStillMigrates(t *testing.T) {
	raw := rawWithServers(srvEnv("real", map[string]interface{}{"TOKEN": "sk-named-ccc333"}))
	f := APIKeyFinding{Section: "mcp_env", KeyPath: "mcp_servers[real].env.TOKEN", EnvVar: "MCP_REAL_TOKEN"}
	entries := map[string]string{}
	migrateMCPFinding(raw, f, entries)
	env, _ := raw["mcp_servers"].([]interface{})[0].(map[string]interface{})["env"].(map[string]interface{})
	got, _ := env["TOKEN"].(string)
	if got != "${MCP_REAL_TOKEN}" {
		t.Fatalf("named server migration regressed: %q", got)
	}
}
