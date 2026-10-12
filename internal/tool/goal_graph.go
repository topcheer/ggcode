package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// GoalGraphTool provides a persistent, project-scoped hierarchical goal
// graph for long-horizon work: goal -> milestone -> task levels, per-node
// status lifecycle, progress rollup, cross-session resume, and stall
// detection. Unlike the session-scoped task board (swarm_task_*) and
// todo list, the goal graph persists in the workspace and is discovered
// by every new session, enabling break-point continuation of multi-day
// projects (frontier: ReAcTree arXiv:2511.02424, HIPIF hierarchical
// subgoal progress, TMS goal management).
//
// Storage: <workingDir>/.ggcode/goal-graph.json

const (
	ggMaxTitle     = 200
	ggMaxNextStep  = 500
	ggMaxNodes     = 300
	ggFileName     = "goal-graph.json"
	ggStaleAfter   = 7 * 24 * time.Hour
	ggResumeResult = 30
)

var ggLevels = map[string]bool{"goal": true, "milestone": true, "task": true}
var ggStatuses = map[string]bool{"pending": true, "in_progress": true, "done": true, "dropped": true}

type ggNode struct {
	ID        string    `json:"id"`
	Level     string    `json:"level"`
	Title     string    `json:"title"`
	Parent    string    `json:"parent,omitempty"`
	Status    string    `json:"status"`
	NextStep  string    `json:"next_step,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type ggStore struct {
	Nodes map[string]*ggNode `json:"nodes"`
}

type ggParams struct {
	Action   string `json:"action"`
	ID       string `json:"id"`
	Level    string `json:"level"`
	Title    string `json:"title"`
	Parent   string `json:"parent"`
	Status   string `json:"status"`
	NextStep string `json:"next_step"`
}

// GoalGraphTool is the durable hierarchical goal-graph tool.
type GoalGraphTool struct {
	WorkingDir string

	mu       sync.Mutex
	filePath string
	cache    *ggStore
	loaded   bool
}

func (t *GoalGraphTool) Name() string { return "goal_graph" }

func (t *GoalGraphTool) Description() string {
	return `Manage a persistent, project-scoped hierarchical goal graph for long-horizon work (goal -> milestone -> task). Persists across sessions in .ggcode/goal-graph.json so a new session can pick up where the last one stopped. Different from todo/task board: those are session-scoped; this is the durable goal layer. Use at the start of long projects (decompose the goal) and when resuming (action=resume returns in-progress leaves with their next steps). Actions: add, update, tree, resume, stats, delete.`
}

func (t *GoalGraphTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"action": {"type": "string", "enum": ["add", "update", "tree", "resume", "stats", "delete"], "description": "Action to perform"},
			"id": {"type": "string", "description": "Node ID (update, delete). Auto-generated from title if omitted on add."},
			"level": {"type": "string", "enum": ["goal", "milestone", "task"], "description": "Hierarchy level (add)"},
			"title": {"type": "string", "description": "Node title (add)"},
			"parent": {"type": "string", "description": "Parent node ID (add) - establishes the hierarchy"},
			"status": {"type": "string", "enum": ["pending", "in_progress", "done", "dropped"], "description": "Status (add, update). Parent progress rolls up automatically."},
			"next_step": {"type": "string", "description": "The concrete next action to continue this node (add, update) - surfaced by resume for break-point continuation"}
		},
		"required": ["action"]
	}`)
}

func (t *GoalGraphTool) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	var p ggParams
	if err := json.Unmarshal(input, &p); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	store, err := t.load()
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("failed to load goal graph: %v", err)}, nil
	}
	switch p.Action {
	case "add":
		return t.doAdd(store, &p)
	case "update":
		return t.doUpdate(store, &p)
	case "tree":
		return t.doTree(store)
	case "resume":
		return t.doResume(store)
	case "stats":
		return t.doStats(store)
	case "delete":
		return t.doDelete(store, &p)
	default:
		return Result{IsError: true, Content: fmt.Sprintf("unknown action %q", p.Action)}, nil
	}
}

func (t *GoalGraphTool) doAdd(s *ggStore, p *ggParams) (Result, error) {
	if p.Title == "" {
		return Result{IsError: true, Content: "title is required for add"}, nil
	}
	if len(p.Title) > ggMaxTitle {
		return Result{IsError: true, Content: fmt.Sprintf("title too long (max %d)", ggMaxTitle)}, nil
	}
	if len(p.NextStep) > ggMaxNextStep {
		return Result{IsError: true, Content: fmt.Sprintf("next_step too long (max %d)", ggMaxNextStep)}, nil
	}
	level := p.Level
	if level == "" {
		level = "task"
	}
	if !ggLevels[level] {
		return Result{IsError: true, Content: fmt.Sprintf("invalid level %q (goal, milestone, task)", p.Level)}, nil
	}
	status := p.Status
	if status == "" {
		status = "pending"
	}
	if !ggStatuses[status] {
		return Result{IsError: true, Content: fmt.Sprintf("invalid status %q", p.Status)}, nil
	}
	if len(s.Nodes) >= ggMaxNodes {
		return Result{IsError: true, Content: fmt.Sprintf("goal graph full (%d/%d nodes)", len(s.Nodes), ggMaxNodes)}, nil
	}
	if p.Parent != "" {
		parent, ok := s.Nodes[p.Parent]
		if !ok {
			return Result{IsError: true, Content: fmt.Sprintf("parent %q not found", p.Parent)}, nil
		}
		// Hierarchy must be strictly goal -> milestone -> task.
		if ggLevelRank(level) != ggLevelRank(parent.Level)+1 {
			return Result{IsError: true, Content: fmt.Sprintf("a %s cannot be nested under a %s (hierarchy is goal -> milestone -> task)", level, parent.Level)}, nil
		}
	}
	id := p.ID
	if id == "" {
		id = slugify(p.Title)
	}
	if len(id) > 80 {
		id = id[:80]
	}
	if idWasAuto := p.ID == ""; idWasAuto {
		for i := 2; ; i++ {
			ex, exists := s.Nodes[id]
			if !exists || strings.EqualFold(ex.Title, p.Title) {
				break
			}
			suffix := fmt.Sprintf("-%d", i)
			base := id
			if len(base)+len(suffix) > 80 {
				base = base[:80-len(suffix)]
			}
			id = base + suffix
		}
	}
	if _, exists := s.Nodes[id]; exists {
		return Result{IsError: true, Content: fmt.Sprintf("node %q already exists (use update)", id)}, nil
	}
	now := time.Now()
	s.Nodes[id] = &ggNode{ID: id, Level: level, Title: p.Title, Parent: p.Parent, Status: status, NextStep: p.NextStep, CreatedAt: now, UpdatedAt: now}
	if err := t.save(s); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("failed to save: %v", err)}, nil
	}
	return Result{Content: fmt.Sprintf("Added %s %q (id=%s, parent=%s, status=%s).", level, p.Title, id, displayParent(p.Parent), status)}, nil
}

func (t *GoalGraphTool) doUpdate(s *ggStore, p *ggParams) (Result, error) {
	n, ok := s.Nodes[p.ID]
	if !ok {
		return Result{IsError: true, Content: fmt.Sprintf("node %q not found", p.ID)}, nil
	}
	if p.Title != "" {
		if len(p.Title) > ggMaxTitle {
			return Result{IsError: true, Content: fmt.Sprintf("title too long (max %d)", ggMaxTitle)}, nil
		}
		n.Title = p.Title
	}
	if p.Status != "" {
		if !ggStatuses[p.Status] {
			return Result{IsError: true, Content: fmt.Sprintf("invalid status %q", p.Status)}, nil
		}
		n.Status = p.Status
	}
	if p.NextStep != "" {
		if len(p.NextStep) > ggMaxNextStep {
			return Result{IsError: true, Content: fmt.Sprintf("next_step too long (max %d)", ggMaxNextStep)}, nil
		}
		n.NextStep = p.NextStep
	}
	n.UpdatedAt = time.Now()
	if err := t.save(s); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("failed to save: %v", err)}, nil
	}
	rollup := t.rollupText(s, n.ID)
	return Result{Content: fmt.Sprintf("Updated %q: status=%s%s", n.Title, n.Status, rollup)}, nil
}

func (t *GoalGraphTool) doTree(s *ggStore) (Result, error) {
	if len(s.Nodes) == 0 {
		return Result{Content: "Goal graph is empty. Start with add level=goal title=... then decompose into milestones/tasks."}, nil
	}
	var b strings.Builder
	for _, g := range t.roots(s) {
		t.renderNode(s, g, "", &b)
	}
	return Result{Content: strings.TrimRight(b.String(), "\n")}, nil
}

func (t *GoalGraphTool) doResume(s *ggStore) (Result, error) {
	// Leaves that are not done/dropped, ordered goal-first for context.
	var leaves []*ggNode
	for _, n := range s.Nodes {
		if n.Status == "done" || n.Status == "dropped" {
			continue
		}
		if !t.hasChildren(s, n.ID) {
			leaves = append(leaves, n)
		}
	}
	if len(leaves) == 0 {
		if len(s.Nodes) == 0 {
			return Result{Content: "No goals tracked. Use add to decompose a long-horizon goal into milestones and tasks."}, nil
		}
		return Result{Content: "All tracked goals are complete or dropped."}, nil
	}
	sort.Slice(leaves, func(i, j int) bool { return ggLevelRank(leaves[i].Level) > ggLevelRank(leaves[j].Level) })
	if len(leaves) > ggResumeResult {
		leaves = leaves[:ggResumeResult]
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Resume: %d open leaf node(s).\n", len(leaves)))
	for _, n := range leaves {
		step := n.NextStep
		if step == "" {
			step = "(no next_step recorded - set one with update)"
		}
		b.WriteString(fmt.Sprintf("- [%s] %s (id=%s, status=%s, updated %s)\n  next: %s\n",
			n.Level, n.Title, n.ID, n.Status, n.UpdatedAt.Format("2006-01-02"), step))
	}
	return Result{Content: strings.TrimRight(b.String(), "\n")}, nil
}

func (t *GoalGraphTool) doStats(s *ggStore) (Result, error) {
	byLevel := map[string]int{"goal": 0, "milestone": 0, "task": 0}
	byStatus := map[string]int{}
	var stalled []string
	now := time.Now()
	for _, n := range s.Nodes {
		byLevel[n.Level]++
		byStatus[n.Status]++
		if n.Status == "in_progress" && now.Sub(n.UpdatedAt) > ggStaleAfter {
			stalled = append(stalled, fmt.Sprintf("%s %q (id=%s) in_progress but untouched for %.0f days",
				n.Level, n.Title, n.ID, now.Sub(n.UpdatedAt).Hours()/24))
		}
	}
	sort.Strings(stalled)
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Goal graph: %d nodes - goals=%d milestones=%d tasks=%d | pending=%d in_progress=%d done=%d dropped=%d\n",
		len(s.Nodes), byLevel["goal"], byLevel["milestone"], byLevel["task"],
		byStatus["pending"], byStatus["in_progress"], byStatus["done"], byStatus["dropped"]))
	if len(s.Nodes) > 0 {
		for _, g := range t.roots(s) {
			if g.Level == "goal" {
				progress := strings.TrimPrefix(t.rollupText(s, g.ID), " |")
				if progress != "" {
					b.WriteString(fmt.Sprintf("  goal %q:%s\n", g.Title, progress))
				}
			}
		}
	}
	if len(stalled) > 0 {
		b.WriteString(fmt.Sprintf("Stalled (>7d in_progress, consider update or drop):\n  %s\n", strings.Join(stalled, "\n  ")))
	}
	return Result{Content: strings.TrimRight(b.String(), "\n")}, nil
}

func (t *GoalGraphTool) doDelete(s *ggStore, p *ggParams) (Result, error) {
	if _, ok := s.Nodes[p.ID]; !ok {
		return Result{IsError: true, Content: fmt.Sprintf("node %q not found", p.ID)}, nil
	}
	// Cascade delete the subtree (a parent without its children is a broken goal).
	removed := t.deleteSubtree(s, p.ID)
	if err := t.save(s); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("failed to save: %v", err)}, nil
	}
	return Result{Content: fmt.Sprintf("Deleted %q and %d descendant(s) (%d total).", p.ID, removed-1, removed)}, nil
}

func (t *GoalGraphTool) deleteSubtree(s *ggStore, id string) int {
	n := s.Nodes[id]
	delete(s.Nodes, id)
	removed := 1
	if n == nil {
		return removed
	}
	for _, c := range s.Nodes {
		if c.Parent == id {
			removed += t.deleteSubtree(s, c.ID)
		}
	}
	return removed
}

// --- Helpers ---

func ggLevelRank(level string) int {
	switch level {
	case "goal":
		return 0
	case "milestone":
		return 1
	default:
		return 2
	}
}

func displayParent(p string) string {
	if p == "" {
		return "(root)"
	}
	return p
}

func (t *GoalGraphTool) roots(s *ggStore) []*ggNode {
	var out []*ggNode
	for _, n := range s.Nodes {
		if n.Parent == "" {
			out = append(out, n)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out
}

func (t *GoalGraphTool) children(s *ggStore, id string) []*ggNode {
	var out []*ggNode
	for _, n := range s.Nodes {
		if n.Parent == id {
			out = append(out, n)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out
}

func (t *GoalGraphTool) hasChildren(s *ggStore, id string) bool {
	for _, n := range s.Nodes {
		if n.Parent == id {
			return true
		}
	}
	return false
}

// rollupText computes descendant progress ("3/5 children done, 60%") for a
// node. A parent's own status is never auto-flipped (the agent owns goal
// semantics); rollup is reported so the agent can decide.
func (t *GoalGraphTool) rollupText(s *ggStore, id string) string {
	kids := t.children(s, id)
	if len(kids) == 0 {
		return ""
	}
	done := 0
	for _, c := range kids {
		if c.Status == "done" || c.Status == "dropped" {
			done++
		}
	}
	return fmt.Sprintf(" | %d/%d children done (%.0f%%)", done, len(kids), float64(done)/float64(len(kids))*100)
}

func (t *GoalGraphTool) renderNode(s *ggStore, n *ggNode, indent string, b *strings.Builder) {
	marker := " "
	switch n.Status {
	case "done":
		marker = "x"
	case "in_progress":
		marker = ">"
	case "dropped":
		marker = "-"
	}
	b.WriteString(fmt.Sprintf("%s[%s] %s (id=%s, %s%s)\n", indent, marker, n.Title, n.ID, n.Status, t.rollupText(s, n.ID)))
	childIndent := indent + "  "
	for _, c := range t.children(s, n.ID) {
		t.renderNode(s, c, childIndent, b)
	}
}

func (t *GoalGraphTool) Clone() Tool {
	return &GoalGraphTool{WorkingDir: t.WorkingDir}
}

func (t *GoalGraphTool) ggPath() string {
	if t.filePath != "" {
		return t.filePath
	}
	t.filePath = filepath.Join(t.WorkingDir, ".ggcode", ggFileName)
	return t.filePath
}

func (t *GoalGraphTool) load() (*ggStore, error) {
	if t.loaded {
		return t.cache, nil
	}
	data, err := os.ReadFile(t.ggPath())
	if err != nil {
		if os.IsNotExist(err) {
			t.cache = &ggStore{Nodes: make(map[string]*ggNode)}
			t.loaded = true
			return t.cache, nil
		}
		return nil, err
	}
	var s ggStore
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("corrupt goal graph: %w", err)
	}
	if s.Nodes == nil {
		s.Nodes = make(map[string]*ggNode)
	}
	t.cache = &s
	t.loaded = true
	return t.cache, nil
}

func (t *GoalGraphTool) save(s *ggStore) error {
	dir := filepath.Dir(t.ggPath())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := t.ggPath() + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	t.cache = s
	return os.Rename(tmp, t.ggPath())
}
