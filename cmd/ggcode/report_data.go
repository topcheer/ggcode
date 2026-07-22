package main

import (
	"encoding/json"
	"sort"
	"time"
)

// reportData is the JSON structure injected into the HTML template.
type reportData struct {
	GeneratedAt   string          `json:"generatedAt"`
	Sessions      []sessionJSON   `json:"sessions"`
	DailyTokens   []dailyTokenRow `json:"dailyTokens"`
	Workspaces    []workspaceRow  `json:"workspaces"`
	ToolSummary   []toolStatJSON  `json:"toolSummary"`
	OldestSession string          `json:"oldestSession"`
}

type sessionJSON struct {
	ID          string      `json:"id"`
	Title       string      `json:"title"`
	Workspace   string      `json:"workspace"`
	Model       string      `json:"model"`
	Vendor      string      `json:"vendor"`
	CreatedAt   string      `json:"createdAt"`
	Updated     string      `json:"updatedAt"`
	MsgCount    int         `json:"msgCount"`
	TotalInput  int         `json:"totalInput"`
	TotalOutput int         `json:"totalOutput"`
	TotalCache  int         `json:"totalCache"`
	LLMCalls    int         `json:"llmCalls"`
	ToolCalls   int         `json:"toolCalls"`
	Turns       []turnJSON  `json:"turns"`
	Tools       []toolJSON  `json:"tools"`
	ModelPerf   []modelPerf `json:"modelPerf"`
}

type turnJSON struct {
	Index   int    `json:"index"`
	Model   string `json:"model,omitempty"`
	Input   int    `json:"input"`
	Output  int    `json:"output"`
	Cache   int    `json:"cache"`
	TTFTMs  int64  `json:"ttftMs"`
	DurMs   int64  `json:"durMs"`
	ThinkMs int64  `json:"thinkMs"`
	Day     string `json:"day,omitempty"`
	TS      string `json:"ts,omitempty"`
	SID     string `json:"sid,omitempty"`
}

type toolJSON struct {
	Name     string  `json:"name"`
	Calls    int     `json:"calls"`
	Failures int     `json:"failures"`
	AvgMs    float64 `json:"avgMs"`
}

type modelPerf struct {
	Model   string  `json:"model"`
	TurnIdx []int   `json:"turnIdx"`
	TTFTMs  []int64 `json:"ttftMs"`
	DurMs   []int64 `json:"durMs"`
	AvgTTFT float64 `json:"avgTtft"`
}

type dailyTokenRow struct {
	Date   string `json:"date"`
	Input  int    `json:"input"`
	Output int    `json:"output"`
	Cache  int    `json:"cache"`
}

type workspaceRow struct {
	Workspace string `json:"workspace"`
	Sessions  int    `json:"sessions"`
	Input     int    `json:"input"`
	Output    int    `json:"output"`
}

type toolStatJSON struct {
	Name     string  `json:"name"`
	Calls    int     `json:"calls"`
	Failures int     `json:"failures"`
	AvgMs    float64 `json:"avgMs"`
	TotalMs  int64   `json:"-"`
}

func buildReport(results []*scanResult) reportData {
	rd := reportData{
		GeneratedAt: time.Now().Format(time.RFC3339),
	}

	dailyMap := make(map[string]*dailyTokenRow)
	wsMap := make(map[string]*workspaceRow)
	toolMap := make(map[string]*toolStatJSON)
	oldest := oldestTime(results)

	for _, sr := range results {
		sj := sessionJSON{
			ID:        sr.meta.ID,
			Title:     sr.meta.Title,
			Workspace: sr.meta.Workspace,
			Model:     sr.meta.Model,
			Vendor:    sr.meta.Vendor,
			CreatedAt: sr.meta.CreatedAt.Format(time.RFC3339),
			Updated:   sr.meta.UpdatedAt.Format(time.RFC3339),
			MsgCount:  sr.msgCount,
		}

		// Aggregate turns
		var turnIndices []int
		for idx := range sr.turns {
			turnIndices = append(turnIndices, idx)
		}
		sort.Ints(turnIndices)

		// Group TTFT by model for comparison chart
		modelTTFT := make(map[string]*modelPerf)

		for _, idx := range turnIndices {
			t := sr.turns[idx]
			sj.TotalInput += t.Input
			sj.TotalOutput += t.Output
			sj.TotalCache += t.Cache
			sj.LLMCalls++

			tj := turnJSON{
				Index:   t.Index,
				Model:   t.Model,
				Input:   t.Input,
				Output:  t.Output,
				Cache:   t.Cache,
				TTFTMs:  t.TTFTMs,
				DurMs:   t.DurMs,
				ThinkMs: t.ThinkMs,
				SID:     sj.ID,
			}
			// Use turn's actual timestamp for daily aggregation
			if !t.Timestamp.IsZero() {
				day := t.Timestamp.Format("2006-01-02")
				tj.Day = day
				tj.TS = t.Timestamp.Format(time.RFC3339)
				dr := dailyMap[day]
				if dr == nil {
					dr = &dailyTokenRow{Date: day}
					dailyMap[day] = dr
				}
				dr.Input += t.Input
				dr.Output += t.Output
				dr.Cache += t.Cache
			}
			sj.Turns = append(sj.Turns, tj)

			// Collect model performance data
			m := t.Model
			if m == "" {
				m = "(unknown)"
			}
			mp, ok := modelTTFT[m]
			if !ok {
				mp = &modelPerf{Model: m}
				modelTTFT[m] = mp
			}
			mp.TurnIdx = append(mp.TurnIdx, idx)
			mp.TTFTMs = append(mp.TTFTMs, t.TTFTMs)
			mp.DurMs = append(mp.DurMs, t.DurMs)
		}

		// Finalize model perf
		for _, mp := range modelTTFT {
			var sum int64
			for _, v := range mp.TTFTMs {
				sum += v
			}
			if len(mp.TTFTMs) > 0 {
				mp.AvgTTFT = float64(sum) / float64(len(mp.TTFTMs))
			}
			sj.ModelPerf = append(sj.ModelPerf, *mp)
		}
		sort.Slice(sj.ModelPerf, func(i, j int) bool {
			return sj.ModelPerf[i].AvgTTFT < sj.ModelPerf[j].AvgTTFT
		})

		// Aggregate tools
		var toolNames []string
		for name := range sr.tools {
			toolNames = append(toolNames, name)
		}
		sort.Strings(toolNames)
		for _, name := range toolNames {
			ta := sr.tools[name]
			avgMs := 0.0
			if ta.Calls > 0 {
				avgMs = float64(ta.TotalMs) / float64(ta.Calls)
			}
			sj.Tools = append(sj.Tools, toolJSON{
				Name:     ta.Name,
				Calls:    ta.Calls,
				Failures: ta.Failures,
				AvgMs:    avgMs,
			})
			sj.ToolCalls += ta.Calls

			// Global tool summary
			ts := toolMap[name]
			if ts == nil {
				ts = &toolStatJSON{Name: name}
				toolMap[name] = ts
			}
			ts.Calls += ta.Calls
			ts.Failures += ta.Failures
			ts.TotalMs += ta.TotalMs
		}

		rd.Sessions = append(rd.Sessions, sj)

		// Workspace aggregation
		ws := sr.meta.Workspace
		if ws == "" {
			ws = "(unknown)"
		}
		wr := wsMap[ws]
		if wr == nil {
			wr = &workspaceRow{Workspace: ws}
			wsMap[ws] = wr
		}
		wr.Sessions++
		wr.Input += sj.TotalInput
		wr.Output += sj.TotalOutput
	}

	// Sort daily tokens by date
	var days []string
	for d := range dailyMap {
		days = append(days, d)
	}
	sort.Strings(days)
	for _, d := range days {
		rd.DailyTokens = append(rd.DailyTokens, *dailyMap[d])
	}

	// Sort workspaces by total tokens desc
	var wsList []*workspaceRow
	for _, wr := range wsMap {
		wsList = append(wsList, wr)
	}
	sort.Slice(wsList, func(i, j int) bool {
		return wsList[i].Input+wsList[i].Output > wsList[j].Input+wsList[j].Output
	})
	for _, wr := range wsList {
		rd.Workspaces = append(rd.Workspaces, *wr)
	}

	// Sort tool summary by calls desc
	var toolList []*toolStatJSON
	for _, ts := range toolMap {
		toolList = append(toolList, ts)
	}
	sort.Slice(toolList, func(i, j int) bool {
		return toolList[i].Calls > toolList[j].Calls
	})
	for _, ts := range toolList {
		if ts.Calls > 0 {
			ts.AvgMs = float64(ts.TotalMs) / float64(ts.Calls)
		}
		rd.ToolSummary = append(rd.ToolSummary, *ts)
	}

	if !oldest.IsZero() {
		rd.OldestSession = oldest.Format(time.RFC3339)
	}

	return rd
}

func generateHTML(rd reportData) (string, error) {
	data, err := json.Marshal(rd)
	if err != nil {
		return "", err
	}
	return htmlTemplate(string(data)), nil
}
