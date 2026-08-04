package agent

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/tool"
)

// mcpEcosystemState tracks MCP server health and tool conflicts across the
// agent run. It fires once at session start to surface actionable intelligence
// about the MCP ecosystem: failed servers, tool name conflicts, empty servers,
// and OAuth-pending servers.
//
// This is a zero-LLM-cost deterministic implementation inspired by:
//   - Claude Code's MCP server status reporting
//   - Cline's MCP marketplace conflict warnings
//   - Cursor's MCP server health checks
type mcpEcosystemState struct {
	mu         sync.RWMutex
	fired      bool      // whether the health check has fired this run
	startTime  time.Time // session start time
	failedOnce bool      // if first attempt found issues, allow one retry
}

func newMCPEcosystemState() *mcpEcosystemState {
	return &mcpEcosystemState{
		startTime: time.Now(),
	}
}

func (s *mcpEcosystemState) reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.fired = false
	s.failedOnce = false
	s.startTime = time.Now()
}

// mcpConflictEntry represents a tool name that exists across multiple servers.
type mcpConflictEntry struct {
	toolName string
	servers  []string
}

// detectToolConflicts scans MCP snapshots for tools that share names across
// servers. MCP tool names follow the pattern mcp__<server>__<tool>, but the
// underlying tool name (td.Name) can collide. We check the raw tool names
// from each server's ToolNames list for duplicates after stripping the
// server prefix.
func detectToolConflicts(snapshots []tool.MCPServerSnapshot) []mcpConflictEntry {
	// Map: raw tool name -> list of servers providing it
	toolOwners := make(map[string][]string)
	for _, snap := range snapshots {
		if !snap.Connected {
			continue
		}
		for _, fullName := range snap.ToolNames {
			raw := extractMCPToolBaseName(fullName)
			if raw == "" {
				continue
			}
			// Avoid duplicate server entries for same tool
			seen := false
			for _, s := range toolOwners[raw] {
				if s == snap.Name {
					seen = true
					break
				}
			}
			if !seen {
				toolOwners[raw] = append(toolOwners[raw], snap.Name)
			}
		}
	}

	var conflicts []mcpConflictEntry
	for raw, servers := range toolOwners {
		if len(servers) > 1 {
			sort.Strings(servers)
			conflicts = append(conflicts, mcpConflictEntry{
				toolName: raw,
				servers:  servers,
			})
		}
	}
	sort.Slice(conflicts, func(i, j int) bool {
		return conflicts[i].toolName < conflicts[j].toolName
	})
	return conflicts
}

// extractMCPToolBaseName strips the mcp__<server>__ prefix to get the raw
// tool name. If the name doesn't follow the MCP naming convention, returns
// the name as-is (for conflict detection purposes).
func extractMCPToolBaseName(fullName string) string {
	// MCP tools are registered as mcp__<server>__<toolName>
	if strings.HasPrefix(fullName, "mcp__") {
		parts := strings.SplitN(fullName, "__", 3)
		if len(parts) == 3 {
			return parts[2]
		}
	}
	return fullName
}

const mcpErrorTruncateLimit = 200

// classifyMCPServer categorizes a single server snapshot into issue buckets.
// Returns the category and whether the server matched any issue.
func classifyMCPServer(snap tool.MCPServerSnapshot) (failed, oauth, empty bool) {
	if snap.Connected {
		if len(snap.ToolNames) == 0 && len(snap.PromptNames) == 0 && len(snap.ResourceNames) == 0 {
			empty = true
		}
	} else if !snap.Pending {
		failed = true
	}

	errLower := strings.ToLower(snap.Error)
	if strings.Contains(errLower, "oauth") ||
		strings.Contains(errLower, "unauthorized") ||
		strings.Contains(errLower, "authentication") {
		oauth = true
	}
	return
}

// mcpIssues holds the categorized results of scanning MCP snapshots.
type mcpIssues struct {
	failedServers []string
	oauthPending  []string
	emptyServers  []string
	errors        map[string]string // server name -> error message
	conflicts     []mcpConflictEntry
}

func (iss *mcpIssues) hasIssues() bool {
	return len(iss.failedServers) > 0 || len(iss.emptyServers) > 0 ||
		len(iss.oauthPending) > 0 || len(iss.conflicts) > 0
}

func truncateMCPError(msg string) string {
	if len(msg) > mcpErrorTruncateLimit {
		return msg[:mcpErrorTruncateLimit] + "..."
	}
	return msg
}

// analyzeMCPEcosystem examines MCP server snapshots and returns a guidance
// message if actionable issues are found. Returns empty string if healthy.
func (s *mcpEcosystemState) analyzeMCPEcosystem(snapshots []tool.MCPServerSnapshot) string {
	if len(snapshots) == 0 {
		return ""
	}

	iss := scanMCPSnapshots(snapshots)
	if !iss.hasIssues() {
		return ""
	}

	debug.Log("mcp-ecosystem", "detected %d failed, %d oauth, %d empty, %d conflicts",
		len(iss.failedServers), len(iss.oauthPending), len(iss.emptyServers), len(iss.conflicts))
	return buildMCPEcosystemReport(iss)
}

// scanMCPSnapshots classifies all servers and detects conflicts.
func scanMCPSnapshots(snapshots []tool.MCPServerSnapshot) *mcpIssues {
	iss := &mcpIssues{errors: make(map[string]string)}
	for _, snap := range snapshots {
		failed, oauth, empty := classifyMCPServer(snap)
		if failed {
			iss.failedServers = append(iss.failedServers, snap.Name)
			if snap.Error != "" {
				iss.errors[snap.Name] = snap.Error
			}
		}
		if oauth {
			iss.oauthPending = append(iss.oauthPending, snap.Name)
		}
		if empty {
			iss.emptyServers = append(iss.emptyServers, snap.Name)
		}
	}
	iss.conflicts = detectToolConflicts(snapshots)
	return iss
}

// buildMCPEcosystemReport formats the issues into a human-readable guidance message.
func buildMCPEcosystemReport(iss *mcpIssues) string {
	var sb strings.Builder
	sb.WriteString("[MCP Ecosystem Intelligence] The following MCP issues were detected:\n\n")

	if len(iss.failedServers) > 0 {
		sb.WriteString(fmt.Sprintf("Failed/disconnected servers (%d): %s\n",
			len(iss.failedServers), strings.Join(iss.failedServers, ", ")))
		for _, name := range iss.failedServers {
			if errMsg, ok := iss.errors[name]; ok {
				sb.WriteString(fmt.Sprintf("  - %s: %s\n", name, truncateMCPError(errMsg)))
			}
		}
		sb.WriteString("  Tip: Check server command/path, network connectivity, or run `ggcode mcp list` to verify config.\n\n")
	}

	if len(iss.oauthPending) > 0 {
		sb.WriteString(fmt.Sprintf("Servers requiring authentication (%d): %s\n",
			len(iss.oauthPending), strings.Join(iss.oauthPending, ", ")))
		sb.WriteString("  Tip: OAuth tokens may be expired. Re-authenticate or update credentials in config.\n\n")
	}

	if len(iss.emptyServers) > 0 {
		sb.WriteString(fmt.Sprintf("Connected servers with no tools/resources (%d): %s\n",
			len(iss.emptyServers), strings.Join(iss.emptyServers, ", ")))
		sb.WriteString("  Tip: These servers connected successfully but expose no capabilities. Verify server configuration or upgrade.\n\n")
	}

	if len(iss.conflicts) > 0 {
		sb.WriteString(fmt.Sprintf("Tool name conflicts (%d):\n", len(iss.conflicts)))
		for _, c := range iss.conflicts {
			sb.WriteString(fmt.Sprintf("  - %s: provided by %s\n", c.toolName, strings.Join(c.servers, ", ")))
		}
		sb.WriteString("  Tip: Only the first server's tool will be available. Consider renaming or disabling duplicate servers.\n\n")
	}

	sb.WriteString("You can still proceed with available MCP tools. Use `list_mcp_capabilities` for current status.")
	return strings.TrimSpace(sb.String())
}

// maybeWarnMCP is called early in the agent loop to surface MCP ecosystem
// issues. It fires at most once per run (after a brief delay to allow servers
// to connect), and can retry once if the first attempt found issues.
func (a *Agent) maybeWarnMCP(iteration int) string {
	if a.mcpEcosystem == nil {
		return ""
	}
	a.mcpEcosystem.mu.RLock()
	fired := a.mcpEcosystem.fired
	a.mcpEcosystem.mu.RUnlock()
	if fired {
		return ""
	}

	// Wait until iteration 2+ to allow MCP servers time to connect
	if iteration < 2 {
		return ""
	}

	// Get MCP snapshots from the runtime if available
	if a.mcpRuntime == nil {
		return ""
	}
	snapshots := a.mcpRuntime.SnapshotMCP()
	msg := a.mcpEcosystem.analyzeMCPEcosystem(snapshots)

	a.mcpEcosystem.mu.Lock()
	a.mcpEcosystem.fired = true
	a.mcpEcosystem.mu.Unlock()

	return msg
}
