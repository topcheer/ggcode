package memory

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
)

// Memory supersession ("change as evolution, not replacement").
//
// CheckContradiction (write guard) only WARNs: the conflicting pair stays
// live forever, and RecallArbitration re-annotates the same conflict on
// every recall. Research on agent memory in 2026 (Mem0 "State of AI Agent
// Memory" open problems; LongMemEval temporal/knowledge-update categories)
// identifies this as the missing lifecycle step: when a new memory carries
// EXPLICIT replacement intent for the same subject, the old entry should be
// retired from prompt injection while its history stays on disk — a
// structured transition instead of parallel-live conflicting claims.
//
// Conservative by design: supersession only fires when BOTH hold:
//  1. the new content contains an explicit active-voice replacement marker
//     ("replaces", "instead of", "switched to", "now use", "取代", "改用",
//     "不再", ...), and
//  2. the new content's claims conflict with the old entry's claims on the
//     same subject (same extractClaims/claimsConflict heuristics as the
//     write guard).
//
// A plain conflicting save without replacement intent keeps the existing
// warn-only behavior. Passive voice ("superseded by X") is deliberately NOT
// a marker: there the new entry describes being replaced by X, so X - not
// the new entry - would be the winner.
//
// Deterministic, zero LLM cost. Sidecar format: .ggcode/memory/.superseded.json
// { "entries": { "<old-key>": { "by": "<new-key>", "at": "<RFC3339>" } } }
const (
	// supersedeSidecarFile stores supersession edges next to the memory .md
	// files (same pattern as the .usage.json sidecar).
	supersedeSidecarFile = ".superseded.json"

	// maxSupersedeTargets caps how many old entries one save can retire,
	// mirroring maxContradictionConflicts.
	maxSupersedeTargets = 3
)

// replacementMarkers detect explicit active-voice replacement intent in the
// NEW memory content. Passive constructions ("replaced by", "superseded by")
// are intentionally excluded — see the file comment.
var replacementMarkers = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\breplaces\b`),
	regexp.MustCompile(`(?i)\bsupersedes\b`),
	regexp.MustCompile(`(?i)\binstead of\b`),
	regexp.MustCompile(`(?i)\bswitched to\b`),
	regexp.MustCompile(`(?i)\bmigrated? to\b`),
	regexp.MustCompile(`(?i)\bnow use\b`),
	regexp.MustCompile(`(?i)\bno longer\b`),
	regexp.MustCompile(`(?i)\bdeprecated\b`),
	regexp.MustCompile(`取代`),
	regexp.MustCompile(`改用`),
	regexp.MustCompile(`替换为`),
	regexp.MustCompile(`不再`),
	regexp.MustCompile(`弃用`),
}

// Replacement prose ("(replaces the old command)", "switched to X, jest is
// gone") dilutes token overlap below the conflict band [0.3, 0.85), so a
// genuine supersession can fail claimsConflict. Strip the commentary before
// extracting claims: values survive, transition prose does not. Claim
// extraction for the WRITE guard stays untouched - this normalization is
// supersession-only.
var (
	parentheticalRe = regexp.MustCompile(`\([^\n)]*\)`)
	markerClauseRe  = regexp.MustCompile(`(?i)[,;.]?\s*(?:instead of|replaces|supersedes|switched to|migrated to|now use|no longer|deprecated)\b[^\n]*`)
	cjkMarkerClause = regexp.MustCompile(`[,;。；]?\s*(?:取代|改用|替换为|不再|弃用)[^\n]*`)
)

func stripReplacementProse(content string) string {
	s := parentheticalRe.ReplaceAllString(content, " ")
	s = markerClauseRe.ReplaceAllString(s, " ")
	s = cjkMarkerClause.ReplaceAllString(s, " ")
	return s
}

// SupersedeRecord is one supersession edge: old key retired, replaced by
// the key in By at time At.
type SupersedeRecord struct {
	By string    `json:"by"`
	At time.Time `json:"at"`
}

type supersedeIndex struct {
	Entries map[string]*SupersedeRecord `json:"entries"`
}

// hasReplacementSemantics reports whether the content states an explicit
// active-voice replacement.
func hasReplacementSemantics(content string) bool {
	for _, re := range replacementMarkers {
		if re.MatchString(content) {
			return true
		}
	}
	return false
}

// DetectSupersession returns the disk keys of existing entries that the new
// entry (key, content) should retire: entries whose claims conflict with the
// new content on a shared subject, while the new content carries explicit
// replacement semantics. Empty when either condition fails.
func (am *AutoMemory) DetectSupersession(key, content string) []string {
	if !hasReplacementSemantics(content) {
		return nil
	}
	// Extract claims from the replacement-stripped content: the marker
	// clauses themselves are transition prose, not the claim value.
	newClaims := extractClaims(stripReplacementProse(content))
	if len(newClaims) == 0 {
		return nil
	}

	metas, err := am.collectMetas()
	if err != nil {
		debug.Log("memory", "supersede detect: failed to read dir %s: %v", am.dir, err)
		return nil
	}

	// Skip the key being saved (same derivation as the contradiction guard,
	// #1280: CJK/space keys get a hash suffix on disk).
	selfKey := disambiguateKey(key, sanitizeKey(key))

	var olds []string
	for _, m := range metas {
		if m.Key == selfKey {
			continue
		}
		path := filepath.Join(am.dir, m.Key+".md")
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		existingClaims := extractClaims(string(data))
		if len(existingClaims) == 0 {
			continue
		}
		for subject, newVal := range newClaims {
			existingVal, ok := existingClaims[subject]
			if !ok {
				continue
			}
			if claimsConflict(newVal, existingVal) {
				olds = append(olds, m.Key)
				break // one conflicting subject is enough to retire the entry
			}
		}
		if len(olds) >= maxSupersedeTargets {
			break
		}
	}
	return olds
}

// ApplySupersession records that the old keys are retired by key, and
// revives key itself if a previous supersession had retired it (a fresh
// explicit save wins back active status).
func (am *AutoMemory) ApplySupersession(key string, oldKeys []string) error {
	idx := am.loadSuperseded()
	if idx.Entries == nil {
		idx.Entries = make(map[string]*SupersedeRecord)
	}
	now := time.Now().UTC()
	for _, old := range oldKeys {
		if old == "" || old == key {
			continue
		}
		idx.Entries[old] = &SupersedeRecord{By: key, At: now}
	}
	// Revival: the saved key is active again.
	delete(idx.Entries, key)
	return am.saveSuperseded(idx)
}

// loadSuperseded reads the sidecar; a missing or corrupt file means "no
// supersessions" (never fail the memory pipeline over the sidecar).
func (am *AutoMemory) loadSuperseded() supersedeIndex {
	var idx supersedeIndex
	data, err := os.ReadFile(filepath.Join(am.dir, supersedeSidecarFile))
	if err != nil {
		return idx
	}
	if err := json.Unmarshal(data, &idx); err != nil {
		debug.Log("memory", "supersede sidecar corrupt in %s: %v", am.dir, err)
		return supersedeIndex{}
	}
	return idx
}

func (am *AutoMemory) saveSuperseded(idx supersedeIndex) error {
	if idx.Entries == nil {
		idx.Entries = make(map[string]*SupersedeRecord)
	}
	data, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(am.dir, 0755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(am.dir, supersedeSidecarFile), data, 0644)
}

// SupersededSet returns the set of retired keys for prompt-injection
// filtering (kept queryable via read_file; only prompt injection skips them).
func (am *AutoMemory) SupersededSet() map[string]bool {
	idx := am.loadSuperseded()
	if len(idx.Entries) == 0 {
		return nil
	}
	set := make(map[string]bool, len(idx.Entries))
	for k := range idx.Entries {
		set[k] = true
	}
	return set
}

// FormatSupersedeNote renders the save_memory result note for retired keys.
func FormatSupersedeNote(oldKeys []string) string {
	if len(oldKeys) == 0 {
		return ""
	}
	shown := oldKeys
	if len(shown) > maxSupersedeTargets {
		shown = shown[:maxSupersedeTargets]
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Superseded %d older %s (retired from prompt, kept on disk for history): ",
		len(oldKeys), pluralMemory(len(oldKeys))))
	b.WriteString(strings.Join(shown, ", "))
	return b.String()
}

func pluralMemory(n int) string {
	if n == 1 {
		return "memory"
	}
	return "memories"
}
