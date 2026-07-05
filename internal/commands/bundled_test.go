package commands

import (
	"strings"
	"testing"
)

func TestBundledSkillsIncludeOperationalSkills(t *testing.T) {
	skills := bundledSkills()
	byName := make(map[string]*Command, len(skills))
	for _, skill := range skills {
		byName[skill.Name] = skill
		if skill.Source != SourceBundled {
			t.Fatalf("skill %q source = %q, want %q", skill.Name, skill.Source, SourceBundled)
		}
		if skill.LoadedFrom != LoadedFromBundled {
			t.Fatalf("skill %q loaded_from = %q, want %q", skill.Name, skill.LoadedFrom, LoadedFromBundled)
		}
		if skill.UserInvocable {
			t.Fatalf("skill %q should not be user slash invocable", skill.Name)
		}
	}

	required := []string{"verify", "debug", "simplify", "update-config", "browser-automation", "documentation-update", "harness-run", "harness-review", "harness-promote", "harness-diagnose"}
	for _, name := range required {
		if _, ok := byName[name]; !ok {
			t.Fatalf("missing bundled skill %q", name)
		}
	}
}

func TestBundledDebugSkillLoadsInline(t *testing.T) {
	var debug *Command
	for _, skill := range bundledSkills() {
		if skill.Name == "debug" {
			debug = skill
			break
		}
	}
	if debug == nil {
		t.Fatal("missing debug skill")
	}
	if debug.Context == "fork" {
		t.Fatalf("debug skill should load inline; fork context starts a sub-agent and blocks until completion")
	}
	if len(debug.AllowedTools) != 0 {
		t.Fatalf("debug skill should not define sub-agent-only allowed tools when loaded inline")
	}
	if !strings.Contains(debug.Template, "Debug systematically") || !strings.Contains(debug.Template, "root cause") {
		t.Fatalf("debug template should still contain debugging guidance, got %q", debug.Template)
	}
}

func TestBundledUpdateConfigSkillTargetsGgcodeSchema(t *testing.T) {
	var skill *Command
	for _, cmd := range bundledSkills() {
		if cmd.Name == "update-config" {
			skill = cmd
			break
		}
	}
	if skill == nil {
		t.Fatal("missing update-config skill")
	}
	for _, needle := range []string{"ggcode.yaml", "mcp_servers", "hooks.pre_tool_use", "ui.sidebar_visible"} {
		if !strings.Contains(skill.Template, needle) {
			t.Fatalf("update-config template missing %q", needle)
		}
	}
}

func TestBundledBrowserAutomationSkillRequiresMCP(t *testing.T) {
	var skill *Command
	for _, cmd := range bundledSkills() {
		if cmd.Name == "browser-automation" {
			skill = cmd
			break
		}
	}
	if skill == nil {
		t.Fatal("missing browser-automation skill")
	}
	for _, needle := range []string{"browser", "navigate", "screenshot", "evaluate"} {
		if !strings.Contains(skill.Template, needle) {
			t.Fatalf("browser-automation template missing %q", needle)
		}
	}
}

func TestBundledDocumentationUpdateSkillIsConservative(t *testing.T) {
	var skill *Command
	for _, cmd := range bundledSkills() {
		if cmd.Name == "documentation-update" {
			skill = cmd
			break
		}
	}
	if skill == nil {
		t.Fatal("missing documentation-update skill")
	}
	for _, needle := range []string{
		"conservatively",
		"user-visible",
		"docs/releases",
		"Do not create branches, commits, pushes, or PRs",
		"no significant documentation impact",
	} {
		if !strings.Contains(skill.Template, needle) {
			t.Fatalf("documentation-update template missing %q", needle)
		}
	}
}
