package agent

import (
	"bufio"
	"encoding/json"
	"os"
	"sort"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

const (
	staleMinRuns             = 10
	staleTailBytes           = 64 * 1024
	staleOtherModelDelivered = 2
)

type guidanceStatRecord struct {
	Type       string `json:"type,omitempty"`
	TS         string `json:"ts"`
	Model      string `json:"model,omitempty"`
	Tag        string `json:"tag"`
	Delivered  int    `json:"delivered"`
	Suppressed int    `json:"suppressed"`
}

func analyzeStaleGuidance(path, currentModel string) {
	if currentModel == "" {
		return
	}
	recs := readGuidanceStatTail(path)
	if len(recs) == 0 {
		return
	}

	runSet := map[string]map[string]bool{}
	type tagKey struct{ model, tag string }
	agg := map[tagKey]*guidanceTagStat{}
	reported := map[tagKey]bool{}
	for _, r := range recs {
		if r.Type == "stale_heuristic" {
			reported[tagKey{r.Model, r.Tag}] = true
			continue
		}
		if r.Tag == "" || r.Model == "" {

			continue
		}
		if runSet[r.Model] == nil {
			runSet[r.Model] = map[string]bool{}
		}
		runSet[r.Model][r.TS] = true
		k := tagKey{r.Model, r.Tag}
		st := agg[k]
		if st == nil {
			st = &guidanceTagStat{}
			agg[k] = st
		}
		st.Delivered += r.Delivered
		st.Suppressed += r.Suppressed
	}

	deliveredPerModel := map[string]map[string]*guidanceTagStat{}
	for k, st := range agg {
		if len(runSet[k.model]) >= staleMinRuns {
			if deliveredPerModel[k.model] == nil {
				deliveredPerModel[k.model] = map[string]*guidanceTagStat{}
			}
			deliveredPerModel[k.model][k.tag] = st
		}
	}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		debug.Log("guidance-stale", "open failed: %v", err)
		return
	}
	defer f.Close()

	for tag, zero := range deliveredPerModel[currentModel] {

		if zero.Delivered != 0 || zero.Suppressed > 0 || reported[tagKey{currentModel, tag}] {
			continue
		}
		others := 0
		for m, tags := range deliveredPerModel {
			if m != currentModel && tags[tag].Delivered >= staleOtherModelDelivered {
				others += tags[tag].Delivered
			}
		}
		if others == 0 {
			continue
		}
		b, err := json.Marshal(guidanceStatRecord{
			Type:  "stale_heuristic",
			TS:    recs[len(recs)-1].TS,
			Model: currentModel,
			Tag:   tag,
		})
		if err != nil {
			continue
		}
		f.Write(append(b, '\n'))
		debug.Log("guidance-stale", "stale heuristic on %s: %q never delivered in %d runs (other models delivered %d)",
			currentModel, tag, len(runSet[currentModel]), others)
	}
}

func readGuidanceStatTail(path string) []guidanceStatRecord {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var start int64
	if fi, err := f.Stat(); err == nil && fi.Size() > staleTailBytes {
		start = fi.Size() - staleTailBytes
	}
	if _, err := f.Seek(start, 0); err != nil {
		return nil
	}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 64*1024)
	var recs []guidanceStatRecord
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || line[0] != '{' {
			continue
		}
		var r guidanceStatRecord
		if json.Unmarshal([]byte(line), &r) == nil && r.Tag != "" {
			recs = append(recs, r)
		}
	}
	return recs
}

func staleHeuristicSummary(path, model string) []string {
	recs := readGuidanceStatTail(path)
	var out []string
	for _, r := range recs {
		if r.Type == "stale_heuristic" && (model == "" || r.Model == model) {
			out = append(out, r.Tag)
		}
	}
	sort.Strings(out)
	return out
}
