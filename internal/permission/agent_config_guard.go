package permission

// Agent Config Guard (sa-43).
//
// Implements the third mandatory control from the NVIDIA AI Red Team's
// sandboxing guidance (Jan 2026,
// https://developer.nvidia.com/blog/practical-security-guidance-for-sandboxing-agentic-workflows-and-managing-execution-risk/):
// "Block all writes to any agent configuration file or extension, no matter
// where they are located." Their rationale: indirect prompt injection is the
// primary threat to coding agents, and the agent's own configuration —
// instruction markdown (GGCODE.md / AGENTS.md / CLAUDE.md), MCP server
// definitions (.mcp.json — stdio transport spawns shell commands), hooks and
// skill definitions — is a durable persistence vector that survives sandbox
// resets and often executes OUTSIDE the sandbox.
//
// Before sa-43, ggcode gated only out-of-workspace and secret-bearing paths
// (isSensitivePath): an in-workspace write to .mcp.json or GGCODE.md was
// silently Allowed in bypass/autopilot/auto, and a learned approval-memory
// hit could auto-approve it forever in every mode.
//
// Design constraints:
//   - Writes only. Reads of these files are legitimate (the agent must read
//     GGCODE.md every session) and stay untouched.
//   - Ask, not Deny. The guard is mode-independent (bypass/autopilot/auto
//     included, mirroring the network-egress precedent) but remains a human
//     gate rather than a hard block, so legitimate workflows ("update
//     GGCODE.md", "add an MCP server") survive with one confirmation.
//   - Approval memory never auto-approves these paths (BlocksAutoApprove),
//     matching the guidance "approvals should never be cached or persisted".
//   - Explicit user-authored config rules (tools.write_file: allow) are
//     direct manual user configuration and are left untouched by design.

import (
	"path/filepath"
	"strings"

	"github.com/topcheer/ggcode/internal/config"
)

// agentInstructionFiles are markdown/rule files that durably shape agent
// behavior. A prompt injection that edits one of these hijacks every future
// session in the workspace. Compared case-insensitively against the base name.
var agentInstructionFiles = []string{
	"ggcode.md", "agents.md", "claude.md",
	"copilot-instructions.md",
	".cursorrules", ".clinerules", ".windsurfrules",
}

// agentExecConfigFiles define executable behavior: MCP stdio servers spawn
// shell commands, hooks run on lifecycle events. Compared case-insensitively
// against the base name.
var agentExecConfigFiles = []string{
	".mcp.json", "mcp.json", ".mcp.yaml", "mcp.yaml",
	"mcp_servers.yaml", "mcp_servers.yml", "mcp-servers.yaml", "mcp-servers.yml",
	"hooks.json", "hooks.yaml", "hooks.yml",
	"harness.yaml", "harness.yml",
}

// agentStateDirs are agent-owned state subdirectories (under .ggcode/) whose
// contents are data, not configuration: writing there is routine agent work
// (memories, session logs, worktrees) and must not trip the guard.
var agentStateDirs = []string{
	"memory", "memories", "sessions", "todos",
	"worktrees", "logs", "debug", "cache", "run", "runfile",
}

// isAgentConfigPath reports whether path points at an agent-owned
// configuration or instruction file that should never be writable without a
// fresh human confirmation, no matter the permission mode. Three surfaces:
//
//  1. Well-known instruction / exec-config files at ANY path depth
//     (monorepos nest CLAUDE.md / .mcp.json in subpackages).
//  2. Configuration-like files under ggcode's own directories — the global
//     config dir (~/.ggcode) and workspace .ggcode/ — except state dirs.
//  3. Configuration-like files under foreign agent dirs (.claude/, .cursor/),
//     which ggcode sessions may encounter in shared workspaces.
func isAgentConfigPath(path string) bool {
	if path == "" {
		return false
	}
	path = filepath.ToSlash(filepath.Clean(expandTilde(path)))
	if path == "." || path == "/" {
		return false
	}
	lower := strings.ToLower(path)
	base := lower[strings.LastIndex(lower, "/")+1:]

	// Surface 1: known file names, any depth.
	for _, f := range agentInstructionFiles {
		if base == f {
			return true
		}
	}
	for _, f := range agentExecConfigFiles {
		if base == f {
			return true
		}
	}

	// Surface 2a: ggcode global config dir (~/.ggcode). Joined from
	// HomeDir() instead of config.ConfigDir(): they are equivalent
	// (env.go ConfigDir is filepath.Join(HomeDir, ".ggcode")), and
	// ConfigDir() is guarded against the real home inside tests, which
	// would panic on every permission test that routes a file tool
	// through Check. isSensitivePath uses HomeDir() for the same reason.
	if cfgDir := filepath.Join(config.HomeDir(), ".ggcode"); cfgDir != "" && cfgDir != ".ggcode" {
		if prefix := strings.ToLower(filepath.ToSlash(filepath.Clean(cfgDir))) + "/"; strings.HasPrefix(lower, prefix) {
			return ggcodeDirPathIsConfig(lower[len(prefix):])
		}
	}

	// Surface 2b / 3: agent config directories in the workspace.
	for _, dir := range []string{".ggcode/", ".claude/", ".cursor/"} {
		if i := strings.Index(lower, dir); i >= 0 {
			if ggcodeDirPathIsConfig(lower[i+len(dir):]) {
				return true
			}
		}
	}
	return false
}

// ggcodeDirPathIsConfig classifies the part of a path AFTER an agent config
// directory (".ggcode/", ".claude/", ".cursor/", or the global config dir).
func ggcodeDirPathIsConfig(rest string) bool {
	if rest == "" {
		return false
	}
	first := rest
	if i := strings.Index(rest, "/"); i >= 0 {
		first = rest[:i]
		// Nested state dirs are data, not configuration.
		for _, d := range agentStateDirs {
			if first == d {
				return false
			}
		}
		// Skill definitions are executable extensions: they run as soon as a
		// skill is invoked, so they stay inside the guard's scope.
		if first == "skills" {
			return true
		}
	}
	return isConfigLikeBase(first)
}

// isConfigLikeBase reports whether a base name looks like configuration that
// defines executable or security-relevant behavior.
func isConfigLikeBase(base string) bool {
	if base == "keys.env" {
		return true
	}
	for _, prefix := range []string{"config", "settings", "mcp", "harness", "hooks", "permission"} {
		if strings.HasPrefix(base, prefix) {
			return true
		}
	}
	for _, ext := range []string{".yaml", ".yml", ".json", ".jsonc", ".toml"} {
		if strings.HasSuffix(base, ext) {
			return true
		}
	}
	return false
}
