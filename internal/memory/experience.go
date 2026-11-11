package memory

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/debug"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
)

type ExperienceStore struct {
	dir string
	mu  sync.Mutex
}

const MaxExperienceCases = 50

const (
	spreadDecay     = 0.45
	spreadMaxAdmits = 2
	spreadMinScore  = 0.5
)

type Experience struct {
	ID       string
	Task     string
	Outcome  string
	Files    []string
	Approach string
	Created  time.Time
	Updated  time.Time
}

type ScoredExperience struct {
	Experience
	Score float64

	Via string
}

func outcomeRankWeight(outcome string) float64 {
	switch outcome {
	case "success":
		return 1.0
	case "partial":
		return 0.7
	case "failed":
		return 0.45
	default:
		return 0.85
	}
}

func NewExperienceStore(dir string) *ExperienceStore {
	if dir != "" {
		_ = os.MkdirAll(dir, 0755)
	}
	return &ExperienceStore{dir: dir}
}

func NewProjectExperienceStore(workingDir string) *ExperienceStore {
	home := config.HomeDir()
	if workingDir == "" || strings.EqualFold(workingDir, home) {
		return nil
	}
	return NewExperienceStore(filepath.Join(workingDir, ".ggcode", "memory", "experience"))
}

func (es *ExperienceStore) Dir() string { return es.dir }

func (es *ExperienceStore) Record(task, approach, outcome string, files []string) (id string, updated bool, err error) {
	if es == nil || es.dir == "" {
		return "", false, fmt.Errorf("experience store disabled")
	}
	task = normalizeTask(task)
	if task == "" {
		return "", false, fmt.Errorf("empty task")
	}
	if outcome != "success" && outcome != "partial" && outcome != "failed" {
		outcome = "partial"
	}

	sum := sha256.Sum256([]byte(task))
	id = hex.EncodeToString(sum[:6])

	es.mu.Lock()
	defer es.mu.Unlock()

	path := filepath.Join(es.dir, id+".md")
	now := time.Now().UTC()
	exp := Experience{
		ID:       id,
		Task:     task,
		Outcome:  outcome,
		Files:    dedupeStrings(files),
		Approach: strings.TrimSpace(approach),
		Created:  now,
		Updated:  now,
	}
	if existing, perr := readExperience(path); perr == nil {
		updated = true
		exp.Created = existing.Created
	}

	if werr := writeExperience(path, exp); werr != nil {
		return id, updated, werr
	}
	debugLogExperience("recorded id=%s task=%q outcome=%s updated=%v", id, oneLine(task, 60), outcome, updated)
	es.evictLocked()
	return id, updated, nil
}

func (es *ExperienceStore) Retrieve(query string, max int) ([]ScoredExperience, error) {
	return es.RetrieveExcluding(query, max, nil)
}

func (es *ExperienceStore) RetrieveExcluding(query string, max int, excludeIDs map[string]bool) ([]ScoredExperience, error) {
	if es == nil || es.dir == "" || max <= 0 {
		return nil, nil
	}
	cases, err := es.List()
	if err != nil || len(cases) == 0 {
		return nil, err
	}
	qTokens := expTokenize(query)
	if len(qTokens) == 0 {
		return nil, nil
	}

	qSet := tokenSet(qTokens)
	df := make(map[string]int)
	caseTokens := make([]map[string]int, 0, len(cases))
	for _, c := range cases {
		toks := tokenSet(expTokenize(c.Task + " " + strings.Join(c.Files, " ") + " " + c.Approach))
		caseTokens = append(caseTokens, toks)
		for t := range toks {
			df[t]++
		}
	}

	n := float64(len(cases))
	idxByID := make(map[string]int, len(cases))
	for i, c := range cases {
		idxByID[c.ID] = i
	}
	minDistinct := 2
	if len(qSet) < 2 {
		minDistinct = 1
	}
	var scored []ScoredExperience
	for i, c := range cases {
		if excludeIDs[c.ID] {
			continue
		}
		toks := caseTokens[i]
		distinct := 0
		var score float64
		for t, qCount := range qSet {
			tf := toks[t]
			if tf == 0 {
				continue
			}
			distinct++
			idf := math.Log(1 + n/float64(df[t]))
			score += idf * (float64(tf) / (float64(tf) + 1.2)) * float64(minInt(qCount, 3))
		}
		if distinct >= minDistinct && score >= 0.5 {

			score *= outcomeRankWeight(c.Outcome)
			scored = append(scored, ScoredExperience{Experience: c, Score: score})
		}
	}

	if len(scored) > 0 {
		admitted := make(map[string]bool, len(scored))
		for _, se := range scored {
			admitted[se.ID] = true
		}
		best := map[string]float64{}
		via := map[string]string{}
		for i, c := range cases {
			if excludeIDs[c.ID] || admitted[c.ID] {
				continue
			}
			for _, se := range scored {
				ls := expLinkStrength(caseTokens[i], caseTokens[idxByID[se.ID]], df, n)
				if ls <= 0 {
					continue
				}
				sc := spreadDecay * se.Score * ls
				if sc >= spreadMinScore && sc > best[c.ID] {
					best[c.ID] = sc
					via[c.ID] = se.ID
				}
			}
		}
		type spreadCand struct {
			id string
			sc float64
		}
		var cs []spreadCand
		for id, sc := range best {
			cs = append(cs, spreadCand{id, sc})
		}
		sort.Slice(cs, func(i, j int) bool { return cs[i].sc > cs[j].sc })
		for k := 0; k < len(cs) && k < spreadMaxAdmits; k++ {
			for _, c := range cases {
				if c.ID == cs[k].id {
					scored = append(scored, ScoredExperience{Experience: c, Score: cs[k].sc, Via: "spread:" + via[c.ID]})
				}
			}
		}
	}
	sort.Slice(scored, func(i, j int) bool {
		if scored[i].Score != scored[j].Score {
			return scored[i].Score > scored[j].Score
		}
		return scored[i].Updated.After(scored[j].Updated)
	})
	if len(scored) > max {
		scored = scored[:max]
	}
	return scored, nil
}

func expLinkStrength(cand, linker map[string]int, df map[string]int, n float64) float64 {
	var s float64
	for t := range cand {
		if linker[t] == 0 {
			continue
		}
		idf := math.Log(1 + n/float64(df[t]))
		if strings.Contains(t, "/") {
			s += 2 * idf
		} else {
			s += idf
		}
	}
	return math.Min(s, 1.0)
}

func (es *ExperienceStore) List() ([]Experience, error) {
	if es == nil || es.dir == "" {
		return nil, nil
	}
	entries, err := os.ReadDir(es.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []Experience
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		if c, perr := readExperience(filepath.Join(es.dir, e.Name())); perr == nil {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created.Before(out[j].Created) })
	return out, nil
}

func (es *ExperienceStore) FormatIndex(query string, max int) string {
	idx, _ := es.FormatIndexTracked(query, max)
	return idx
}

func (es *ExperienceStore) FormatIndexTracked(query string, max int) (string, []string) {
	scored, err := es.Retrieve(query, max)
	if err != nil || len(scored) == 0 {
		return "", nil
	}
	ids := make([]string, 0, len(scored))
	for _, s := range scored {

		if s.Via != "" {
			continue
		}
		ids = append(ids, s.ID)
	}
	return formatScoredExperiences(scored), ids
}

func (es *ExperienceStore) FormatIndexExcluding(query string, max int, excludeIDs map[string]bool) string {
	scored, err := es.RetrieveExcluding(query, max, excludeIDs)
	if err != nil || len(scored) == 0 {
		return ""
	}
	return formatScoredExperiences(scored)
}

// FormatIndexPointerTruncated is the P1 prompt-slimming variant (#5446): one
// line per case instead of the 3-line body (Task/Files/Approach), plus a
// recall pointer. Full detail stays retrievable via the recall_experience
// tool - the injection only seeds discovery.
func (es *ExperienceStore) FormatIndexPointerTruncated(query string, max int) (string, []string) {
	scored, err := es.Retrieve(query, max)
	if err != nil || len(scored) == 0 {
		return "", nil
	}
	ids := make([]string, 0, len(scored))
	var b strings.Builder
	b.WriteString("Past experience with similar tasks exists (case-based memory). One-line index only - for a case that looks like the current task, call the recall_experience tool with its keywords BEFORE re-deriving an approach:\n")
	for _, s := range scored {
		if s.Via != "" {
			continue
		}
		ids = append(ids, s.ID)
		b.WriteString(fmt.Sprintf("- [%s] %s\n", s.Outcome, oneLine(s.Task, 80)))
	}
	if len(ids) == 0 {
		return "", nil
	}
	return b.String(), ids
}

func formatScoredExperiences(scored []ScoredExperience) string {
	var b strings.Builder
	b.WriteString("Past experience with similar tasks in this project (case-based memory):\n")
	for _, s := range scored {
		b.WriteString(fmt.Sprintf("- [outcome: %s] Task: %s\n", s.Outcome, oneLine(s.Task, 120)))
		if len(s.Files) > 0 {
			b.WriteString(fmt.Sprintf("  Files touched: %s\n", oneLine(strings.Join(s.Files, ", "), 120)))
		}
		if approach := strings.TrimSpace(s.Approach); approach != "" {
			b.WriteString(fmt.Sprintf("  Approach: %s\n", oneLine(approach, 240)))
		}
	}
	b.WriteString("Treat these as hints about what worked before — verify against current code, not blind recipe.\n")
	return b.String()
}

func (es *ExperienceStore) evictLocked() {
	cases, err := es.List()
	if err != nil || len(cases) <= MaxExperienceCases {
		return
	}

	byUpdated := make([]Experience, len(cases))
	copy(byUpdated, cases)
	sort.Slice(byUpdated, func(i, j int) bool { return byUpdated[i].Updated.Before(byUpdated[j].Updated) })
	excess := len(byUpdated) - MaxExperienceCases
	for i := 0; i < excess; i++ {
		_ = os.Remove(filepath.Join(es.dir, byUpdated[i].ID+".md"))
	}
}

func readExperience(path string) (Experience, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Experience{}, err
	}
	lines := strings.Split(string(data), "\n")
	var exp Experience
	inBody := false
	var body []string
	for _, line := range lines {
		if inBody {
			body = append(body, line)
			continue
		}
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			inBody = true
			continue
		}
		key, value, ok := strings.Cut(trimmed, ":")
		if !ok {

			inBody = true
			body = append(body, line)
			continue
		}
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "task":
			exp.Task = strings.TrimSpace(value)
		case "outcome":
			exp.Outcome = strings.TrimSpace(value)
		case "created":
			exp.Created = parseTime(value)
		case "updated":
			exp.Updated = parseTime(value)
		case "files":
			for _, f := range strings.Split(value, ",") {
				f = strings.TrimSpace(f)
				if f != "" {
					exp.Files = append(exp.Files, f)
				}
			}
		case "id":
			exp.ID = strings.TrimSpace(value)
		default:

		}
	}
	if exp.ID == "" {
		base := strings.TrimSuffix(filepath.Base(path), ".md")
		exp.ID = base
	}
	if exp.Updated.IsZero() {
		exp.Updated = exp.Created
	}
	exp.Approach = strings.TrimSpace(strings.Join(body, "\n"))
	if exp.Task == "" {
		return Experience{}, fmt.Errorf("case %s has no task header", path)
	}
	return exp, nil
}

func writeExperience(path string, exp Experience) error {
	var b strings.Builder
	fmt.Fprintf(&b, "id: %s\n", exp.ID)
	fmt.Fprintf(&b, "task: %s\n", oneLine(exp.Task, 300))
	fmt.Fprintf(&b, "outcome: %s\n", exp.Outcome)
	fmt.Fprintf(&b, "created: %s\n", exp.Created.UTC().Format(time.RFC3339))
	fmt.Fprintf(&b, "updated: %s\n", exp.Updated.UTC().Format(time.RFC3339))
	fmt.Fprintf(&b, "files: %s\n", strings.Join(dedupeStrings(exp.Files), ", "))
	b.WriteString("\n")
	if exp.Approach != "" {
		b.WriteString(strings.TrimSpace(exp.Approach))
		b.WriteString("\n")
	}

	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.WriteString(b.String()); err != nil {
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

func normalizeTask(task string) string {
	fields := strings.FieldsFunc(strings.ToLower(task), func(r rune) bool {
		return unicode.IsSpace(r)
	})
	return strings.Join(fields, " ")
}

func expTokenize(s string) []string {
	var toks []string
	var cur []rune
	flush := func() {
		if len(cur) >= 2 {
			toks = append(toks, string(cur))
		}
		cur = cur[:0]
	}
	for _, r := range strings.ToLower(s) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):

			if r >= 0x2E80 {
				flush()
				toks = append(toks, string(r))
			} else {
				cur = append(cur, r)
			}
		default:
			flush()
		}
	}
	flush()
	return toks
}

func tokenSet(toks []string) map[string]int {
	set := make(map[string]int, len(toks))
	for _, t := range toks {
		set[t]++
	}
	return set
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func parseTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(s))
	if err != nil {
		return time.Time{}
	}
	return t
}

func dedupeStrings(in []string) []string {
	seen := make(map[string]bool, len(in))
	var out []string
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

func oneLine(s string, maxRunes int) string {
	s = strings.Join(strings.Fields(s), " ")
	runes := []rune(s)
	if len(runes) > maxRunes {
		return string(runes[:maxRunes]) + "..."
	}
	return s
}

func debugLogExperience(format string, args ...interface{}) {
	debug.Log("memory", "experience: "+format, args...)
}
