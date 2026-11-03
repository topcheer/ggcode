//go:build goolm

package wailskit

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/im"
)

func setupIssue591Home(t *testing.T) {
	t.Helper()
	tmpDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tmpDir, ".ggcode"), 0o755); err != nil {
		t.Fatalf("create .ggcode dir: %v", err)
	}
	t.Setenv("HOME", tmpDir)
}

func TestIssue591_C1_PrivateclawInRegistry(t *testing.T) {
	if imPlatformByID("privateclaw") == nil {
		t.Fatal("privateclaw missing from registry — doc-compliant adapters fail Test Connection")
	}
	if imPlatformByID("Telegram") == nil || imPlatformByID("telegram") == nil {
		t.Fatal("platform lookup must be case-insensitive")
	}
	if imPlatformByID("no-such-platform") != nil {
		t.Fatal("unknown platform must return nil")
	}
}

func TestIssue591_C1_PrivateclawAdapterTestable(t *testing.T) {
	setupIssue591Home(t)
	if err := SaveIMAdapter("pc", map[string]string{
		"platform": "privateclaw", "enabled": "true",
	}); err != nil {
		t.Fatalf("SaveIMAdapter: %v", err)
	}
	if err := TestIMConnection("pc"); err != nil {
		t.Fatalf("privateclaw adapter must be testable: %v", err)
	}
}

func TestIssue591_C2_IntExtraNotMisreported(t *testing.T) {
	setupIssue591Home(t)

	cfg, err := config.Load(config.ConfigPath())
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	cfg.IM.Adapters = map[string]config.IMAdapterConfig{
		"qq-int": {
			Enabled:  true,
			Platform: "qq",
			Extra:    map[string]interface{}{"appid": 123456789, "appsecret": "s3cret"},
		},
		"qq-empty": {
			Enabled:  true,
			Platform: "qq",
			Extra:    map[string]interface{}{"appid": "", "appsecret": "s3cret"},
		},
	}
	if err := cfg.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}

	if err := TestIMConnection("qq-int"); err != nil {
		t.Fatalf("populated int field misreported as empty: %v", err)
	}
	if err := TestIMConnection("qq-empty"); err == nil {
		t.Fatal("empty string field must still fail validation")
	}
}

func TestIssue591_C3_EmptyWorkspaceNeverMatches(t *testing.T) {
	ws, isCurrent := firstBoundWorkspace([]string{""}, "", "")
	if isCurrent {
		t.Fatal("empty workspace must never match (both-empty comparison regression)")
	}
	if ws != "" {
		t.Fatalf("empty binding must not surface as display workspace, got %q", ws)
	}

	ws, isCurrent = firstBoundWorkspace([]string{"/proj"}, "/proj", "/proj")
	if !isCurrent || ws != "/proj" {
		t.Fatalf("real match broken: ws=%q isCurrent=%v", ws, isCurrent)
	}
}

var _ = im.ChannelBinding{}
