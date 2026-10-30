package main

import (
	"os"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/version"
)

func TestMaskConfigSecrets_InlineAndList(t *testing.T) {
	in := `vendor: openai
model: gpt-5
api_key: sk-abcdefghijklmnop1234
session_token_budget: 500000
im:
  adapters:
    telegram:
      token: "000000000:AAFAKE_TOKEN_FAKE_TOKEN_FAKE_TOKEN_FAKE"
mcp_servers:
  fetch:
    env:
      API_TOKEN: sk-secretvalue123456
`
	out := maskConfigSecrets(in)

	if !strings.Contains(out, "vendor: openai") || !strings.Contains(out, "model: gpt-5") {
		t.Errorf("non-secret keys must pass through:\n%s", out)
	}
	if strings.Contains(out, "sk-abcdefghijklmnop1234") {
		t.Errorf("inline api_key leaked:\n%s", out)
	}
	if !strings.Contains(out, "api_key: sk-a") {
		t.Errorf("api_key should be masked with prefix retained:\n%s", out)
	}
	// Budget knob contains "token" but must NOT be masked.
	if !strings.Contains(out, "session_token_budget: 500000") {
		t.Errorf("session_token_budget is a budget knob, not a secret:\n%s", out)
	}
	// Env-var style references stay readable.
	if strings.Contains(out, "AAEhBOweik6ad9r") {
		t.Errorf("IM token leaked:\n%s", out)
	}
}

func TestMaskConfigSecrets_SecretKeyListItems(t *testing.T) {
	in := `a2a:
  api_keys:
    - sk-firstsecretvalue00000
    - sk-secondsecretvalue11
  enabled: true
`
	out := maskConfigSecrets(in)
	if strings.Contains(out, "sk-firstsecretvalue00000") || strings.Contains(out, "sk-secondsecretvalue11") {
		t.Errorf("api_keys list items leaked:\n%s", out)
	}
	if !strings.Contains(out, "enabled: true") {
		t.Errorf("sibling key after list must survive:\n%s", out)
	}
}

func TestMaskConfigSecrets_EmptyAndEnvRef(t *testing.T) {
	in := "api_key: \nother: ${MY_KEY}\n"
	out := maskConfigSecrets(in)
	// Empty value stays empty (unset), env ref passes through.
	if !strings.Contains(out, "api_key:") {
		t.Errorf("empty api_key line mangled:\n%s", out)
	}
	if !strings.Contains(out, "other: ${MY_KEY}") {
		t.Errorf("env reference must pass through:\n%s", out)
	}
}

func TestNewConfigCmd_PrintsVersionPathAndMaskedConfig(t *testing.T) {
	t.Setenv("HOME", t.TempDir()) // isolate the machine's global ggcode.yaml
	dir := t.TempDir()
	cfgPath := dir + "/ggcode.yaml"
	yaml := "vendor: testv\nendpoint: ep1\nmodel: testmodel\napi_key: sk-verylongsecretkey123\n" +
		"vendors:\n  testv:\n    api_key: sk-verylongsecretkey123\n    endpoints:\n      ep1:\n        protocol: openai\n"
	if err := writeFileForTest(cfgPath, yaml); err != nil {
		t.Fatal(err)
	}
	cfgFile := cfgPath
	cmd := newConfigCmd(&cfgFile)
	var sb strings.Builder
	cmd.SetOut(&sb)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := sb.String()
	if !strings.Contains(got, "ggcode "+version.Display()) {
		t.Errorf("version line missing:\n%s", got)
	}
	if !strings.Contains(got, "config file: "+cfgPath) {
		t.Errorf("config path line missing:\n%s", got)
	}
	if !strings.Contains(got, "vendor: testv") || !strings.Contains(got, "model: testmodel") {
		t.Errorf("main config values missing:\n%s", got)
	}
	if strings.Contains(got, "sk-verylongsecretkey123") {
		t.Errorf("api_key leaked in output:\n%s", got)
	}
}

func TestNewConfigCmd_HonorsRootConfigFlagComposition(t *testing.T) {
	// The root --config flag passes its value via the shared pointer; the
	// subcommand must prefer it over the default resolution.
	t.Setenv("HOME", t.TempDir()) // isolate the machine's global ggcode.yaml
	dir := t.TempDir()
	cfgPath := dir + "/alt.yaml"
	if err := writeFileForTest(cfgPath, "vendor: altv\nendpoint: ep1\nvendors:\n  altv:\n    endpoints:\n      ep1:\n        protocol: openai\n"); err != nil {
		t.Fatal(err)
	}
	cfgFile := cfgPath
	cmd := newConfigCmd(&cfgFile)
	var sb strings.Builder
	cmd.SetOut(&sb)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(sb.String(), "vendor: altv") {
		t.Errorf("--config=alt.yaml composition failed:\n%s", sb.String())
	}
}

func writeFileForTest(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o644)
}
