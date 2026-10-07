package agent

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
)

// Exfiltration Chain Detector (Log-to-Leak).
//
// Research basis:
//   - "Log-to-Leak" framework (openreview UVgbFuXPaO): injected tool
//     descriptions / tool outputs can induce agents to exfiltrate data
//     through a CHAIN of individually-legitimate actions.
//   - Microsoft disclosure (2026-06-30): tool-description injection
//     campaigns where every single action looked legal -- the secret read
//     was a normal read, the outbound call was a normal web call -- and
//     only the SEQUENCE "sensitive source read -> outbound transmission
//     with payload linkage" reveals the attack.
//
// Gap this closes:
//   ggcode's existing defenses are all CONTENT-side: secret_redact.go
//   masks secrets in tool output (the model never sees the value), and
//   hardcoded_secret_check.go detects secrets WRITTEN to disk. Neither
//   watches the CALL-PATTERN chain. taint_influence_check.go has the IFC
//   architecture but its source/sink directions are mismatched for this
//   threat: its source is injected content and its sinks are writes/exec,
//   which does not cover "sensitive file read -> outbound network send".
//
//   This detector reuses that proven architecture with corrected
//   directions: source = sensitive reads (secret-pattern tool results or
//   sensitive-path arguments), sink = outbound transmission tools
//   (web_fetch/web_search/browser/im/lanchat/mobile-send, and run_command
//   restricted to curl|wget|nc|ssh-style egress commands).
//
// Two tiers, mirroring taint_influence_check.go:
//  1. DIRECT PAYLOAD PROPAGATION (high precision): the sensitive value or
//     sensitive path recorded earlier appears VERBATIM in the outbound
//     tool's arguments.
//  2. PROXIMITY WINDOW (medium precision, low confidence): an outbound
//     tool fires within a few steps of a sensitive read even without a
//     verbatim match.
//
// Both tiers are heuristic, deterministic, zero-LLM-cost. This is a
// detection/guidance layer only: it does NOT change permission semantics
// (irrev_gate.go keeps owning irreversibility).

// exfilSinkTools are tools that transmit data OUT of the local workspace
// to external services. If sensitive-source content appears in their
// arguments, that is a potential exfiltration chain.
var exfilSinkTools = map[string]bool{
	"web_fetch":           true, // GET to arbitrary URL (data can ride query strings)
	"web_search":          true, // query text goes to a search provider
	"browser":             true, // navigations/typed text leave the machine
	"im":                  true, // send/send_file push content to IM channels
	"lanchat":             true, // DMs to other agents/users
	"send_file_to_mobile": true, // pushes local files off the machine
}

// exfilOutboundCmdRe gates the run_command sink: the tool's args are
// arbitrary shell text, so treating every command as a sink would be pure
// noise. Only network-egress commands count. List per command_cache.go's
// exclusion scan (curl/wget/scp/ssh) plus netcat variants.
var exfilOutboundCmdRe = regexp.MustCompile(`(?i)\b(curl|wget|nc|ncat|netcat|ssh|scp)\b`)

// exfilLocalHostRe matches loopback / RFC1918 private destinations.
// Commands targeting these are local testing, not exfiltration, and are
// exempt from sink classification. Known tradeoff (documented, accepted):
// a command that mentions BOTH an external host and a private IP is
// exempted too -- exemptions err toward false negatives, never false
// positives.
var exfilLocalHostRe = regexp.MustCompile(`(?i)(localhost|127\.0\.0\.1|::1|\b10\.\d{1,3}\.\d{1,3}\.\d{1,3}\b|\b192\.168\.\d{1,3}\.\d{1,3}\b|\b172\.(1[6-9]|2[0-9]|3[01])\.\d{1,3}\.\d{1,3}\b)`)

// exfilSensitivePathMarkers: reading paths containing these markers is
// treated as touching a sensitive source even when the content does not
// match a secret pattern (e.g. an id_rsa body, a .env of opaque values).
var exfilSensitivePathMarkers = []string{
	"~/.ssh", ".ssh/", ".pem", ".env", "credentials",
}

const (
	maxExfilFingerprints      = 6   // max fingerprints stored per run (taint parity)
	maxDirectExfilWarnings    = 3   // Tier-1 budget: verbatim payload propagation
	maxProximityExfilWarnings = 2   // Tier-2 budget: proximity-only notices
	exfilExpirySeconds        = 300 // fingerprints expire after 5 minutes
	exfilProximityWindowSteps = 6   // tool-call proximity window for Tier-2
	exfilMinSnippetLen        = 8   // snippets shorter than this never verbatim-match
)

// exfilFingerprint is a sensitive value or path recorded from a source
// read, so we can later check whether it flows into outbound tool args.
type exfilFingerprint struct {
	snippet    string    // secret value or sensitive path token
	kind       string    // "secret-value" | "sensitive-path"
	sourceTool string    // tool that produced/read the sensitive content
	recordedAt time.Time // for expiry
	stepIndex  int       // tool-call index when the source was read
}

// exfilChainState tracks sensitive-source fingerprints and checks whether
// they flow into outbound tool calls.
type exfilChainState struct {
	fingerprints    []exfilFingerprint
	warnedDirect    int // Tier-1 warnings emitted this run
	warnedProximity int // Tier-2 warnings emitted this run
	stepCounter     int // increments each tool call
}

func newExfilChainState() *exfilChainState {
	return &exfilChainState{}
}

func (s *exfilChainState) reset() {
	s.fingerprints = nil
	s.warnedDirect = 0
	s.warnedProximity = 0
	s.stepCounter = 0
}

// exfilSourceExempt mirrors the exemption table of
// hardcoded_secret_check.go (secretExemptDirs via pathHasSegment, plus
// .env.example): reads from test fixtures and template env files are
// intentional mocks, not live sensitive sources.
func exfilSourceExempt(argsStr string) bool {
	lower := strings.ToLower(argsStr)
	if strings.Contains(lower, ".env.example") {
		return true
	}
	for _, dir := range secretExemptDirs {
		if pathHasSegment(lower, dir) {
			return true
		}
	}
	return false
}

// exfilSensitivePathMarker returns the first sensitive path marker found
// in the arguments, or "".
func exfilSensitivePathMarker(argsStr string) string {
	lower := strings.ToLower(argsStr)
	for _, m := range exfilSensitivePathMarkers {
		if strings.Contains(lower, m) {
			return m
		}
	}
	return ""
}

// exfilPathDelim reports whether b can terminate a path token inside
// shell text or JSON tool arguments.
func exfilPathDelim(b byte) bool {
	switch b {
	case ' ', '"', '\'', ',', '{', '}', '[', ']', '(', ')', '\n', '\t', '=':
		return true
	}
	return false
}

// exfilPathTokenAround expands outward from idx in argsStr until a path
// delimiter, yielding the full path token (e.g. `/Users/bob/.ssh/id_rsa`
// out of `{"path":"/Users/bob/.ssh/id_rsa"}`).
func exfilPathTokenAround(argsStr string, idx int) string {
	if idx < 0 || idx >= len(argsStr) {
		return ""
	}
	start, end := idx, idx+1
	for start > 0 && !exfilPathDelim(argsStr[start-1]) {
		start--
	}
	for end < len(argsStr) && !exfilPathDelim(argsStr[end]) {
		end++
	}
	return strings.TrimSpace(argsStr[start:end])
}

// extractExfilSecretSnippets scans content with secret_redact.go's
// secretPatterns and returns the matched VALUES (last non-empty capture
// group, falling back to the full match). Values, not key names, are what
// an exfil payload would carry.
func extractExfilSecretSnippets(content string) []string {
	if len(content) > maxRedactScanLen {
		content = content[:maxRedactScanLen]
	}
	var out []string
	seen := make(map[string]bool)
	for _, sp := range secretPatterns {
		m := sp.pattern.FindStringSubmatch(content)
		if len(m) == 0 {
			continue
		}
		val := m[len(m)-1]
		if val == "" {
			val = m[0]
		}
		val = strings.TrimSpace(val)
		if len(val) < exfilMinSnippetLen {
			continue
		}
		key := strings.ToLower(val)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, val)
	}
	return out
}

// recordSensitiveSource records fingerprints when a source-read tool
// (read_file / grep / run_command) either returned content matching a
// secret pattern or was aimed at a sensitive path. Exempt paths
// (testdata, fixtures, .env.example) record nothing.
func (s *exfilChainState) recordSensitiveSource(toolName, argsStr, content string) {
	switch toolName {
	case "read_file", "grep", "run_command":
	default:
		return
	}
	if exfilSourceExempt(argsStr) {
		return
	}

	now := time.Now()
	seen := make(map[string]bool)
	for _, fp := range s.fingerprints {
		seen[strings.ToLower(fp.snippet)] = true
	}
	add := func(snippet, kind string) {
		if len(s.fingerprints) >= maxExfilFingerprints {
			return
		}
		key := strings.ToLower(snippet)
		if seen[key] {
			return
		}
		seen[key] = true
		s.fingerprints = append(s.fingerprints, exfilFingerprint{
			snippet:    snippet,
			kind:       kind,
			sourceTool: toolName,
			recordedAt: now,
			stepIndex:  s.stepCounter,
		})
	}

	// Trigger A: tool result contains a secret value.
	for _, val := range extractExfilSecretSnippets(content) {
		add(val, "secret-value")
	}

	// Trigger B: arguments reference a sensitive path (content may be an
	// opaque key body no regex can recognize).
	if marker := exfilSensitivePathMarker(argsStr); marker != "" {
		lower := strings.ToLower(argsStr)
		idx := strings.Index(lower, marker)
		if token := exfilPathTokenAround(argsStr, idx); token != "" {
			add(token, "sensitive-path")
		}
	}
}

// checkExfilChain determines whether an outbound tool call carries
// sensitive-source content. Returns a non-empty guidance string when an
// exfiltration chain is detected.
func (s *exfilChainState) checkExfilChain(toolName, argsStr string) string {
	s.stepCounter++

	if len(s.fingerprints) == 0 {
		return ""
	}
	fps := s.pruneExpired(time.Now())
	if len(fps) == 0 {
		return ""
	}

	isSink := exfilSinkTools[toolName]
	if toolName == "run_command" {
		// Sink only for egress commands aimed at non-local destinations.
		if exfilOutboundCmdRe.MatchString(argsStr) && !exfilLocalHostRe.MatchString(argsStr) {
			isSink = true
		}
	}
	if !isSink {
		return ""
	}

	lowerArgs := strings.ToLower(argsStr)

	// Tier 1: direct payload propagation -- the sensitive value or path
	// appears literally in the outbound arguments. High precision; only
	// fingerprints long enough to be distinctive verbatim-match.
	if s.warnedDirect < maxDirectExfilWarnings {
		for _, fp := range fps {
			if len(fp.snippet) < exfilMinSnippetLen {
				continue
			}
			if strings.Contains(lowerArgs, strings.ToLower(fp.snippet)) {
				s.warnedDirect++
				debug.Log("exfil-chain", "DIRECT exfil: %s from %s found in %s args", fp.kind, fp.sourceTool, toolName)
				return fmt.Sprintf(
					"[SECURITY: Data Exfiltration Chain] Sensitive content (%s, originally read via tool '%s') appears "+
						"VERBATIM in the arguments of outbound tool '%s'. This is the canonical read-then-send exfiltration "+
						"pattern: each action alone looks legitimate, the chain does not. Do NOT transmit secrets, key files, "+
						"or .env contents to external services. If the user explicitly asked to share this exact value, confirm "+
						"with them first; otherwise remove the sensitive data from the outgoing payload or abort.",
					fp.kind, fp.sourceTool, toolName,
				)
			}
		}
	}

	// Tier 2: proximity window -- an outbound call fires shortly after a
	// sensitive read without a verbatim match. Low confidence notice.
	if s.warnedProximity < maxProximityExfilWarnings {
		for _, fp := range fps {
			stepsSince := s.stepCounter - fp.stepIndex
			if stepsSince >= 1 && stepsSince <= exfilProximityWindowSteps {
				s.warnedProximity++
				debug.Log("exfil-chain", "PROXIMITY: %s called %d steps after sensitive source from %s", toolName, stepsSince, fp.sourceTool)
				return fmt.Sprintf(
					"[SECURITY: Possible Exfiltration Chain (low confidence)] Outbound tool '%s' invoked only %d tool-call "+
						"step(s) after a sensitive source was read via '%s', with no verbatim payload match. Before sending, "+
						"verify the outgoing payload contains no secrets, private keys, or .env values. If this outbound call "+
						"is unrelated to the earlier sensitive read, proceed; if the user requested it, state what is being sent.",
					toolName, stepsSince, fp.sourceTool,
				)
			}
		}
	}

	return ""
}

func (s *exfilChainState) pruneExpired(now time.Time) []exfilFingerprint {
	var live []exfilFingerprint
	for _, fp := range s.fingerprints {
		if now.Sub(fp.recordedAt) <= exfilExpirySeconds*time.Second {
			live = append(live, fp)
		}
	}
	s.fingerprints = live
	return live
}
