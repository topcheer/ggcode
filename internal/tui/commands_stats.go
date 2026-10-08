package tui

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/topcheer/ggcode/internal/agent"
)

// handleGuidanceStatsCommand (r14) surfaces the guidance-stats.jsonl
// aggregate - the only observability layer covering ALL landed detectors
// (200+ from prior research rounds). Command is /guidance (not /stats:
// that name is taken by the usage stats panel). Before this, which
// detectors actually fire or get suppressed in real sessions was
// invisible: the only access was hand-grepping the jsonl
// (docs/guide/stale-heuristic-detection.md:37). Integration-audit
// finding sa-164: dimension-1 exposure gap + the root cause of
// dimension-2 (guidance-budget contention) being unobservable.
//
// Usage:
//
//	/guidance        - aggregate view (last 200 jsonl lines)
//	/guidance <n>    - aggregate over the last n lines (1..1000)
//	/guidance <tag>  - drill down: the last hint text injected for that
//	                  tag from guidance-hints.jsonl (r15 introspection
//	                  span-payload layer: aggregate counts → exact prose)
func (m *Model) handleGuidanceStatsCommand(parts []string) tea.Cmd {
	wd := m.agent.WorkingDir()
	if wd == "" {
		m.chatWriteSystem(nextSystemID(), m.t("guidance.unavailable"))
		return nil
	}
	// r16: manual override subcommands must be intercepted BEFORE the
	// drill-down branch - "suppress"/"reset" are non-numeric and would
	// otherwise be treated as a tag query by the drill-down.
	if len(parts) >= 3 && (parts[1] == "suppress" || parts[1] == "reset") {
		m.chatWriteSystem(nextSystemID(), m.handleGuidanceOverride(wd, parts[1], strings.Join(parts[2:], " ")))
		return nil
	}
	// Non-numeric first arg (possibly multi-word) selects a tag drill-down.
	if len(parts) > 1 && !isAllDigits(parts[1]) {
		m.chatWriteSystem(nextSystemID(), m.drilldownGuidanceHint(wd, strings.Join(parts[1:], " ")))
		return nil
	}
	n := agent.WhyCountArg(parts)
	if n < 1 || n > 1000 {
		n = 200
	}
	path := filepath.Join(wd, ".ggcode", "memory", "guidance-stats.jsonl")
	lines, err := readTailLines(path, n)
	if err != nil || len(lines) == 0 {
		m.chatWriteSystem(nextSystemID(), m.t("guidance.empty"))
		return nil
	}
	m.chatWriteSystem(nextSystemID(), summarizeGuidanceStats(lines, m.agent.ClaimsSupervisionEnabled()))
	return nil
}

// isAllDigits reports whether s is a non-empty pure digit string.
func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// handleGuidanceOverride (r16) is the control half of the
// observability→controllability loop: after drilling into a misfiring
// detector the user can suppress it (/guidance suppress <tag>) or undo
// (/guidance reset <tag>). Writes go through the same atomic store the
// sa-109 auto channel uses, pinned Model:"manual" so they never count
// against the auto budget. State changes take effect immediately (the
// agent refreshes its process cache under the same lock).
func (m *Model) handleGuidanceOverride(wd, verb, query string) string {
	q := strings.TrimSpace(query)
	if q == "" {
		return m.t("guidance.usage")
	}
	tag, ok := m.resolveUniqueGuidanceTag(wd, q)
	if !ok {
		return fmt.Sprintf(m.t("guidance.tagmiss"), q, m.knownGuidanceTags(wd))
	}
	if tag == "" {
		return "" // ambiguous: resolveUnique already reported via candidates
	}
	switch verb {
	case "suppress":
		if err := agent.SetHarnessSuppressed(tag); err != nil {
			return fmt.Sprintf("suppress failed: %v", err)
		}
		return fmt.Sprintf(m.t("guidance.suppressed"), tag)
	case "reset":
		cleared, err := agent.ClearHarnessOverride(tag)
		if err != nil {
			return fmt.Sprintf("reset failed: %v", err)
		}
		if !cleared {
			return fmt.Sprintf(m.t("guidance.noreset"), tag)
		}
		return fmt.Sprintf(m.t("guidance.reset"), tag)
	}
	return m.t("guidance.usage")
}

// resolveUniqueGuidanceTag resolves a user query to exactly one recorded
// tag: exact (case-insensitive) match first, then substring. Returns
// ("", false) on no match and ("", true) on ambiguity - the latter prints
// the candidate list so a state-changing verb never fires on a guess
// (drill-down display tolerates picking the last hit; suppression must
// not - r16 review note from sa-166).
func (m *Model) resolveUniqueGuidanceTag(wd, query string) (string, bool) {
	q := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(query), "##"))
	tags := m.knownGuidanceTagsList(wd)
	if len(tags) == 0 {
		return "", false
	}
	var exact string
	for _, t := range tags {
		if strings.EqualFold(t, q) {
			exact = t
		}
	}
	if exact != "" {
		return exact, true
	}
	var cands []string
	for _, t := range tags {
		if strings.Contains(strings.ToLower(t), strings.ToLower(q)) {
			cands = append(cands, t)
		}
	}
	if len(cands) == 1 {
		return cands[0], true
	}
	if len(cands) > 1 {
		sort.Strings(cands)
		m.chatWriteSystem(nextSystemID(), fmt.Sprintf(m.t("guidance.ambiguous"), q, strings.Join(cands, "\n  ")))
		return "", true // ambiguous: candidates already reported
	}
	return "", false
}

// knownGuidanceTagsList returns the deduped, insertion-ordered tag list
// recorded in guidance-hints.jsonl.
func (m *Model) knownGuidanceTagsList(wd string) []string {
	lines, err := readTailLines(filepath.Join(wd, ".ggcode", "memory", "guidance-hints.jsonl"), 1000)
	if err != nil {
		return nil
	}
	var tags []string
	seen := map[string]bool{}
	for _, line := range lines {
		var rec struct {
			Tag string `json:"tag"`
		}
		if json.Unmarshal([]byte(line), &rec) == nil && rec.Tag != "" && !seen[rec.Tag] {
			seen[rec.Tag] = true
			tags = append(tags, rec.Tag)
		}
	}
	return tags
}

// knownGuidanceTags renders the recorded tag list for miss messages.
func (m *Model) knownGuidanceTags(wd string) string {
	tags := m.knownGuidanceTagsList(wd)
	if len(tags) == 0 {
		return m.t("guidance.nohints")
	}
	sort.Strings(tags)
	return strings.Join(tags, "\n  ")
}

// drilldownGuidanceHint (r15) resolves a tag query against
// guidance-hints.jsonl: exact match first, then unique case-insensitive
// prefix/substring match. Falls back to listing the available tags so the
// user can retry without opening the file.
func (m *Model) drilldownGuidanceHint(wd, query string) string {
	q := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(query), "##"))
	path := filepath.Join(wd, ".ggcode", "memory", "guidance-hints.jsonl")
	lines, err := readTailLines(path, 1000)
	if err != nil || len(lines) == 0 {
		return m.t("guidance.nohints")
	}
	type hintRec struct {
		TS   string `json:"ts"`
		Tag  string `json:"tag"`
		Text string `json:"text"`
	}
	var recs []hintRec
	for _, line := range lines {
		var r hintRec
		if json.Unmarshal([]byte(line), &r) == nil && r.Tag != "" && r.Text != "" {
			recs = append(recs, r)
		}
	}
	if len(recs) == 0 {
		return m.t("guidance.nohints")
	}
	// Exact match wins (last record = most recent).
	var last *hintRec
	for i := range recs {
		if strings.EqualFold(recs[i].Tag, q) {
			last = &recs[i]
		}
	}
	if last == nil {
		// Otherwise a unique case-insensitive substring match.
		var cand []int
		for i := range recs {
			if strings.Contains(strings.ToLower(recs[i].Tag), strings.ToLower(q)) {
				cand = append(cand, i)
			}
		}
		if len(cand) >= 1 {
			last = &recs[cand[len(cand)-1]]
		}
	}
	if last == nil {
		tags := make([]string, 0, len(recs))
		seen := map[string]bool{}
		for _, r := range recs {
			if !seen[r.Tag] {
				seen[r.Tag] = true
				tags = append(tags, r.Tag)
			}
		}
		sort.Strings(tags)
		return fmt.Sprintf(m.t("guidance.tagmiss"), q, strings.Join(tags, "\n  "))
	}
	return fmt.Sprintf("%s  (%s)\n\n%s", last.Tag, trimTS(last.TS), last.Text)
}

// readTailLines returns up to the last n lines of path. Files are small
// (one line per detector tag per run), so a full read is fine; the cap
// exists to bound very long-lived workspaces.
func readTailLines(path string, n int) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var all []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		if line := strings.TrimSpace(sc.Text()); line != "" {
			all = append(all, line)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if len(all) > n {
		all = all[len(all)-n:]
	}
	return all, nil
}

// summarizeGuidanceStats aggregates jsonl records
// {ts, model?, tag, delivered, suppressed} by tag, sorted by total
// injections (delivered+suppressed) descending, plus a header with the
// observed time window and per-model split. claimsOn labels the gated
// claimsSupervision family (default off, sa-164 discoverability finding).
// type=stale_heuristic report lines in the same file surface as a trailing
// "stale" list (r22 harness-assumption expiry, previously grep-only).
func summarizeGuidanceStats(lines []string, claimsOn bool) string {
	type stat struct {
		delivered, suppressed int
	}
	tags := map[string]*stat{}
	models := map[string]int{}
	var staleTags []string
	seenStale := map[string]bool{}
	firstTS, lastTS := "", ""
	for _, line := range lines {
		var rec struct {
			TS         string `json:"ts"`
			Model      string `json:"model"`
			Tag        string `json:"tag"`
			Type       string `json:"type"`
			Delivered  int    `json:"delivered"`
			Suppressed int    `json:"suppressed"`
		}
		if err := json.Unmarshal([]byte(line), &rec); err != nil || rec.Tag == "" {
			continue
		}
		if rec.Type == "stale_heuristic" {
			if !seenStale[rec.Tag] {
				seenStale[rec.Tag] = true
				staleTags = append(staleTags, rec.Tag)
			}
			continue
		}
		st := tags[rec.Tag]
		if st == nil {
			st = &stat{}
			tags[rec.Tag] = st
		}
		st.delivered += rec.Delivered
		st.suppressed += rec.Suppressed
		if rec.Model != "" {
			models[rec.Model] += rec.Delivered + rec.Suppressed
		}
		if rec.TS != "" {
			if firstTS == "" || rec.TS < firstTS {
				firstTS = rec.TS
			}
			if rec.TS > lastTS {
				lastTS = rec.TS
			}
		}
	}
	if len(tags) == 0 {
		return ""
	}
	window := ""
	if firstTS != "" && lastTS != "" {
		window = fmt.Sprintf("%s .. %s", trimTS(firstTS), trimTS(lastTS))
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Guidance stats: %d tag(s), window %s  [claimsSupervision: %s]\n", len(tags), window, onOff(claimsOn)))
	if len(models) > 0 {
		ml := make([]string, 0, len(models))
		for mo, c := range models {
			ml = append(ml, fmt.Sprintf("%s=%d", mo, c))
		}
		sort.Strings(ml)
		b.WriteString("models: " + strings.Join(ml, " ") + "\n")
	}
	b.WriteString(fmt.Sprintf("%-4s %5s %5s %6s  %s\n", "#", "fire", "supp", "tot", "tag"))
	order := make([]string, 0, len(tags))
	for t := range tags {
		order = append(order, t)
	}
	sort.Slice(order, func(i, j int) bool {
		a, b2 := tags[order[i]], tags[order[j]]
		if a.delivered+a.suppressed != b2.delivered+b2.suppressed {
			return a.delivered+a.suppressed > b2.delivered+b2.suppressed
		}
		return order[i] < order[j]
	})
	for i, t := range order {
		st := tags[t]
		b.WriteString(fmt.Sprintf("%-4d %5d %5d %6d  %s\n", i+1, st.delivered, st.suppressed, st.delivered+st.suppressed, t))
	}
	if len(staleTags) > 0 {
		sort.Strings(staleTags)
		b.WriteString(fmt.Sprintf("\nstale (harness-flagged, sa-109 auto-suppress candidates):\n"))
		for _, t := range staleTags {
			b.WriteString("  - " + t + "\n")
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// onOff renders a bool as on/off for the gated-detector header label.
func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

// trimTS shortens an RFC3339 timestamp to minute precision for the header.
func trimTS(ts string) string {
	if t, err := time.Parse(time.RFC3339, ts); err == nil {
		return t.Local().Format("01-02 15:04")
	}
	return ts
}
