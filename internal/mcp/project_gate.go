package mcp

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/debug"
)

// Project MCP containment gate.
//
// Project-local MCP config (.mcp.json in the workspace, Source
// "claude-project") defines stdio COMMANDS that ggcode launches as child
// processes at startup. A cloned repository therefore reaches arbitrary
// code execution before the user has consented to anything - the same
// "everything before the trust dialog" class Anthropic documented for
// Claude Code (three responsible-disclosure reports, fixed by gating
// project-local configuration behind an explicit trust decision).
//
// This file gates project-sourced servers at the startup merge: a server
// only launches if the user approved this exact name+command signature
// for this workspace (persisted per-instance), or set
// GGCODE_ALLOW_PROJECT_MCP=1 for the invocation. Approval is bound to the
// command signature, so a repo silently changing the command re-triggers
// the gate. User-owned sources (ggcode yaml, claude-user) are untouched.

// ProjectGateEnvVar opts a single invocation out of the project MCP gate.
// Deliberate, explicit, per-process: CI and scripted users can set it in
// the environment they already control.
const ProjectGateEnvVar = "GGCODE_ALLOW_PROJECT_MCP"

// ProjectGrantsFile is the per-workspace approval store, kept in the
// instance dir (~/.ggcode/instances/<sha256>/) so it never travels with
// the repository it governs.
const ProjectGrantsFile = "mcp_project_grants.json"

// projectGateSource is the Source marker written for workspace-local
// .mcp.json entries (see knownClaudeSources). Only this source is gated.
const projectGateSource = "claude-project"

// IsProjectSource reports whether a server config came from the
// workspace .mcp.json - the source the project gate governs.
func IsProjectSource(source string) bool {
	return strings.TrimSpace(source) == projectGateSource
}

// ProjectGrants maps approved server name -> approved signature. The
// signature binds the approval to source + transport + command + args, so
// it approves what will actually execute for that source, not just the
// label (a user-yaml server with the same name must not cover a project
// entry, and vice versa).
type ProjectGrants struct {
	Version int               `json:"version"`
	Grants  map[string]string `json:"grants"`
}

// ProjectGrantsPath returns the approval store path for a workspace.
func ProjectGrantsPath(workspace string) string {
	if workspace == "" {
		return ""
	}
	dir := config.InstanceDir(workspace)
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, ProjectGrantsFile)
}

// LoadProjectGrants reads the approval store. A missing file is an empty
// grant set, not an error; a corrupt file is reported but treated as empty
// (fail closed for the gate, never crash startup).
func LoadProjectGrants(path string) (*ProjectGrants, error) {
	g := &ProjectGrants{Version: 1, Grants: map[string]string{}}
	if path == "" {
		return g, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return g, nil
		}
		return g, err
	}
	if err := json.Unmarshal(data, g); err != nil {
		return g, fmt.Errorf("corrupt project grants file %s: %w", path, err)
	}
	if g.Grants == nil {
		g.Grants = map[string]string{}
	}
	return g, nil
}

// Approve records name -> signature. Stale entries for the same name are
// replaced; approving a changed command is always an explicit act.
func (g *ProjectGrants) Approve(name, signature string) {
	if g.Grants == nil {
		g.Grants = map[string]string{}
	}
	g.Grants[name] = signature
}

// Revoke drops the approval for name. Returns true if an entry existed.
func (g *ProjectGrants) Revoke(name string) bool {
	if g.Grants == nil {
		return false
	}
	_, existed := g.Grants[name]
	delete(g.Grants, name)
	return existed
}

// Save writes the store atomically with owner-only permissions.
func (g *ProjectGrants) Save(path string) error {
	if path == "" {
		return fmt.Errorf("project grants path is empty")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(g, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func projectGateBypassed() bool {
	switch strings.TrimSpace(strings.ToLower(os.Getenv(ProjectGateEnvVar))) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// GateProjectServers filters servers whose config came from the workspace
// (.mcp.json, Source "claude-project") down to the approved set. Returns
// the servers allowed to launch, the blocked ones, and human-readable
// warnings in the same style as MergeStartupServers.
//
// User-owned sources (explicit ggcode yaml, claude-user) pass through
// untouched: this boundary exists because .mcp.json arrives with cloned
// repositories, not because MCP servers are untrusted in general.
func GateProjectServers(servers []config.MCPServerConfig, workspace string) (allowed, blocked []config.MCPServerConfig, warnings []string) {
	if projectGateBypassed() {
		debug.Log("mcp", "project MCP gate bypassed via %s", ProjectGateEnvVar)
		return servers, nil, []string{fmt.Sprintf("warning: %s is set - project MCP servers launch without approval", ProjectGateEnvVar)}
	}

	grants, err := LoadProjectGrants(ProjectGrantsPath(workspace))
	if err != nil {
		// Corrupt store: fail closed (nothing project-sourced launches),
		// but do not block user-owned servers.
		debug.Log("mcp", "project MCP gate: %v", err)
		warnings = append(warnings, fmt.Sprintf("warning: project MCP approvals unreadable (%v); project servers are blocked until repaired", err))
		grants = &ProjectGrants{Version: 1, Grants: map[string]string{}}
	}

	for _, server := range servers {
		if !IsProjectSource(server.Source) {
			allowed = append(allowed, server)
			continue
		}
		name := strings.TrimSpace(server.Name)
		approved, hasGrant := grants.Grants[name]
		wantSig := projectGateSource + "|" + serverSignature(server)
		switch {
		case hasGrant && approved == wantSig:
			allowed = append(allowed, server)
		case hasGrant:
			blocked = append(blocked, server)
			warnings = append(warnings, fmt.Sprintf(
				"warning: blocked project MCP server %q (command changed since approval; re-approve with `ggcode mcp approve-project %s`)",
				server.Name, server.Name))
		default:
			blocked = append(blocked, server)
			warnings = append(warnings, fmt.Sprintf(
				"warning: blocked project MCP server %q from %s (command %q); approve with `ggcode mcp approve-project %s` or set %s=1",
				server.Name, projectGateSource, serverSignatureCommand(server), server.Name, ProjectGateEnvVar))
		}
	}
	if len(blocked) > 0 {
		debug.Log("mcp", "project MCP gate blocked %d server(s) in workspace %s", len(blocked), workspace)
	}
	return allowed, blocked, warnings
}

// serverSignatureCommand renders the human-facing command of a server for
// warning text (signature itself is a marshaled slice, poor for reading).
func serverSignatureCommand(cfg config.MCPServerConfig) string {
	if normalizedTransport(cfg.Type) == "stdio" {
		return strings.TrimSpace(cfg.Command)
	}
	return strings.TrimSpace(cfg.URL)
}

// ProjectServerStatus is one gate-status row for reporting surfaces.
type ProjectServerStatus struct {
	Name                   string
	Command                string // human-facing command or URL
	Approved               bool
	ApprovedCommandChanged bool // grant exists but signature drifted
}

// projectGrantSignature is the grant-store value for one approval: source-
// prefixed so grants never leak across config sources.
func projectGrantSignature(server config.MCPServerConfig) string {
	return projectGateSource + "|" + serverSignature(server)
}

// ProjectServersStatus merges the startup sources for workspace and
// reports per-project-server gate status. Used by `ggcode mcp list-project`
// so users can see what is blocked and why without reading JSON.
func ProjectServersStatus(workspace string, explicit []config.MCPServerConfig, deleted []string) ([]ProjectServerStatus, []string, error) {
	merged, warnings := MergeStartupServersWithDeleted(workspace, explicit, deleted)
	grants, err := LoadProjectGrants(ProjectGrantsPath(workspace))
	if err != nil {
		return nil, warnings, err
	}
	status := make([]ProjectServerStatus, 0)
	for _, server := range merged {
		if !IsProjectSource(server.Source) {
			continue
		}
		row := ProjectServerStatus{
			Name:    strings.TrimSpace(server.Name),
			Command: serverSignatureCommand(server),
		}
		approved, hasGrant := grants.Grants[row.Name]
		row.Approved = hasGrant && approved == projectGrantSignature(server)
		row.ApprovedCommandChanged = hasGrant && !row.Approved
		status = append(status, row)
	}
	return status, warnings, nil
}

// ApproveProjectServers approves the named project servers found in the
// merged startup list and persists the grants. Returns the approved count.
func ApproveProjectServers(workspace string, servers []config.MCPServerConfig, names []string) (int, error) {
	grants, err := LoadProjectGrants(ProjectGrantsPath(workspace))
	if err != nil {
		return 0, err
	}
	want := make(map[string]bool, len(names))
	for _, n := range names {
		want[strings.TrimSpace(n)] = true
	}
	approved := 0
	for _, server := range servers {
		if !IsProjectSource(server.Source) || !want[strings.TrimSpace(server.Name)] {
			continue
		}
		grants.Approve(strings.TrimSpace(server.Name), projectGrantSignature(server))
		approved++
	}
	if approved > 0 {
		if err := grants.Save(ProjectGrantsPath(workspace)); err != nil {
			return approved, err
		}
	}
	return approved, nil
}

// RevokeProjectServers drops approvals by name and persists. Returns the
// number of revoked entries.
func RevokeProjectServers(workspace string, names []string) (int, error) {
	grants, err := LoadProjectGrants(ProjectGrantsPath(workspace))
	if err != nil {
		return 0, err
	}
	revoked := 0
	for _, n := range names {
		if grants.Revoke(strings.TrimSpace(n)) {
			revoked++
		}
	}
	if revoked > 0 {
		if err := grants.Save(ProjectGrantsPath(workspace)); err != nil {
			return revoked, err
		}
	}
	return revoked, nil
}
