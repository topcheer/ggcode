package agent

// Detector guidance effectiveness telemetry (empirical evaluation quadrant).
//
// Research basis:
//   - ICLR Blogposts 2026, "Why AI Evaluations Need Statistical Rigor"
//     (https://iclr-blogposts.github.io/2026/blog/2026/why-ai-evaluations-need-error-bars/):
//     single-run point estimates are unstable for stochastic systems;
//     evaluation outputs must carry counts WITH intervals. ggcode's 190+
//     detectors previously had zero longitudinal firing telemetry - the
//     guidanceBudget only counted per-turn suppressions into debug logs -
//     so consolidation decisions ("which detectors are noise?") had no
//     empirical basis.
//   - NIST AI 800-3 binomial-interval guidance (via S. Yang, "Statistical
//     Confidence for AI Agent Evaluations", 2026,
//     https://stanleycyang.com/writing/statistical-confidence-agent-evals):
//     prefer the Wilson score interval to the Wald interval for proportions
//     near 0/1 or with modest samples; zero observed failures does NOT
//     establish zero risk - report the upper bound; always show the
//     numerator and denominator; distinguish "no detected regression" from
//     "evidence of acceptable performance".
//   - Eval feedback-loop design (FutureAGI, "LLM Eval Feedback Loop Design
//     2026", https://futureagi.com/blog/llm-eval-feedback-loop-design-2026/):
//     capture -> join -> calibrate -> report; the JOIN step (pairing
//     firings with subsequent negative user signals) is the step most
//     teams skip.
//
// What this module does (deterministic, zero LLM cost, purely observational):
//
//	1. CAPTURE: every DELIVERED detector hint (tagged, e.g.
//	   "[STRATEGY-STAGNATION] ...") is recorded with a timestamp - on both
//	   the iteration-level path (injectGuidance) and the tool-result hint
//	   path (appendGuidance / applyToolResultGuidance), AFTER budget
//	   gating, so counts reflect what the model actually saw.
//	2. JOIN: user negative signals (textual negative feedback detected by
//	   user_sentiment, user file reverts detected by correction_feedback)
//	   are attributed to tags that fired within the attribution window.
//	3. REPORT: per-tag fires, repeat-fire rate (same tag re-fires within
//	   the repeat window -> guidance did not stick -> ineffectiveness
//	   proxy) and negative-attribution rate (annoyance / false-positive
//	   proxy), each with Wilson 95% intervals; small-sample tags are
//	   marked "insufficient evidence" instead of judged. One JSONL line is
//	   appended per run under <workDir>/.ggcode/guidance-stats.jsonl for
//	   offline aggregation via `ggcode guidance-stats`.
//
// Protocol compatibility: no messages are added to the conversation and no
// tool-call/tool-result pairing is altered; the module only observes the
// delivery points that already exist.
//
// Known limitations (documented, not hidden):
//   - Temporal attribution is heuristic: a negative user message within the
//     window is a proxy, not a proven causal link to a specific hint.
//   - Events within one session are clustered (ICLR post, §2.2): per-session
//     correlation means raw event counts overstate effective sample size.
//     The report shows raw n and intervals, and this caveat is surfaced in
//     the CLI output.
//   - This is REPORT-ONLY: nothing is auto-suppressed based on these stats.
//     Consolidation decisions stay human-reviewed.

import (
	"bufio"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
)

const (
	// guidanceRepeatWindow: a tag firing again within this window counts as
	// a "repeat" - the previous guidance did not change behavior.
	guidanceRepeatWindow = 10 * time.Minute

	// guidanceNegWindow: user negative signals within this window after a
	// fire are attributed to recently fired tags.
	guidanceNegWindow = 6 * time.Minute

	// guidanceMinSample: below this many fires a tag is reported as
	// "insufficient evidence" rather than judged (NIST small-sample
	// guidance; Wilson intervals are wide at small n).
	guidanceMinSample = 5

	// guidanceFireLogCap: bounded in-memory event log used for negative
	// attribution (ring buffer; old events fall off).
	guidanceFireLogCap = 256

	// guidanceStatsFile is the JSONL file under <workDir>/.ggcode/.
	guidanceStatsFile = "guidance-stats.jsonl"

	// wilsonZ is the 95% two-sided normal quantile.
	wilsonZ = 1.96
)

// guidanceFireEvent is one delivered hint, kept in the bounded event log.
type guidanceFireEvent struct {
	at         time.Time
	tag        string
	attributed bool
}

// guidanceTagStats aggregates per-tag counters for one run.
type guidanceTagStats struct {
	Fires        int       `json:"fires"`
	Repeats      int       `json:"repeats"`
	NegativeHits int       `json:"negative_hits"`
	LastFire     time.Time `json:"last_fire,omitempty"`
}

// guidanceStats is the session-scoped collector.
type guidanceStats struct {
	mu           sync.Mutex
	session      string
	startedAt    time.Time
	repeatWindow time.Duration
	negWindow    time.Duration
	minSample    int
	negatives    int
	fireLog      []guidanceFireEvent
	tags         map[string]*guidanceTagStats
	persisted    bool
}

// GuidanceTagReport is one tag's aggregated statistics (used both for the
// in-session summary and the offline CLI aggregation).
type GuidanceTagReport struct {
	Tag             string  `json:"tag"`
	Fires           int     `json:"fires"`
	Repeats         int     `json:"repeats"`
	NegativeHits    int     `json:"negative_hits"`
	RepeatRate      float64 `json:"repeat_rate"`
	RepeatLo        float64 `json:"repeat_lo"`
	RepeatHi        float64 `json:"repeat_hi"`
	RepeatCIDefined bool    `json:"repeat_ci_defined"`
	NegRate         float64 `json:"negative_rate"`
	NegLo           float64 `json:"negative_lo"`
	NegHi           float64 `json:"negative_hi"`
	NegCIDefined    bool    `json:"negative_ci_defined"`
}

// guidanceStatsLine is the persisted per-run JSONL record.
type guidanceStatsLine struct {
	Session   string                       `json:"session"`
	Started   time.Time                    `json:"started"`
	Ended     time.Time                    `json:"ended"`
	Runs      int                          `json:"runs"`
	Negatives int                          `json:"negatives"`
	Tags      map[string]*guidanceTagStats `json:"tags"`
}

func newGuidanceStats() *guidanceStats {
	now := time.Now()
	return &guidanceStats{
		session:      now.Format("20060102-150405"),
		startedAt:    now,
		repeatWindow: guidanceRepeatWindow,
		negWindow:    guidanceNegWindow,
		minSample:    guidanceMinSample,
		tags:         make(map[string]*guidanceTagStats),
	}
}

// recordFire logs one delivered hint. Empty tags (untagged protocol nudges)
// are ignored by the caller helper.
func (s *guidanceStats) recordFire(tag string, now time.Time) {
	if s == nil || tag == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.tags[tag]
	if st == nil {
		st = &guidanceTagStats{}
		s.tags[tag] = st
	}
	if !st.LastFire.IsZero() && now.Sub(st.LastFire) <= s.repeatWindow {
		st.Repeats++
	}
	st.Fires++
	st.LastFire = now

	s.fireLog = append(s.fireLog, guidanceFireEvent{at: now, tag: tag})
	if len(s.fireLog) > guidanceFireLogCap {
		// Drop oldest entries down to the cap.
		s.fireLog = s.fireLog[len(s.fireLog)-guidanceFireLogCap:]
	}
}

// recordNegative records a user negative signal and attributes it to tags
// that fired within the attribution window. Each fire event is attributed
// at most once so repeated negative messages cannot inflate a tag's rate.
func (s *guidanceStats) recordNegative(now time.Time) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.negatives++
	for i := range s.fireLog {
		ev := &s.fireLog[i]
		if ev.attributed || now.Sub(ev.at) > s.negWindow {
			continue
		}
		ev.attributed = true
		if st := s.tags[ev.tag]; st != nil {
			st.NegativeHits++
		}
	}
}

// report returns per-tag reports sorted by fires desc, then tag asc.
func (s *guidanceStats) report() []GuidanceTagReport {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	reports := make([]GuidanceTagReport, 0, len(s.tags))
	for tag, st := range s.tags {
		r := GuidanceTagReport{
			Tag:          tag,
			Fires:        st.Fires,
			Repeats:      st.Repeats,
			NegativeHits: st.NegativeHits,
		}
		r.RepeatLo, r.RepeatHi, _ = wilsonInterval(st.Repeats, st.Fires, wilsonZ)
		r.RepeatRate = ratio(st.Repeats, st.Fires)
		r.RepeatCIDefined = st.Fires > 0
		r.NegLo, r.NegHi, _ = wilsonInterval(st.NegativeHits, st.Fires, wilsonZ)
		r.NegRate = ratio(st.NegativeHits, st.Fires)
		r.NegCIDefined = st.Fires > 0
		reports = append(reports, r)
	}
	sort.Slice(reports, func(i, j int) bool {
		if reports[i].Fires != reports[j].Fires {
			return reports[i].Fires > reports[j].Fires
		}
		return reports[i].Tag < reports[j].Tag
	})
	return reports
}

// RenderGuidanceReport renders a per-tag effectiveness table with Wilson
// 95% intervals and evidence-aware verdicts. Shared by the in-session
// summary and `ggcode guidance-stats`.
func RenderGuidanceReport(reports []GuidanceTagReport, sessions int) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Detector guidance effectiveness (%d run(s), local only)\n", sessions))
	b.WriteString("Rates: repeat = share of firings that re-fired within the repeat window (guidance did not stick);\n")
	b.WriteString("negative = share of firings followed by user negative feedback within the attribution window.\n")
	b.WriteString("Intervals: Wilson 95%. Within-session events are clustered; treat n as an upper bound on effective sample size.\n\n")
	b.WriteString(fmt.Sprintf("%-30s %6s %8s %6s  %-16s  %-16s  %s\n",
		"TAG", "FIRES", "REPEATS", "NEG", "REPEAT 95%", "NEGATIVE 95%", "NOTES"))
	for _, r := range reports {
		notes := guidancetagNotes(r)
		b.WriteString(fmt.Sprintf("%-30s %6d %8d %6d  %-16s  %-16s  %s\n",
			truncateTag(r.Tag, 30),
			r.Fires, r.Repeats, r.NegativeHits,
			formatCI(r.RepeatRate, r.RepeatLo, r.RepeatHi, r.RepeatCIDefined),
			formatCI(r.NegRate, r.NegLo, r.NegHi, r.NegCIDefined),
			notes))
	}
	return b.String()
}

// guidancetagNotes implements the evidence-aware verdict policy:
//   - n < minSample   -> "insufficient evidence" (never judged on tiny n)
//   - repeatLo >= 0.5 -> guidance rarely sticks
//   - negLo    >= 0.3 -> high negative attribution
//   - repeatHi < 0.5 && negHi <= 0.1 -> clean, with the zero-hit upper
//     bound spelled out (zero failures != zero risk, NIST guidance).
func guidancetagNotes(r GuidanceTagReport) string {
	if r.Fires < guidanceMinSample {
		return fmt.Sprintf("insufficient evidence (n=%d)", r.Fires)
	}
	var notes []string
	if r.RepeatCIDefined && r.RepeatLo >= 0.5 {
		notes = append(notes, "repeat: guidance rarely sticks")
	}
	if r.NegCIDefined && r.NegLo >= 0.3 {
		notes = append(notes, "high negative attribution")
	}
	if len(notes) == 0 && r.RepeatCIDefined && r.RepeatHi < 0.5 && r.NegHi <= 0.1 {
		notes = append(notes, "clean")
		if r.NegativeHits == 0 {
			notes = append(notes, fmt.Sprintf("(0 neg hits, 95%% upper %.2f)", r.NegHi))
		}
	}
	if len(notes) == 0 {
		return "inconclusive"
	}
	return strings.Join(notes, "; ")
}

func truncateTag(tag string, max int) string {
	if len(tag) <= max {
		return tag
	}
	return tag[:max-1] + "~"
}

func ratio(num, den int) float64 {
	if den <= 0 {
		return 0
	}
	return float64(num) / float64(den)
}

func formatCI(rate, lo, hi float64, defined bool) string {
	if !defined {
		return "-"
	}
	return fmt.Sprintf("%.2f[%.2f,%.2f]", rate, lo, hi)
}

// wilsonInterval computes the Wilson score interval for a binomial
// proportion. Returns ok=false when n==0. Preferred over the Wald interval
// near 0/1 and for modest samples (NIST binomial-interval guidance).
func wilsonInterval(successes, n int, z float64) (lo, hi float64, ok bool) {
	if n <= 0 {
		return 0, 0, false
	}
	nf := float64(n)
	p := float64(successes) / nf
	z2 := z * z
	denom := 1 + z2/nf
	center := (p + z2/(2*nf)) / denom
	margin := z * math.Sqrt(p*(1-p)/nf+z2/(4*nf*nf)) / denom
	lo = math.Max(0, center-margin)
	hi = math.Min(1, center+margin)
	return lo, hi, true
}

// persist appends this run's aggregate as one JSONL line under
// <workDir>/.ggcode/guidance-stats.jsonl (best-effort; called once per run).
func (s *guidanceStats) persist(workDir string) {
	if s == nil || workDir == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.persisted || len(s.tags) == 0 {
		return
	}
	dir := filepath.Join(workDir, ".ggcode")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		debug.Log("guidance-stats", "persist skipped: %v", err)
		return
	}
	line := guidanceStatsLine{
		Session:   s.session,
		Started:   s.startedAt,
		Ended:     time.Now(),
		Negatives: s.negatives,
		Tags:      s.tags,
	}
	data, err := json.Marshal(line)
	if err != nil {
		debug.Log("guidance-stats", "persist marshal: %v", err)
		return
	}
	f, err := os.OpenFile(filepath.Join(dir, guidanceStatsFile), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		debug.Log("guidance-stats", "persist open: %v", err)
		return
	}
	defer f.Close()
	if _, err := f.Write(append(data, '\n')); err != nil {
		debug.Log("guidance-stats", "persist write: %v", err)
		return
	}
	s.persisted = true

	total := 0
	topTag, topFires := "", 0
	for tag, st := range s.tags {
		total += st.Fires
		if st.Fires > topFires {
			topTag, topFires = tag, st.Fires
		}
	}
	debug.Log("guidance-stats", "session %s: %d fires across %d tags (top: %s x%d), %d negative signals",
		s.session, total, len(s.tags), topTag, topFires, s.negatives)
}

// AggregateGuidanceStats loads the JSONL history for the project and
// aggregates per-tag counters across runs. Missing file -> (nil, 0, nil).
// Malformed lines are skipped.
func AggregateGuidanceStats(workDir string) (reports []GuidanceTagReport, runs int, err error) {
	path := filepath.Join(workDir, ".ggcode", guidanceStatsFile)
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, 0, nil
		}
		return nil, 0, err
	}
	defer f.Close()

	agg := make(map[string]*guidanceTagStats)
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var rec guidanceStatsLine
		if jsonErr := json.Unmarshal([]byte(line), &rec); jsonErr != nil {
			continue
		}
		runs++
		for tag, st := range rec.Tags {
			if st == nil {
				continue
			}
			cur := agg[tag]
			if cur == nil {
				cur = &guidanceTagStats{}
				agg[tag] = cur
			}
			cur.Fires += st.Fires
			cur.Repeats += st.Repeats
			cur.NegativeHits += st.NegativeHits
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, runs, err
	}

	reports = make([]GuidanceTagReport, 0, len(agg))
	for tag, st := range agg {
		r := GuidanceTagReport{
			Tag:          tag,
			Fires:        st.Fires,
			Repeats:      st.Repeats,
			NegativeHits: st.NegativeHits,
		}
		r.RepeatRate = ratio(st.Repeats, st.Fires)
		r.RepeatLo, r.RepeatHi, _ = wilsonInterval(st.Repeats, st.Fires, wilsonZ)
		r.RepeatCIDefined = st.Fires > 0
		r.NegRate = ratio(st.NegativeHits, st.Fires)
		r.NegLo, r.NegHi, _ = wilsonInterval(st.NegativeHits, st.Fires, wilsonZ)
		r.NegCIDefined = st.Fires > 0
		reports = append(reports, r)
	}
	sort.Slice(reports, func(i, j int) bool {
		if reports[i].Fires != reports[j].Fires {
			return reports[i].Fires > reports[j].Fires
		}
		return reports[i].Tag < reports[j].Tag
	})
	return reports, runs, nil
}

// recordGuidanceFire is the nil-safe hook helpers use on both delivery
// paths. Untagged guidance (loop-recovery nudges etc.) is not tracked.
func (a *Agent) recordGuidanceFire(text string) {
	if a == nil || a.guidanceStats == nil {
		return
	}
	tag := extractHintTag(text)
	if tag == "" {
		return
	}
	a.guidanceStats.recordFire(tag, time.Now())
}

// recordGuidanceNegative is the nil-safe hook used by the negative-signal
// sources (user sentiment, user file reverts).
func (a *Agent) recordGuidanceNegative() {
	if a == nil || a.guidanceStats == nil {
		return
	}
	a.guidanceStats.recordNegative(time.Now())
}
