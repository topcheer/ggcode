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

const (
	obsMaxArtifacts = 50

	obsNearMatchMinLen = 8

	obsNearMatchDistance = 2

	obsResultScanLimit = 64 * 1024
)

var (
	obsSHARe     = regexp.MustCompile(`\b[0-9a-f]{40}\b`)
	obsRefRe     = regexp.MustCompile(`#\d{1,6}\b`)
	obsURLRe     = regexp.MustCompile(`https?://[^\s"'<>]+[?][^\s"'<>]+`)
	obsTokenRe   = regexp.MustCompile(`\b[A-Za-z0-9_-]{16,}\b`)
	obsTTLParams = []string{"X-Amz-Expires", "X-Expires", "Expires"}
)

type obsArtifact struct {
	value     string
	tool      string
	iter      int
	at        time.Time
	ttlSecs   int
	expiresAt time.Time
}

type observationContractTracker struct {
	artifacts  []obsArtifact
	hintsGiven map[string]bool
}

func newObservationContractTracker() *observationContractTracker {
	return &observationContractTracker{hintsGiven: make(map[string]bool)}
}

func (t *observationContractTracker) reset() {
	t.artifacts = nil
	t.hintsGiven = make(map[string]bool)
}

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

func obsSameURLBase(a, b string) bool {
	ua, erra := url.Parse(a)
	ub, errb := url.Parse(b)
	if erra != nil || errb != nil {
		return false
	}
	return ua.Scheme == ub.Scheme && ua.Host == ub.Host && ua.Path == ub.Path
}

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

func (t *observationContractTracker) hasExact(value string) bool {
	for _, a := range t.artifacts {
		if a.value == value {
			return true
		}
	}
	return false
}

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

		if len(arg) < obsNearMatchMinLen || t.hintsGiven["obs_integrity"] {
			continue
		}
		for _, a := range t.artifacts {
			if len(a.value) < obsNearMatchMinLen {
				continue
			}

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

func truncForHint(s string) string {
	const max = 48
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}

func durShort(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	return fmt.Sprintf("%dh", int(d.Hours()))
}
