package hooks

import (
	"encoding/json"
	"strings"
)

// Structured hook decision protocol (r61). Mirrors the Claude Code hook
// convention: a blocking-event hook can answer with a JSON object on stdout
// (or the HTTP response body) instead of relying on exit codes:
//
//	{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"..."}}
//	{"permissionDecision":"deny","permissionDecisionReason":"..."}   (compact)
//	{"decision":"deny","reason":"..."}                               (shorthand)
//
// deny  → block the operation; the reason becomes the tool result
// allow → skip the interactive approval prompt for this call
// ask   → force the interactive approval prompt even if policy would allow
//
// Parsing is deliberately forgiving: hooks routinely print progress logs
// before the decision, so the whole output is tried first, then trailing
// lines that contain a JSON object. Output that does not carry a recognized
// decision key leaves the decision empty and the legacy exit-code semantics
// (exit 2 / HTTP 403) apply unchanged.
const maxDecisionScanBytes = 64 * 1024

type hookDecisionDoc struct {
	Decision           string `json:"decision"`
	Reason             string `json:"reason"`
	HookSpecificOutput *struct {
		HookEventName            string `json:"hookEventName"`
		PermissionDecision       string `json:"permissionDecision"`
		PermissionDecisionReason string `json:"permissionDecisionReason"`
	} `json:"hookSpecificOutput"`
	PermissionDecision       string `json:"permissionDecision"`
	PermissionDecisionReason string `json:"permissionDecisionReason"`
}

// decision resolves the first recognized decision and its reason, preferring
// the nested hookSpecificOutput schema, then top-level permissionDecision,
// then the compact shorthand.
func (d hookDecisionDoc) decision() (string, string) {
	if d.HookSpecificOutput != nil {
		if v := normalizeDecision(d.HookSpecificOutput.PermissionDecision); v != "" {
			return v, d.HookSpecificOutput.PermissionDecisionReason
		}
	}
	if v := normalizeDecision(d.PermissionDecision); v != "" {
		return v, d.PermissionDecisionReason
	}
	if v := normalizeDecision(d.Decision); v != "" {
		return v, d.Reason
	}
	return "", ""
}

func normalizeDecision(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case DecisionDeny:
		return DecisionDeny
	case DecisionAllow:
		return DecisionAllow
	case DecisionAsk:
		return DecisionAsk
	default:
		return ""
	}
}

// parseHookDecision extracts a structured decision from hook stdout or an
// HTTP response body. Returns ("", "") when the output carries no decision.
func parseHookDecision(out string) (string, string) {
	if strings.TrimSpace(out) == "" {
		return "", ""
	}
	if len(out) > maxDecisionScanBytes {
		out = out[len(out)-maxDecisionScanBytes:]
	}
	if dec, reason := tryParseDecision(out); dec != "" {
		return dec, reason
	}
	// Hooks often log before deciding; scan trailing lines so a JSON object
	// printed last still wins over earlier non-JSON noise.
	lines := strings.Split(out, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if !strings.Contains(line, "{") {
			continue
		}
		if dec, reason := tryParseDecision(line); dec != "" {
			return dec, reason
		}
	}
	return "", ""
}

func tryParseDecision(s string) (string, string) {
	var doc hookDecisionDoc
	if err := json.Unmarshal([]byte(strings.TrimSpace(s)), &doc); err != nil {
		return "", ""
	}
	return doc.decision()
}
