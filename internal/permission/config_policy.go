package permission

import (
	"encoding/json"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/debug"
)

// DefaultMode is the default permission mode if not specified.
var DefaultMode = SupervisedMode

// ConfigPolicy implements PermissionPolicy based on configuration rules.
type ConfigPolicy struct {
	rules           map[string]Decision
	sandbox         *PathSandbox
	readOnlySandbox *PathSandbox
	detector        *DangerousDetector
	mode            PermissionMode
	cmdRules        *CommandRuleSet
	networkDetector bool // if true, network egress commands trigger Ask in auto/bypass
	mu              sync.RWMutex
}

// NewConfigPolicy creates a policy from tool rules and allowed directories.
// Default decision is Ask for any tool not explicitly listed.
func NewConfigPolicy(rules map[string]Decision, allowedDirs []string) *ConfigPolicy {
	return NewConfigPolicyWithMode(rules, allowedDirs, DefaultMode)
}

// NewConfigPolicyWithMode creates a policy with an explicit permission mode.
func NewConfigPolicyWithMode(rules map[string]Decision, allowedDirs []string, mode PermissionMode) *ConfigPolicy {
	return NewConfigPolicyWithModeAndReadOnlyDirs(rules, allowedDirs, nil, mode)
}

// NewConfigPolicyWithModeAndReadOnlyDirs creates a policy with optional
// read-only file access outside the main writable sandbox.
func NewConfigPolicyWithModeAndReadOnlyDirs(rules map[string]Decision, allowedDirs, readOnlyDirs []string, mode PermissionMode) *ConfigPolicy {
	if rules == nil {
		rules = make(map[string]Decision)
	}
	return &ConfigPolicy{
		rules:           rules,
		sandbox:         NewPathSandbox(allowedDirs),
		readOnlySandbox: newOptionalPathSandbox(readOnlyDirs),
		detector:        NewDangerousDetector(),
		mode:            mode,
		cmdRules:        NewCommandRuleSet(),
		networkDetector: true,
	}
}

// SetCommandRuleSet replaces the command-level permission rules.
func (p *ConfigPolicy) SetCommandRuleSet(rs *CommandRuleSet) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.cmdRules = rs
}

// CommandRuleSet returns the current command-level rule set.
func (p *ConfigPolicy) CommandRuleSet() *CommandRuleSet {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.cmdRules
}

// AllowCommandPattern adds a runtime allow pattern for commands.
func (p *ConfigPolicy) AllowCommandPattern(pattern string) {
	p.mu.RLock()
	rs := p.cmdRules
	p.mu.RUnlock()
	if rs != nil {
		rs.AddAllowPattern(pattern)
	}
}

// Check returns the permission decision for a tool call.
func (p *ConfigPolicy) Check(toolName string, input json.RawMessage) (Decision, error) {
	// Don't dump tool input JSON - can be huge and contains file content

	// #1595-C: an explicit user Deny is the STRONGEST signal (the #741 /
	// #573-B comment itself says so) - it must precede the fast paths.
	// IsAlwaysAllowedTool includes lanchat/im, and the benign-action
	// branch returned Allow BEFORE the Deny lookup: a user who denied
	// lanchat/im was silently ignored, and the ignored channel was
	// exactly the outbound one Deny is most meant to gate.
	//
	// #1776 case 1: this fast path read p.rules UNLOCKED (the function's
	// RLock only comes later) - a concurrent SetOverride/ClearOverride
	// (TUI rule save) hit the same map and went fatal: concurrent map
	// read and map write. The very race class #1596-D claimed to fix,
	// reintroduced on the new fast path. Snapshot the deny state (and
	// the command rule set, also read unlocked below) under one short
	// RLock.
	p.mu.RLock()
	denyRule, hasDenyRule := p.rules[toolName]
	cmdRuleSnapshot := p.cmdRules
	p.mu.RUnlock()
	if hasDenyRule && denyRule == Deny {
		return Deny, nil
	}

	// Interactive/communication tools are always auto-approved regardless of mode.
	// ask_user: the tool itself IS the user interaction - requiring approval would be circular.
	// save_memory: writing project memory is always safe and expected.
	// lanchat: P2P messaging between ggcode instances - no local filesystem or system impact.
	if IsAlwaysAllowedTool(toolName) {
		// #1705 case 1: switch_mode stays reachable in EVERY mode (the
		// #1210 self-rescue invariant - an agent trapped in plan mode
		// must always be able to switch OUT), but ESCALATING to
		// bypass/autopilot now needs consent: one zero-confirmation
		// call used to grant near-total permissions (self-escalation),
		// and switching to auto/bypass from plan escaped #551-D's
		// exit_plan_mode Ask gate through the WIDER unguarded channel.
		// Downgrade/same-level switches (and escaping plan BACK to the
		// session's supervised mode) stay auto-allowed.
		if toolName == "switch_mode" && switchModeEscalates(input) {
			return Ask, nil
		}
		return Allow, nil
	}
	// #1283: im is no longer blanket-approved - only its side-effect-free
	// actions stay on the fast path. send/send_file must flow through the
	// normal pipeline (deny rules first, then mode policy: plan denies,
	// supervised asks) because send_file uploads arbitrary local files to
	// external IM channels.
	if toolName == "im" && imActionIsBenign(input) {
		return Allow, nil
	}
	switch toolName {
	// The Anthropic Memory Tool executes against its own confined store
	// (internal/agent/memory_tool.go: every path is pinned under
	// <workingDir>/.ggcode/memories, with traversal and symlink escape
	// checks), making it the memory-tool analogue of save_memory: agent-owned
	// state, never project source. Fast-path approval also keeps the
	// protocol usable in plan mode, where the model may legitimately record
	// research findings.
	case "ask_user", "save_memory", "delete_memory", "memory", "recall_experience":
		return Allow, nil
	}

	p.mu.RLock()
	defer p.mu.RUnlock()

	// #573-B: deny rules are mode-independent. They were previously consulted
	// only in the Supervised branch, so Bypass/Autopilot/Auto returned Allow for
	// commands the user explicitly denied (e.g. deny "npm run boom"). Deny is
	// the strongest user intent (cmd_rules.go: deny patterns always take
	// precedence) and must win in every mode.
	if isCommandTool(toolName) {
		// #713: deny rules must cover every command channel. The terminal
		// tools' `text` field (iterm2/warp "write text") is typed into the
		// user's interactive shell and executes; leaving it unaudited let a
		// deny rule be bypassed via that second channel in every mode.
		for _, cmd := range extractCommandsForTool(toolName, input) {
			if rs := cmdRuleSnapshot; rs != nil { // #1776: locked snapshot, not the live field
				if d, matched := rs.Check(cmd); matched && d == Deny {
					debug.Log("permission", "command denied by deny rule (mode-independent)")
					return Deny, nil
				}
			}
		}
	}

	// #741 / #573-B extension: an explicit user Deny rule is mode-independent
	// for EVERY tool, not just command tools. AutoMode previously never
	// consulted p.rules, so a user-configured `tools.browser: deny` was
	// silently ignored while the browser tool could navigate to file:// URLs
	// that read_file would have sandboxed. Deny is the strongest user intent
	// and must win in every mode, before any mode-specific logic runs.
	if hasDenyRule && denyRule == Deny { // #1776: locked snapshot from the fast path
		debug.Log("permission", "tool denied by explicit deny rule (mode-independent): %s", toolName)
		return Deny, nil
	}

	// Review (techwriter222, medium): an explicit user "ask" rule must stay
	// meaningful after the two-mode refactor made the default Allow — a user
	// who pinned a tool to ask (rules editor / always-ask memory) would
	// otherwise be silently auto-approved in every mode. Floors below can
	// still add their own Ask on top; this never downgrades a floor to Allow.
	if hasDenyRule && denyRule == Ask {
		debug.Log("permission", "tool pinned to ask by explicit user rule (mode-independent): %s", toolName)
		return Ask, nil
	}
	// Command-pattern ask rules: same respect, per command. Checked after the
	// deny loop above so deny patterns (stronger) win first.
	if isCommandTool(toolName) && cmdRuleSnapshot != nil {
		for _, cmd := range extractCommandsForTool(toolName, input) {
			if d, matched := cmdRuleSnapshot.Check(cmd); matched && d == Ask {
				debug.Log("permission", "command pinned to ask by cmd rule (mode-independent)")
				return Ask, nil
			}
		}
	}

	// Mode-specific handling
	switch p.mode {
	case BypassMode, AutopilotMode:
		// Bypass mode: nothing is gated except system-level destructive
		// commands AND persistence-backdoor writes (shell redirects to
		// sensitive files, file-tool writes outside the sandbox) - the
		// #573-C / locks.md S5 floors. These trigger on paths like
		// ~/.ssh/authorized_keys, ~/.bashrc, /etc/** - rare by construction
		// and never a daily-interruption source. The network-exfiltration
		// and sensitive-read gates were removed in the two-mode refactor
		// (they were the false-positive sources); mode-independent Deny
		// rules at the top of Check still win. Cruise control (goal +
		// strategist) rides on this mode.
		if isCommandTool(toolName) {
			// #713: audit every channel (command + terminal `text`).
			for _, cmd := range extractCommandsForTool(toolName, input) {
				if p.detector.IsExtremelyDangerous(cmd) {
					return Ask, nil
				}
				// Watchdog review (MED): the general exfil gate was removed from
				// bypass as an FP source, but credential-theft uploads must not
				// become zero-friction. Narrow floor: an upload-direction
				// transfer whose command text carries a literal credential-path
				// payload asks. Ordinary uploads (scp secret.txt host:) stay free.
				if p.networkDetector && credentialPayloadExfil(cmd) {
					debug.Log("permission", "credential-payload exfil gated in bypass mode (watchdog narrow floor)")
					return Ask, nil
				}
				// #573-C: redirection targets are file writes - a shell
				// redirect is the classic prompt-injection persistence path
				// (`> ~/.ssh/authorized_keys`). Out-of-sandbox or sensitive
				// targets gate.
				for _, tgt := range extractRedirectTargets(cmd) {
					resolved := expandTilde(tgt)
					if !p.sandbox.Allowed(resolved) || isSensitivePath(resolved) {
						debug.Log("permission", "redirect to out-of-sandbox/sensitive path gated in bypass mode (#573-C)")
						return Ask, nil
					}
				}
			}
		}
		// File-tool writes outside the writable sandbox gate (locks.md S5):
		// ~/.aws/credentials, /etc/** etc. - same persistence-backdoor class.
		if isWriteFileTool(toolName) {
			for _, path := range extractFilePaths(input) {
				if path != "" && !p.sandbox.Allowed(path) {
					return Ask, nil
				}
			}
		}
		return Allow, nil
	case PlanMode:
		// Plan mode is tool-entered (enter_plan_mode/exit_plan_mode); users
		// cannot switch into it directly. Inside plan mode everything is
		// allowed EXCEPT explicit file-writing tools - run_command and other
		// shell access is permitted (planning may need builds, probes,
		// read-only git inspection), gated only by the extremely-dangerous
		// and exfiltration floors below so a plan detour cannot become a
		// soft bypass around those.
		if toolName == "enter_plan_mode" {
			return Allow, nil
		}
		if toolName == "exit_plan_mode" {
			// Exiting plan mode restores the mode the USER had chosen
			// before entering plan mode (c47a99ec): not an escalation.
			return Allow, nil
		}
		if isWriteFileTool(toolName) {
			return Deny, nil
		}
		if isCommandTool(toolName) {
			for _, cmd := range extractCommandsForTool(toolName, input) {
				if p.detector.IsExtremelyDangerous(cmd) {
					return Ask, nil
				}
				if p.networkDetector && IsNetworkExfiltrate(cmd) {
					return Ask, nil
				}
				// Review (plan indirect-write channel): a shell redirect IS a
				// file write - held to an Ask floor (deliberately softer than
				// the write-tool Deny above: one extra confirm beats killing
				// a legitimate planning probe) so `echo x > file` cannot
				// silently bypass isWriteFileTool.
				for _, tgt := range extractRedirectTargets(cmd) {
					resolved := expandTilde(tgt)
					if !p.sandbox.Allowed(resolved) || isSensitivePath(resolved) {
						debug.Log("permission", "redirect to out-of-sandbox/sensitive path gated in plan mode")
						return Ask, nil
					}
				}
			}
		}
		return Allow, nil
	case AutoMode:
		// Deprecated alias: the two-mode refactor folded auto INTO
		// supervised (supervised now flows most calls without prompting).
		// Kept as a distinct case only so legacy ConfigPolicy instances
		// take the same code path as SupervisedMode below.
		fallthrough
	case SupervisedMode:
		// Supervised (two-mode refactor): most calls flow WITHOUT
		// prompting. Gated floors only:
		//   - commands: system-level destruction (IsExtremelyDangerous) or
		//     credential exfiltration (IsNetworkExfiltrate) -> Ask
		//   - file writes outside the writable sandbox -> Ask (the ~/.ssh,
		//     ~/.aws, /etc/** tamper floor)
		// The old "Ask for anything unspecified" default was the #1 source
		// of over-conservative interruption and is gone: default is Allow.
		if isCommandTool(toolName) {
			// #713: audit every channel (command + terminal `text`); the
			// mode-independent deny short-circuit at the top of Check has
			// already returned on any Deny match, so an allow-match here is safe.
			for _, cmd := range extractCommandsForTool(toolName, input) {
				if p.detector.IsExtremelyDangerous(cmd) {
					return Ask, nil
				}
				// Data egress requires human gating even in supervised (#194):
				// a wildcard allow can cover the command word while the payload
				// exfiltrates credentials. Guarded by the same user-configurable
				// flag as the plan branch (review: guard consistency).
				if p.networkDetector && IsNetworkExfiltrate(cmd) {
					return Ask, nil
				}
				// #711: redirect targets are file writes - hold them to the same
				// persistence-backdoor floor as the bypass branch (#573-C):
				// `> ~/.docker/config.json` or a LaunchAgents plist write via
				// run_command must not bypass the write-tool sandbox gate.
				for _, tgt := range extractRedirectTargets(cmd) {
					resolved := expandTilde(tgt)
					if !p.sandbox.Allowed(resolved) || isSensitivePath(resolved) {
						debug.Log("permission", "redirect to out-of-sandbox/sensitive path gated in supervised mode (#711)")
						return Ask, nil
					}
				}
			}
		}
		if isWriteFileTool(toolName) {
			for _, path := range extractFilePaths(input) {
				if path != "" && !p.sandbox.Allowed(path) {
					return Ask, nil
				}
			}
		}
		// Explicit user rules still win when present.
		if d, ok := p.rules[toolName]; ok {
			return d, nil
		}
		return Allow, nil
	}

	// Default (unreachable: every enum value has a case above, but Go
	// requires the function to end in a statement).
	return Allow, nil
}

// imActionIsBenign reports whether an im tool call performs only local,
// side-effect-free adapter management (#1283). Anything that can move data
// out of the machine (send, send_file) or an unknown/missing action is NOT
// benign - fail closed toward the normal approval pipeline.
// switchModeEscalates reports whether a switch_mode call targets a MORE
// privileged mode (#1705 case 1): bypass/autopilot ask for consent; every
// other target (including plan escapes) flows Allow.
func switchModeEscalates(input json.RawMessage) bool {
	var a struct {
		Mode string `json:"mode"`
	}
	_ = json.Unmarshal(input, &a)
	// #4260: the tool side parses the mode case-INSENSITIVELY
	// (switch_mode.go lowercases before ParsePermissionMode), and the
	// permission Check sees the raw JSON args BEFORE CoerceEnumValues
	// normalizes them. A case-sensitive compare here let {"mode":"Bypass"}
	// skip the escalation Ask entirely while still switching into bypass -
	// normalize the same way the parser does.
	m := strings.ToLower(strings.TrimSpace(a.Mode))
	return m == "bypass" || m == "autopilot"
}

func imActionIsBenign(input json.RawMessage) bool {
	var m struct {
		Action string `json:"action"`
	}
	if err := json.Unmarshal(input, &m); err != nil {
		return false
	}
	switch m.Action {
	case "status", "mute", "unmute", "enable", "disable":
		return true
	}
	return false
}

// IsDangerous returns true if the command is inherently dangerous.
func (p *ConfigPolicy) IsDangerous(command string) bool {
	return p.detector.IsDangerous(command)
}

// BlocksAutoApprove reports whether a tool call must NOT be auto-approved
// from learned approval memory even when the pattern matches (#1281):
// dangerous commands (danger-level detectors) and network exfiltration
// always need a human in the loop. Consulted at the memory-hit site before
// treating a learned approval as an Allow.
func (p *ConfigPolicy) BlocksAutoApprove(toolName string, input json.RawMessage) bool {
	if isCommandTool(toolName) {
		for _, cmd := range extractCommandsForTool(toolName, input) {
			if p.detector.IsDangerous(cmd) || IsNetworkExfiltrate(cmd) {
				return true
			}
		}
	}
	return false
}

// AllowedPath returns true if the path is within the sandbox.
func (p *ConfigPolicy) AllowedPath(path string) bool {
	if p.sandbox.Allowed(path) {
		return true
	}
	return p.readOnlySandbox != nil && p.readOnlySandbox.Allowed(path)
}

// AllowedPathForTool returns true if the path is allowed for the specific tool.
// In non-plan modes, if execution reaches here the permission layer has already
// approved the tool call (either Allow directly or user approved an Ask), so
// sandbox restrictions are lifted. In PlanMode, strict sandbox enforcement
// applies since plan mode never writes outside the workspace.
func (p *ConfigPolicy) AllowedPathForTool(toolName, path string) bool {
	if p.sandbox.Allowed(path) {
		return true
	}
	// Non-plan modes: permission layer already approved, allow execution
	p.mu.RLock()
	isPlan := p.mode == PlanMode
	p.mu.RUnlock()
	if !isPlan {
		return true
	}
	// Plan mode: read-only tools bypass sandbox (no risk since they can't write)
	if isReadOnlyFileTool(toolName) {
		return true
	}
	return false
}

// SetOverride allows runtime modification of per-tool policy.
func (p *ConfigPolicy) SetOverride(toolName string, decision Decision) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.rules[toolName] = decision
}

// ClearOverride removes a previously set override for the given tool.
// Used by worker agents to exempt themselves from the strict
// write guard applied to the main agent.
func (p *ConfigPolicy) ClearOverride(toolName string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.rules, toolName)
}

// Mode returns the current permission mode.
func (p *ConfigPolicy) Mode() PermissionMode {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.mode
}

// SetMode changes the permission mode at runtime.
func (p *ConfigPolicy) SetMode(mode PermissionMode) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.mode = mode
}

// CurrentMode returns the current permission mode (thread-safe).
func (p *ConfigPolicy) CurrentMode() PermissionMode {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.mode
}

// GetDecision returns the current decision for a tool (for TUI display).
func (p *ConfigPolicy) GetDecision(toolName string) Decision {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if d, ok := p.rules[toolName]; ok {
		return d
	}
	return Ask
}

// isSensitivePath returns true for paths that are system-critical or user-config.
// Comparison is case-insensitive on platforms with case-insensitive filesystems
// (macOS APFS, Windows) so `/Users/zhanjU/.backdoorrc` cannot dodge the check
// by flipping case (#573-G); Linux keeps byte-exact comparison.
func isSensitivePath(path string) bool {
	// Normalize separators before comparing: the suffix table and home
	// prefix below use "/", but Windows paths carry "\" (filepath.Clean
	// keeps them). Without ToSlash the suffix match (".ssh/id_rsa" vs
	// "C:\Users\u\.ssh\id_rsa") and the home-prefix match ("C:\Users\u/"
	// vs "C:\Users\u\...") never fired on Windows, silently disabling the
	// sensitive-path gates. ToSlash is a no-op on Unix.
	path = filepath.ToSlash(filepath.Clean(path))
	home := filepath.ToSlash(config.HomeDir())
	sensitiveFiles := []string{
		".bashrc", ".bash_profile", ".zshrc", ".zprofile", ".profile",
		".ssh/config", ".ssh/authorized_keys", ".ssh/id_rsa", ".ssh/id_ed25519",
		".gitconfig", ".gnupg",
		// Credential and secret files
		".aws/credentials", ".aws/config",
		".docker/config.json",
		".npmrc", ".pypirc", ".netrc",
		// Keys and token files (used by various CLI tools)
		"keys.env", ".env",
	}
	for _, f := range sensitiveFiles {
		if pathHasSuffixFold(path, f) {
			return true
		}
	}
	// .env files anywhere in the project (contain secrets)
	base := filepath.Base(path)
	if pathEqualFold(base, ".env") || pathHasPrefixFold(base, ".env.") {
		return true
	}
	// Files containing credentials/secrets/tokens in their name
	lowerBase := strings.ToLower(base)
	if strings.Contains(lowerBase, "credential") || strings.Contains(lowerBase, "id_rsa") || strings.Contains(lowerBase, "id_ed25519") {
		return true
	}
	// Writing directly to $HOME root (e.g., ~/.somefile where somefile is not a known app)
	if home != "" && pathHasPrefixFold(path, home+"/") {
		rest := path[len(home)+1:]
		if !strings.Contains(rest, "/") {
			// Single file directly in home dir - could be sensitive
			if strings.HasPrefix(rest, ".") && len(rest) > 1 {
				return true
			}
		}
	}
	return false
}

// pathFoldActive reports whether the current platform's filesystem compares
// paths case-insensitively by default (macOS APFS/HFS+, Windows NTFS).
func pathFoldActive() bool {
	switch runtime.GOOS {
	case "darwin", "windows":
		return true
	}
	return false
}

// asciiEqualFold compares two strings using ASCII case folding only. Unicode
// case folding (strings.EqualFold) is avoided because it can fold runes of
// different byte lengths, which is wrong for path components.
func asciiEqualFold(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		ca, cb := a[i], b[i]
		if 'A' <= ca && ca <= 'Z' {
			ca += 'a' - 'A'
		}
		if 'A' <= cb && cb <= 'Z' {
			cb += 'a' - 'A'
		}
		if ca != cb {
			return false
		}
	}
	return true
}

func pathEqualFold(a, b string) bool {
	if pathFoldActive() {
		return asciiEqualFold(a, b)
	}
	return a == b
}

func pathHasPrefixFold(s, prefix string) bool {
	return len(s) >= len(prefix) && pathEqualFold(s[:len(prefix)], prefix)
}

func pathHasSuffixFold(s, suffix string) bool {
	return len(s) >= len(suffix) && pathEqualFold(s[len(s)-len(suffix):], suffix)
}

func isFileTool(name string) bool {
	switch name {
	case "read_file", "multi_file_read", "write_file", "edit_file", "multi_edit_file", "multi_file_edit", "multi_file_write", "notebook_edit", "list_directory", "search_files", "glob", "code_search":
		return true
	}
	return false
}

func isReadOnlyFileTool(name string) bool {
	return IsReadOnlyTool(name)
}

// isWriteFileTool returns true for file tools that mutate disk (used for
// extra sandbox enforcement in bypass/autopilot modes).
func isWriteFileTool(name string) bool {
	switch name {
	case "write_file", "edit_file", "multi_edit_file", "multi_file_edit", "multi_file_write", "notebook_edit", "file_ops", "batch_replace":
		return true
	}
	return false
}

func isCommandTool(name string) bool {
	switch name {
	// write_command_input removed (#197): its `input` field is arbitrary data
	// written to a job's stdin (script bodies, docs, logs), not a shell
	// command - pattern-matching it caused false Deny on normal content.
	case "run_command", "start_command", "tmux", "ghostty", "warp", "kitty", "iterm2":
		return true
	}
	return false
}

func extractFilePaths(input json.RawMessage) []string {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(input, &m); err != nil {
		return nil
	}
	var paths []string
	for _, key := range []string{"file_path", "path", "directory", "notebook_path"} {
		if v, ok := m[key]; ok {
			var s string
			if err := json.Unmarshal(v, &s); err == nil {
				paths = append(paths, s)
				break
			}
		}
	}
	if v, ok := m["files"]; ok {
		var files []map[string]json.RawMessage
		if err := json.Unmarshal(v, &files); err == nil {
			for _, file := range files {
				if rawPath, ok := file["path"]; ok {
					var s string
					if err := json.Unmarshal(rawPath, &s); err == nil && s != "" {
						paths = append(paths, s)
					}
				}
			}
		} else {
			var strFiles []string
			if err := json.Unmarshal(v, &strFiles); err == nil {
				paths = append(paths, strFiles...)
			}
		}
	}
	if v, ok := m["operations"]; ok {
		var ops []map[string]json.RawMessage
		if err := json.Unmarshal(v, &ops); err == nil {
			for _, op := range ops {
				if rawSrc, ok := op["source"]; ok {
					var s string
					if err := json.Unmarshal(rawSrc, &s); err == nil && s != "" {
						paths = append(paths, s)
					}
				}
				if rawDst, ok := op["destination"]; ok {
					var d string
					if err := json.Unmarshal(rawDst, &d); err == nil && d != "" {
						paths = append(paths, d)
					}
				}
			}
		}
	}
	return paths
}

// isTerminalTool reports whether the tool drives an external terminal
// multiplexer/emulator (tmux/ghostty/warp/kitty/iterm2). Their `text`
// parameter is typed into the user's interactive shell and EXECUTES
// (iterm2 write-text drives AppleScript "write text"), making it a real
// second command channel alongside `command` (#713). Scoping the `text`
// audit to exactly these tools avoids the #197 over-matching that removing
// the generic `input` key from command matching fixed.
func isTerminalTool(name string) bool {
	switch name {
	case "tmux", "ghostty", "warp", "kitty", "iterm2":
		return true
	}
	return false
}

// extractCommandsForTool returns every shell-command channel in a command
// tool's input: the `command` key, the `input` key, and - for the terminal
// tools only - the `text` key. Empty strings are filtered. Callers must
// classify each channel separately because command patterns are
// start-anchored; concatenating channels would leave all but the first
// unaudited (#713).
func extractCommandsForTool(toolName string, input json.RawMessage) []string {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(input, &m); err != nil {
		return nil
	}
	keys := []string{"command", "input"}
	if isTerminalTool(toolName) {
		keys = append(keys, "text")
	}
	var cmds []string
	for _, key := range keys {
		v, ok := m[key]
		if !ok {
			continue
		}
		var s string
		if err := json.Unmarshal(v, &s); err == nil && s != "" {
			cmds = append(cmds, s)
		}
	}
	return cmds
}

func extractCommand(input json.RawMessage) (string, bool) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(input, &m); err != nil {
		return "", false
	}
	if v, ok := m["command"]; ok {
		var s string
		if err := json.Unmarshal(v, &s); err == nil {
			return s, true
		}
	}
	if v, ok := m["input"]; ok {
		var s string
		if err := json.Unmarshal(v, &s); err == nil {
			return s, true
		}
	}
	return "", false
}

// credentialPayloadExfil reports whether cmd is an upload-direction
// network transfer that carries a literal credential-path payload. It is
// the narrow credential-theft floor kept in bypass mode after the general
// exfiltration gate was removed (watchdog review): only transfers whose
// own text references credential locations (~/.ssh, ~/.aws, ~/.gnupg,
// /etc/shadow, .env files) gate, so the everyday-upload false positives
// that motivated the removal stay free.
func credentialPayloadExfil(cmd string) bool {
	if !IsNetworkExfiltrate(cmd) {
		return false
	}
	for _, pat := range []string{
		"~/.ssh/", ".ssh/id", "~/.aws/", ".aws/credentials",
		"~/.gnupg/", "/etc/shadow", "/etc/passwd",
		"credentials.json", ".env",
	} {
		if strings.Contains(cmd, pat) {
			return true
		}
	}
	return false
}

// isAbsolutePathish reports whether tok is absolute or home-anchored - the
// conservative capture predicate for redirections inside quoted bodies.
func isAbsolutePathish(tok string) bool {
	return strings.HasPrefix(tok, "/") || strings.HasPrefix(tok, "~")
}

// extractRedirectTargets returns file paths a shell command writes to via
// output redirection (> and >>), including fd-prefixed forms (2>, &>). Used
// to give command tools the same sensitive-path / out-of-sandbox gating that
// file tools get (#573-C). Quoted sections are skipped so string literals
// containing '>' don't fool the scanner; fd duplication (2>&1), process
// substitution (>(cmd)) and sink devices (/dev/null) are excluded.
func extractRedirectTargets(cmd string) []string {
	var targets []string
	var quote rune
	runes := []rune(cmd)
	for i := 0; i < len(runes); i++ {
		c := runes[i]
		if quote != 0 {
			if c == quote {
				quote = 0
				continue
			}
			// Review PoC (sh -c 'echo x > <target>'): a redirection inside
			// a quoted body still executes when the quote wraps an
			// interpreter -c argument. A full parse cannot distinguish that
			// from data quotes (echo "a > b"), so capture the conservative
			// subset only: single-quoted bodies (no variable expansion =>
			// literal path) with absolute/home-anchored targets - exactly
			// the paths the out-of-sandbox/sensitive gates key on.
			if c == '>' && quote == '\'' {
				j := i
				for j < len(runes) && runes[j] == '>' {
					j++
				}
				if j-i <= 2 {
					if tok := scanRedirectToken(runes, j); isAbsolutePathish(tok) {
						targets = append(targets, tok)
					}
				}
				if j > i {
					i = j - 1
				}
			}
			continue
		}
		if c == '\'' || c == '"' {
			quote = c
			continue
		}
		if c != '>' {
			continue
		}
		// Measure the '>' run (>, >>); longer runs are not redirections.
		j := i
		for j < len(runes) && runes[j] == '>' {
			j++
		}
		if j-i > 2 {
			i = j - 1
			continue
		}
		if tok := scanRedirectToken(runes, j); tok != "" {
			targets = append(targets, tok)
		}
		i = j - 1
	}
	return targets
}

// scanRedirectToken reads the target token following a redirection operator
// that ends at index opEnd. It returns the unquoted file path, or "" when the
// token is rejected: missing, fd duplication (2>&1), process substitution
// (>(cmd)), non-path punctuation, or a harmless device sink (/dev/null).
func scanRedirectToken(runes []rune, opEnd int) string {
	k := opEnd
	for k < len(runes) && (runes[k] == ' ' || runes[k] == '\t') {
		k++
	}
	start := k
	for k < len(runes) && !isRedirectDelim(runes[k]) {
		k++
	}
	if k == start {
		return ""
	}
	tok := unquoteToken(string(runes[start:k]))
	if tok == "" {
		return ""
	}
	switch tok[0] {
	case '&', '(', ')', '|':
		return ""
	}
	if isDevSink(tok) {
		return ""
	}
	return tok
}

func isRedirectDelim(c rune) bool {
	switch c {
	case ' ', '\t', '\n', '\r', ';', '|', '&', '(', ')', '<', '>':
		return true
	}
	return false
}

func unquoteToken(tok string) string {
	if len(tok) >= 2 {
		if (tok[0] == '"' && tok[len(tok)-1] == '"') || (tok[0] == '\'' && tok[len(tok)-1] == '\'') {
			return tok[1 : len(tok)-1]
		}
	}
	return tok
}

// isDevSink reports whether a redirect target is a harmless device sink.
func isDevSink(path string) bool {
	switch path {
	case "/dev/null", "/dev/stdout", "/dev/stderr", "/dev/tty", "/dev/zero":
		return true
	}
	return false
}

// expandTilde expands a leading ~ to the user's home directory so sandbox
// and sensitive-path checks judge the real target (#573-C).
func expandTilde(path string) string {
	if path == "~" {
		return config.HomeDir()
	}
	if strings.HasPrefix(path, "~/") {
		return filepath.Join(config.HomeDir(), path[2:])
	}
	return path
}

// isWebEgressTool reports whether the tool sends caller-supplied data to
// external endpoints - the egress half of the plan-mode read→exfiltrate
// loop (#712). Covers built-in web tools and their whitelisted MCP twins.
func isWebEgressTool(name string) bool {
	switch name {
	case "web_fetch", "web_search",
		"mcp__web_reader__webReader", "mcp__web-search-prime__web_search_prime":
		return true
	}
	return false
}

// exfilPayloadMarkers are case-insensitive signatures that a web tool's
// request payload carries secret/credential material: key file paths,
// private-key block headers, known token prefixes (#712).
var exfilPayloadMarkers = []string{
	// Secret file paths commonly cat'd then embedded in a URL/body
	"id_rsa", "id_ed25519", "authorized_keys", "known_hosts",
	".aws/credentials", "credentials.json", "keys.env", ".netrc",
	".npmrc", ".pypirc", ".ssh/",
	// PEM/OpenSSH private-key block headers
	"begin rsa private key", "begin ec private key", "begin openssh private key",
	"begin private key", "begin dsa private key",
	// Known token prefixes
	"ghp_", "gho_", "ghu_", "ghs_", "github_pat_", "sk-ant-api",
	"xoxb-", "xoxp-", "xapp-", "akia", // GitHub / Slack / AWS prefixes
}

// hasDataEgressSignature returns true if a web tool call's arguments carry
// secret/credential material - the classic read-then-exfiltrate shape where
// a previously-read key ends up in the fetched URL or request body (#712).
func hasDataEgressSignature(input json.RawMessage) bool {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(input, &m); err != nil {
		return false
	}
	for _, v := range m {
		var s string
		if err := json.Unmarshal(v, &s); err != nil || s == "" {
			continue
		}
		// URL query strings encode spaces as '+', so "-----BEGIN+RSA+PRIVATE
		// KEY-----" must be normalized before matching space-containing markers.
		lower := strings.ReplaceAll(strings.ToLower(s), "+", " ")
		for _, marker := range exfilPayloadMarkers {
			if strings.Contains(lower, marker) {
				return true
			}
		}
	}
	return false
}

// checkPlanReadOnlyExceptions applies the #712 exceptions to plan mode's
// read-only Allow: the unconditional Allow had two holes that made plan mode
// WEAKER than supervised for sensitive reads - (a) a sensitive-path read
// (~/.ssh/id_rsa, credentials) silently put secrets into model context, and
// (b) web_fetch/web_search - also whitelisted - could carry them out, closing
// a read→exfiltrate loop with zero prompts. Sensitive out-of-sandbox reads
// and web calls carrying secret material are denied outright (plan mode has
// no human loop to route an Ask through). Returns Deny when an exception
// trips, Allow when the call is benign.
func (p *ConfigPolicy) checkPlanReadOnlyExceptions(toolName string, input json.RawMessage) Decision {
	if isFileTool(toolName) {
		for _, path := range extractFilePaths(input) {
			if path != "" {
				resolved := expandTilde(path)
				if !p.sandbox.Allowed(resolved) && isSensitivePath(resolved) {
					debug.Log("permission", "sensitive-path read denied in plan mode (#712)")
					return Deny
				}
			}
		}
	}
	if isWebEgressTool(toolName) && hasDataEgressSignature(input) {
		debug.Log("permission", "web call with data-egress signature denied in plan mode (#712)")
		return Deny
	}
	return Allow
}

func newOptionalPathSandbox(allowedDirs []string) *PathSandbox {
	if len(allowedDirs) == 0 {
		return nil
	}
	return NewPathSandbox(allowedDirs)
}
