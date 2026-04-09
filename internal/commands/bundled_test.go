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

	required := []string{"verify", "debug", "simplify", "update-config", "browser-automation"}
	for _, name := range required {
		if _, ok := byName[name]; !ok {
			t.Fatalf("missing bundled skill %q", name)
		}
	}
}

func TestBundledDebugSkillUsesForkedContext(t *testing.T) {
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
	if debug.Context != "fork" {
		t.Fatalf("debug context = %q, want fork", debug.Context)
	}
	if len(debug.AllowedTools) == 0 {
		t.Fatal("debug skill should constrain its sub-agent tools")
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
	for _, needle := range []string{"list_mcp_capabilities", "/mcp", "mcp_servers", "@playwright/mcp"} {
		if !strings.Contains(skill.Template, needle) {
			t.Fatalf("browser-automation template missing %q", needle)
		}
	}
}
