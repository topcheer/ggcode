package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadParsesIMConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ggcode.yaml")
	if err := os.WriteFile(path, []byte(`
vendor: zai
endpoint: cn-coding-openai
model: glm-5-turbo
im:
  enabled: true
  active_session_policy: local-active
  require_local_session: true
  streaming:
    enabled: true
    transport: edit
    edit_interval_sec: 1.5
    buffer_threshold: 48
    cursor: " ▉"
  stt:
    provider: zai
    base_url: https://example.com/asr
    api_key: secret
    model: glm-asr
  adapters:
    qq:
      enabled: true
      platform: qq
      transport: builtin
      allow_from: ["u1", "u2"]
      targets:
        - id: ops
          label: Ops Group
          channel: group-1
      extra:
        markdown_support: true
`), 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if !cfg.IM.Enabled {
		t.Fatalf("expected IM to be enabled")
	}
	if cfg.IM.ActiveSessionPolicy != "local-active" {
		t.Fatalf("unexpected active session policy: %q", cfg.IM.ActiveSessionPolicy)
	}
	if cfg.IM.RequireLocalSession == nil || !*cfg.IM.RequireLocalSession {
		t.Fatalf("expected require_local_session to be true")
	}
	if cfg.IM.Streaming.Transport != "edit" || !cfg.IM.Streaming.Enabled {
		t.Fatalf("unexpected streaming config: %#v", cfg.IM.Streaming)
	}
	if cfg.IM.STT.Provider != "zai" || cfg.IM.STT.Model != "glm-asr" {
		t.Fatalf("unexpected STT config: %#v", cfg.IM.STT)
	}
	qq, ok := cfg.IM.Adapters["qq"]
	if !ok {
		t.Fatalf("expected qq adapter config")
	}
	if !qq.Enabled || qq.Platform != "qq" || qq.Transport != "builtin" {
		t.Fatalf("unexpected qq adapter config: %#v", qq)
	}
	if len(qq.AllowFrom) != 2 {
		t.Fatalf("unexpected qq ACL config: %#v", qq)
	}
	if len(qq.Targets) != 1 || qq.Targets[0].ID != "ops" || qq.Targets[0].Channel != "group-1" {
		t.Fatalf("unexpected qq targets config: %#v", qq.Targets)
	}
	if got := qq.Extra["markdown_support"]; got != true {
		t.Fatalf("unexpected qq extra config: %#v", qq.Extra)
	}
}

func TestAddIMTargetPersistsTarget(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ggcode.yaml")
	cfg := DefaultConfig()
	cfg.FilePath = path
	cfg.IM.Enabled = true
	cfg.IM.Adapters = map[string]IMAdapterConfig{
		"qq-bot-1": {
			Enabled:  true,
			Platform: "qq",
		},
	}
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save returned error: %v", err)
	}
	if err := cfg.AddIMTarget("qq-bot-1", IMTargetConfig{
		ID:      "ops",
		Label:   "Ops Room",
		Channel: "group-1",
	}); err != nil {
		t.Fatalf("AddIMTarget returned error: %v", err)
	}
	reloaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	targets := reloaded.IM.Adapters["qq-bot-1"].Targets
	if len(targets) != 1 {
		t.Fatalf("expected one target, got %#v", targets)
	}
	if targets[0].ID != "ops" || targets[0].Channel != "group-1" || targets[0].Label != "Ops Room" {
		t.Fatalf("unexpected target: %#v", targets[0])
	}
}

func TestAddIMAdapterPersistsAdapter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ggcode.yaml")
	cfg := DefaultConfig()
	cfg.FilePath = path
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save returned error: %v", err)
	}
	if err := cfg.AddIMAdapter("qq-main", IMAdapterConfig{
		Enabled:  true,
		Platform: "qq",
		Extra: map[string]interface{}{
			"appid":     "123456",
			"appsecret": "secret-abc",
		},
	}); err != nil {
		t.Fatalf("AddIMAdapter returned error: %v", err)
	}
	reloaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	adapter, ok := reloaded.IM.Adapters["qq-main"]
	if !ok {
		t.Fatalf("expected qq-main adapter, got %#v", reloaded.IM.Adapters)
	}
	if adapter.Platform != "qq" {
		t.Fatalf("unexpected adapter: %#v", adapter)
	}
	if got := adapter.Extra["appid"]; got != "123456" {
		t.Fatalf("unexpected appid: %#v", adapter.Extra)
	}
	if got := adapter.Extra["appsecret"]; got != "secret-abc" {
		t.Fatalf("unexpected appsecret: %#v", adapter.Extra)
	}
}
