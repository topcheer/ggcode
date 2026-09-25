// Package spec implements spec-driven development artifact tracking for
// ggcode (Spec Kit / AWS Kiro style).
//
// Frontier grounding (2025-2026):
//   - GitHub Spec Kit: specify → plan → tasks → implement, with artifacts
//     re-anchored into the agent context at every phase
//     (https://github.com/github/spec-kit).
//   - TDAD (arXiv 2603.17973): agents regress less when given *contextual*
//     information (which requirements to verify) rather than procedural
//     instructions — the "TDD Prompting Paradox".
//   - ggcode already ships a bundled "spec" skill (procedural template) and
//     plan_drift.go (per-run exit_plan_mode reconciliation). What was missing:
//     persistent artifact state, an active-spec selection, and per-turn
//     grounding injection so the spec survives context drift in long sessions.
//
// Conventions:
//   - Artifacts live in <workingDir>/specs/<slug>/ mirroring the bundled skill
//     (requirements.md, design.md, tasks.md — plain markdown, committable).
//   - Runtime state (active slug) lives in <workingDir>/.ggcode/spec-state.json
//     (per-workspace, like playbook.json; not a spec artifact).
//   - Progress = markdown checkbox counts across the spec's .md files.
package spec

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const (
	specsDirName  = "specs"
	stateDirName  = ".ggcode"
	stateFileName = "spec-state.json"
	maxSlugLen    = 64
	maxUnchecked  = 5
	maxLineLen    = 160
	maxTitleLen   = 120
)

// Spec is one feature spec directory under specs/.
type Spec struct {
	Slug  string `json:"slug"`
	Dir   string `json:"dir"`
	Title string `json:"title"`
}

// Progress summarizes checkbox state across a spec's markdown artifacts.
type Progress struct {
	Done  int
	Total int
}

// Complete reports whether every tracked requirement is checked off.
func (p Progress) Complete() bool { return p.Total > 0 && p.Done >= p.Total }

var slugRe = regexp.MustCompile(`[^a-z0-9-]+`)

// SanitizeSlug normalizes a user-provided slug: lowercase, [a-z0-9-] only,
// collapsed dashes, trimmed, bounded length. Returns an error when nothing
// usable remains.
func SanitizeSlug(raw string) (string, error) {
	s := strings.ToLower(strings.TrimSpace(raw))
	s = slugRe.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	for strings.Contains(s, "--") {
		s = strings.ReplaceAll(s, "--", "-")
	}
	if s == "" {
		return "", fmt.Errorf("spec slug is empty after sanitization")
	}
	if len(s) > maxSlugLen {
		s = s[:maxSlugLen]
	}
	return s, nil
}

// SpecsRoot returns the specs artifact directory for a workspace.
func SpecsRoot(workingDir string) string {
	if workingDir == "" {
		return ""
	}
	return filepath.Join(workingDir, specsDirName)
}

// LoadAll lists every spec under <workingDir>/specs/, sorted by slug.
// A missing specs directory is not an error (empty result).
func LoadAll(workingDir string) ([]Spec, error) {
	root := SpecsRoot(workingDir)
	if root == "" {
		return nil, nil
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var specs []Spec
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(root, e.Name())
		specs = append(specs, Spec{
			Slug:  e.Name(),
			Dir:   dir,
			Title: specTitle(dir, e.Name()),
		})
	}
	sort.Slice(specs, func(i, j int) bool { return specs[i].Slug < specs[j].Slug })
	return specs, nil
}

// specTitle extracts the first markdown H1 from the spec's requirements.md
// (or any .md fallback), trimming to a bounded length.
func specTitle(dir, fallback string) string {
	candidates := []string{"requirements.md", "spec.md", "design.md", "tasks.md"}
	for _, name := range candidates {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "# ") && len(line) > 2 {
				t := strings.TrimSpace(line[2:])
				if len(t) > maxTitleLen {
					t = t[:maxTitleLen]
				}
				return t
			}
		}
	}
	return fallback
}

// Create scaffolds a new spec directory with a requirements.md skeleton
// (EARS-like acceptance criteria, matching the bundled spec skill style)
// and an empty tasks.md checklist.
func Create(workingDir, rawSlug, title string) (*Spec, error) {
	slug, err := SanitizeSlug(rawSlug)
	if err != nil {
		return nil, err
	}
	root := SpecsRoot(workingDir)
	if root == "" {
		return nil, fmt.Errorf("empty working directory")
	}
	dir := filepath.Join(root, slug)
	if _, err := os.Stat(dir); err == nil {
		return nil, fmt.Errorf("spec %q already exists", slug)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	if title == "" {
		title = slug
	}
	req := fmt.Sprintf("# %s\n\n## Requirements\n\n<!-- Add user stories and EARS-like acceptance criteria as `- [ ]` checkboxes. -->\n\n- Requirement 1: ...\n- Requirement 2: ...\n", title)
	if err := os.WriteFile(filepath.Join(dir, "requirements.md"), []byte(req), 0o644); err != nil {
		return nil, err
	}
	tasks := "# Tasks\n\n<!-- Verifiable implementation steps as `- [ ]` checkboxes; check items off as they are verified. -->\n\n- Task 1: ...\n"
	if err := os.WriteFile(filepath.Join(dir, "tasks.md"), []byte(tasks), 0o644); err != nil {
		return nil, err
	}
	return &Spec{Slug: slug, Dir: dir, Title: title}, nil
}

// state is the persisted active-spec selection.
type state struct {
	Active string `json:"active"`
}

func statePath(workingDir string) string {
	if workingDir == "" {
		return ""
	}
	return filepath.Join(workingDir, stateDirName, stateFileName)
}

func loadState(workingDir string) state {
	p := statePath(workingDir)
	if p == "" {
		return state{}
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return state{}
	}
	var s state
	if err := json.Unmarshal(data, &s); err != nil {
		return state{} // corrupt state: treat as inactive, never fail the run
	}
	return s
}

func saveState(workingDir string, s state) error {
	p := statePath(workingDir)
	if p == "" {
		return fmt.Errorf("empty working directory")
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return os.WriteFile(p, data, 0o644)
}

// SetActive marks an existing spec as active. Errors when the spec directory
// does not exist (prevents grounding the agent against a phantom spec).
func SetActive(workingDir, slug string) error {
	dir := filepath.Join(SpecsRoot(workingDir), slug)
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return fmt.Errorf("spec %q not found", slug)
	}
	return saveState(workingDir, state{Active: slug})
}

// ClearActive deactivates any active spec.
func ClearActive(workingDir string) error {
	return saveState(workingDir, state{Active: ""})
}

// Active returns the currently active spec, or nil when none is active or
// the active slug no longer exists on disk.
func Active(workingDir string) *Spec {
	s := loadState(workingDir)
	if s.Active == "" {
		return nil
	}
	dir := filepath.Join(SpecsRoot(workingDir), s.Active)
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return nil
	}
	return &Spec{Slug: s.Active, Dir: dir, Title: specTitle(dir, s.Active)}
}

// Progress counts markdown checkboxes across all .md files in a spec dir.
// Unreadable/missing files count as zero; the function never fails.
func (s Spec) Progress() Progress {
	var p Progress
	entries, err := os.ReadDir(s.Dir)
	if err != nil {
		return p
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(s.Dir, e.Name()))
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			t := strings.TrimSpace(line)
			switch {
			case strings.HasPrefix(t, "- [x]") || strings.HasPrefix(t, "- [X]"):
				p.Done++
				p.Total++
			case strings.HasPrefix(t, "- [ ]"):
				p.Total++
			}
		}
	}
	return p
}

// nextUnchecked returns up to maxUnchecked unchecked checklist item texts.
func (s Spec) nextUnchecked() []string {
	var items []string
	entries, err := os.ReadDir(s.Dir)
	if err != nil {
		return items
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(s.Dir, e.Name()))
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			t := strings.TrimSpace(line)
			if !strings.HasPrefix(t, "- [ ]") {
				continue
			}
			item := strings.TrimSpace(strings.TrimPrefix(t, "- [ ]"))
			if item == "" {
				continue
			}
			if len(item) > maxLineLen {
				item = item[:maxLineLen] + "..."
			}
			items = append(items, item)
			if len(items) >= maxUnchecked {
				return items
			}
		}
	}
	return items
}

// PromptInjection renders the active-spec grounding block for the system
// prompt, or "" when no spec is active. Bounded in size (context-rot
// awareness): at most maxUnchecked unchecked items, each truncated.
func PromptInjection(workingDir string) string {
	if workingDir == "" {
		return ""
	}
	s := Active(workingDir)
	if s == nil {
		return ""
	}
	rel := filepath.Join(specsDirName, s.Slug)
	var b strings.Builder
	fmt.Fprintf(&b, "[Active spec: %s — %s]\n", rel, s.Title)
	b.WriteString("This session is grounded to a spec-driven workflow. Re-read the spec ")
	b.WriteString("artifacts when unsure about scope; implement what the spec requires, ")
	b.WriteString("nothing else, and keep its checklists in sync with reality.\n")
	p := s.Progress()
	if p.Total > 0 {
		fmt.Fprintf(&b, "Requirement progress: %d/%d checked.\n", p.Done, p.Total)
	}
	if p.Complete() {
		b.WriteString("All checklist items are checked off — finish verification (build/test) ")
		b.WriteString("and summarize; do not start unrelated work.\n")
		return strings.TrimRight(b.String(), "\n")
	}
	if items := s.nextUnchecked(); len(items) > 0 {
		b.WriteString("Next unchecked items (work toward these; mark [x] only after build/test verification):\n")
		for _, it := range items {
			fmt.Fprintf(&b, "- %s\n", it)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}
