package config

import (
	"os"
	"path/filepath"
	"testing"
)

// #4858: mcp_servers.<name>.skip_osv_check yaml surface - the OSV malware
// gate escape hatch must parse through to MCPServerConfig.SkipOSVCheck.
func TestLoad_MCPSkipOSVCheck(t *testing.T) {
	withTestHome(t)
	path := filepath.Join(t.TempDir(), "ggcode.yaml")
	yaml := `mcp_servers:
  - name: mirror-server
    command: npx
    args: ["-y", "@internal/mirror-mcp"]
    skip_osv_check: true
  - name: public-server
    command: npx
    args: ["-y", "@modelcontextprotocol/server-git"]
`
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(cfg.MCPServers) != 2 {
		t.Fatalf("expected 2 servers, got %d", len(cfg.MCPServers))
	}
	if !cfg.MCPServers[0].SkipOSVCheck {
		t.Error("mirror-server: skip_osv_check: true must parse to SkipOSVCheck=true")
	}
	if cfg.MCPServers[1].SkipOSVCheck {
		t.Error("public-server: omitted skip_osv_check must default to false (gate active)")
	}
}
