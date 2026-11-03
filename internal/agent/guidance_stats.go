package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
)

const (
	guidanceTagMaxRunes = 48
	guidanceStatsPath   = ".ggcode/memory/guidance-stats.jsonl"
	guidanceHintsPath   = ".ggcode/memory/guidance-hints.jsonl"
)

type guidanceHintRec struct {
	TS   string `json:"ts"`
	Tag  string `json:"tag"`
	Text string `json:"text"`
}

type guidanceTagStat struct {
	Delivered  int `json:"delivered"`
	Suppressed int `json:"suppressed"`
}

type guidanceRunStats map[string]*guidanceTagStat

func (g guidanceRunStats) record(tag string, delivered bool) {
	st := g[tag]
	if st == nil {
		st = &guidanceTagStat{}
		g[tag] = st
	}
	if delivered {
		st.Delivered++
	} else {
		st.Suppressed++
	}
}

func guidanceTag(text string) string {
	line := text
	if i := strings.IndexByte(line, '\n'); i >= 0 {
		line = line[:i]
	}
	line = strings.TrimSpace(line)
	line = strings.TrimLeft(line, "#=-* \t")
	r := []rune(line)
	if len(r) > guidanceTagMaxRunes {
		r = r[:guidanceTagMaxRunes]
	}
	return string(r)
}

func (a *Agent) recordGuidanceHint(tag, text, ts string) {
	if tag == "" || text == "" {
		return
	}
	if a.guidanceHints == nil {
		a.guidanceHints = make(map[string]guidanceHintRec)
	}
	a.guidanceHints[tag] = guidanceHintRec{TS: ts, Tag: tag, Text: text}
}

func (a *Agent) writeGuidanceHints(workingDir string) {
	if len(a.guidanceHints) == 0 || workingDir == "" {
		return
	}
	path := filepath.Join(workingDir, filepath.FromSlash(guidanceHintsPath))
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		debug.Log("guidance-hints", "mkdir failed: %v", err)
		return
	}
	var b []byte
	for _, rec := range a.guidanceHints {
		line, err := json.Marshal(rec)
		if err != nil {
			continue
		}
		b = append(b, line...)
		b = append(b, '\n')
	}
	if err := os.WriteFile(path, b, 0644); err != nil {
		debug.Log("guidance-hints", "write failed: %v", err)
	}
}

func (a *Agent) flushGuidanceStats() {
	if len(a.guidanceStats) == 0 {
		return
	}
	workingDir := a.WorkingDir()
	if workingDir == "" {
		return
	}
	path := filepath.Join(workingDir, filepath.FromSlash(guidanceStatsPath))
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		debug.Log("guidance-stats", "mkdir failed: %v", err)
		return
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		debug.Log("guidance-stats", "open failed: %v", err)
		return
	}
	defer f.Close()
	ts := time.Now().UTC().Format(time.RFC3339)
	model := ""
	if m, ok := a.provider.(interface{ ModelName() string }); ok {
		model = m.ModelName()
	}
	for tag, st := range a.guidanceStats {
		rec := struct {
			TS         string `json:"ts"`
			Model      string `json:"model,omitempty"`
			Tag        string `json:"tag"`
			Delivered  int    `json:"delivered"`
			Suppressed int    `json:"suppressed"`
		}{ts, model, tag, st.Delivered, st.Suppressed}
		b, err := json.Marshal(rec)
		if err != nil {
			continue
		}
		f.Write(append(b, '\n'))
	}
	debug.Log("guidance-stats", "flushed %d tag(s) to %s", len(a.guidanceStats), path)

	a.writeGuidanceHints(workingDir)

	analyzeStaleGuidance(path, model)

	if err := MaybeTuneHarness(staleHeuristicSummary(path, model), path); err != nil {
		debug.Log("guidance-stats", "harness tuning skipped: %v", err)
	}
}
