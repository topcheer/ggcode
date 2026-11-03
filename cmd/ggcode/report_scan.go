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

	"github.com/topcheer/ggcode/internal/safego"
)

type scanResult struct {
	meta     sessionMeta
	turns    map[int]*turnAgg
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
	LLMCalls  int
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

func scanSessionFile(path string) (*scanResult, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	sr := newScanResult()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 256*1024), 4*1024*1024)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if len(line) == 0 || line[0] != '{' {
			continue
		}

		recType := fastType(line)
		if recType != "meta" && recType != "usage" && recType != "metric" && recType != "message" {
			continue
		}

		var rec jsonlRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			continue
		}

		if recType == "message" {
			sr.msgCount++
			continue
		}

		switch rec.Type {
		case "meta":

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

						if t.LLMCalls == 0 {
							t.TTFTMs = me.TTFT.Milliseconds()
						}
						t.LLMCalls++
						t.DurMs += me.Duration.Milliseconds()
						t.ThinkMs += me.ThinkTime.Milliseconds()

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
		go safego.Run("cmd.reportScan.file", func() {

			var sr *scanResult
			defer func() {
				if sr != nil && (sr.hasMeta || len(sr.turns) > 0) {
					ch <- fileResult{idx: i, sr: sr}
				} else {
					ch <- fileResult{idx: i, sr: nil}
				}
			}()
			var err error
			sr, err = scanSessionFile(path)

			if err != nil && sr != nil {
				fmt.Fprintf(os.Stderr, "report: partial session scan %s: %v\n", path, err)
			}
			if sr == nil {
				return
			}
			if sr.meta.ID == "" {
				sr.meta.ID = strings.TrimSuffix(filepath.Base(path), ".jsonl")
			}
			if sr.meta.CreatedAt.IsZero() {
				if info, err := os.Stat(path); err == nil {
					sr.meta.CreatedAt = info.ModTime()
				}
			}

		})
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

	sort.Slice(valid, func(i, j int) bool {
		return valid[i].meta.CreatedAt.After(valid[j].meta.CreatedAt)
	})

	return valid, nil
}

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
