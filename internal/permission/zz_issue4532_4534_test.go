//go:build goolm

package permission

// Regression tests for GitHub issues #4532/#4533/#4534 (config_policy):
// - #4532: write-tool sensitive-path gates must expandTilde before the
//   check - "~/.backdoorrc" must gate exactly like the absolute path.
// - #4533: an explicit user ask rule must outrank every fast- Allow path
//   (always-allowed tools, benign im actions, save_memory) - the mirror
//   of the pre-existing deny pre-check.
// - #4534: an explicit read-only grant (readOnlySandbox) must survive
//   plan mode's sensitive-read exception instead of being denied.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func issue452xPolicy(t *testing.T) *ConfigPolicy {
	t.Helper()
	p := &ConfigPolicy{
		mode: SupervisedMode,
	}
	return p
}

func TestIssue4532_TildeSensitiveWriteGates(t *testing.T) {
	p := issue452xPolicy(t)
	p.mode = BypassMode
	input := json.RawMessage(`{"path":"~/.backdoorrc"}`)
	// The tilde form must gate exactly like the expanded absolute path.
	if d, _ := p.Check("write_file", input); d != Ask {
		t.Fatalf("~/.backdoorrc write in bypass must Ask, got %v", d)
	}
	// Control: an ordinary non-sensitive write stays allowed.
	if d, _ := p.Check("write_file", json.RawMessage(`{"path":"/tmp/notes.txt"}`)); d != Allow {
		t.Fatalf("ordinary write in bypass must Allow, got %v", d)
	}
	p.mode = SupervisedMode
	if d, _ := p.Check("write_file", input); d != Ask {
		t.Fatalf("~/.backdoorrc write in supervised must Ask, got %v", d)
	}
}

func TestIssue4533_ExplicitAskOutranksFastAllow(t *testing.T) {
	p := issue452xPolicy(t)
	p.rules = map[string]Decision{
		"save_memory": Ask,
		"lanchat":     Ask,
	}
	if d, _ := p.Check("save_memory", json.RawMessage(`{}`)); d != Ask {
		t.Fatalf("explicit ask pin on save_memory must be honored, got %v", d)
	}
	if d, _ := p.Check("lanchat", json.RawMessage(`{}`)); d != Ask {
		t.Fatalf("explicit ask pin on lanchat must be honored, got %v", d)
	}
	// Control: with no rule the fast path still Allows.
	p.rules = nil
	if d, _ := p.Check("save_memory", json.RawMessage(`{}`)); d != Allow {
		t.Fatalf("unpinned save_memory stays fast-Allow, got %v", d)
	}
}

func TestIssue4534_ReadOnlyGrantSurvivesPlan(t *testing.T) {
	p := issue452xPolicy(t)
	p.mode = PlanMode
	home, _ := os.UserHomeDir()
	granted := filepath.Join(home, "granted-refs")
	p.sandbox = NewPathSandbox(nil)
	p.readOnlySandbox = NewPathSandbox([]string{granted})
	target := filepath.Join(granted, "doc.md")
	// Explicit read-only grant: allowed outside plan mode, and the same
	// decision must hold inside plan mode even though the path is outside
	// the writable sandbox.
	if !p.AllowedPath(target) {
		t.Fatalf("setup: path must be covered by the read-only grant")
	}
	input := json.RawMessage(`{"path":"` + target + `"}`)
	if d, _ := p.Check("read_file", input); d == Deny {
		t.Fatalf("explicitly granted read-only path must not be denied in plan mode")
	}
}
