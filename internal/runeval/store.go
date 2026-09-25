package runeval

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/util"
)

// ReportRecord is the compact persisted form of one /runreport evaluation.
//
// A single absolute scorecard cannot answer the question frontier
// trajectory-evaluation practice cares about most: "did trajectory efficiency
// regress after the last harness/prompt/model change?" (agentic-harness
// engineering treats observability as falsifiable contracts across harness
// edits; agent eval platforms regression-test new versions). Records
// accumulate in a local JSONL store so consecutive /runreport invocations can
// show a trend line instead of an isolated snapshot.
type ReportRecord struct {
	At            int64   `json:"at"`          // unix seconds
	SessionID     string  `json:"session_id"`  // may be empty
	Score         int     `json:"score"`       // 0-100 efficiency score
	Turns         int     `json:"turns"`       // turns that issued tool calls
	ToolCalls     int     `json:"tool_calls"`  // total tool_use blocks
	ToolErrors    int     `json:"tool_errors"` // IsError results
	WastedRepeats int     `json:"wasted_repeats"`
	OverheadPct   float64 `json:"overhead_pct"` // non-agent token share, 0-100 (0 = unknown)
	WastedTokens  int     `json:"wasted_tokens"`
	TotalTokens   int     `json:"total_tokens"`
}

// RecordFromReport projects a Report into its persisted form.
func RecordFromReport(r Report, sessionID string, at time.Time) ReportRecord {
	overhead := 0.0
	if r.TotalTokens > 0 {
		overhead = 100 * float64(r.OverheadTokens) / float64(r.TotalTokens)
	}
	return ReportRecord{
		At:            at.Unix(),
		SessionID:     sessionID,
		Score:         r.EfficiencyScore,
		Turns:         r.TurnCount,
		ToolCalls:     r.ToolCalls,
		ToolErrors:    r.ToolErrors,
		WastedRepeats: r.WastedRepeatCalls,
		OverheadPct:   overhead,
		WastedTokens:  r.WastedTokenEstimate,
		TotalTokens:   r.TotalTokens,
	}
}

// DefaultStorePath returns ~/.ggcode/runreports.jsonl, or "" when the home
// directory cannot be determined.
func DefaultStorePath() string {
	dir := util.ConfigDir()
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, "runreports.jsonl")
}

var storeMu sync.Mutex

// AppendReport appends one record to the JSONL store, creating it (0600) if
// needed. Safe for concurrent use within one process.
func AppendReport(path string, rec ReportRecord) error {
	line, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	storeMu.Lock()
	defer storeMu.Unlock()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(line, '\n'))
	return err
}

// maxHistoryRecords caps how much of the store is parsed per load so a very
// long-lived installation cannot turn /runreport into a slow command.
const maxHistoryRecords = 1000

// LoadHistory returns up to limit most recent records, oldest first.
// limit <= 0 uses the capped default. A missing store is not an error.
func LoadHistory(path string, limit int) ([]ReportRecord, error) {
	if limit <= 0 || limit > maxHistoryRecords {
		limit = maxHistoryRecords
	}
	storeMu.Lock()
	defer storeMu.Unlock()
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	var recs []ReportRecord
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var rec ReportRecord
		if json.Unmarshal([]byte(line), &rec) != nil {
			continue // best-effort store: skip corrupt lines
		}
		recs = append(recs, rec)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if len(recs) > limit {
		recs = recs[len(recs)-limit:]
	}
	return recs, nil
}

// RenderDelta compares the current report against the previous recorded run
// and renders a one-line trend. Returns "" when no baseline exists yet.
func RenderDelta(cur Report, prev ReportRecord) string {
	if prev.At == 0 {
		return ""
	}
	d := cur.EfficiencyScore - prev.Score
	sign := "+"
	if d < 0 {
		sign = "-"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "vs previous run (%s, session %s): score %d → %d (%s%d)",
		shortAge(time.Now().Unix()-prev.At), shortSession(prev.SessionID),
		prev.Score, cur.EfficiencyScore, sign, abs(d))
	if cur.ToolErrors != prev.ToolErrors {
		fmt.Fprintf(&b, " · tool errors %d → %d", prev.ToolErrors, cur.ToolErrors)
	}
	if cur.WastedRepeatCalls != prev.WastedRepeats {
		fmt.Fprintf(&b, " · wasted repeats %d → %d", prev.WastedRepeats, cur.WastedRepeatCalls)
	}
	curOverhead := 0
	if cur.TotalTokens > 0 {
		curOverhead = int(100*float64(cur.OverheadTokens)/float64(cur.TotalTokens) + 0.5)
	}
	prevOverhead := int(prev.OverheadPct + 0.5)
	if curOverhead != prevOverhead && (curOverhead > 0 || prevOverhead > 0) {
		fmt.Fprintf(&b, " · overhead %d%% → %d%%", prevOverhead, curOverhead)
	}
	return b.String()
}

// RenderHistory renders the recorded run trend, newest last. Records are
// cross-session by design: the trend answers whether harness/prompt changes
// moved overall trajectory efficiency, not per-task identity.
func RenderHistory(recs []ReportRecord) string {
	if len(recs) == 0 {
		return "No recorded runs yet. Each /runreport evaluation is recorded here for trend comparison."
	}
	const shown = 10
	if len(recs) > shown {
		recs = recs[len(recs)-shown:]
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Run history (last %d recorded, newest last):", len(recs))
	for _, rec := range recs {
		fmt.Fprintf(&b, "\n  %s  score %3d  turns %4d  calls %4d  err %d  waste %d  overhead %d%%  sess %s",
			time.Unix(rec.At, 0).Format("01-02 15:04"),
			rec.Score, rec.Turns, rec.ToolCalls, rec.ToolErrors,
			rec.WastedRepeats, int(rec.OverheadPct+0.5), shortSession(rec.SessionID))
	}
	return b.String()
}

// shortAge renders a coarse "how long ago" label for delta lines.
func shortAge(seconds int64) string {
	switch {
	case seconds < 0:
		return "future"
	case seconds < 90:
		return "just now"
	case seconds < 3600:
		return fmt.Sprintf("%dm ago", seconds/60)
	case seconds < 86400:
		return fmt.Sprintf("%dh ago", seconds/3600)
	default:
		return fmt.Sprintf("%dd ago", seconds/86400)
	}
}

// shortSession truncates a session ID for display.
func shortSession(id string) string {
	if id == "" {
		return "-"
	}
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
