package trust

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// isolate redirects the trust store into a temp HOME so tests never touch
// the real ~/.ggcode/trust.json. Relies on config.HomeDir honoring HOME.
func isolate(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
}

func TestUnknownDirIsRestricted(t *testing.T) {
	isolate(t)
	dir := t.TempDir()
	if IsTrusted(dir) {
		t.Fatalf("unknown dir must be restricted (fail-closed)")
	}
	state, entry := Decide(dir)
	if state != StateRestricted || entry.Path != "" {
		t.Fatalf("State = %q entry %+v, want restricted with no deciding entry", state, entry)
	}
}

func TestTrustAndInheritance(t *testing.T) {
	isolate(t)
	parent := t.TempDir()
	child := filepath.Join(parent, "repo", "sub")
	if err := os.MkdirAll(child, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Trust(parent); err != nil {
		t.Fatalf("Trust: %v", err)
	}
	if !IsTrusted(child) {
		t.Fatalf("child of trusted parent must inherit trust")
	}
	if !IsTrusted(parent) {
		t.Fatalf("trusted dir itself must be trusted")
	}
	state, entry := Decide(child)
	if state != StateTrusted || entry.Path != Normalize(parent) {
		t.Fatalf("State(child) = %q entry %q, want decided by parent entry", state, entry.Path)
	}
}

func TestChildRevocationShadowsParent(t *testing.T) {
	isolate(t)
	parent := t.TempDir()
	child := filepath.Join(parent, "untrusted-repo")
	if err := os.MkdirAll(child, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Trust(parent); err != nil {
		t.Fatal(err)
	}
	if err := Untrust(child); err != nil {
		t.Fatal(err)
	}
	if IsTrusted(child) {
		t.Fatalf("explicit child revocation must shadow trusted parent")
	}
	sibling := filepath.Join(parent, "trusted-repo")
	if err := os.MkdirAll(sibling, 0o755); err != nil {
		t.Fatal(err)
	}
	if !IsTrusted(sibling) {
		t.Fatalf("sibling without revocation must stay trusted")
	}
}

func TestForgetFallsBackToParent(t *testing.T) {
	isolate(t)
	parent := t.TempDir()
	child := filepath.Join(parent, "repo")
	if err := os.MkdirAll(child, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Trust(parent); err != nil {
		t.Fatal(err)
	}
	if err := Untrust(child); err != nil {
		t.Fatal(err)
	}
	if IsTrusted(child) {
		t.Fatalf("revoked child must be restricted")
	}
	if err := Forget(child); err != nil {
		t.Fatal(err)
	}
	if !IsTrusted(child) {
		t.Fatalf("after Forget, child must inherit parent trust again")
	}
}

func TestCorruptStoreFailsClosed(t *testing.T) {
	isolate(t)
	if err := os.MkdirAll(filepath.Dir(StorePath()), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(StorePath(), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if IsTrusted(dir) {
		t.Fatalf("corrupt store must fail closed")
	}
	// Trust must also recover by rewriting a valid store.
	if err := Trust(dir); err != nil {
		t.Fatalf("Trust after corrupt store: %v", err)
	}
	if !IsTrusted(dir) {
		t.Fatalf("store must be usable after recovery rewrite")
	}
	data, err := os.ReadFile(StorePath())
	if err != nil {
		t.Fatal(err)
	}
	var s store
	if err := json.Unmarshal(data, &s); err != nil {
		t.Fatalf("recovered store is not valid JSON: %v", err)
	}
}

func TestNormalizeAliases(t *testing.T) {
	isolate(t)
	dir := t.TempDir()
	a := Normalize(dir + string(filepath.Separator) + "." + string(filepath.Separator))
	if a != Normalize(dir) {
		t.Fatalf("Normalize aliasing: %q vs %q", a, Normalize(dir))
	}
	if Normalize("") != "" {
		t.Fatalf("empty path must normalize to empty string")
	}
}

func TestEntriesSortedAndPersisted(t *testing.T) {
	isolate(t)
	d1 := t.TempDir()
	d2 := t.TempDir()
	if err := Trust(d2); err != nil {
		t.Fatal(err)
	}
	if err := Trust(d1); err != nil {
		t.Fatal(err)
	}
	entries, err := Entries()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("want 2 entries, got %d", len(entries))
	}
	if entries[0].Path > entries[1].Path {
		t.Fatalf("entries not sorted: %q > %q", entries[0].Path, entries[1].Path)
	}
	if entries[0].TrustedAt == "" || entries[0].Host == "" {
		t.Fatalf("entry metadata missing: %+v", entries[0])
	}
}

func TestEmptyDirRejected(t *testing.T) {
	isolate(t)
	if err := Trust(""); err == nil {
		t.Fatalf("Trust(\"\") must error")
	}
	if err := Untrust(""); err == nil {
		t.Fatalf("Untrust(\"\") must error")
	}
}

func TestNeedsTrustPrompt(t *testing.T) {
	isolate(t)
	// Plain empty folder: no prompt noise, and trusted folders never prompt.
	plain := t.TempDir()
	if NeedsTrustPrompt(plain) {
		t.Fatalf("plain folder must not request trust prompt")
	}
	// Project skills dir present → prompt.
	withSkills := t.TempDir()
	if err := os.MkdirAll(filepath.Join(withSkills, ".ggcode", "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !NeedsTrustPrompt(withSkills) {
		t.Fatalf("folder with project skills must request trust prompt")
	}
	// Project memory file present → prompt.
	withMemory := t.TempDir()
	if err := os.WriteFile(filepath.Join(withMemory, "AGENTS.md"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !NeedsTrustPrompt(withMemory) {
		t.Fatalf("folder with AGENTS.md must request trust prompt")
	}
	// .mcp.json present → prompt.
	withMCP := t.TempDir()
	if err := os.WriteFile(filepath.Join(withMCP, ".mcp.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !NeedsTrustPrompt(withMCP) {
		t.Fatalf("folder with .mcp.json must request trust prompt")
	}
	// After trusting, no prompt.
	if err := Trust(withSkills); err != nil {
		t.Fatal(err)
	}
	if NeedsTrustPrompt(withSkills) {
		t.Fatalf("trusted folder must not request trust prompt")
	}
}
