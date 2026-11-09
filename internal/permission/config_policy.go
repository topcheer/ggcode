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

var DefaultMode = SupervisedMode

type ConfigPolicy struct {
	rules           map[string]Decision
	sandbox         *PathSandbox
	readOnlySandbox *PathSandbox
	detector        *DangerousDetector
	mode            PermissionMode
	cmdRules        *CommandRuleSet
	networkDetector bool
	mu              sync.RWMutex
}

func NewConfigPolicy(rules map[string]Decision, allowedDirs []string) *ConfigPolicy {
	return NewConfigPolicyWithMode(rules, allowedDirs, DefaultMode)
}

func NewConfigPolicyWithMode(rules map[string]Decision, allowedDirs []string, mode PermissionMode) *ConfigPolicy {
	return NewConfigPolicyWithModeAndReadOnlyDirs(rules, allowedDirs, nil, mode)
}

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

func (p *ConfigPolicy) SetCommandRuleSet(rs *CommandRuleSet) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.cmdRules = rs
}

func (p *ConfigPolicy) CommandRuleSet() *CommandRuleSet {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.cmdRules
}

func (p *ConfigPolicy) AllowCommandPattern(pattern string) {
	p.mu.RLock()
	rs := p.cmdRules
	p.mu.RUnlock()
	if rs != nil {
		rs.AddAllowPattern(pattern)
	}
}

func (p *ConfigPolicy) Check(toolName string, input json.RawMessage) (Decision, error) {

	p.mu.RLock()
	denyRule, hasDenyRule := p.rules[toolName]
	askRule, hasAskRule := p.rules[toolName]
	cmdRuleSnapshot := p.cmdRules
	p.mu.RUnlock()
	if hasDenyRule && denyRule == Deny {
		return Deny, nil
	}

	if hasAskRule && askRule == Ask {
		return Ask, nil
	}

	if IsAlwaysAllowedTool(toolName) {

		if toolName == "switch_mode" && switchModeEscalates(input) {
			return Ask, nil
		}
		return Allow, nil
	}

	if toolName == "im" && imActionIsBenign(input) {
		return Allow, nil
	}
	switch toolName {

	case "ask_user", "save_memory", "delete_memory", "memory", "recall_experience", "memory_recall":
		return Allow, nil
	}

	p.mu.RLock()
	defer p.mu.RUnlock()

	if isCommandTool(toolName) {

		for _, cmd := range extractCommandsForTool(toolName, input) {
			if rs := cmdRuleSnapshot; rs != nil {
				if d, matched := rs.Check(cmd); matched && d == Deny {
					debug.Log("permission", "command denied by deny rule (mode-independent)")
					return Deny, nil
				}
			}
		}
	}

	if hasDenyRule && denyRule == Deny {
		debug.Log("permission", "tool denied by explicit deny rule (mode-independent): %s", toolName)
		return Deny, nil
	}

	if hasDenyRule && denyRule == Ask {
		debug.Log("permission", "tool pinned to ask by explicit user rule (mode-independent): %s", toolName)
		return Ask, nil
	}

	if isCommandTool(toolName) && cmdRuleSnapshot != nil {
		for _, cmd := range extractCommandsForTool(toolName, input) {
			if d, matched := cmdRuleSnapshot.Check(cmd); matched && d == Ask {
				debug.Log("permission", "command pinned to ask by cmd rule (mode-independent)")
				return Ask, nil
			}
		}
	}

	switch p.mode {
	case BypassMode, AutopilotMode:

		if isCommandTool(toolName) {

			for _, cmd := range extractCommandsForTool(toolName, input) {
				if p.detector.IsExtremelyDangerous(cmd) {
					return Ask, nil
				}

				if p.networkDetector && credentialPayloadExfil(cmd) {
					debug.Log("permission", "credential-payload exfil gated in bypass mode (watchdog narrow floor)")
					return Ask, nil
				}

				for _, tgt := range extractRedirectTargets(cmd) {
					resolved := expandTilde(tgt)
					if isSensitivePath(resolved) {
						debug.Log("permission", "redirect to sensitive path gated in bypass mode (#573-C)")
						return Ask, nil
					}
				}
			}
		}

		if isWriteFileTool(toolName) {
			for _, path := range extractFilePaths(input) {
				if path != "" {

					if isSensitivePath(expandTilde(path)) {
						return Ask, nil
					}
				}
			}
		}
		return Allow, nil
	case PlanMode:

		if toolName == "enter_plan_mode" {
			return Allow, nil
		}
		if toolName == "exit_plan_mode" {

			return Allow, nil
		}
		if isWriteFileTool(toolName) {
			return Deny, nil
		}

		if d := p.checkPlanReadOnlyExceptions(toolName, input); d != Allow {
			return d, nil
		}

		if strings.HasPrefix(toolName, "mcp__") && !IsReadOnlyTool(toolName) {
			debug.Log("permission", "non-read-only MCP tool denied in plan mode (#596-P2/#4261)")
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

		fallthrough
	case SupervisedMode:

		if isCommandTool(toolName) {

			for _, cmd := range extractCommandsForTool(toolName, input) {
				if p.detector.IsExtremelyDangerous(cmd) {
					return Ask, nil
				}

				if p.networkDetector && credentialPayloadExfil(cmd) {
					debug.Log("permission", "credential-payload exfil gated in supervised mode")
					return Ask, nil
				}

				for _, tgt := range extractRedirectTargets(cmd) {
					resolved := expandTilde(tgt)
					if isSensitivePath(resolved) {
						debug.Log("permission", "redirect to sensitive path gated in supervised mode (#711)")
						return Ask, nil
					}
				}
			}
		}
		if isWriteFileTool(toolName) {
			for _, path := range extractFilePaths(input) {
				if path != "" {

					if isSensitivePath(expandTilde(path)) {
						return Ask, nil
					}
				}
			}
		}

		if d, ok := p.rules[toolName]; ok {
			return d, nil
		}
		return Allow, nil
	}

	return Allow, nil
}

func switchModeEscalates(input json.RawMessage) bool {
	var a struct {
		Mode string `json:"mode"`
	}
	_ = json.Unmarshal(input, &a)

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

func (p *ConfigPolicy) IsDangerous(command string) bool {
	return p.detector.IsDangerous(command)
}

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

func (p *ConfigPolicy) AllowedPath(path string) bool {
	if p.sandbox.Allowed(path) {
		return true
	}
	return p.readOnlySandbox != nil && p.readOnlySandbox.Allowed(path)
}

func (p *ConfigPolicy) AllowedPathForTool(toolName, path string) bool {
	if p.sandbox.Allowed(path) {
		return true
	}

	p.mu.RLock()
	isPlan := p.mode == PlanMode
	p.mu.RUnlock()
	if !isPlan {
		return true
	}

	if isReadOnlyFileTool(toolName) {
		return true
	}
	return false
}

func (p *ConfigPolicy) SetOverride(toolName string, decision Decision) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.rules[toolName] = decision
}

func (p *ConfigPolicy) ClearOverride(toolName string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.rules, toolName)
}

func (p *ConfigPolicy) Mode() PermissionMode {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.mode
}

func (p *ConfigPolicy) SetMode(mode PermissionMode) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.mode = mode
}

func (p *ConfigPolicy) CurrentMode() PermissionMode {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.mode
}

func (p *ConfigPolicy) GetDecision(toolName string) Decision {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if d, ok := p.rules[toolName]; ok {
		return d
	}
	return Ask
}

func isSensitivePath(path string) bool {

	path = filepath.ToSlash(filepath.Clean(path))
	home := filepath.ToSlash(config.HomeDir())
	sensitiveFiles := []string{
		".bashrc", ".bash_profile", ".zshrc", ".zprofile", ".profile",
		".ssh/config", ".ssh/authorized_keys", ".ssh/id_rsa", ".ssh/id_ed25519",
		".gitconfig", ".gnupg",

		".aws/credentials", ".aws/config",
		".docker/config.json",
		".npmrc", ".pypirc", ".netrc",

		"keys.env", ".env",
	}
	for _, f := range sensitiveFiles {
		if pathHasSuffixFold(path, f) {
			return true
		}
	}

	base := filepath.Base(path)
	if pathEqualFold(base, ".env") || pathHasPrefixFold(base, ".env.") {
		return true
	}

	lowerBase := strings.ToLower(base)
	if strings.Contains(lowerBase, "credential") || strings.Contains(lowerBase, "id_rsa") || strings.Contains(lowerBase, "id_ed25519") {
		return true
	}

	if home != "" && pathHasPrefixFold(path, home+"/") {
		rest := path[len(home)+1:]
		if !strings.Contains(rest, "/") {

			if strings.HasPrefix(rest, ".") && len(rest) > 1 {
				return true
			}
		}
	}

	sensitivePrefixes := []string{
		"/etc/",
		"/var/spool/cron/", "/var/cron/tabs/", "/etc/cron",
		"/Library/LaunchAgents/", "/Library/LaunchDaemons/",
		"/System/Library/LaunchAgents/", "/System/Library/LaunchDaemons/",
		"/usr/lib/systemd/system/", "/etc/systemd/",
		"/windows/system32/tasks/", "/windows/tasks/",
	}
	if !pathFoldActive() {

		for _, p := range sensitivePrefixes {
			if strings.HasPrefix(path, p) {
				return true
			}
		}
	} else {
		for _, p := range sensitivePrefixes {
			if pathHasPrefixFold(path, p) {
				return true
			}
		}
	}

	if home != "" && (pathHasPrefixFold(path, home+"/Library/LaunchAgents/") || pathHasPrefixFold(path, home+"/Library/LaunchDaemons/")) {
		return true
	}
	return false
}

func pathFoldActive() bool {
	switch runtime.GOOS {
	case "darwin", "windows":
		return true
	}
	return false
}

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

func isWriteFileTool(name string) bool {
	switch name {
	case "write_file", "edit_file", "multi_edit_file", "multi_file_edit", "multi_file_write", "notebook_edit", "file_ops", "batch_replace":
		return true
	}
	return false
}

func isCommandTool(name string) bool {

	switch {
	case strings.EqualFold(name, "run_command"),
		strings.EqualFold(name, "start_command"),
		strings.EqualFold(name, "bash"),
		strings.EqualFold(name, "powershell"),
		strings.EqualFold(name, "tmux"),
		strings.EqualFold(name, "ghostty"),
		strings.EqualFold(name, "warp"),
		strings.EqualFold(name, "kitty"),
		strings.EqualFold(name, "iterm2"):
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

func isTerminalTool(name string) bool {
	switch name {
	case "tmux", "ghostty", "warp", "kitty", "iterm2":
		return true
	}
	return false
}

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

func credentialPayloadExfil(cmd string) bool {
	if !IsNetworkExfiltrate(cmd) {
		return false
	}
	for _, pat := range []string{
		"~/.ssh/", ".ssh/id", "~/.aws/", ".aws/credentials",
		"~/.gnupg/", "/etc/shadow", "/etc/passwd",
		"credentials.json",
	} {
		if strings.Contains(cmd, pat) {
			return true
		}
	}
	return matchesEnvPath(cmd)
}

func matchesEnvPath(cmd string) bool {
	for i := 0; ; {
		idx := strings.Index(cmd[i:], ".env")
		if idx < 0 {
			return false
		}
		pos := i + idx
		i = pos + 1
		if pos+len(".env") >= len(cmd) {
			return true
		}
		after := cmd[pos+len(".env")]
		if after == '[' {
			continue
		}
		if after == '.' && strings.HasPrefix(cmd[pos:], ".env.example") {
			continue
		}
		var before byte = ' '
		if pos > 0 {
			before = cmd[pos-1]
		}
		if before == ']' || before == '_' || isAlnumByte(before) {
			continue
		}
		return true
	}
}

func isAlnumByte(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9'
}

func isAbsolutePathish(tok string) bool {
	return strings.HasPrefix(tok, "/") || strings.HasPrefix(tok, "~")
}

func trimQuoteRunes(tok string) string {
	for len(tok) > 0 && (tok[0] == '\'' || tok[0] == '"') {
		tok = tok[1:]
	}
	for len(tok) > 0 && (tok[len(tok)-1] == '\'' || tok[len(tok)-1] == '"') {
		tok = tok[:len(tok)-1]
	}
	return tok
}

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

			if c == '>' && quote == '\'' {
				j := i
				for j < len(runes) && runes[j] == '>' {
					j++
				}
				if j-i <= 2 {
					if tok := scanRedirectToken(runes, j); isAbsolutePathish(tok) {

						targets = append(targets, trimQuoteRunes(tok))
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

		lower := strings.ReplaceAll(strings.ToLower(s), "+", " ")
		for _, marker := range exfilPayloadMarkers {
			if strings.Contains(lower, marker) {
				return true
			}
		}
	}
	return false
}

func (p *ConfigPolicy) checkPlanReadOnlyExceptions(toolName string, input json.RawMessage) Decision {
	if isFileTool(toolName) {
		for _, path := range extractFilePaths(input) {
			if path != "" {
				resolved := expandTilde(path)

				if p.AllowedPath(resolved) {
					continue
				}
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
