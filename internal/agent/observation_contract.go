package agent

import (
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
)

// Observation Contract Guard — mid-trajectory value fidelity across tool calls
//
// Research basis: ContractBench (NeurIPS 2026 E&D Track, arXiv:2605.17281)
// shows that LLM agents frequently FAIL to preserve "observation contracts":
// when a tool result carries an opaque artifact (40-hex commit SHA, PR/issue
// number, presigned URL, session/JWT token), the model often re-types it into
// a LATER tool call's arguments with small transcription errors (integrity
// failure), or reuses it after its validity window has lapsed (validity
// failure). Across 38 evaluated models the best (Opus-4.6) preserved contracts
// in only 77.8% of mid-trajectory probes — this is a restraint capability
// that does not disappear with model upgrades.
//
// This guard is different from existing mechanisms:
//   - tool_sequence.go: call ORDER anti-patterns, not value fidelity
//   - file_integrity.go: file mtime staleness, not tool-artifact bytes
//   - write_integrity.go: post-write content echo, not cross-call reuse
//   - typosquat_check.go: dependency NAME confusion at write time
//
// Mechanism (zero LLM cost, deterministic):
//  1. recordResult(): extract artifacts (SHA-1/SHA-256 hex, #N PR/issue
//     refs, URLs carrying query params, JWT/base64-ish tokens ≥16 chars)
//     from each successful tool result into a bounded ring buffer.
//  2. checkArgs(): extract the same artifact shapes from each subsequent
//     tool call's arguments. An artifact that exactly matches a prior
//     observation is fine. One that is a NEAR match (edit distance ≤2 on
//     artifacts ≥8 chars) with no exact match is a likely mistranscription
//     (integrity). A stored artifact whose URL TTL (X-Amz-Expires / Expires
//     / X-Expires seconds) has lapsed since observation raises a validity
//     warning when re-used.
//
// Each warning type fires at most once per run (toolSequence convention) to
// avoid nagging; the outcome is a single guidance line appended to the tool
// result, not a hard block.

const (
	// obsMaxArtifacts: bounded ring buffer of observed artifacts.
	obsMaxArtifacts = 50

	// obsNearMatchMinLen: artifacts shorter than this never trigger
	// near-match integrity warnings (too collision-prone); they participate
	// in exact lookup only.
	obsNearMatchMinLen = 8

	// obsNearMatchDistance: max edit distance for a "likely mistranscribed"
	// artifact. SHA hex, JWTs and full URLs that differ in ≥3 positions are
	// treated as unrelated values, not typos.
	obsNearMatchDistance = 2

	// obsResultScanLimit: cap on result text scanned for artifacts —
	// pathological multi-MB tool outputs must not stall the loop.
	obsResultScanLimit = 64 * 1024
)

var (
	// 40-hex: git SHA-1 / SHA-256 truncated forms used by gh and git output.
	obsSHARe = regexp.MustCompile(`\b[0-9a-f]{40}\b`)
	// #N: PR / issue references (gh CLI output, review comments).
	obsRefRe = regexp.MustCompile(`#\d{1,6}\b`)
	// URLs carrying a query string (presigned URLs, API calls with tokens).
	obsURLRe = regexp.MustCompile(`https?://[^\s"'<>]+[?][^\s"'<>]+`)
	// JWT (eyJ... two dots) or long base64/base64url-ish token runs.
	obsTokenRe = regexp.MustCompile(`\b[A-Za-z0-9_-]{16,}\b`)
	// TTL query parameters interpreted as seconds.
	obsTTLParams = []string{"X-Amz-Expires", "X-Expires", "Expires"}
)

// obsArtifact is one observed tool-output artifact eligible for contract
// checking.
type obsArtifact struct {
	value     string
	tool      string
	iter      int
	at        time.Time
	ttlSecs   int // >0 when the artifact is a TTL-bearing URL
	expiresAt time.Time
}

type observationContractTracker struct {
	artifacts  []obsArtifact // ring buffer, newest last
	hintsGiven map[string]bool
}

func newObservationContractTracker() *observationContractTracker {
	return &observationContractTracker{hintsGiven: make(map[string]bool)}
}

func (t *observationContractTracker) reset() {
	t.artifacts = nil
	t.hintsGiven = make(map[string]bool)
}

// tokenish filters obsTokenRe hits that look like real opaque tokens rather
// than ordinary identifiers: must mix upper AND lower case AND contain a
// digit. Pure-word camelCase identifiers survive this filter, but they only
// ever matter on near-match (edit distance ≤2) without exact match, which
// two unrelated identifiers essentially never satisfy.
func obsTokenish(s string) bool {
	var hasUp, hasLow, hasDigit bool
	for _, r := range s {
		switch {
		case r >= 'A' && r <= 'Z':
			hasUp = true
		case r >= 'a' && r <= 'z':
			hasLow = true
		case r >= '0' && r <= '9':
			hasDigit = true
		}
	}
	return hasUp && hasLow && hasDigit
}

// obsSameURLBase reports whether two http(s) strings share scheme, host and
// path — i.e. they differ only in query (or fragment) values. Agents
// intentionally vary query parameters when paginating or slicing requests
// (page=1→page=2, date windows, ids), so a query-only difference is a
// semantic mutation, not a mistranscription (#3981). Presigned signature
// re-orderings almost always exceed the near-match distance anyway, so
// exempting query differences costs essentially no detection power.
func obsSameURLBase(a, b string) bool {
	ua, erra := url.Parse(a)
	ub, errb := url.Parse(b)
	if erra != nil || errb != nil {
		return false
	}
	return ua.Scheme == ub.Scheme && ua.Host == ub.Host && ua.Path == ub.Path
}

// obsTTLSeconds extracts a validity window in seconds from a URL's query
// parameters; 0 when the URL carries no TTL.
func obsTTLSeconds(rawURL string) int {
	u, err := url.Parse(rawURL)
	if err != nil {
		return 0
	}
	for _, p := range obsTTLParams {
		if v := u.Query().Get(p); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				return n
			}
		}
	}
	return 0
}

// obsExtract returns the artifacts present in text, deduplicated in order of
// appearance. Reused for both tool results (record side) and tool arguments
// (check side) so the two views stay symmetric.
func obsExtract(text string) []string {
	if text == "" {
		return nil
	}
	if len(text) > obsResultScanLimit {
		text = text[:obsResultScanLimit]
	}
	var out []string
	seen := make(map[string]bool)
	add := func(v string) {
		if v != "" && !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	for _, m := range obsSHARe.FindAllString(text, -1) {
		add(m)
	}
	for _, m := range obsURLRe.FindAllString(text, -1) {
		add(strings.TrimRight(m, ".,;)"))
	}
	for _, m := range obsTokenRe.FindAllString(text, -1) {
		// Exact-string dedup only: a token run that is also a substring of a
		// captured URL/SHA stays as its own artifact, which is harmless (both
		// views stay symmetric across the record/check sides).
		if obsTokenish(m) {
			add(m)
		}
	}
	for _, m := range obsRefRe.FindAllString(text, -1) {
		add(m)
	}
	if len(out) > obsMaxArtifacts {
		out = out[:obsMaxArtifacts]
	}
	return out
}

// recordResult stores the artifacts of a completed tool result.
func (t *observationContractTracker) recordResult(toolName, content string, iter int) {
	for _, v := range obsExtract(content) {
		a := obsArtifact{value: v, tool: toolName, iter: iter, at: time.Now()}
		if strings.HasPrefix(v, "http") {
			a.ttlSecs = obsTTLSeconds(v)
			if a.ttlSecs > 0 {
				a.expiresAt = a.at.Add(time.Duration(a.ttlSecs) * time.Second)
			}
		}
		t.artifacts = append(t.artifacts, a)
	}
	if len(t.artifacts) > obsMaxArtifacts {
		t.artifacts = t.artifacts[len(t.artifacts)-obsMaxArtifacts:]
	}
}

// hasExact reports whether value was observed verbatim earlier.
func (t *observationContractTracker) hasExact(value string) bool {
	for _, a := range t.artifacts {
		if a.value == value {
			return true
		}
	}
	return false
}

// checkArgs validates the artifacts re-typed into a tool call's arguments
// against prior observations, returning guidance text when a contract
// violation is likely. Integrity and validity each fire at most once per run.
func (t *observationContractTracker) checkArgs(toolName string, arguments []byte, iter int) string {
	if len(arguments) == 0 || len(t.artifacts) == 0 {
		return ""
	}
	argArts := obsExtract(string(arguments))
	if len(argArts) == 0 {
		return ""
	}

	for _, arg := range argArts {
		if t.hasExact(arg) {
			// Faithful reproduction — the contract HOLDS. Also the validity
			// axis: if the matching stored artifact is a TTL URL past its
			// window, warn before it is used.
			if !t.hintsGiven["obs_validity"] {
				for _, a := range t.artifacts {
					if a.value == arg && a.ttlSecs > 0 && time.Now().After(a.expiresAt) {
						t.hintsGiven["obs_validity"] = true
						hint := fmt.Sprintf(
							"[observation-contract] %q was observed from %s (iter %d) %s ago but its URL TTL is %ds — "+
								"it has likely EXPIRED. Re-fetch a fresh value before relying on it.",
							truncForHint(arg), a.tool, a.iter, durShort(time.Since(a.at)), a.ttlSecs,
						)
						debug.Log("obs-contract", "validity failure: ttl expired (iter=%d tool=%s)", iter, toolName)
						return hint
					}
				}
			}
			continue
		}
		// Integrity axis: near-match without exact match.
		if len(arg) < obsNearMatchMinLen || t.hintsGiven["obs_integrity"] {
			continue
		}
		for _, a := range t.artifacts {
			if len(a.value) < obsNearMatchMinLen {
				continue
			}
			// URL awareness (#3981): when both sides are URLs and only query
			// parameters differ (pagination, parameter changes), that is an
			// intentional mutation rather than a mistranscription — skip it.
			// The near-match hint stays reserved for host/path-level typos.
			if strings.HasPrefix(arg, "http") && strings.HasPrefix(a.value, "http") && obsSameURLBase(arg, a.value) {
				continue
			}
			d := levenshtein(arg, a.value)
			if d > 0 && d <= obsNearMatchDistance {
				t.hintsGiven["obs_integrity"] = true
				hint := fmt.Sprintf(
					"[observation-contract] argument artifact %q differs by %d character(s) from %q observed in an earlier %s result (iter %d) — "+
						"likely mistranscribed. Re-copy the exact value from that tool output instead of re-typing it.",
					truncForHint(arg), d, truncForHint(a.value), a.tool, a.iter,
				)
				debug.Log("obs-contract", "integrity failure: near-match d=%d (iter=%d tool=%s)", d, iter, toolName)
				return hint
			}
		}
	}
	return ""
}

// truncForHint keeps guidance lines readable for long URLs/tokens.
func truncForHint(s string) string {
	const max = 48
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}

// durShort renders short human durations for hint text.
func durShort(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	return fmt.Sprintf("%dh", int(d.Hours()))
}
