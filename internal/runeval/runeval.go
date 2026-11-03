package runeval

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/topcheer/ggcode/internal/provider"
)

type UsageSample struct {
	Source string
	Usage  provider.TokenUsage
}

type DuplicateGroup struct {
	Tool      string
	Input     string
	Count     int
	Repeats   int
	Identical bool
	ReadOnly  bool
}

type Report struct {
	TurnCount int

	ToolCalls  int
	ToolErrors int

	DistinctTools int

	DuplicateGroups []DuplicateGroup

	WastedRepeatCalls int

	WastedResultBytes int

	WastedTokenEstimate int

	OverheadTokens int
	TotalTokens    int

	SteeringEvents int

	AskUserCalls int

	CorrectionSignals int

	Coordination *CoordinationQualityReport

	EfficiencyScore int

	Findings []string
}

type callRecord struct {
	tool      string
	canonical string
	count     int
	hashes    map[string]bool
	readOnly  bool
}

func (c *callRecord) identical() bool { return len(c.hashes) == 1 }

type callKey struct {
	tool  string
	input string
}

const (
	bytesPerToken = 4

	displayInputCap = 96

	resultHashCap = 256

	minToolsForOverheadPenalty = 5

	steeringFindingsThreshold = 3

	correctionFindingsThreshold = 2
)

var readOnlyTools = map[string]bool{
	"read_file":       true,
	"multi_file_read": true,
	"search_files":    true,
	"grep":            true,
	"glob":            true,
	"list_directory":  true,
	"code_search":     true,
	"lsp_symbols":     true,
	"lsp_hover":       true,
	"lsp_references":  true,
	"lsp_definition":  true,
	"lsp_diagnostics": true,
	"git_status":      true,
	"git_log":         true,
	"git_diff":        true,
	"git_blame":       true,
	"task_list":       true,
	"list_worktree":   true,
	"web_search":      true,
	"knowledge_list":  true,
	"knowledge_query": true,
	"memory_list":     true,
	"memory_query":    true,
	"endpoint_stats":  true,
	"screenshot":      true,
	"snapshot_ui":     true,
	"find_element":    true,
	"list_windows":    true,
	"display_info":    true,
}

func Evaluate(msgs []provider.Message, usage []UsageSample) Report {
	var r Report

	byID := map[string]*callKey{}
	records := map[callKey]*callRecord{}
	var keyOrder []callKey

	type resultObs struct {
		key     callKey
		isError bool
		outLen  int
	}
	var results []resultObs

	for i := range msgs {
		for _, b := range msgs[i].Content {
			switch b.Type {
			case "tool_use":
				key := callKey{
					tool:  b.ToolName,
					input: canonicalJSON(b.Input),
				}
				byID[b.ToolID] = &key
				rec := records[key]
				if rec == nil {
					rec = &callRecord{
						tool:      key.tool,
						canonical: key.input,
						hashes:    map[string]bool{},
						readOnly:  readOnlyTools[key.tool],
					}
					records[key] = rec
					keyOrder = append(keyOrder, key)
				}
				rec.count++
				r.ToolCalls++
			case "tool_result":
				key := byID[b.ToolID]
				if key == nil {
					continue
				}
				rec := records[*key]
				if rec == nil {
					continue
				}
				rec.hashes[truncateHash(b.Output)] = true
				if b.IsError {
					r.ToolErrors++
				}
				results = append(results, resultObs{
					key:     *key,
					isError: b.IsError,
					outLen:  len(b.Output),
				})
			}
		}
	}

	seenAssistant := false
	for i := range msgs {
		switch msgs[i].Role {
		case "assistant":
			seenAssistant = true
			for _, b := range msgs[i].Content {
				if b.Type == "tool_use" && b.ToolName == "ask_user" {
					r.AskUserCalls++
				}
			}
		case "user":
			if !seenAssistant {
				continue
			}
			hasText := false
			for _, b := range msgs[i].Content {
				if b.Type != "text" || strings.TrimSpace(b.Text) == "" {
					continue
				}
				hasText = true
				if isCorrectionSignal(b.Text) {
					r.CorrectionSignals++
				}
			}
			if hasText {
				r.SteeringEvents++
			}
		}
	}

	seenTools := map[string]bool{}
	for _, k := range keyOrder {
		seenTools[records[k].tool] = true
	}
	r.DistinctTools = len(seenTools)

	for i := range msgs {
		if msgs[i].Role != "assistant" {
			continue
		}
		for _, b := range msgs[i].Content {
			if b.Type == "tool_use" {
				r.TurnCount++
				break
			}
		}
	}

	lastIdx := map[callKey]int{}
	for i, res := range results {
		lastIdx[res.key] = i
	}
	for i, res := range results {
		rec := records[res.key]
		if rec == nil || i == lastIdx[res.key] {
			continue
		}
		if res.isError || (rec.readOnly && rec.identical()) {
			r.WastedRepeatCalls++
			r.WastedResultBytes += res.outLen
		}
	}

	for _, k := range keyOrder {
		rec := records[k]
		if rec.count < 2 {
			continue
		}
		r.DuplicateGroups = append(r.DuplicateGroups, DuplicateGroup{
			Tool:      rec.tool,
			Input:     truncateDisplay(rec.canonical),
			Count:     rec.count,
			Repeats:   rec.count - 1,
			Identical: rec.identical(),
			ReadOnly:  rec.readOnly,
		})
	}
	sort.Slice(r.DuplicateGroups, func(a, b int) bool {
		if r.DuplicateGroups[a].Repeats != r.DuplicateGroups[b].Repeats {
			return r.DuplicateGroups[a].Repeats > r.DuplicateGroups[b].Repeats
		}
		return r.DuplicateGroups[a].Tool < r.DuplicateGroups[b].Tool
	})

	r.WastedTokenEstimate = r.WastedResultBytes / bytesPerToken

	for _, s := range usage {
		t := s.Usage.Total()
		r.TotalTokens += t
		if s.Source != "" && s.Source != "agent" {
			r.OverheadTokens += t
		}
	}

	r.EfficiencyScore = score(r)
	r.Findings = findings(r)
	return r
}

func score(r Report) int {
	s := 100.0
	if r.ToolCalls > 0 {
		s -= 40 * float64(r.ToolErrors) / float64(r.ToolCalls)
		s -= 30 * float64(r.WastedRepeatCalls) / float64(r.ToolCalls)
	}
	if r.ToolCalls >= minToolsForOverheadPenalty && r.TotalTokens > 0 {
		s -= 20 * float64(r.OverheadTokens) / float64(r.TotalTokens)
	}
	if s < 0 {
		s = 0
	}
	if s > 100 {
		s = 100
	}
	return int(s + 0.5)
}

func findings(r Report) []string {
	var f []string

	shown := 0
	for _, g := range r.DuplicateGroups {
		if g.Repeats <= 0 || shown >= 3 {
			continue
		}
		noun := "calls"
		if g.Repeats == 1 {
			noun = "call"
		}
		if g.Identical && g.ReadOnly {
			f = append(f, fmt.Sprintf(
				"%d repeated %s %s(%s) returned identical results - the answer was already in context",
				g.Repeats, noun, g.Tool, g.Input))
		} else {
			f = append(f, fmt.Sprintf(
				"%d repeated %s %s(%s) - hit the same inputs %d×; check why earlier results were insufficient",
				g.Repeats, noun, g.Tool, g.Input, g.Count))
		}
		shown++
	}

	if r.ToolErrors > 0 {
		f = append(f, fmt.Sprintf(
			"%d of %d tool calls failed - each failure costs a retry turn plus the error text re-ingested into context",
			r.ToolErrors, r.ToolCalls))
	}

	if r.TotalTokens > 0 && r.ToolCalls >= minToolsForOverheadPenalty {
		share := 100 * float64(r.OverheadTokens) / float64(r.TotalTokens)
		if share >= 15 {
			f = append(f, fmt.Sprintf(
				"%.0f%% of tokens came from non-agent machinery (compaction/strategist/verify…) - weigh the cost-quality tradeoff of that overhead",
				share))
		}
	}

	if r.WastedTokenEstimate > 0 {
		f = append(f, fmt.Sprintf(
			"~%s tokens of context tax from failed/duplicate results (~%s bytes)",
			comma(r.WastedTokenEstimate), comma(r.WastedResultBytes)))
	}

	if r.SteeringEvents >= steeringFindingsThreshold {
		f = append(f, fmt.Sprintf(
			"%d mid-run user steer%s after work started (incl. %d explicit correction%s) - frequent takeover may signal task-alignment drift; front-load ambiguity resolution instead",
			r.SteeringEvents, plural(r.SteeringEvents), r.CorrectionSignals, plural(r.CorrectionSignals)))
	} else if r.CorrectionSignals >= correctionFindingsThreshold {
		f = append(f, fmt.Sprintf(
			"%d explicit user correction%s of work already done - check whether the initial plan matched the request before executing",
			r.CorrectionSignals, plural(r.CorrectionSignals)))
	}

	return f
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func Render(r Report) string {
	var b strings.Builder
	grade := "good"
	switch {
	case r.EfficiencyScore >= 90:
		grade = "excellent"
	case r.EfficiencyScore < 60:
		grade = "needs work"
	}
	fmt.Fprintf(&b, "Run report - trajectory scorecard (efficiency %d/100, %s)\n", r.EfficiencyScore, grade)
	fmt.Fprintf(&b, "  Steps: %d turns with tool calls · %d tool calls · %d distinct tools\n",
		r.TurnCount, r.ToolCalls, r.DistinctTools)
	fmt.Fprintf(&b, "  Failures: %d/%d tool calls errored · wasted repeats: %d\n",
		r.ToolErrors, r.ToolCalls, r.WastedRepeatCalls)
	fmt.Fprintf(&b, "  Interaction: %d mid-run steers · %d ask_user deferrals · %d corrections\n",
		r.SteeringEvents, r.AskUserCalls, r.CorrectionSignals)
	if r.TotalTokens > 0 {
		share := 100 * float64(r.OverheadTokens) / float64(r.TotalTokens)
		fmt.Fprintf(&b, "  Tokens: %s total · %.0f%% overhead machinery · ~%s wasted context tax (est.)\n",
			comma(r.TotalTokens), share, comma(r.WastedTokenEstimate))
	}
	if len(r.DuplicateGroups) > 0 {
		fmt.Fprintf(&b, "  Duplicate groups: %d (worst: %s ×%d)\n",
			len(r.DuplicateGroups), r.DuplicateGroups[0].Tool, r.DuplicateGroups[0].Count)
	} else {
		b.WriteString("  Duplicate groups: none\n")
	}

	if r.Coordination != nil {
		renderCoordination(&b, r.Coordination)
	}
	if len(r.Findings) > 0 {
		b.WriteString("Findings:\n")
		for i, line := range r.Findings {
			fmt.Fprintf(&b, "  %d. %s\n", i+1, line)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

var correctionLexicon = []string{
	"不对", "错了", "不是这", "取消", "撤销", "回滚", "别用", "重来",
	"wrong", "not what i", "not what I", "instead of that", "cancel that",
	"undo that", "undo it", "revert that", "don't use", "do not use",
}

func isCorrectionSignal(text string) bool {
	lower := strings.ToLower(text)
	for _, m := range correctionLexicon {
		if strings.Contains(lower, strings.ToLower(m)) {
			return true
		}
	}
	return false
}

func canonicalJSON(raw json.RawMessage) string {
	if len(raw) == 0 {
		return "{}"
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return string(raw)
	}

	out, err := json.Marshal(v)
	if err != nil {
		return string(raw)
	}
	return string(out)
}

func truncateHash(s string) string {
	if len(s) > resultHashCap {
		return s[:resultHashCap]
	}
	return s
}

func truncateDisplay(s string) string {
	if s == "" {
		return "∅"
	}
	if len(s) > displayInputCap {
		return s[:displayInputCap] + "…"
	}
	return s
}

func comma(n int) string {
	s := fmt.Sprintf("%d", n)
	neg := strings.HasPrefix(s, "-")
	if neg {
		s = s[1:]
	}
	var parts []string
	for len(s) > 3 {
		parts = append([]string{s[len(s)-3:]}, parts...)
		s = s[:len(s)-3]
	}
	parts = append([]string{s}, parts...)
	out := strings.Join(parts, ",")
	if neg {
		out = "-" + out
	}
	return out
}
