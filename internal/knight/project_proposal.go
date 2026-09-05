package knight

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"os/exec"

	"github.com/topcheer/ggcode/internal/util"
)

type ProjectImprovementProposal struct {
	ID        string    `json:"id"`
	Time      time.Time `json:"time"`
	Goal      string    `json:"goal"`
	Title     string    `json:"title"`
	Summary   string    `json:"summary,omitempty"`
	Path      string    `json:"path"`
	CreatedBy string    `json:"created_by"`
	// Lifecycle
	Status     string    `json:"status,omitempty"` // proposed | approved | rejected
	StatusNote string    `json:"status_note,omitempty"`
	StatusBy   string    `json:"status_by,omitempty"`
	StatusAt   time.Time `json:"status_at,omitempty"`
}

var projectProposalSlugPattern = regexp.MustCompile(`[^a-z0-9._-]+`)

// filterKnightBookkeeping drops porcelain status lines whose path is inside
// `.ggcode/` — knight's own bookkeeping domain (budget usage jsonl appended
// per LLM call DURING the run, staging/queue/semantic-memory writes from the
// background tick racing a user-initiated proposal). Most repos are unaffected
// (.ggcode gitignored or collapsed into one `?? .ggcode/` line that appends
// cannot change), but repos where `.ggcode/` was committed list new jsonl
// files line-by-line, and the proposal's own budget recording would trip the
// guardrail. Paths outside `.ggcode/` keep full protection.
func filterKnightBookkeeping(snapshot string) string {
	var kept []string
	for _, line := range strings.Split(snapshot, "\n") {
		if line == "" {
			continue
		}
		// porcelain: "XY <path>" (or "XY <orig> -> <path>"); strip the 2-char
		// status plus space, keep quoting intact for paths with spaces.
		rest := line
		if len(rest) > 3 {
			rest = rest[3:]
		}
		// #1576-C / #1617-A: the exemption exists for INTERNAL bookkeeping
		// writes only (the proposal's own budget/usage appends). A rename
		// crosses the boundary when EITHER side is outside .ggcode:
		//   '.ggcode/x -> out'  - the write LANDS in the project (visible);
		//   'tracked.txt -> .ggcode/h' - a project file is MOVED INTO the
		//    bookkeeping dir (a stash-the-changes channel the guard must
		//    see; the original DESTINATION-only test swallowed the line).
		// Filter only when BOTH source and destination are inside .ggcode.
		src := rest
		dst := rest
		if idx := strings.Index(rest, " -> "); idx >= 0 {
			src = rest[:idx]
			dst = rest[idx+4:]
		}
		inGG := func(p string) bool {
			return strings.HasPrefix(strings.TrimPrefix(strings.TrimSpace(p), "\""), ".ggcode/")
		}
		if inGG(src) && inGG(dst) {
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}

// countStatusDiff counts status lines present in exactly one of the two
// porcelain snapshots (set difference both ways) for the violation message.
// Bookkeeping-domain lines are filtered out first (#1261 review: the
// proposal's own budget/usage appends must not read as violations).
func countStatusDiff(before, after string) int {
	drop := func(s string) map[string]bool {
		m := map[string]bool{}
		for _, l := range strings.Split(filterKnightBookkeeping(s), "\n") {
			if l != "" {
				m[l] = true
			}
		}
		return m
	}
	bs := drop(before)
	as := drop(after)
	n := 0
	for l := range bs {
		if !as[l] {
			n++
		}
	}
	for l := range as {
		if !bs[l] {
			n++
		}
	}
	return n
}

// gitStatusSnapshot returns the working-tree status of dir as porcelain
// lines, or "" when git is unavailable or dir is not a repository (in that
// case the post-run comparison degenerates to a no-op and the prompt rules
// remain the only guard).
func gitStatusSnapshot(dir string) string {
	out, err := exec.Command("git", "-C", dir, "status", "--porcelain").Output()
	if err != nil {
		return ""
	}
	return string(out)
}

func (k *Knight) GenerateProjectImprovementProposal(ctx context.Context, goal string) (ProjectImprovementProposal, TaskResult, error) {
	goal = strings.TrimSpace(goal)
	if goal == "" {
		return ProjectImprovementProposal{}, TaskResult{}, fmt.Errorf("knight: empty proposal goal")
	}
	factory := k.getFactory()
	if factory == nil {
		return ProjectImprovementProposal{}, TaskResult{}, fmt.Errorf("knight: proposal runner unavailable")
	}

	prompt := fmt.Sprintf(`Create a reviewable project-improvement proposal for the current project.

Goal: %s

Requirements:
- Do NOT modify project source files, configuration files, tests, or documentation.
- Produce only a proposal document for human review.
- Prefer small, reversible improvements that can be validated with existing project checks.
- Include concrete target files or areas when known, but do not invent exact diffs if you have not inspected the code.
- Include a rollback/risk section.

Return Markdown with these sections:
# <short proposal title>
## Summary
## Proposed Changes
## Validation Plan
## Risks and Rollback`, goal)

	// #1261: the "do not modify the project" guardrail used to live in the
	// prompt only, while the factory injected the full interactive tool
	// registry - a real unauthorized-write path. Snapshot the working tree
	// before and after the run; any drift means the LLM wrote despite the
	// rules, so the proposal is discarded and the violation surfaced loudly
	// (no automatic rollback - reverting user-visible state automatically
	// from a background agent would be its own hazard).
	treeBefore := gitStatusSnapshot(k.projDir)
	result := k.RunTaskWithTurns(ctx, "project-improvement-proposal", prompt, factory, 6)
	if result.Error != nil {
		return ProjectImprovementProposal{}, result, result.Error
	}
	content := strings.TrimSpace(result.Output)
	if content == "" {
		return ProjectImprovementProposal{}, result, fmt.Errorf("knight: proposal output is empty")
	}
	// Compare BEFORE writing our own proposal artifact below - knight's own
	// .ggcode write is expected and must not trip the guardrail. Both sides
	// are filtered to the non-bookkeeping domain (#1261 review).
	treeAfter := filterKnightBookkeeping(gitStatusSnapshot(k.projDir))
	if treeAfter != filterKnightBookkeeping(treeBefore) {
		diffLines := countStatusDiff(treeBefore, gitStatusSnapshot(k.projDir))
		return ProjectImprovementProposal{}, result, fmt.Errorf(
			"knight: READ-ONLY GUARDRAIL VIOLATED: the proposal task modified the working tree (%d changed status line(s)); run `git status` / `git diff` to review. Proposal discarded",
			diffLines)
	}
	proposal, err := k.writeProjectImprovementProposal(goal, content)
	if err != nil {
		return ProjectImprovementProposal{}, result, err
	}
	k.emitReport(fmt.Sprintf("📝 Knight project proposal created: %s\nReview with /knight proposals %s", proposal.Title, proposal.ID))
	return proposal, result, nil
}

func (k *Knight) RecentProjectImprovementProposals(limit int) ([]ProjectImprovementProposal, error) {
	if k == nil {
		return nil, nil
	}
	items, err := readProjectProposalLog(k.projectProposalLogPath())
	if err != nil {
		return nil, err
	}
	// Collapse multiple log entries per id to the latest one so lifecycle
	// transitions (proposed → approved/rejected) are reflected without
	// rewriting the append-only log.
	latest := map[string]ProjectImprovementProposal{}
	order := []string{}
	for _, item := range items {
		if _, seen := latest[item.ID]; !seen {
			order = append(order, item.ID)
		}
		prev, ok := latest[item.ID]
		if !ok {
			latest[item.ID] = item
			continue
		}
		if item.StatusAt.After(prev.StatusAt) || (item.StatusAt.IsZero() && item.Time.After(prev.Time)) {
			// Preserve original creation time/path even when later status entries
			// omit them.
			merged := item
			if merged.Time.IsZero() {
				merged.Time = prev.Time
			}
			if merged.Path == "" {
				merged.Path = prev.Path
			}
			if merged.Goal == "" {
				merged.Goal = prev.Goal
			}
			if merged.Title == "" {
				merged.Title = prev.Title
			}
			if merged.CreatedBy == "" {
				merged.CreatedBy = prev.CreatedBy
			}
			latest[item.ID] = merged
		}
	}
	collapsed := make([]ProjectImprovementProposal, 0, len(order))
	for _, id := range order {
		collapsed = append(collapsed, latest[id])
	}
	sort.SliceStable(collapsed, func(i, j int) bool {
		return collapsed[i].Time.After(collapsed[j].Time)
	})
	if limit > 0 && len(collapsed) > limit {
		collapsed = collapsed[:limit]
	}
	return collapsed, nil
}

// ApproveProposal marks a stored proposal as user-approved. Knight does not
// implement approved proposals on its own; the user runs implementation as a
// separate normal agent task. Approved proposals also seed semantic memory so
// future Knight runs remember the lesson across sessions.
func (k *Knight) ApproveProposal(id, note string) (ProjectImprovementProposal, error) {
	prop, err := k.transitionProposal(id, "approved", note, "user")
	if err != nil {
		return prop, err
	}
	summary := prop.Title
	if summary == "" {
		summary = prop.Goal
	}
	if note != "" {
		summary = summary + " — " + note
	}
	_ = k.RecordSemanticMemory("project-proposal-approved", summary, []string{"proposal:" + prop.ID}, prop.Path)
	return prop, nil
}

// RejectProposal marks a stored proposal as user-rejected and prevents future
// proposal listings from showing it as still pending.
func (k *Knight) RejectProposal(id, note string) (ProjectImprovementProposal, error) {
	prop, err := k.transitionProposal(id, "rejected", note, "user")
	if err != nil {
		return prop, err
	}
	summary := prop.Title
	if summary == "" {
		summary = prop.Goal
	}
	if note != "" {
		summary = summary + " — rejected: " + note
	} else {
		summary = summary + " — rejected by user"
	}
	_ = k.RecordSemanticMemory("project-proposal-rejected", summary, []string{"proposal:" + prop.ID}, prop.Path)
	return prop, nil
}

func (k *Knight) transitionProposal(id, status, note, by string) (ProjectImprovementProposal, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return ProjectImprovementProposal{}, fmt.Errorf("knight: empty proposal id")
	}
	items, err := k.RecentProjectImprovementProposals(0)
	if err != nil {
		return ProjectImprovementProposal{}, err
	}
	for _, item := range items {
		if item.ID != id {
			continue
		}
		// #1576-A: terminal states are terminal - a REJECTED proposal
		// (e.g. delete-skill) must not flip to approved by one stray
		// command, and vice versa. Re-applying the SAME terminal state is
		// idempotent (returns the current record untouched).
		if item.Status == "approved" || item.Status == "rejected" {
			if item.Status != status {
				return ProjectImprovementProposal{}, fmt.Errorf("proposal %q is already %s (terminal); create a new proposal instead", id, item.Status)
			}
			return item, nil
		}
		now := time.Now()
		item.Status = status
		item.StatusNote = strings.TrimSpace(note)
		item.StatusBy = by
		item.StatusAt = now
		if err := appendProjectProposalLog(k.projectProposalLogPath(), item); err != nil {
			return ProjectImprovementProposal{}, err
		}
		return item, nil
	}
	return ProjectImprovementProposal{}, fmt.Errorf("proposal %q not found", id)
}

func (k *Knight) ReadProjectImprovementProposal(id string) (ProjectImprovementProposal, string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return ProjectImprovementProposal{}, "", fmt.Errorf("knight: empty proposal id")
	}
	items, err := k.RecentProjectImprovementProposals(0)
	if err != nil {
		return ProjectImprovementProposal{}, "", err
	}
	for _, item := range items {
		if item.ID != id {
			continue
		}
		content, err := os.ReadFile(item.Path)
		if err != nil {
			return ProjectImprovementProposal{}, "", fmt.Errorf("read proposal %q: %w", id, err)
		}
		return item, string(content), nil
	}
	return ProjectImprovementProposal{}, "", fmt.Errorf("proposal %q not found", id)
}

func (k *Knight) writeProjectImprovementProposal(goal, content string) (ProjectImprovementProposal, error) {
	now := time.Now()
	title := extractProjectProposalTitle(content)
	if title == "" {
		title = "Project Improvement Proposal"
		content = "# " + title + "\n\n" + content
	}
	// #1576-B: second-resolution IDs collided for same-second same-title
	// proposals - AtomicWriteFile overwrote the first .md and the jsonl
	// collapse kept only the latest, silently losing the first proposal.
	// Millisecond suffix disambiguates.
	id := now.Format("20060102-150405") + "-" + slugifyProjectProposal(title) + "-" + now.Format("150405.000000000")[9:]
	if len(id) > 80 {
		id = id[:80]
	}
	dir := filepath.Join(k.projDir, ".ggcode", "project-proposals")
	path := filepath.Join(dir, id+".md")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return ProjectImprovementProposal{}, err
	}
	body := fmt.Sprintf(`---
id: %s
created_at: %s
created_by: knight
status: proposed
goal: %q
---

%s
`, id, now.Format(time.RFC3339), goal, content)
	if err := util.AtomicWriteFile(path, []byte(body), 0644); err != nil {
		return ProjectImprovementProposal{}, fmt.Errorf("write project proposal: %w", err)
	}
	proposal := ProjectImprovementProposal{
		ID:        id,
		Time:      now,
		Goal:      goal,
		Title:     title,
		Summary:   extractProjectProposalSummary(content),
		Path:      path,
		CreatedBy: "knight",
		Status:    "proposed",
		StatusAt:  now,
	}
	if err := appendProjectProposalLog(k.projectProposalLogPath(), proposal); err != nil {
		return ProjectImprovementProposal{}, err
	}
	return proposal, nil
}

func (k *Knight) projectProposalLogPath() string {
	return filepath.Join(k.projDir, ".ggcode", "project-proposals.jsonl")
}

func appendProjectProposalLog(path string, proposal ProjectImprovementProposal) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	line, err := json.Marshal(proposal)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(line, '\n'))
	return err
}

func readProjectProposalLog(path string) ([]ProjectImprovementProposal, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	var items []ProjectImprovementProposal
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(strings.TrimSpace(string(line))) == 0 {
			continue
		}
		var item ProjectImprovementProposal
		if err := json.Unmarshal(line, &item); err != nil {
			continue
		}
		items = append(items, item)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

func extractProjectProposalTitle(content string) string {
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "# ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "# "))
		}
	}
	return ""
}

func extractProjectProposalSummary(content string) string {
	lines := strings.Split(content, "\n")
	inSummary := false
	var parts []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "## ") {
			if inSummary {
				break
			}
			inSummary = strings.EqualFold(strings.TrimSpace(strings.TrimPrefix(trimmed, "## ")), "summary")
			continue
		}
		if inSummary && trimmed != "" {
			parts = append(parts, trimmed)
		}
	}
	return truncateRunes(strings.Join(parts, " "), 240)
}

func slugifyProjectProposal(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = projectProposalSlugPattern.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-._")
	if s == "" {
		return "proposal"
	}
	return s
}
