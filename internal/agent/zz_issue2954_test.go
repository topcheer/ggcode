package agent

import "testing"

// #2954: bare-filename self-mod patterns (agents.md, system_prompt.json,
// mcp_server.json, ...) must match ONLY at the workspace root. Subdirectory
// files with the same basename are ordinary project files and must not
// trigger HIGH/CRITICAL "Self-Modification Warning". Anchored (.ggcode/...)
// patterns keep substring matching.

func TestIssue2954SubdirectoryBareNamesDoNotMatch(t *testing.T) {
	ordinary := []string{
		"docs/agents.md", "docs/guide/ggcode.md", "examples/claude.md",
		"assets/system_prompt.json", "examples/mcp_server.json",
		"config/hook_config", "sub/allowlist.json", "deep/dir/denylist.json",
		"vendor/skills-lock.json", "pkg/copilot.md", "subdir/ggcode.yaml",
	}
	for _, p := range ordinary {
		if m := matchSelfModTarget(p); m != nil {
			t.Errorf("matchSelfModTarget(%q) matched %q, want nil (ordinary subdirectory file, #2954)", p, m.name)
		}
	}
}

func TestIssue2954RootLevelBareNamesStillMatch(t *testing.T) {
	roots := []struct {
		path string
		name string
	}{
		{"agents.md", "memory store"},
		{"GGCODE.MD", "memory store"},
		{"./claude.md", "memory store"},
		{"ggcode.yaml", "ggcode config"},
		{"system_prompt.json", "system prompt"},
		{"mcp_server.json", "MCP server config"},
		{"skills-lock.json", "skills/commands"},
	}
	for _, r := range roots {
		m := matchSelfModTarget(r.path)
		if m == nil {
			t.Errorf("matchSelfModTarget(%q) = nil, want %q (root-level self-infrastructure file)", r.path, r.name)
			continue
		}
		if m.name != r.name {
			t.Errorf("matchSelfModTarget(%q) = %q, want %q", r.path, m.name, r.name)
		}
	}
}

func TestIssue2954AnchoredDotGgcodePatternsKeepSubstringMatch(t *testing.T) {
	anchored := []string{
		".ggcode/memory/insights.md",
		"work/.ggcode/hooks/pre.sh",
		".ggcode/skills/my-skill/SKILL.md",
	}
	for _, p := range anchored {
		if m := matchSelfModTarget(p); m == nil {
			t.Errorf("matchSelfModTarget(%q) = nil, want match (.ggcode/ anchored infrastructure, unchanged semantics)", p)
		}
	}
}

func TestIssue2954RootAwareAbsolutePathNormalization(t *testing.T) {
	// Real agent flow: tools pass ABSOLUTE paths; the state receives the
	// workspace root and must normalize before bare-name matching.
	s := newSelfModState()
	s.setRoot("/Volumes/ws/repo")

	if w := s.checkSelfModification("write_file", mustJSON(t, map[string]any{
		"file_path": "/Volumes/ws/repo/AGENTS.md", "content": "x",
	})); w == "" {
		t.Error("absolute workspace-root AGENTS.md produced no warning, want one")
	}

	if w := s.checkSelfModification("write_file", mustJSON(t, map[string]any{
		"file_path": "/Volumes/ws/repo/docs/agents.md", "content": "x",
	})); w != "" {
		t.Errorf("absolute docs/agents.md produced self-mod warning, want none: %.120s", w)
	}
}

func TestIssue2954CheckSelfModificationEndToEnd(t *testing.T) {
	// End-to-end: editing docs/agents.md must produce no warning, while
	// writing the root AGENTS.md must still produce the advisory.
	s := newSelfModState()

	if w := s.checkSelfModification("write_file", mustJSON(t, map[string]any{
		"file_path": "docs/agents.md", "content": "hello",
	})); w != "" {
		t.Errorf("write docs/agents.md produced self-mod warning, want none: %.120s", w)
	}

	if w := s.checkSelfModification("write_file", mustJSON(t, map[string]any{
		"file_path": "AGENTS.md", "content": "hello",
	})); w == "" {
		t.Error("write root AGENTS.md produced no self-mod warning, want one")
	}
}
