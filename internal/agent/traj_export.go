package agent

// sa-152: trajectory-to-training-data export (RLVR / SFT sample outlet).
//
// Research basis: agentic RL pipelines (RLVR, AgentGym, SWE-agent RL variants,
// 2026 self-evolving agent surveys) need (trajectory, reward) pairs as
// fine-tuning data. ggcode's self-evolution loops — traj_intel insight
// extraction, policy_distill prompt rewriting — all operate at the runtime
// PROMPT layer and never leave the machine as reusable training samples.
// This file adds the missing data outlet: post-run, opt-in, it serializes the
// full redacted message trajectory plus a deterministic reward computed from
// RunStats into an append-only JSONL store.
//
// Privacy is the defining constraint (trajectories contain user code):
// the export is OFF by default and only activates when the user explicitly
// sets GGCODE_TRAINING_EXPORT=1. All text passes the same secret-pattern
// masking used for tool outputs (secret_redact.go) — here applied
// unconditionally to every text field, not just external-content tools.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/provider"
)

const (
	// trainExportEnv gates the entire feature. Opt-in only: trajectories
	// contain user source code and must never be persisted as training
	// data without explicit consent.
	trainExportEnv = "GGCODE_TRAINING_EXPORT"

	// trainExportFile is the JSONL store under .ggcode/.
	trainExportFile = "training-samples.jsonl"

	// trainExportMaxEntries bounds the JSONL store (rolling, keep newest).
	trainExportMaxEntries = 200

	// trainExportMinToolCalls is the minimum trajectory substance (total
	// tool invocations) before a run is worth exporting. Below this the
	// run is trivial chatter, not agentic experience.
	trainExportMinToolCalls = 3

	// trainExportMaxMessages caps the per-sample message tail. Long runs
	// after several compactions carry little extra training signal.
	trainExportMaxMessages = 400

	// trainSampleSchemaVer tags the serialization format so external
	// consumers can evolve independently.
	trainSampleSchemaVer = 1
)

// TrainMetrics captures the run-shape signals the reward is computed from.
// Kept in the sample so downstream consumers can re-weight or re-label
// without re-deriving them.
type TrainMetrics struct {
	Iterations        int     `json:"iterations"`
	ToolCalls         int     `json:"tool_calls"`
	DistinctTools     int     `json:"distinct_tools"`
	ErrorCount        int     `json:"error_count"`
	CompactionCount   int     `json:"compaction_count"`
	TotalTokens       int     `json:"total_tokens"`
	ContextPeakTokens int     `json:"context_peak_tokens"`
	DurationSec       float64 `json:"duration_sec"`
}

// TrainSample is one (trajectory, reward) pair in RLVR/SFT shape.
type TrainSample struct {
	SchemaVer  int                `json:"schema_ver"`
	RunID      string             `json:"run_id"`
	Task       string             `json:"task"`
	Messages   []provider.Message `json:"messages"`
	Reward     float64            `json:"reward"`
	Outcome    string             `json:"outcome"` // "success" | "failure"
	Metrics    TrainMetrics       `json:"metrics"`
	ExportedAt time.Time          `json:"exported_at"`
}

// computeTrainReward derives a deterministic 0..1 scalar from run shape.
// Weighting (documented so re-labeling is possible):
//
//	0.5 outcome       — the dominant signal: did the run complete cleanly
//	0.2 error health  — 1 - min(1, ErrorCount/10)
//	0.2 tool diversity — distinct tools used over the 3..8 call band
//	0.1 context health — 1 - min(1, CompactionCount/5)
//
// Pure function of RunStats; no LLM, no I/O.
func computeTrainReward(s *RunStats) float64 {
	if s == nil {
		return 0
	}
	success := 0.0
	if s.Success {
		success = 1
	}
	errorHealth := 1 - float64(s.ErrorCount)/10
	if errorHealth < 0 {
		errorHealth = 0
	}
	total := 0
	for _, n := range s.ToolCalls {
		total += n
	}
	band := float64(total)
	if band < 3 {
		band = 3
	}
	if band > 8 {
		band = 8
	}
	diversity := float64(len(s.ToolCalls)) / band
	if diversity > 1 {
		diversity = 1
	}
	contextHealth := 1 - float64(s.CompactionCount)/5
	if contextHealth < 0 {
		contextHealth = 0
	}
	return 0.5*success + 0.2*errorHealth + 0.2*diversity + 0.1*contextHealth
}

// trainMetricsFromRunStats snapshots the reward inputs.
func trainMetricsFromRunStats(s *RunStats) TrainMetrics {
	if s == nil {
		return TrainMetrics{}
	}
	total := 0
	for _, n := range s.ToolCalls {
		total += n
	}
	return TrainMetrics{
		Iterations:        s.Iterations,
		ToolCalls:         total,
		DistinctTools:     len(s.ToolCalls),
		ErrorCount:        s.ErrorCount,
		CompactionCount:   s.CompactionCount,
		TotalTokens:       s.TotalTokens,
		ContextPeakTokens: s.ContextPeakTokens,
		DurationSec:       s.Duration.Seconds(),
	}
}

// redactTrainText masks secret VALUES using the same pattern set as
// tool-output redaction (secret_redact.go), but unconditionally: training
// samples mix user prompts, assistant prose, and tool I/O, so the
// external-content-tool gate does not apply. Multi-group patterns mask
// only the value group, mirroring redactSecrets' #1682 splice logic.
func redactTrainText(s string) string {
	if len(s) < 10 {
		return s
	}
	scan := s
	if len(scan) > maxRedactScanLen {
		scan = scan[:maxRedactScanLen]
	}
	changed := false
	for _, sp := range secretPatterns {
		groups := sp.pattern.NumSubexp()
		if groups >= 2 {
			var sb strings.Builder
			last := 0
			locs := sp.pattern.FindAllStringSubmatchIndex(scan, -1)
			for _, loc := range locs {
				if len(loc) < 6 || loc[4] < 0 || loc[5] < 0 {
					continue
				}
				changed = true
				sb.WriteString(scan[last:loc[4]])
				sb.WriteString("[REDACTED:" + sp.name + "]")
				last = loc[5]
			}
			if changed || locs != nil {
				sb.WriteString(scan[last:])
				scan = sb.String()
			}
		} else {
			replaced := sp.pattern.ReplaceAllStringFunc(scan, func(match string) string {
				changed = true
				return "[REDACTED:" + sp.name + "]"
			})
			scan = replaced
		}
	}
	if !changed {
		return s
	}
	// Keep any tail beyond the scan window verbatim (see #1195 note in
	// secret_redact.go): dropping it silently would corrupt the sample.
	if len(s) > maxRedactScanLen {
		return scan + s[maxRedactScanLen:]
	}
	return scan
}

// redactTrainMessages masks every text-bearing field of the trajectory:
// text blocks, tool_use inputs, and tool_result outputs. Image blocks are
// dropped entirely — base64 screenshots bloat samples without training
// value for the text-first pipelines this outlet targets.
func redactTrainMessages(msgs []provider.Message) []provider.Message {
	out := make([]provider.Message, 0, len(msgs))
	for _, m := range msgs {
		blocks := make([]provider.ContentBlock, 0, len(m.Content))
		for _, b := range m.Content {
			switch b.Type {
			case "image":
				continue
			case "text":
				b.Text = redactTrainText(b.Text)
			case "tool_use":
				if len(b.Input) > 0 {
					b.Input = json.RawMessage(redactTrainText(string(b.Input)))
				}
			case "tool_result":
				b.Output = redactTrainText(b.Output)
			}
			blocks = append(blocks, b)
		}
		m.Content = blocks
		out = append(out, m)
	}
	return out
}

// trainingExportEnabled reports the opt-in gate. Read at call time so the
// user can toggle without restarting the session.
func trainingExportEnabled() bool {
	return os.Getenv(trainExportEnv) == "1"
}

// maybeExportTrainingSample is the Agent-side entry point, called from the
// post-run block next to traj-intel persistence. Fail-open: any error logs
// at debug level and drops the sample — export must never break a run.
func (a *Agent) maybeExportTrainingSample(workingDir string, stats *RunStats) {
	if !trainingExportEnabled() || workingDir == "" || stats == nil || a.contextManager == nil {
		return
	}
	// Cancelled runs were excluded by the caller (post-run block only runs
	// this on the non-cancelled path). Substance gate: trivial runs are
	// chatter, not agentic experience.
	total := 0
	for _, n := range stats.ToolCalls {
		total += n
	}
	if total < trainExportMinToolCalls {
		return
	}

	msgs, _ := a.contextManager.MessagesAndTokenCount()
	if len(msgs) == 0 {
		return
	}
	if len(msgs) > trainExportMaxMessages {
		msgs = msgs[len(msgs)-trainExportMaxMessages:]
	}

	outcome := "failure"
	if stats.Success {
		outcome = "success"
	}
	sample := TrainSample{
		SchemaVer:  trainSampleSchemaVer,
		RunID:      stats.runID,
		Task:       stats.UserPrompt,
		Messages:   redactTrainMessages(msgs),
		Reward:     computeTrainReward(stats),
		Outcome:    outcome,
		Metrics:    trainMetricsFromRunStats(stats),
		ExportedAt: time.Now().UTC(),
	}

	ggDir := filepath.Join(workingDir, ".ggcode")
	if err := os.MkdirAll(ggDir, 0o755); err != nil {
		debug.Log("traj-export", "mkdir failed: %v", err)
		return
	}
	if err := appendTrainingSample(filepath.Join(ggDir, trainExportFile), sample); err != nil {
		debug.Log("traj-export", "append failed: %v", err)
		return
	}
	debug.Log("traj-export", "exported run %s: outcome=%s reward=%.2f msgs=%d", sample.RunID, outcome, sample.Reward, len(sample.Messages))
}

// ExportSessionTrainingSample is the manual /export-training entry point:
// serializes an arbitrary message slice (typically the current TUI session)
// as one redacted sample. Reward is 0 and Outcome "manual" — the automatic
// post-run path is the only reward-bearing source; manual exports exist for
// SFT-style corpus collection where the user curates labels. Returns the
// number of (redacted) messages written.
func ExportSessionTrainingSample(workingDir, task string, msgs []provider.Message) (int, error) {
	if workingDir == "" {
		return 0, os.ErrInvalid
	}
	if len(msgs) == 0 {
		return 0, nil
	}
	if len(msgs) > trainExportMaxMessages {
		msgs = msgs[len(msgs)-trainExportMaxMessages:]
	}
	if task == "" {
		task = "(manual export)"
	}
	sample := TrainSample{
		SchemaVer:  trainSampleSchemaVer,
		RunID:      "manual-" + time.Now().UTC().Format("20060102T150405"),
		Task:       task,
		Messages:   redactTrainMessages(msgs),
		Reward:     0,
		Outcome:    "manual",
		ExportedAt: time.Now().UTC(),
	}
	ggDir := filepath.Join(workingDir, ".ggcode")
	if err := os.MkdirAll(ggDir, 0o755); err != nil {
		return 0, err
	}
	if err := appendTrainingSample(filepath.Join(ggDir, trainExportFile), sample); err != nil {
		return 0, err
	}
	return len(sample.Messages), nil
}

// appendTrainingSample appends one JSONL line and enforces the rolling cap
// by rewriting the file with the newest trainExportMaxEntries entries.
func appendTrainingSample(path string, sample TrainSample) error {
	line, err := json.Marshal(sample)
	if err != nil {
		return err
	}
	existing, readErr := os.ReadFile(path)
	newLines := []string{}
	if readErr == nil {
		for _, l := range strings.Split(string(existing), "\n") {
			if strings.TrimSpace(l) != "" {
				newLines = append(newLines, l)
			}
		}
	}
	newLines = append(newLines, string(line))
	if len(newLines) > trainExportMaxEntries {
		newLines = newLines[len(newLines)-trainExportMaxEntries:]
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(strings.Join(newLines, "\n")+"\n"), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
