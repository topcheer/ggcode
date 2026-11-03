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
