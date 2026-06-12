package acp

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

// AgentDef describes a known ACP-compatible CLI tool.
type AgentDef struct {
	Name        string   // canonical name: "copilot", "droid", "opencode"
	Title       string   // display name: "GitHub Copilot", "Droid"
	Binaries    []string // candidate binary names to search in $PATH
	ACPCommand  []string // args to start ACP mode, e.g. ["--acp"] or ["acp"]
	Description string   // short description for the tool registry
}

// DiscoveredAgent represents an agent found on the system.
type DiscoveredAgent struct {
	Def  AgentDef
	Path string // absolute path to the binary
}

// KnownAgents is the built-in registry of known ACP agents.
var KnownAgents = []AgentDef{
	{
		Name:        "copilot",
		Title:       "GitHub Copilot",
		Binaries:    []string{"copilot"},
		ACPCommand:  []string{"--acp"},
		Description: "GitHub Copilot coding assistant — strong at GitHub workflows, code explanation, and refactoring",
	},
	{
		Name:        "droid",
		Title:       "Droid (Factory)",
		Binaries:    []string{"droid"},
		ACPCommand:  []string{"--acp"},
		Description: "Droid AI coding agent by Factory — excels at autonomous code generation and multi-file refactoring",
	},
	{
		Name:        "opencode",
		Title:       "OpenCode",
		Binaries:    []string{"opencode"},
		ACPCommand:  []string{"acp"},
		Description: "OpenCode terminal-based coding agent — lightweight agent with multi-provider LLM support",
	},
}

// Discover scans $PATH for known ACP agents.
// Returns only agents whose binary is found and is executable.
func Discover() []DiscoveredAgent {
	var found []DiscoveredAgent
	for _, def := range KnownAgents {
		path, err := findBinary(def.Binaries)
		if err != nil {
			continue
		}
		debug.Log("acp-client", "discovered agent %q at %s", def.Name, path)
		found = append(found, DiscoveredAgent{Def: def, Path: path})
	}
	sort.Slice(found, func(i, j int) bool {
		return found[i].Def.Name < found[j].Def.Name
	})
	return found
}

// findBinary searches for the first match among candidate binary names in $PATH.
func findBinary(names []string) (string, error) {
	cwd, _ := os.Getwd()
	for _, name := range names {
		path, err := exec.LookPath(name)
		if err != nil {
			continue
		}
		if !filepath.IsAbs(path) {
			debug.Log("acp-client", "skipping %q at non-absolute path %q", name, path)
			continue
		}
		if resolved, err := filepath.EvalSymlinks(path); err == nil && resolved != "" {
			path = resolved
		}
		if cwd != "" {
			if rel, err := filepath.Rel(cwd, path); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
				debug.Log("acp-client", "skipping %q at workspace-local path %q", name, path)
				continue
			}
		}
		return path, nil
	}
	return "", exec.ErrNotFound
}
