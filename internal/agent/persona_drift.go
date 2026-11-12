package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Cross-session constraint anchors (#persona-drift, P0). Research basis:
// Nautilus Compass (arXiv:2605.09863) - production coding agents silently
// forget user-stated constraints, regress to user-corrected mistakes, and
// invent prior agreements; behavioral anchors persisted across sessions with
// no-LLM keyword matching detect drift (ROC AUC 0.83). ggcode's 8 in-session
// drift detectors all reset every run; this store persists the contract.

type ConstraintAnchor struct {
	ID           string    `json:"id"`
	Text         string    `json:"text"`
	Source       string    `json:"source"` // "explicit" | "correction"
	CreatedAt    time.Time `json:"createdAt"`
	Violations   int       `json:"violations"`
	Affirmations int       `json:"affirmations"`
}

type AnchorStore struct {
	mu       sync.Mutex
	path     string
	boundDir string             // workspace this store is bound to (#5498)
	Anchors  []ConstraintAnchor `json:"anchors"`
}

// AnchorPath returns the persistence path under the given workspace.
func AnchorPath(workspace string) string {
	return filepath.Join(workspace, ".ggcode", "anchors.json")
}

func LoadAnchorStore(path string) *AnchorStore {
	s := &AnchorStore{path: path}
	data, err := os.ReadFile(path)
	if err != nil {
		return s
	}
	if json.Unmarshal(data, s) != nil {
		s.Anchors = nil
	}
	return s
}

func (s *AnchorStore) save() {
	if s == nil || s.path == "" {
		return
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return
	}
	_ = os.MkdirAll(filepath.Dir(s.path), 0o755)
	_ = os.WriteFile(s.path, data, 0o600)
}

// anchorConstraintPatterns: EN+CN constraint phrasing used for forbidden-key
// extraction. In-session EN extraction reuses constraint_amnesia's richer
// constraintPatterns; this file adds the cross-session persistence layer.
var anchorConstraintPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(do\s+not|don't|never|must\s+not|no\s+longer)\s+([a-z][^.;\n]{3,80})`),
	regexp.MustCompile(`(?i)must\s+(use|go|run|be|stay|keep|always)\s+([a-z][^.;\n]{3,80})`),
	regexp.MustCompile(`(?i)always\s+(use|run|include|check|verify)\s+([a-z][^.;\n]{3,80})`),
	regexp.MustCompile(`(不要|禁止|不得|不许)([^\n。；;]{2,40})`),
	regexp.MustCompile(`(必须|一定要|务必|铁律)([^\n。；;]{2,40})`),
}

// anchorCnPatterns: CN-only subset also fed into extraction (the shared
// constraint_amnesia patterns are EN-only).
var anchorCnPatterns = anchorConstraintPatterns[3:]

// ExtractConstraints pulls constraint sentences from a user message and
// persists new anchors (deduped by normalized text).
func (s *AnchorStore) ObserveUserMessage(text string) int {
	if s == nil || strings.TrimSpace(text) == "" {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	added := 0
	seenText := map[string]bool{}
	candidates := extractConstraints(text)
	for _, re := range anchorCnPatterns {
		for _, m := range re.FindAllString(text, 3) {
			candidates = append(candidates, strings.TrimSpace(m))
		}
	}
	for _, full := range candidates {
		full = strings.TrimSpace(strings.Join(strings.Fields(full), " "))
		if len(full) < 6 || seenText[strings.ToLower(full)] || s.hasLocked(full) {
			continue
		}
		seenText[strings.ToLower(full)] = true
		s.Anchors = append(s.Anchors, ConstraintAnchor{
			ID:        fmt.Sprintf("anchor-%d", time.Now().UnixNano()),
			Text:      full,
			Source:    "explicit",
			CreatedAt: time.Now(),
		})
		added++
	}
	const maxAnchors = 50
	if len(s.Anchors) > maxAnchors {
		s.Anchors = s.Anchors[len(s.Anchors)-maxAnchors:]
	}
	if added > 0 {
		s.save()
	}
	return added
}

func (s *AnchorStore) hasLocked(norm string) bool {
	for _, a := range s.Anchors {
		if strings.EqualFold(a.Text, norm) {
			return true
		}
	}
	return false
}

// forbiddenModalWords: modal/auxiliary tokens never meaningful as targets.
var forbiddenModalWords = map[string]bool{
	"never": true, "always": true, "must": true, "avoid": true,
	"only": true, "should": true, "don't": true,
}

// forbiddenKeywords splits a prohibition anchor into word-level content
// keywords for target scanning (whole-phrase matching misses paraphrased
// tool targets).
func forbiddenKeywords(anchorText string) []string {
	var kws []string
	seen := map[string]bool{}
	for _, re := range anchorConstraintPatterns {
		for _, m := range re.FindAllStringSubmatch(anchorText, 1) {
			for _, g := range m[1:] {
				for _, w := range strings.Fields(g) {
					w := strings.ToLower(cleanToken(w))
					if len(w) < 4 || forbiddenModalWords[w] || seen[w] {
						continue
					}
					seen[w] = true
					kws = append(kws, w)
				}
			}
		}
	}
	return kws
}

// CheckViolation scans one tool call's target against prohibition anchors.
// Returns an advisory when a forbidden keyword appears in the target.
func (s *AnchorStore) CheckViolation(toolName, target string) string {
	if s == nil || len(s.Anchors) == 0 || strings.TrimSpace(target) == "" {
		return ""
	}
	lower := strings.ToLower(target)
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.Anchors {
		a := &s.Anchors[i]
		for _, kw := range forbiddenKeywords(a.Text) {
			if strings.Contains(lower, kw) {
				a.Violations++
				s.save()
				return fmt.Sprintf(
					"[anchor-violation] tool %s touches %q which conflicts with the persisted user constraint %q (violations: %d). "+
						"The user asked for this constraint in a previous exchange - honor it or explicitly ask before overriding.",
					toolName, kw, a.Text, a.Violations)
			}
		}
	}
	return ""
}

// RenderAnchorsSection builds the system-prompt section listing persisted
// constraints (the affirmation surface; empty when none).
func (s *AnchorStore) RenderAnchorsSection() string {
	if s == nil || len(s.Anchors) == 0 {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var b strings.Builder
	fmt.Fprintf(&b, "## Persisted User Constraints (behavioral anchors)\n")
	fmt.Fprintf(&b, "These constraints were stated by the user in past exchanges and persist across sessions. Honor them unless the user explicitly overrides:\n")
	n := len(s.Anchors)
	if n > 8 {
		n = 8
	}
	for i := len(s.Anchors) - n; i < len(s.Anchors); i++ {
		a := s.Anchors[i]
		vio := ""
		if a.Violations > 0 {
			vio = fmt.Sprintf(" [%d violation(s) on record]", a.Violations)
		}
		fmt.Fprintf(&b, "- %s%s\n", a.Text, vio)
	}
	return b.String()
}
