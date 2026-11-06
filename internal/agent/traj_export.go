package agent

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
	trainExportEnv = "GGCODE_TRAINING_EXPORT"

	trainExportFile = "training-samples.jsonl"

	trainExportMaxEntries = 200

	trainExportMinToolCalls = 3

	trainExportMaxMessages = 400

	trainSampleSchemaVer = 1
)

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

type TrainSample struct {
	SchemaVer  int                `json:"schema_ver"`
	RunID      string             `json:"run_id"`
	Task       string             `json:"task"`
	Messages   []provider.Message `json:"messages"`
	Reward     float64            `json:"reward"`
	Outcome    string             `json:"outcome"`
	Metrics    TrainMetrics       `json:"metrics"`
	ExportedAt time.Time          `json:"exported_at"`
}

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

	if len(s) > maxRedactScanLen {
		return scan + s[maxRedactScanLen:]
	}
	return scan
}

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

func trainingExportEnabled() bool {
	return os.Getenv(trainExportEnv) == "1"
}

func (a *Agent) maybeExportTrainingSample(workingDir string, stats *RunStats) {
	if !trainingExportEnabled() || workingDir == "" || stats == nil || a.contextManager == nil {
		return
	}

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
		Task:       redactTrainText(stats.UserPrompt),
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
		Task:       redactTrainText(task),
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
