package memory

import (
	"regexp"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

type PreflushMsg struct {
	Role string
	Text string
}

var preflushMarkers = []string{
	"don't", "do not", "never", "must ", "must not", "always ", "forbidden",
	"不要", "必须", "别用", "别改", "不能", "禁止", "务必", "只能",
}

var preflushSpeakerRe = regexp.MustCompile(`(?i)^(?:user|assistant|system|tool|human|ai)\s*[>:]\s*`)

const (
	preflushKey = "compaction-facts"

	preflushMaxLineLen = 200

	preflushMaxExtract = 12

	preflushMaxEntries = 24
)

func extractPreflushLines(m PreflushMsg) []string {
	if m.Role != "user" && m.Role != "assistant" {
		return nil
	}
	var out []string
	seen := map[string]bool{}
	for _, raw := range strings.Split(m.Text, "\n") {
		line := strings.TrimSpace(preflushSpeakerRe.ReplaceAllString(strings.TrimSpace(raw), ""))
		if line == "" || len(line) > preflushMaxLineLen || seen[line] {
			continue
		}
		low := strings.ToLower(line)
		for _, mk := range preflushMarkers {
			if strings.Contains(low, mk) {
				seen[line] = true
				out = append(out, line)
				if len(out) >= preflushMaxExtract {
					return out
				}
				break
			}
		}
	}
	return out
}

func mergeFactLines(existing string, facts []string) (string, int) {
	var lines []string
	for _, l := range strings.Split(existing, "\n") {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		lines = append(lines, strings.TrimPrefix(l, "- "))
	}
	seen := map[string]bool{}
	for _, l := range lines {
		seen[strings.ToLower(l)] = true
	}
	added := 0
	for _, f := range facts {
		key := strings.ToLower(f)
		if seen[key] {
			continue
		}
		seen[key] = true
		lines = append(lines, f)
		added++
	}
	if len(lines) > preflushMaxEntries {
		lines = lines[len(lines)-preflushMaxEntries:]
	}
	var b strings.Builder
	for _, l := range lines {
		b.WriteString("- ")
		b.WriteString(l)
		b.WriteByte('\n')
	}
	return b.String(), added
}

func PreflushFacts(workingDir string, msgs []PreflushMsg) int {
	if workingDir == "" || len(msgs) == 0 {
		return 0
	}
	var facts []string
	fseen := map[string]bool{}
	for _, m := range msgs {
		for _, line := range extractPreflushLines(m) {
			if !fseen[line] {
				fseen[line] = true
				facts = append(facts, line)
			}
		}
	}
	if len(facts) == 0 {
		return 0
	}
	autoMem := NewProjectAutoMemory(workingDir)
	if autoMem == nil {
		return 0
	}
	existing, err := autoMem.LoadKey(preflushKey)
	if err != nil {
		debug.Log("preflush", "failed to load existing %s, skipping save: %v", preflushKey, err)
		return 0
	}
	merged, added := mergeFactLines(existing, facts)
	if added == 0 {
		return 0
	}
	if err := autoMem.SaveMemoryWithSource(preflushKey, merged, "preflush"); err != nil {
		debug.Log("preflush", "save failed: %v", err)
		return 0
	}
	debug.Log("preflush", "persisted %d new fact(s) before compaction", added)
	return added
}
