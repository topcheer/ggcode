package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// scanResult holds the parsed data from one session JSONL file.
type scanResult struct {
	meta     sessionMeta
	turns    map[int]*turnAgg // turn_index → aggregated data
	tools    map[string]*toolAgg
	msgCount int
	hasMeta  bool
}

type turnAgg struct {
	Index     int
	Model     string
	Input     int
	Output    int
	Cache     int
	TTFTMs    int64
	DurMs     int64
	ThinkMs   int64
	Timestamp time.Time
}

type toolAgg struct {
	Name     string
	Calls    int
	Failures int
	TotalMs  int64
}

func newScanResult() *scanResult {
	return &scanResult{
		turns: make(map[int]*turnAgg),
		tools: make(map[string]*toolAgg),
	}
}

func (s *scanResult) getTurn(idx int) *turnAgg {
	t, ok := s.turns[idx]
	if !ok {
		t = &turnAgg{Index: idx}
		s.turns[idx] = t
	}
	return t
}

// scanSessionFile reads a single JSONL file and extracts report data.
func scanSessionFile(path string) (*scanResult, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	sr := newScanResult()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 256*1024), 4*1024*1024) // up to 4MB per line

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if len(line) == 0 || line[0] != '{' {
			continue
		}

		// Fast-path: extract the "type" field without full JSON unmarshal.
		// Most lines in a session file are "reasoning", "tool_result",
		// "tool_use", "text", "tunnel_event" etc. which we don't need.
		// Only "meta", "usage", "metric", "message" are processed.
		// This avoids parsing gigabytes of irrelevant message content.
		recType := fastType(line)
		if recType != "meta" && recType != "usage" && recType != "metric" && recType != "message" {
			continue
		}

		var rec jsonlRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			continue // skip malformed lines
		}

		// For "message" type, we only need the count — skip full unmarshal
		// of the message body.
		if recType == "message" {
			sr.msgCount++
			continue
		}

		switch rec.Type {
		case "meta":
			// Meta fields are directly on the record, not nested.
			// Use SessionID as fallback if meta has no explicit ID.
			sr.meta.Title = rec.Title
			if rec.Workspace != "" {
				sr.meta.Workspace = rec.Workspace
			}
			sr.meta.Vendor = rec.Vendor
			sr.meta.Endpoint = rec.Endpoint
			sr.meta.Model = rec.Model
			if !rec.CreatedAt.IsZero() {
				sr.meta.CreatedAt = rec.CreatedAt
			}
			if !rec.UpdatedAt.IsZero() {
				sr.meta.UpdatedAt = rec.UpdatedAt
			}
			if sr.meta.ID == "" && rec.SessionID != "" {
				sr.meta.ID = rec.SessionID
			}
			sr.hasMeta = true
		case "usage":
			if len(rec.UsageEntry) > 0 {
				var u usageEntry
				if err := json.Unmarshal(rec.UsageEntry, &u); err == nil {
					t := sr.getTurn(u.TurnIndex)
					t.Input += u.Usage.InputTokens
					t.Output += u.Usage.OutputTokens
					t.Cache += u.Usage.CacheRead
					if !u.Timestamp.IsZero() {
						t.Timestamp = u.Timestamp
					}
					if u.Model != "" && t.Model == "" {
						t.Model = u.Model
					}
				}
			}
		case "metric":
			if len(rec.MetricEvent) > 0 {
				var me metricEvent
				if err := json.Unmarshal(rec.MetricEvent, &me); err == nil {
					switch me.Type {
					case "llm":
						t := sr.getTurn(me.TurnIndex)
						t.TTFTMs = me.TTFT.Milliseconds()
						t.DurMs = me.Duration.Milliseconds()
						t.ThinkMs = me.ThinkTime.Milliseconds()
						// Fallback: fill token data from metric if usage record missing
						if t.Input == 0 && me.InputTokens > 0 {
							t.Input = me.InputTokens
						}
						if t.Output == 0 && me.OutputTokens > 0 {
							t.Output = me.OutputTokens
						}
					case "tool":
						name := me.ToolName
						if name == "" {
							name = "(unknown)"
						}
						ta := sr.tools[name]
						if ta == nil {
							ta = &toolAgg{Name: name}
							sr.tools[name] = ta
						}
						ta.Calls++
						if !me.ToolSuccess {
							ta.Failures++
						}
						ta.TotalMs += me.ToolDuration.Milliseconds()
					}
				}
			}
		}
	}

	return sr, scanner.Err()
}

// scanAllSessions scans the sessions directory and returns aggregated results.
// Files are scanned in parallel — each file's I/O and JSON parsing is independent.
func scanAllSessions(sessionsDir string) ([]*scanResult, error) {
	entries, err := os.ReadDir(sessionsDir)
	if err != nil {
		return nil, fmt.Errorf("read sessions dir: %w", err)
	}

	var paths []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}
		paths = append(paths, filepath.Join(sessionsDir, entry.Name()))
	}

	type fileResult struct {
		idx int
		sr  *scanResult
	}

	results := make([]*scanResult, len(paths))
	ch := make(chan fileResult, len(paths))

	for i, path := range paths {
		go func(idx int, p string) {
			sr, err := scanSessionFile(p)
			if err != nil || sr == nil {
				ch <- fileResult{idx: idx, sr: nil}
				return
			}
			if sr.meta.ID == "" {
				sr.meta.ID = strings.TrimSuffix(filepath.Base(p), ".jsonl")
			}
			if sr.meta.CreatedAt.IsZero() {
				if info, err := os.Stat(p); err == nil {
					sr.meta.CreatedAt = info.ModTime()
				}
			}
			ch <- fileResult{idx: idx, sr: sr}
		}(i, path)
	}

	for range paths {
		fr := <-ch
		if fr.sr != nil && (fr.sr.hasMeta || len(fr.sr.turns) > 0) {
			results[fr.idx] = fr.sr
		}
	}

	var valid []*scanResult
	for _, sr := range results {
		if sr != nil {
			valid = append(valid, sr)
		}
	}

	// Sort by created_at descending (newest first)
	sort.Slice(valid, func(i, j int) bool {
		return valid[i].meta.CreatedAt.After(valid[j].meta.CreatedAt)
	})

	return valid, nil
}

// findSessionsDir locates the sessions directory.
func findSessionsDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".ggcode", "sessions")
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return "", fmt.Errorf("sessions directory not found: %s", dir)
	}
	return dir, nil
}

// oldestTime returns the earliest timestamp for the "Generated at" display.
func oldestTime(results []*scanResult) time.Time {
	var oldest time.Time
	for _, sr := range results {
		t := sr.meta.CreatedAt
		if !t.IsZero() && (oldest.IsZero() || t.Before(oldest)) {
			oldest = t
		}
	}
	return oldest
}

// fastType extracts the JSON "type" field value from a line without a full
// json.Unmarshal. It searches for `"type":"..."` in the first 100 characters
// of the line (the field is always near the start of session JSONL records).
// Returns "" if not found. This avoids parsing multi-MB message content lines.
func fastType(line string) string {
	maxScan := len(line)
	if maxScan > 100 {
		maxScan = 100
	}
	i := 0
	for i < maxScan-6 {
		if line[i] == '"' && (line[i+1:i+5] == "type" || line[i+1:i+5] == "Type") && line[i+5] == '"' {
			j := i + 6
			for j < maxScan && (line[j] == ' ' || line[j] == ':') {
				j++
			}
			if j < maxScan && line[j] == '"' {
				j++
				start := j
				for j < maxScan && line[j] != '"' {
					j++
				}
				return line[start:j]
			}
		}
		i++
	}
	return ""
}
