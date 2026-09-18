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

// ExperienceStore implements case-based reasoning memory (Memento, arXiv:2508.16153;
// "From Storage to Experience", ACL 2026 Findings): each completed run is
// distilled into an Experience case (task, approach, outcome, files touched)
// instead of being merged into a single rolling insights blob. At the start of
// a new run the k most relevant past cases are retrieved (lexical IDF scoring,
// no embeddings) and injected as few-shot procedural guidance, so hard-won
// knowledge about HOW tasks of this shape were completed compounds across
// sessions.
//
// Cases live in <memoryDir>/experience/ as one markdown file per case. The
// parent AutoMemory reader walks only top-level *.md files (directories are
// skipped), so the subdirectory never leaks into the regular memory index.
type ExperienceStore struct {
	dir string
	mu  sync.Mutex
}

// MaxExperienceCases bounds the store; recording beyond the cap evicts the
// oldest cases. Experiences decay: the freshest procedural knowledge is what
// a codebase's current reality validates.
const MaxExperienceCases = 50

// Experience is a single distilled case.
type Experience struct {
	ID       string
	Task     string
	Outcome  string // "success", "partial", or "failed"
	Files    []string
	Approach string
	Created  time.Time
	Updated  time.Time
}

// ScoredExperience pairs a case with its relevance score for a query.
type ScoredExperience struct {
	Experience
	Score float64
}

// NewExperienceStore builds a store rooted at dir (the "experience"
// subdirectory itself, created on demand). A nil-safe empty root is allowed
// for tests.
func NewExperienceStore(dir string) *ExperienceStore {
	if dir != "" {
		_ = os.MkdirAll(dir, 0755)
	}
	return &ExperienceStore{dir: dir}
}

// NewProjectExperienceStore builds the experience store for a working
// directory's project memory (<workingDir>/.ggcode/memory/experience/).
// Returns nil when workingDir is the user's HOME (mirrors NewProjectAutoMemory:
// never treat HOME as a project root).
func NewProjectExperienceStore(workingDir string) *ExperienceStore {
	home := config.HomeDir()
	if workingDir == "" || strings.EqualFold(workingDir, home) {
		return nil
	}
	return NewExperienceStore(filepath.Join(workingDir, ".ggcode", "memory", "experience"))
}

// Dir returns the backing directory ("" when disabled).
func (es *ExperienceStore) Dir() string { return es.dir }

// Record distills a completed run into a case. Re-recording a task whose
// normalized form matches an existing case UPDATES that case (fresh outcome
// and approach, original Created preserved) instead of duplicating it —
// reconsolidation upon retrieval (Microsoft HMA): repeated evidence that the
// same approach works strengthens rather than forks the memory.
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

// Retrieve scores stored cases against query (lexical IDF overlap) and
// returns up to max matches above the relevance floor, best first.
func (es *ExperienceStore) Retrieve(query string, max int) ([]ScoredExperience, error) {
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

	// Document frequency over the case set, for IDF weighting.
	qSet := tokenSet(qTokens)
	df := make(map[string]int)
	caseTokens := make([]map[string]int, len(cases))
	for i, c := range cases {
		toks := tokenSet(expTokenize(c.Task + " " + strings.Join(c.Files, " ") + " " + c.Approach))
		caseTokens[i] = toks
		for t := range toks {
			df[t]++
		}
	}

	n := float64(len(cases))
	minDistinct := 2
	if len(qSet) < 2 {
		minDistinct = 1
	}
	var scored []ScoredExperience
	for i, c := range cases {
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
			scored = append(scored, ScoredExperience{Experience: c, Score: score})
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

// List returns all cases, oldest first.
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

// FormatIndex renders the retrieval prompt section: top matched cases as
// compact procedural guidance. Returns "" when nothing is relevant, so
// callers can skip injection entirely (no prompt noise for cold stores).
func (es *ExperienceStore) FormatIndex(query string, max int) string {
	scored, err := es.Retrieve(query, max)
	if err != nil || len(scored) == 0 {
		return ""
	}
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

// evictLocked trims the store to MaxExperienceCases, oldest Created first.
// Caller must hold es.mu.
func (es *ExperienceStore) evictLocked() {
	cases, err := es.List()
	if err != nil || len(cases) <= MaxExperienceCases {
		return
	}
	excess := len(cases) - MaxExperienceCases
	for i := 0; i < excess; i++ {
		_ = os.Remove(filepath.Join(es.dir, cases[i].ID+".md"))
	}
}

// readExperience parses one case file. Lenient: unknown/missing header keys
// keep zero values; only a missing Task invalidates the case.
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
			// Header ended without a blank line separator.
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
			// Future-proof: unknown header keys are ignored, not body.
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

// writeExperience serializes a case atomically (temp + rename, same pattern
// as AutoMemory.SaveMemory) so concurrent readers never see a torn file.
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

// normalizeTask collapses whitespace/case so the same logical task maps to
// one case id regardless of formatting.
func normalizeTask(task string) string {
	fields := strings.FieldsFunc(strings.ToLower(task), func(r rune) bool {
		return unicode.IsSpace(r)
	})
	return strings.Join(fields, " ")
}

// expTokenize lowercases and splits on non-alphanumeric runes; tokens shorter
// than 2 runes are dropped (CJK runes are kept individually — each is a word).
// Named with the exp- prefix: package memory already has a tokenize helper
// (duplicate_check.go) with different semantics.
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
			// CJK: each rune is a word unit; Latin/digits accumulate.
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

// tokenSet counts token frequencies.
func tokenSet(toks []string) map[string]int {
	set := make(map[string]int, len(toks))
	for _, t := range toks {
		set[t]++
	}
	return set
}

// minInt keeps an explicit helper so package memory never shadows the
// Go 1.21+ builtin min for its other files.
func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// parseTime is a lenient RFC3339 reader.
func parseTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(s))
	if err != nil {
		return time.Time{}
	}
	return t
}

// dedupeStrings preserves order while dropping duplicates/empties.
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

// oneLine collapses newlines and hard-truncates on a rune boundary.
func oneLine(s string, maxRunes int) string {
	s = strings.Join(strings.Fields(s), " ")
	runes := []rune(s)
	if len(runes) > maxRunes {
		return string(runes[:maxRunes]) + "..."
	}
	return s
}

// debugLogExperience records store activity for the memory debug category;
// no-op on nil store keeps call sites terse.
func debugLogExperience(format string, args ...interface{}) {
	debug.Log("memory", "experience: "+format, args...)
}
