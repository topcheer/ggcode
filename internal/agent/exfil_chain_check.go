package agent

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
)

var exfilSinkTools = map[string]bool{
	"web_fetch":           true,
	"web_search":          true,
	"browser":             true,
	"im":                  true,
	"lanchat":             true,
	"send_file_to_mobile": true,
}

var exfilOutboundCmdRe = regexp.MustCompile(`(?i)\b(curl|wget|nc|ncat|netcat|ssh|scp)\b`)

var exfilLocalHostRe = regexp.MustCompile(`(?i)(localhost|127\.0\.0\.1|::1|\b10\.\d{1,3}\.\d{1,3}\.\d{1,3}\b|\b192\.168\.\d{1,3}\.\d{1,3}\b|\b172\.(1[6-9]|2[0-9]|3[01])\.\d{1,3}\.\d{1,3}\b)`)

var exfilSensitivePathMarkers = []string{
	"~/.ssh", ".ssh/", ".pem", ".env", "credentials",
}

// exfilPlaceholderNamePrefixes (#5769) is the placeholder vocabulary: a
// sensitive token living inside an identifier that carries one of these
// case-insensitive prefixes is a test-fixture / doc-example name, not a
// credential. Test placeholder names (TEST_API_KEY=, ISSUE_5759_KEY, ...)
// must not feed the outbound chain alarm.
var exfilPlaceholderNamePrefixes = []string{
	"test", "example", "fake", "dummy", "sample", "mock", "stub",
	"placeholder", "fixture",
}

// exfilIssueNameRe matches tracker-prefixed identifiers such as
// ISSUE_5759_KEY or ISSUE-5759-API-KEY (regression-test naming convention).
var exfilIssueNameRe = regexp.MustCompile("(?i)^issue[_-]?[0-9]+[_-]")

// exfilPlaceholderName reports whether id is a placeholder-style identifier.
func exfilPlaceholderName(id string) bool {
	if id == "" {
		return false
	}
	lower := strings.ToLower(id)
	if exfilIssueNameRe.MatchString(lower) {
		return true
	}
	for _, p := range exfilPlaceholderNamePrefixes {
		if strings.HasPrefix(lower, p) {
			return true
		}
	}
	return false
}

// exfilIdentChar reports whether b can appear inside an identifier-ish token.
func exfilIdentChar(b byte) bool {
	return b == '_' || b == '-' ||
		(b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9')
}

// exfilIdentifierBefore returns the identifier-looking token that ends
// immediately before content[idx], where idx is the start of a pattern match.
// Sensitive keywords match mid-identifier (TEST_API_KEY contains API_KEY),
// so the surrounding identifier is the naming context to classify.
func exfilIdentifierBefore(content string, idx int) string {
	start := idx
	for start > 0 && exfilIdentChar(content[start-1]) {
		start--
	}
	return content[start:idx]
}

// exfilPlaceholderShapedValue reports whether an assignment value looks like
// an identifier or word-like placeholder rather than a credential
// (#5769 mitigation 2). Credential-shaped values mix letter cases or carry
// non-identifier specials (sk-ant-..., AKIA.../+, base64 =+); single-case
// identifier-ish tokens (ISSUE_5759_KEY_PINS_THE_FIX, dummy_value_1234567890,
// bare names picked up across a newline) are placeholders.
func exfilPlaceholderShapedValue(val string) bool {
	if len(val) < 16 {
		return true
	}
	var hasLower, hasUpper, hasSpecial bool
	for i := 0; i < len(val); i++ {
		switch c := val[i]; {
		case c >= 'a' && c <= 'z':
			hasLower = true
		case c >= 'A' && c <= 'Z':
			hasUpper = true
		case c >= '0' && c <= '9':
			// digit: class-neutral
		case c == '_':
			// identifier glue, not a credential special
		default:
			hasSpecial = true
		}
	}
	if hasSpecial {
		return false
	}
	return !(hasLower && hasUpper)
}

// exfilIdentifierValueRe matches values that are themselves identifier-shaped
// (no separators/specials), used for the placeholder-prefix value check.
var exfilIdentifierValueRe = regexp.MustCompile("^[A-Za-z][A-Za-z0-9_-]*$")

const (
	maxExfilFingerprints      = 6
	maxDirectExfilWarnings    = 3
	maxProximityExfilWarnings = 2
	exfilExpirySeconds        = 300
	exfilProximityWindowSteps = 6
	exfilMinSnippetLen        = 8
)

type exfilFingerprint struct {
	snippet    string
	kind       string
	sourceTool string
	recordedAt time.Time
	stepIndex  int
}

type exfilChainState struct {
	fingerprints    []exfilFingerprint
	warnedDirect    int
	warnedProximity int
	stepCounter     int
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

func exfilSensitivePathMarker(argsStr string) string {
	lower := strings.ToLower(argsStr)
	for _, m := range exfilSensitivePathMarkers {
		if strings.Contains(lower, m) {
			return m
		}
	}
	return ""
}

func exfilPathDelim(b byte) bool {
	switch b {
	case ' ', '"', '\'', ',', '{', '}', '[', ']', '(', ')', '\n', '\t', '=':
		return true
	}
	return false
}

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

func extractExfilSecretSnippets(content string) []string {
	if len(content) > maxRedactScanLen {
		content = content[:maxRedactScanLen]
	}
	var out []string
	seen := make(map[string]bool)
	for _, sp := range secretPatterns {
		loc := sp.pattern.FindStringSubmatchIndex(content)
		if loc == nil || len(loc) < 2 || loc[0] < 0 {
			continue
		}
		// #5769 mitigation 1: context/prefix whitelist. A sensitive token
		// inside a test-fixture or doc-example identifier is a placeholder
		// name; suppress the chain fingerprint so it can trigger neither the
		// direct nor the proximity alarm.
		if exfilPlaceholderName(exfilIdentifierBefore(content, loc[0])) {
			debug.Log("exfil-chain", "suppressed placeholder-named match: pattern=%s", sp.name)
			continue
		}
		m := make([]string, len(loc)/2)
		for i := range m {
			if loc[i*2] >= 0 {
				m[i] = content[loc[i*2]:loc[i*2+1]]
			}
		}
		val := m[len(m)-1]
		if val == "" {
			val = m[0]
		}
		val = strings.TrimSpace(val)
		if len(val) < exfilMinSnippetLen {
			continue
		}
		// #5769 mitigation 2: value-shape threshold. A generic scalar name
		// (API_KEY=, SECRET=) followed by nothing, a short placeholder, or an
		// identifier-ish word token differs materially from a real
		// credential value; only credential-shaped assignments feed the
		// chain alarm. Value-anchored patterns (sk-..., AKIA..., ghp_...,
		// JWTs, PEM blocks) are exempt: real keys keep recording regardless
		// of the variable name they sit under.
		if sp.name == "assignment_secret" {
			if exfilPlaceholderShapedValue(val) {
				debug.Log("exfil-chain", "suppressed placeholder-shaped assignment value: pattern=%s", sp.name)
				continue
			}
			if exfilIdentifierValueRe.MatchString(val) && exfilPlaceholderName(val) {
				debug.Log("exfil-chain", "suppressed placeholder-prefixed assignment value: pattern=%s", sp.name)
				continue
			}
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

	for _, val := range extractExfilSecretSnippets(content) {
		add(val, "secret-value")
	}

	if marker := exfilSensitivePathMarker(argsStr); marker != "" {
		lower := strings.ToLower(argsStr)
		idx := strings.Index(lower, marker)
		if token := exfilPathTokenAround(argsStr, idx); token != "" {
			add(token, "sensitive-path")
		}
	}
}

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

		if exfilOutboundCmdRe.MatchString(argsStr) && !exfilLocalHostRe.MatchString(argsStr) {
			isSink = true
		}
	}
	if !isSink {
		return ""
	}

	lowerArgs := strings.ToLower(argsStr)

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
