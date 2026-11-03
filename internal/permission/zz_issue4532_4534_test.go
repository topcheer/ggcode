//go:build goolm

package permission

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

	if d, _ := p.Check("write_file", input); d != Ask {
		t.Fatalf("~/.backdoorrc write in bypass must Ask, got %v", d)
	}

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

	if !p.AllowedPath(target) {
		t.Fatalf("setup: path must be covered by the read-only grant")
	}
	input := json.RawMessage(`{"path":"` + target + `"}`)
	if d, _ := p.Check("read_file", input); d == Deny {
		t.Fatalf("explicitly granted read-only path must not be denied in plan mode")
	}
}
