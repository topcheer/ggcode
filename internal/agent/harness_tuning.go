package agent

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
)

const (
	tuningDeliveredWindow = 20
	tuningFollowupRuns    = 3
	tuningMinFollowups    = 5
	tuningDefaultBaseline = 0.3
	tuningMaxOverrides    = 3
	tuningRelockRuns      = 20
	tuningConfirmations   = 2
	tuningSuppressTier    = 0
)

type tierOverride struct {
	Tier      int       `json:"tier"`
	Version   int       `json:"version"`
	Model     string    `json:"model,omitempty"`
	AppliedAt time.Time `json:"applied_at"`
}

type harnessOverride map[string]tierOverride

var (
	harnessMu        sync.Mutex
	harnessOverrides harnessOverride
	harnessConfirmed = map[string]int{}
	harnessPathFn    = defaultHarnessOverridesPath
)

func defaultHarnessOverridesPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".ggcode", "harness-overrides.json")
}

func loadHarnessOverrides(path string) harnessOverride {
	o := harnessOverride{}
	b, err := os.ReadFile(path)
	if err != nil {
		return o
	}
	if err := json.Unmarshal(b, &o); err != nil {
		debug.Log("harness-tuning", "corrupt override store %s (%v), backed up", path, err)
		_ = os.Rename(path, path+".bak")
		return harnessOverride{}
	}
	if o == nil {
		o = harnessOverride{}
	}
	return o
}

func storeHarnessOverrides(path string, o harnessOverride) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(o, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "harness-overrides-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Chmod(tmpName, 0644); err != nil {
		os.Remove(tmpName)
		return err
	}
	return os.Rename(tmpName, path)
}

type tuningRun struct {
	deliv   bool
	errMark bool
}

func tuningRunsFor(tag string, recs []guidanceStatRecord) []tuningRun {
	var runs []tuningRun
	idx := map[string]int{}
	for _, r := range recs {
		if r.Type == "stale_heuristic" || r.TS == "" {
			continue
		}
		i, ok := idx[r.TS]
		if !ok {
			i = len(runs)
			idx[r.TS] = i
			runs = append(runs, tuningRun{})
		}
		lower := strings.ToLower(r.Tag)
		if strings.Contains(lower, "error") || strings.Contains(lower, "fail") {
			runs[i].errMark = true
		}
		if r.Tag == tag && r.Delivered > 0 {
			runs[i].deliv = true
		}
	}
	return runs
}

func followupError(runs []tuningRun, delivered []int) (float64, int) {
	if len(delivered) == 0 {
		return 0, 0
	}
	hits, total := 0, 0
	for _, i := range delivered {
		hit := false
		for j := i + 1; j < len(runs) && j <= i+tuningFollowupRuns; j++ {
			total++
			if runs[j].errMark {
				hit = true
			}
		}
		if hit {
			hits++
		}
	}
	return float64(hits) / float64(len(delivered)), total
}

func proposeTierChange(tag string, stats []guidanceStatRecord) bool {
	runs := tuningRunsFor(tag, stats)
	var delivered []int
	for i, r := range runs {
		if r.deliv {
			delivered = append(delivered, i)
		}
	}
	if len(delivered) == 0 {
		return false
	}
	var recent, earlier []int
	if len(delivered) > tuningDeliveredWindow {
		recent = delivered[len(delivered)-tuningDeliveredWindow:]
		earlier = delivered[:len(delivered)-tuningDeliveredWindow]
	} else {
		recent = delivered
	}
	ratio, total := followupError(runs, recent)
	if total < tuningMinFollowups {
		return false
	}
	baseline := tuningDefaultBaseline
	if b, n := followupError(runs, earlier); n >= tuningMinFollowups {
		baseline = b
	}
	return ratio <= baseline+1e-9
}

func runsSinceOverride(appliedAt time.Time, recs []guidanceStatRecord) int {
	seen := map[string]bool{}
	n := 0
	for _, r := range recs {
		if r.Type == "stale_heuristic" || r.TS == "" || seen[r.TS] {
			continue
		}
		seen[r.TS] = true
		if t, err := time.Parse(time.RFC3339, r.TS); err == nil && t.After(appliedAt) {
			n++
		}
	}
	return n
}

func tuningModelFor(recs []guidanceStatRecord) string {
	model := ""
	for i := len(recs) - 1; i >= 0; i-- {
		if recs[i].Model != "" {
			model = recs[i].Model
			if recs[i].Type == "stale_heuristic" {
				break
			}
		}
	}
	return model
}

func tuningCountModel(o harnessOverride, model string) int {
	n := 0
	for _, ov := range o {
		if ov.Model == model {
			n++
		}
	}
	return n
}

func MaybeTuneHarness(staleTags []string, statsPath string) error {
	if len(staleTags) == 0 {
		return nil
	}
	recs := readGuidanceStatTail(statsPath)
	if len(recs) == 0 {
		return nil
	}
	model := tuningModelFor(recs)
	if model == "" {
		return nil
	}
	overridesPath := harnessPathFn()
	if overridesPath == "" {
		return nil
	}
	harnessMu.Lock()
	defer harnessMu.Unlock()
	cur := loadHarnessOverrides(overridesPath)
	changed := false
	for _, tag := range staleTags {
		if tag == "" {
			continue
		}
		if ov, ok := cur[tag]; ok {

			if ov.Tier <= tuningSuppressTier {
				continue
			}
			if runsSinceOverride(ov.AppliedAt, recs) < tuningRelockRuns {
				continue
			}
		}
		harnessConfirmed[tag]++
		if harnessConfirmed[tag] < tuningConfirmations {
			continue
		}
		if !proposeTierChange(tag, recs) {
			continue
		}
		if tuningCountModel(cur, model) >= tuningMaxOverrides {
			continue
		}
		cur[tag] = tierOverride{Tier: tuningSuppressTier, Version: 1, Model: model, AppliedAt: time.Now().UTC()}
		changed = true
		debug.Log("harness-tuning", "override accepted: %q -> tier %d (model %s)", tag, tuningSuppressTier, model)
	}
	if changed {
		if err := storeHarnessOverrides(overridesPath, cur); err != nil {
			return err
		}
		harnessOverrides = cur
	}
	return nil
}

func harnessOverrideSuppresses(tag string) bool {
	return !ApplyOverridesToAllow(func(string) bool { return true })(tag)
}

func ApplyOverridesToAllow(allow func(tag string) bool) func(tag string) bool {
	return func(tag string) bool {
		if path := harnessPathFn(); path != "" {
			harnessMu.Lock()
			if harnessOverrides == nil {
				harnessOverrides = loadHarnessOverrides(path)
			}
			ov, ok := harnessOverrides[tag]
			harnessMu.Unlock()
			if ok && ov.Tier <= tuningSuppressTier {
				return false
			}
		}
		if allow == nil {
			return true
		}
		return allow(tag)
	}
}

func SetHarnessSuppressed(tag string) error {
	if tag == "" {
		return errors.New("harness override: empty tag")
	}
	path := harnessPathFn()
	if path == "" {
		return errors.New("harness override: store path unavailable")
	}
	harnessMu.Lock()
	defer harnessMu.Unlock()
	cur := loadHarnessOverrides(path)
	cur[tag] = tierOverride{Tier: tuningSuppressTier, Version: 1, Model: "manual", AppliedAt: time.Now().UTC()}
	if err := storeHarnessOverrides(path, cur); err != nil {
		return err
	}
	harnessOverrides = cur
	debug.Log("harness-tuning", "manual suppress: %q", tag)
	return nil
}

func ClearHarnessOverride(tag string) (bool, error) {
	if tag == "" {
		return false, errors.New("harness override: empty tag")
	}
	path := harnessPathFn()
	if path == "" {
		return false, errors.New("harness override: store path unavailable")
	}
	harnessMu.Lock()
	defer harnessMu.Unlock()
	cur := loadHarnessOverrides(path)
	if _, ok := cur[tag]; !ok {
		return false, nil
	}
	delete(cur, tag)
	if err := storeHarnessOverrides(path, cur); err != nil {
		return false, err
	}
	harnessOverrides = cur
	debug.Log("harness-tuning", "manual reset: %q", tag)
	return true, nil
}
