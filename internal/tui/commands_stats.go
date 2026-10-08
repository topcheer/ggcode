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
// Usage: /guidance [n]  - aggregate the last n jsonl lines (default 200,
// clamped 1..1000, same arg discipline as /why).
func (m *Model) handleGuidanceStatsCommand(parts []string) tea.Cmd {
	n := agent.WhyCountArg(parts)
	if n < 1 || n > 1000 {
		n = 200
	}
	wd := m.agent.WorkingDir()
	if wd == "" {
		m.chatWriteSystem(nextSystemID(), m.t("guidance.unavailable"))
		return nil
	}
	path := filepath.Join(wd, ".ggcode", "memory", "guidance-stats.jsonl")
	lines, err := readTailLines(path, n)
	if err != nil || len(lines) == 0 {
		m.chatWriteSystem(nextSystemID(), m.t("guidance.empty"))
		return nil
	}
	m.chatWriteSystem(nextSystemID(), summarizeGuidanceStats(lines))
	return nil
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
// observed time window and per-model split.
func summarizeGuidanceStats(lines []string) string {
	type stat struct {
		delivered, suppressed int
	}
	tags := map[string]*stat{}
	models := map[string]int{}
	firstTS, lastTS := "", ""
	for _, line := range lines {
		var rec struct {
			TS         string `json:"ts"`
			Model      string `json:"model"`
			Tag        string `json:"tag"`
			Delivered  int    `json:"delivered"`
			Suppressed int    `json:"suppressed"`
		}
		if err := json.Unmarshal([]byte(line), &rec); err != nil || rec.Tag == "" {
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
	b.WriteString(fmt.Sprintf("Guidance stats: %d tag(s), window %s\n", len(tags), window))
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
	return strings.TrimRight(b.String(), "\n")
}

// trimTS shortens an RFC3339 timestamp to minute precision for the header.
func trimTS(ts string) string {
	if t, err := time.Parse(time.RFC3339, ts); err == nil {
		return t.Local().Format("01-02 15:04")
	}
	return ts
}
