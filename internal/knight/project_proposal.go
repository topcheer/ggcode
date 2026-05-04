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

	result := k.RunTaskWithTurns(ctx, "project-improvement-proposal", prompt, factory, 6)
	if result.Error != nil {
		return ProjectImprovementProposal{}, result, result.Error
	}
	content := strings.TrimSpace(result.Output)
	if content == "" {
		return ProjectImprovementProposal{}, result, fmt.Errorf("knight: proposal output is empty")
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
	id := now.Format("20060102-150405") + "-" + slugifyProjectProposal(title)
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
