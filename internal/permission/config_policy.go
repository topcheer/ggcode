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

// ToolRule defines the permission level for a tool.
type ToolRule struct {
	Decision Decision `yaml:"decision"`
}

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
	// Don't dump tool input JSON — can be huge and contains file content

	// Interactive/communication tools are always auto-approved regardless of mode.
	// ask_user: the tool itself IS the user interaction — requiring approval would be circular.
	// save_memory: writing project memory is always safe and expected.
	// lanchat: P2P messaging between ggcode instances — no local filesystem or system impact.
	if IsAlwaysAllowedTool(toolName) {
		return Allow, nil
	}
	switch toolName {
	case "ask_user", "save_memory", "delete_memory":
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
		if cmd, ok := extractCommand(input); ok && cmd != "" {
			if rs := p.cmdRules; rs != nil {
				if d, matched := rs.Check(cmd); matched && d == Deny {
					debug.Log("permission", "command denied by deny rule (mode-independent)")
					return Deny, nil
				}
			}
		}
	}

	// Mode-specific handling
	switch p.mode {
	case BypassMode, AutopilotMode:
		// Bypass mode: allow everything except extremely dangerous operations
		// and network exfiltration (data egress always requires human gating).
		if isCommandTool(toolName) {
			cmd, _ := extractCommand(input)
			if cmd != "" {
				if p.detector.IsExtremelyDangerous(cmd) {
					return Ask, nil
				}
				// Network exfiltration (file contents sent externally) always
				// requires confirmation, even in bypass/autopilot. General
				// network access is allowed in bypass mode.
				if p.networkDetector && IsNetworkExfiltrate(cmd) {
					debug.Log("permission", "network exfiltration blocked in bypass mode")
					return Ask, nil
				}
				// #573-C: redirection targets are file writes — hold them to the
				// same bar as write tools (out-of-sandbox or sensitive path →
				// Ask). Without this, `> ~/.ssh/authorized_keys` (High) slipped
				// through because bypass only gates ≥Critical patterns and the
				// file-tool sandbox fallback below never sees shell redirects —
				// the classic prompt-injection persistence path.
				for _, tgt := range extractRedirectTargets(cmd) {
					resolved := expandTilde(tgt)
					if !p.sandbox.Allowed(resolved) || isSensitivePath(resolved) {
						debug.Log("permission", "redirect to out-of-sandbox/sensitive path blocked in bypass mode")
						return Ask, nil
					}
				}
			}
		}
		// Check sandbox for file tools (still protect workspace boundary).
		// In bypass/autopilot, any *write* outside the writable sandbox is
		// downgraded to Ask, regardless of whether the path is on the small
		// "sensitive" allow-list. Without this, ~/.aws/credentials,
		// ~/.docker/config.json, /etc/**, and arbitrary user files outside
		// the workspace get silently overwritten on a prompt-injected tool
		// call. See locks.md S5.
		if isWriteFileTool(toolName) {
			for _, path := range extractFilePaths(input) {
				if path != "" && !p.sandbox.Allowed(path) {
					return Ask, nil
				}
			}
		} else if isFileTool(toolName) {
			for _, path := range extractFilePaths(input) {
				if path != "" && !p.sandbox.Allowed(path) && isSensitivePath(path) {
					return Ask, nil
				}
			}
		}
		return Allow, nil
	case PlanMode:
		// Plan mode: mode control tools + read-only tools allowed, everything else denied
		// enter_plan_mode is always allowed (entering read-only mode is safe).
		// exit_plan_mode requires user confirmation in supervised mode — the plan
		// determines what code changes the agent will make next.
		if toolName == "enter_plan_mode" {
			return Allow, nil
		}
		if toolName == "exit_plan_mode" {
			// Exiting plan mode restores write tools and the presented plan
			// determines what code changes the agent makes next, so it needs
			// user confirmation — Ask, not unconditional Allow (#551-D). The
			// doc comment above has required confirmation all along; this branch
			// previously contradicted it and let a plan-mode agent exit on its
			// own, silently regaining write access without review.
			return Ask, nil
		}
		if IsReadOnlyTool(toolName) {
			// Read-only tools are always allowed in plan mode, even outside sandbox.
			// Plan mode is strictly read-only, so there's no risk.
			return Allow, nil
		}
		return Deny, nil
	case AutoMode:
		// Auto mode: allow safe ops, deny dangerous ones, no prompts.
		// Network egress commands are downgraded to Ask — they require human
		// confirmation even in auto mode because they can exfiltrate data
		// to external endpoints (prompt injection defense).
		if isCommandTool(toolName) {
			cmd, _ := extractCommand(input)
			if cmd != "" {
				if p.detector.IsDangerous(cmd) {
					return Deny, nil
				}
				if p.networkDetector {
					if nc := CheckNetwork(cmd); nc.Risk != NetworkNone {
						debug.Log("permission", "network egress in auto mode: %s (risk=%s)", nc.Reason, nc.Risk)
						return Ask, nil
					}
				}
			}
		}
		// Check sandbox for file tools (including write-only tools like
		// file_ops and batch_replace that mutate disk but aren't in isFileTool).
		if isFileTool(toolName) || isWriteFileTool(toolName) {
			for _, path := range extractFilePaths(input) {
				if path != "" && !p.sandbox.Allowed(path) {
					return Deny, nil
				}
			}
		}
		return Allow, nil
	}

	// Supervised mode (default): read-only tools are auto-allowed
	// (no per-call confirmation needed), but sensitive paths outside the
	// sandbox still require confirmation to prevent secret exfiltration.
	if IsReadOnlyTool(toolName) {
		if isFileTool(toolName) {
			for _, path := range extractFilePaths(input) {
				if path != "" && !p.sandbox.Allowed(path) && isSensitivePath(path) {
					return Ask, nil
				}
			}
		}
		return Allow, nil
	}
	if isCommandTool(toolName) {
		cmd, _ := extractCommand(input)
		if cmd != "" {
			rs := p.cmdRules
			if rs != nil {
				if d, matched := rs.Check(cmd); matched {
					if d == Deny {
						return Deny, nil
					}
					// Allow matched, but still check dangerous detector
					if p.detector.IsDangerous(cmd) {
						return Ask, nil
					}
					// Data egress always requires human gating, even when a
					// saved allow rule matched (#194): a wildcard allow can
					// cover the command word while the payload exfiltrates
					// credentials (curl -d @~/.ssh/id_rsa ...). Mirrors the
					// bypass/auto branches' IsNetworkExfiltrate calls.
					if IsNetworkExfiltrate(cmd) {
						return Ask, nil
					}
					return Allow, nil
				}
			}
		}
	}
	if d, ok := p.rules[toolName]; ok {
		if isFileTool(toolName) {
			for _, path := range extractFilePaths(input) {
				if path != "" && !p.sandbox.Allowed(path) {
					// Out-of-sandbox access under a static rule downgrades to Ask
					// (human review), mirroring the bypass branch, which also
					// keeps a human in the loop for out-of-sandbox writes. An
					// explicit user Allow/Ask rule must not become a harder Deny
					// than the no-rule default (Ask); only an explicit Deny rule
					// stays Deny (#525 Bug D).
					if d == Deny {
						return Deny, nil
					}
					return Ask, nil
				}
			}
		}
		if isCommandTool(toolName) {
			cmd, _ := extractCommand(input)
			if cmd != "" && p.detector.IsDangerous(cmd) {
				// Dangerous command under a static allow rule downgrades to Ask,
				// matching the runtime cmdRules branch above: both rule sources
				// are explicit user allows, so they must share the same downgrade
				// target. An explicit allow must not be a harder Deny than the
				// no-rule default (Ask). An explicit Deny rule stays Deny
				// (#525 Bug C).
				if d == Deny {
					return Deny, nil
				}
				return Ask, nil
			}
			// Data egress always requires human gating, even when a static
			// config allow rule matched (#256): a tools.run_command: allow
			// setting covers the command word while the payload can still
			// exfiltrate credentials (curl -d @~/.ssh/id_rsa ...). Mirrors
			// the cmdRules branch above and the bypass/auto branches.
			if cmd != "" && IsNetworkExfiltrate(cmd) {
				return Ask, nil
			}
		}
		return d, nil
	}

	return Ask, nil
}

// IsDangerous returns true if the command is inherently dangerous.
func (p *ConfigPolicy) IsDangerous(command string) bool {
	return p.detector.IsDangerous(command)
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
	path = filepath.Clean(path)
	home := config.HomeDir()
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
	// command — pattern-matching it caused false Deny on normal content.
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

func newOptionalPathSandbox(allowedDirs []string) *PathSandbox {
	if len(allowedDirs) == 0 {
		return nil
	}
	return NewPathSandbox(allowedDirs)
}
