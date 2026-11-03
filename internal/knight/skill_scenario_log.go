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

	if info, err := os.Stat(path); err == nil && info.Size() > skillScenarioRotateBytes {
		if err := k.compactSkillScenarioLog(path); err != nil {
			debug.Log("knight", "scenario log compaction failed: %v", err)
		}
	}
	return nil
}

func (k *Knight) compactSkillScenarioLog(path string) error {

	entries, err := readSkillScenarios(path)
	if err != nil {
		return err
	}
	var buf bytes.Buffer
	for _, e := range entries {
		line, err := json.Marshal(e)
		if err != nil {
			continue
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

func (k *Knight) formatRecentSemanticMemoryForEval(limit int) string {
	return k.formatSemanticMemoryForEvalIDs(limit).text
}

type semanticEvalContext struct {
	text string
	ids  []string
}

func (k *Knight) formatSemanticMemoryForEvalIDs(limit int) semanticEvalContext {
	wide := limit * 4
	if wide < limit {
		wide = limit
	}

	entries, err := k.TopSemanticMemoryByUtility(wide)
	if err != nil || len(entries) == 0 {
		return semanticEvalContext{}
	}
	lines := make([]string, 0, limit)
	ids := make([]string, 0, limit)
	selfReflectionSeen := false
	for _, e := range entries {
		if e.Kind == "self-reflection" {
			if selfReflectionSeen {
				continue
			}
			selfReflectionSeen = true
		}
		if len(lines) == limit {
			break
		}
		summary := truncateRunes(e.Summary, 220)
		when := ""
		if !e.Time.IsZero() {
			when = e.Time.Format("2006-01-02") + " "
		}
		lines = append(lines, fmt.Sprintf("- %s[%s] %s", when, e.Kind, summary))
		if e.ID != "" {
			ids = append(ids, e.ID)
		}
	}
	return semanticEvalContext{text: strings.Join(lines, "\n"), ids: ids}
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
