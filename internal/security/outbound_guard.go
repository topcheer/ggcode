package security

// Outbound secret guard (OWASP 2026 LLM02: sensitive information disclosure).
//
// Every outbound tool argument (a URL passed to web_fetch/browser, a query
// passed to web_search) is a potential data-exfiltration channel: secrets the
// agent has read earlier in the session — or that an injected document
// steered into an argument — must not leave the machine embedded in a request
// without an explicit opt-out.
//
// This module performs NO new detection: it reuses the displaySecretPatterns
// list that backs RedactForDisplay, applied at the outbound boundary instead
// of the display boundary. It reports findings (pattern name + masked value)
// so callers can produce an actionable error for the agent.

// OutboundFinding describes one secret-shaped match inside an outbound
// argument. Masked is safe to show to the agent/user.
type OutboundFinding struct {
	Name   string
	Masked string
}

// maxFindingsPerPattern caps how many matches of one pattern are reported,
// so a pathological argument cannot flood the result.
const maxFindingsPerPattern = 3

// CheckOutboundSecrets scans content with the shared secret-pattern list and
// returns findings (pattern name + masked sample). Returns nil when nothing
// secret-shaped is present or content is too short to contain one.
func CheckOutboundSecrets(content string) []OutboundFinding {
	if len(content) < 10 {
		return nil
	}
	var findings []OutboundFinding
	seen := make(map[string]bool)
	for _, sp := range displaySecretPatterns {
		matches := sp.pattern.FindAllString(content, maxFindingsPerPattern)
		for _, m := range matches {
			masked := m
			if sp.pattern.NumSubexp() >= 2 {
				sub := sp.pattern.FindStringSubmatch(m)
				// sub[1] prefix, sub[2] value (same layout RedactForDisplay uses).
				if len(sub) < 3 {
					continue
				}
				masked = maskValue(sub[2])
			} else {
				masked = maskValue(m)
			}
			key := sp.name + ":" + masked
			if seen[key] {
				continue
			}
			seen[key] = true
			findings = append(findings, OutboundFinding{Name: sp.name, Masked: masked})
		}
	}
	return findings
}
