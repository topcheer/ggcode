package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
)

const (
	trajHoldoutMinRuns       = 5
	trajHoldoutReleaseDelta  = 0.10
	trajHoldoutMinInjected   = 3
	trajHoldoutDecayFloor    = 0.05
	trajHoldoutReleaseWindow = 7 * 24 * time.Hour
)

var trajHoldoutDay = func() int64 { return time.Now().UTC().Unix() / 86400 }

var trajHoldoutEnabled = true

type trajHoldoutEntry struct {
	InsightKey       string    `json:"insight_key"`
	Category         string    `json:"category"`
	Type             string    `json:"type"`
	HeldSince        time.Time `json:"held_since"`
	HoldoutRuns      int       `json:"holdout_runs"`
	HoldoutSuccesses int       `json:"holdout_successes"`
	ReleasedUntil    time.Time `json:"released_until,omitempty"`
}

func trajHoldoutPath(workingDir string) string {
	return filepath.Join(workingDir, ".ggcode", "trajectory-holdout.jsonl")
}

func trajHoldoutKeyString(k trajKey) string { return k.cat + "\x00" + k.typ }

func loadHoldoutLedger(path string) ([]trajHoldoutEntry, error) {
	entries, err := loadRawHoldout(path)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	return entries, nil
}

func trajHoldoutSelect(workingDir string, entries []trajectoryLearning) map[trajKey]bool {
	if !trajHoldoutEnabled || workingDir == "" || len(entries) == 0 {
		return nil
	}

	byCat := map[string][]trajKey{}
	injectedOnce := map[trajKey]bool{}
	for _, l := range entries {
		if l.EffectiveConfidence() < trajPromptMinConfidence || effectivenessGated(l) {
			continue
		}
		k := trajKeyOf(l)
		if l.InjectedRuns > 0 {
			injectedOnce[k] = true
		}
		if _, seen := containsKey(byCat[k.cat], k); !seen {
			byCat[k.cat] = append(byCat[k.cat], k)
		}
	}
	if len(byCat) == 0 {
		return nil
	}
	day := trajHoldoutDay()
	held := map[trajKey]bool{}

	path := trajHoldoutPath(workingDir)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil
	}
	unlock, lockErr := lockTrajFile(path + ".lock")
	if lockErr != nil {
		return nil
	}
	defer unlock()

	ledger, _ := loadHoldoutLedger(path)

	byCatLedger := map[string]map[string]trajHoldoutEntry{}
	for _, e := range ledger {
		if byCatLedger[e.Category] == nil {
			byCatLedger[e.Category] = map[string]trajHoldoutEntry{}
		}
		byCatLedger[e.Category][e.InsightKey] = e
	}
	now := time.Now().UTC()
	var out []trajHoldoutEntry
	for _, cat := range sortedKeys(byCat) {
		keys := byCat[cat]
		var eligible []trajKey
		for _, k := range keys {
			if injectedOnce[k] {
				eligible = append(eligible, k)
			}
		}

		if len(eligible) <= 1 {
			continue
		}
		keys = eligible
		sort.Slice(keys, func(i, j int) bool { return trajHoldoutKeyString(keys[i]) < trajHoldoutKeyString(keys[j]) })
		target := keys[day%int64(len(keys))]
		tks := trajHoldoutKeyString(target)
		prev, ok := byCatLedger[cat][tks]

		if ok && prev.ReleasedUntil.After(now) {
			out = append(out, prev)
			continue
		}
		held[target] = true
		if ok {
			prev.HeldSince = now
			prev.ReleasedUntil = time.Time{}
			out = append(out, prev)
		} else {
			out = append(out, trajHoldoutEntry{
				InsightKey: tks,
				Category:   cat, Type: target.typ,
				HeldSince: now,
			})
		}

		for k, e := range byCatLedger[cat] {
			if k != tks {
				out = append(out, e)
			}
		}
	}

	outSeen := map[string]bool{}
	for _, e := range out {
		outSeen[e.Category+"\x00"+e.InsightKey] = true
	}
	for cat, rows := range byCatLedger {
		for k, e := range rows {
			if !outSeen[cat+"\x00"+k] {
				out = append(out, e)
			}
		}
	}
	if err := writeHoldoutLedger(path, out); err != nil {
		return nil
	}
	return held
}

func (s *trajIntelState) recordHoldoutLocked(l trajectoryLearning) {
	if s.holdoutThisRun == nil {
		s.holdoutThisRun = make(map[trajKey]bool)
	}
	s.holdoutThisRun[trajKeyOf(l)] = true
}

func (s *trajIntelState) recordHoldoutOutcome(workingDir string, success bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.holdoutThisRun) == 0 {
		return
	}
	keys := s.holdoutThisRun
	s.holdoutThisRun = nil
	if workingDir == "" {
		return
	}
	if s.filePath == "" {
		s.filePath = filepath.Join(workingDir, ".ggcode", "trajectory-learnings.jsonl")
	}
	hp := trajHoldoutPath(workingDir)
	if err := os.MkdirAll(filepath.Dir(hp), 0o755); err != nil {
		return
	}
	unlock, lockErr := lockTrajFile(hp + ".lock")
	if lockErr != nil {
		return
	}
	defer unlock()

	ledger, _ := loadHoldoutLedger(hp)
	var learnings []trajectoryLearning
	if ls, err := s.loadFromFile(); err == nil {
		learnings = ls
	}
	decayed := map[trajKey]bool{}
	var out []trajHoldoutEntry
	for _, e := range ledger {
		k := trajKey{cat: e.Category, typ: e.Type}
		if keys[k] {
			e.HoldoutRuns++
			if success {
				e.HoldoutSuccesses++
			}
		}

		if e.ReleasedUntil.After(time.Now().UTC()) {
			out = append(out, e)
			continue
		}
		if e.HoldoutRuns >= trajHoldoutMinRuns && e.HoldoutRuns > 0 {
			var inj *trajectoryLearning
			for i := range learnings {
				if trajKeyOf(learnings[i]) == k {
					inj = &learnings[i]
					break
				}
			}
			if inj != nil && inj.InjectedRuns >= trajHoldoutMinInjected {
				delta := float64(inj.AfterSuccess)/float64(inj.InjectedRuns) -
					float64(e.HoldoutSuccesses)/float64(e.HoldoutRuns)
				if delta <= 0 {
					decayed[k] = true

					e.ReleasedUntil = time.Now().UTC().Add(trajHoldoutReleaseWindow)
					out = append(out, e)
					continue
				}
				if delta >= trajHoldoutReleaseDelta {

					e.ReleasedUntil = time.Now().UTC().Add(trajHoldoutReleaseWindow)
					out = append(out, e)
					continue
				}
			}
		}
		out = append(out, e)
	}
	if err := writeHoldoutLedger(hp, out); err != nil {
		return
	}
	if len(decayed) == 0 {
		return
	}
	if err := s.rewriteAllLocked(func(existing []trajectoryLearning, loadErr error) ([]trajectoryLearning, error) {
		if loadErr != nil && !os.IsNotExist(loadErr) {
			return nil, fmt.Errorf("load: %w", loadErr)
		}
		changed := false
		for i := range existing {
			if !decayed[trajKeyOf(existing[i])] {
				continue
			}
			c := existing[i].Confidence - trajConfidenceStep
			if c < trajHoldoutDecayFloor {
				c = trajHoldoutDecayFloor
			}
			existing[i].Confidence = c
			changed = true
		}
		if !changed {
			return nil, nil
		}
		return existing, nil
	}); err != nil {
		debug.Log("traj-holdout", "verdict decay failed: %v", err)
	}
}

func trajHoldoutSnapshot(workingDir string) map[trajKey]trajHoldoutEntry {
	out := map[trajKey]trajHoldoutEntry{}
	entries, err := loadHoldoutLedger(trajHoldoutPath(workingDir))
	if err != nil {
		return out
	}
	for _, e := range entries {
		out[trajKey{cat: e.Category, typ: e.Type}] = e
	}
	return out
}

func trajHoldoutDelta(l trajectoryLearning, e trajHoldoutEntry) (delta float64, ok bool) {
	if e.HoldoutRuns <= 0 || l.InjectedRuns < trajHoldoutMinInjected {
		return 0, false
	}
	return float64(l.AfterSuccess)/float64(l.InjectedRuns) -
		float64(e.HoldoutSuccesses)/float64(e.HoldoutRuns), true
}

func loadRawHoldout(path string) ([]trajHoldoutEntry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var out []trajHoldoutEntry
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var e trajHoldoutEntry
		if json.Unmarshal([]byte(line), &e) == nil {
			out = append(out, e)
		}
	}
	return out, nil
}

func writeHoldoutLedger(path string, entries []trajHoldoutEntry) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmpF, err := os.CreateTemp(filepath.Dir(path), ".traj-holdout-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmpF.Name()
	enc := json.NewEncoder(tmpF)
	for _, e := range entries {
		if encErr := enc.Encode(e); encErr != nil {
			tmpF.Close()
			os.Remove(tmpPath)
			return encErr
		}
	}
	if closeErr := tmpF.Close(); closeErr != nil {
		os.Remove(tmpPath)
		return closeErr
	}
	return os.Rename(tmpPath, path)
}

func containsKey(keys []trajKey, k trajKey) (int, bool) {
	for i, kk := range keys {
		if kk == k {
			return i, true
		}
	}
	return 0, false
}

func sortedKeys(m map[string][]trajKey) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
