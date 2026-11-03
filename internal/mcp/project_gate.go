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

const ProjectGateEnvVar = "GGCODE_ALLOW_PROJECT_MCP"

const ProjectGrantsFile = "mcp_project_grants.json"

const projectGateSource = "claude-project"

var gatedSources = map[string]bool{
	projectGateSource: true,
	AgentPluginSource: true,
}

func IsProjectSource(source string) bool {
	return gatedSources[strings.TrimSpace(source)]
}

type ProjectGrants struct {
	Version int               `json:"version"`
	Grants  map[string]string `json:"grants"`
}

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

func (g *ProjectGrants) Approve(name, signature string) {
	if g.Grants == nil {
		g.Grants = map[string]string{}
	}
	g.Grants[name] = signature
}

func (g *ProjectGrants) Revoke(name string) bool {
	if g.Grants == nil {
		return false
	}
	_, existed := g.Grants[name]
	delete(g.Grants, name)
	return existed
}

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

func GateProjectServers(servers []config.MCPServerConfig, workspace string) (allowed, blocked []config.MCPServerConfig, warnings []string) {
	if projectGateBypassed() {
		debug.Log("mcp", "project MCP gate bypassed via %s", ProjectGateEnvVar)
		return servers, nil, []string{fmt.Sprintf("warning: %s is set - project MCP servers launch without approval", ProjectGateEnvVar)}
	}

	grants, err := LoadProjectGrants(ProjectGrantsPath(workspace))
	if err != nil {

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
		wantSig := projectGrantSignature(server)
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
				server.Name, server.Source, serverSignatureCommand(server), server.Name, ProjectGateEnvVar))
		}
	}
	if len(blocked) > 0 {
		debug.Log("mcp", "project MCP gate blocked %d server(s) in workspace %s", len(blocked), workspace)
	}
	return allowed, blocked, warnings
}

func serverSignatureCommand(cfg config.MCPServerConfig) string {
	if normalizedTransport(cfg.Type) == "stdio" {
		return strings.TrimSpace(cfg.Command)
	}
	return strings.TrimSpace(cfg.URL)
}

type ProjectServerStatus struct {
	Name                   string
	Command                string
	Approved               bool
	ApprovedCommandChanged bool
}

func projectGrantSignature(server config.MCPServerConfig) string {
	return server.Source + "|" + serverSignature(server)
}

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
