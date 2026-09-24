package config

import (
	"testing"

	"gopkg.in/yaml.v3"
)

func TestStatusLineConfigParse(t *testing.T) {
	var cfg Config
	src := []byte("vendor: ZAI\nstatusline:\n  command: \"~/bin/mystatus.sh\"\n  timeout_ms: 500\n")
	if err := yaml.Unmarshal(src, &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if cfg.StatusLine.Command != "~/bin/mystatus.sh" {
		t.Fatalf("Command = %q", cfg.StatusLine.Command)
	}
	if cfg.StatusLine.TimeoutMS != 500 {
		t.Fatalf("TimeoutMS = %d, want 500", cfg.StatusLine.TimeoutMS)
	}
	if !cfg.StatusLine.Enabled() {
		t.Fatal("Enabled() = false, want true")
	}
}

func TestStatusLineConfigDisabledByDefault(t *testing.T) {
	var cfg Config
	if err := yaml.Unmarshal([]byte("vendor: ZAI\n"), &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if cfg.StatusLine.Enabled() {
		t.Fatal("Enabled() = true for empty config, want false")
	}
}
