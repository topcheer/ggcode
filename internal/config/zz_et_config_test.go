package config

import (
	"os"
	"path/filepath"
	"testing"
)

// /et setup|login 持久化契约:config etunnel 节 yaml 往返(subdomain/token +
// login 回执字段 plan/subdomains)。
// 服务端由 etunnel SDK 内置默认决定,config 不存 server。
// 沿 TestLoad_A2ADelegationConfig 的 tmpfile+Load(path) 模式。
func TestETunnelConfigRoundTrip(t *testing.T) {
	withTestHome(t)
	path := filepath.Join(t.TempDir(), "ggcode.yaml")
	yaml := `etunnel:
  subdomain: myteam
  token: sk-paid-1234
  plan: pro
  subdomains:
    - myteam
    - myteam2
`
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.ETunnel.Subdomain != "myteam" || cfg.ETunnel.Token != "sk-paid-1234" {
		t.Fatalf("round-trip mismatch: %+v", cfg.ETunnel)
	}
	if cfg.ETunnel.Plan != "pro" || len(cfg.ETunnel.Subdomains) != 2 || cfg.ETunnel.Subdomains[1] != "myteam2" {
		t.Fatalf("login receipt fields round-trip mismatch: %+v", cfg.ETunnel)
	}
}
