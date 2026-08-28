package knight

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/util"
)

const (
	maxSkillScenarioEntries = 200
	maxSkillScenarioTaskLen = 2000
	maxSkillScenarioErrLen  = 500
	// #1270: compaction trigger. ~2.5KB/entry x 200 kept entries ~= 500KB
	// steady state; rotating at 1MB gives ~200 appends of headroom between
	// compactions.
	skillScenarioRotateBytes = 1 << 20
)

type SkillScenarioLogEntry struct {
	Time      time.Time `json:"time"`
	Task      string    `json:"task"`
	SkillRefs []string  `json:"skill_refs"`
	Success   bool      `json:"success"`
	Error     string    `json:"error,omitempty"`
}

func (k *Knight) RecordPromptSkillScenario(refs []string, content []provider.ContentBlock, success bool, runErr error) error {
	if k == nil {
		return nil
	}
	task := summarizeScenarioContent(content)
	if task == "" {
		return nil
	}
	refs = normalizeScenarioRefs(refs)
	if len(refs) == 0 {
		return nil
	}
	entry := SkillScenarioLogEntry{
		Time:      time.Now(),
		Task:      truncateSanitized(task, maxSkillScenarioTaskLen),
		SkillRefs: refs,
		Success:   success,
	}
	if runErr != nil {
		entry.Error = truncateSanitized(runErr.Error(), maxSkillScenarioErrLen)
	}
	return k.appendSkillScenario(entry)
}

func (k *Knight) RecentSkillScenarios(limit int) ([]SkillScenarioLogEntry, error) {
	if k == nil {
		return nil, nil
	}
	entries, err := readSkillScenarios(k.skillScenarioLogPath())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	for i, j := 0, len(entries)-1; i < j; i, j = i+1, j-1 {
		entries[i], entries[j] = entries[j], entries[i]
	}
	if limit > 0 && len(entries) > limit {
		entries = entries[:limit]
	}
	return entries, nil
}

func (k *Knight) appendSkillScenario(entry SkillScenarioLogEntry) error {
	path := k.skillScenarioLogPath()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	line, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	// Append a single line instead of read-modify-write rewriting the whole
	// file: concurrent writers (e.g. TUI + daemon finishing the same project)
	// previously overwrote each other's entries. The size cap is enforced on
	// read by readSkillScenarios. O_APPEND is supported on all platforms Go
	// supports, including Windows.
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	// #1270: the size cap used to live only on the read path, so the jsonl
	// grew without bound on disk (the in-memory view was trimmed but the
	// file never shrank, and every read scanned the whole file). Rotate on
	// the write side: past 1MB, rewrite the file to the most recent
	// maxSkillScenarioEntries. A concurrent appender racing the rewrite can
	// lose its line (appends land on the replaced inode) - acceptable for an
	// advisory eval-context log, bounded to a couple of entries per rotation.
	if info, err := os.Stat(path); err == nil && info.Size() > skillScenarioRotateBytes {
		if err := k.compactSkillScenarioLog(path); err != nil {
			debug.Log("knight", "scenario log compaction failed: %v", err)
		}
	}
	return nil
}

// compactSkillScenarioLog rewrites the scenario log to its newest
// maxSkillScenarioEntries lines (#1270). readSkillScenarios applies the same
// cap on its returned view, so the rewrite trims to the newest window.
func (k *Knight) compactSkillScenarioLog(path string) error {
	// readSkillScenarios returns chronological (append) order already
	// trimmed to the newest maxSkillScenarioEntries window - exactly the
	// bytes the compacted file should hold.
	entries, err := readSkillScenarios(path)
	if err != nil {
		return err
	}
	var buf bytes.Buffer
	for _, e := range entries {
		line, err := json.Marshal(e)
		if err != nil {
			continue // malformed entry: drop instead of failing rotation
		}
		buf.Write(line)
		buf.WriteByte('\n')
	}
	return util.AtomicWriteFile(path, buf.Bytes(), 0600)
}

func (k *Knight) formatRecentSkillScenariosForEval(limit int) string {
	scenarios, err := k.RecentSkillScenarios(limit)
	if err != nil || len(scenarios) == 0 {
		return ""
	}
	lines := make([]string, 0, len(scenarios))
	for _, scenario := range scenarios {
		outcome := "success"
		if !scenario.Success {
			outcome = "failure"
		}
		task := truncateRunes(scenario.Task, 240)
		if scenario.Error != "" {
			lines = append(lines, fmt.Sprintf("- [%s] %s (refs: %s, error: %s)", outcome, task, strings.Join(scenario.SkillRefs, ", "), truncateRunes(scenario.Error, 120)))
			continue
		}
		lines = append(lines, fmt.Sprintf("- [%s] %s (refs: %s)", outcome, task, strings.Join(scenario.SkillRefs, ", ")))
	}
	return strings.Join(lines, "\n")
}

func (k *Knight) formatActiveSkillBaselinesForEval(candidate *SkillEntry, limit int) string {
	if k == nil || k.index == nil {
		return ""
	}
	active, err := k.index.ActiveSkills()
	if err != nil || len(active) == 0 {
		return ""
	}
	lines := make([]string, 0, len(active))
	for _, entry := range active {
		if entry == nil || entry.Staging {
			continue
		}
		if candidate != nil && entry.Scope == candidate.Scope && entry.Name == candidate.Name {
			continue
		}
		content := ""
		if data, err := readSkillContent(entry.Path); err == nil {
			content = truncateRunes(string(data), 500)
		}
		desc := strings.TrimSpace(entry.Meta.Description)
		if desc == "" {
			desc = "(no description)"
		}
		if content != "" {
			lines = append(lines, fmt.Sprintf("- %s:%s — %s\n  excerpt: %s", entry.Scope, entry.Name, desc, strings.ReplaceAll(content, "\n", " ")))
		} else {
			lines = append(lines, fmt.Sprintf("- %s:%s — %s", entry.Scope, entry.Name, desc))
		}
		if limit > 0 && len(lines) >= limit {
			break
		}
	}
	return strings.Join(lines, "\n")
}

func (k *Knight) skillScenarioLogPath() string {
	return filepath.Join(k.projDir, ".ggcode", "skill-scenarios.jsonl")
}

// formatRecentSemanticMemoryForEval renders recent semantic memory entries for
// inclusion in Knight evaluator prompts so past lessons influence new gating
// decisions. Returns "" when no memory exists.
func (k *Knight) formatRecentSemanticMemoryForEval(limit int) string {
	entries, err := k.RecentSemanticMemory(limit)
	if err != nil || len(entries) == 0 {
		return ""
	}
	lines := make([]string, 0, len(entries))
	for _, e := range entries {
		summary := truncateRunes(e.Summary, 220)
		when := ""
		if !e.Time.IsZero() {
			when = e.Time.Format("2006-01-02") + " "
		}
		lines = append(lines, fmt.Sprintf("- %s[%s] %s", when, e.Kind, summary))
	}
	return strings.Join(lines, "\n")
}

func readSkillScenarios(path string) ([]SkillScenarioLogEntry, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, os.ErrNotExist
		}
		return nil, err
	}
	defer f.Close()

	var entries []SkillScenarioLogEntry
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(strings.TrimSpace(string(line))) == 0 {
			continue
		}
		var entry SkillScenarioLogEntry
		if err := json.Unmarshal(line, &entry); err != nil {
			continue
		}
		entries = append(entries, entry)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	// The append path no longer rewrites the file to enforce the ring cap,
	// so keep only the most recent entries when reading.
	if len(entries) > maxSkillScenarioEntries {
		entries = entries[len(entries)-maxSkillScenarioEntries:]
	}
	return entries, nil
}

func normalizeScenarioRefs(refs []string) []string {
	seen := make(map[string]struct{}, len(refs))
	out := make([]string, 0, len(refs))
	for _, ref := range refs {
		ref = strings.TrimSpace(ref)
		if ref == "" {
			continue
		}
		if _, ok := seen[ref]; ok {
			continue
		}
		seen[ref] = struct{}{}
		out = append(out, ref)
	}
	return out
}

func summarizeScenarioContent(content []provider.ContentBlock) string {
	parts := make([]string, 0, len(content))
	for _, block := range content {
		switch block.Type {
		case "text":
			if text := strings.TrimSpace(block.Text); text != "" {
				parts = append(parts, text)
			}
		case "image":
			mime := strings.TrimSpace(block.ImageMIME)
			if mime == "" {
				mime = "image"
			}
			parts = append(parts, "[image:"+mime+"]")
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

func truncateRunes(s string, max int) string {
	if max <= 0 {
		return ""
	}
	runes := []rune(strings.TrimSpace(s))
	if len(runes) <= max {
		return string(runes)
	}
	return string(runes[:max]) + "..."
}
