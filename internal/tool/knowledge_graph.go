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
	"unicode/utf8"
)

// KnowledgeGraphTool provides a persistent, project-scoped knowledge graph
// where the agent records structured facts about the codebase - architectural
// decisions, design patterns, key entities, known issues - and the relationships
// between them. Unlike flat key-value memory (save_memory), the graph supports
// typed nodes, typed edges, status lifecycle (proposed -> accepted -> superseded),
// and relationship traversal (trace).
//
// Storage: <workingDir>/.ggcode/knowledge-graph.json
//
// Competitor gap: No major AI coding agent (Claude Code, Cursor, Cline,
// OpenHands, Aider, Windsurf) maintains a structured, queryable knowledge
// graph. They rely on flat files (CLAUDE.md, .cursorrules) or ephemeral
// per-session context.

const (
	kgMaxTitle      = 200
	kgMaxContent    = 4000
	kgMaxTags       = 8
	kgMaxNodes      = 500
	kgMaxEdges      = 1000
	kgFileName      = "knowledge-graph.json"
	kgQueryResults  = 30
	kgTraceMaxDepth = 5
	kgTraceMaxNodes = 50
)

var kgNodeTypes = map[string]bool{
	"decision": true, "pattern": true, "entity": true, "issue": true, "note": true,
}

var kgEdgeTypes = map[string]bool{
	"depends-on": true, "supersedes": true, "relates-to": true,
	"implements": true, "contradicts": true, "evolves-from": true,
}

type kgNode struct {
	ID        string    `json:"id"`
	Type      string    `json:"type"`
	Title     string    `json:"title"`
	Content   string    `json:"content,omitempty"`
	Tags      []string  `json:"tags,omitempty"`
	Status    string    `json:"status,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type kgEdge struct {
	From string `json:"from"`
	To   string `json:"to"`
	Type string `json:"type"`
}

type kgStore struct {
	Nodes map[string]*kgNode `json:"nodes"`
	Edges []kgEdge           `json:"edges"`
}

type kgParams struct {
	Action  string   `json:"action"`
	ID      string   `json:"id"`
	Type    string   `json:"type"`
	Title   string   `json:"title"`
	Content string   `json:"content"`
	Tags    []string `json:"tags"`
	Status  string   `json:"status"`
	To      string   `json:"to"`
	Query   string   `json:"query"`
}

// KnowledgeGraphTool implements the knowledge_graph tool.
type KnowledgeGraphTool struct {
	WorkingDir string

	mu       sync.Mutex
	filePath string
	cache    *kgStore
	loaded   bool
}

func (t *KnowledgeGraphTool) Name() string { return "knowledge_graph" }

func (t *KnowledgeGraphTool) Description() string {
	return `Manage a persistent knowledge graph for the codebase. Records structured facts (decisions, patterns, entities, issues) and their relationships (depends-on, supersedes, relates-to) that accumulate across sessions. Different from save_memory: this is structured domain knowledge about the codebase, not behavioral rules for the agent. Actions: add, link, query, list, delete, trace, stats.`
}

func (t *KnowledgeGraphTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"action": {"type": "string", "enum": ["add", "link", "query", "list", "delete", "trace", "stats"], "description": "Action to perform"},
			"id": {"type": "string", "description": "Node ID (for delete, trace, link). Auto-generated from title if omitted on add."},
			"type": {"type": "string", "description": "Node type (add) or edge type (link). Nodes: decision, pattern, entity, issue, note. Edges: depends-on, supersedes, relates-to, implements, contradicts, evolves-from"},
			"title": {"type": "string", "description": "Node title (add, query)"},
			"content": {"type": "string", "description": "Node content/detail (add)"},
			"tags": {"type": "array", "items": {"type": "string"}, "description": "Tags for categorization (add, query filter)"},
			"status": {"type": "string", "description": "Lifecycle status for decisions: proposed, accepted, superseded, rejected (add)"},
			"to": {"type": "string", "description": "Target node ID (link)"},
			"query": {"type": "string", "description": "Search text matching title/content/tags (query)"}
		},
		"required": ["action"]
	}`)
}

func (t *KnowledgeGraphTool) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	var p kgParams
	if err := json.Unmarshal(input, &p); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	store, err := t.load()
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("failed to load knowledge graph: %v", err)}, nil
	}

	switch p.Action {
	case "add":
		return t.doAdd(store, &p)
	case "link":
		return t.doLink(store, &p)
	case "query":
		return t.doQuery(store, &p)
	case "list":
		return t.doList(store)
	case "delete":
		return t.doDelete(store, &p)
	case "trace":
		return t.doTrace(store, &p)
	case "stats":
		return t.doStats(store)
	default:
		return Result{IsError: true, Content: fmt.Sprintf("unknown action %q", p.Action)}, nil
	}
}

func (t *KnowledgeGraphTool) doAdd(s *kgStore, p *kgParams) (Result, error) {
	if p.Title == "" {
		return Result{IsError: true, Content: "title is required for add"}, nil
	}
	if len(p.Title) > kgMaxTitle {
		return Result{IsError: true, Content: fmt.Sprintf("title too long (max %d)", kgMaxTitle)}, nil
	}
	if len(p.Content) > kgMaxContent {
		return Result{IsError: true, Content: fmt.Sprintf("content too long (max %d)", kgMaxContent)}, nil
	}
	nt := p.Type
	if nt == "" {
		nt = "note"
	}
	if !kgNodeTypes[nt] {
		return Result{IsError: true, Content: fmt.Sprintf("invalid node type %q", nt)}, nil
	}
	if len(p.Tags) > kgMaxTags {
		return Result{IsError: true, Content: fmt.Sprintf("too many tags (max %d)", kgMaxTags)}, nil
	}
	if len(s.Nodes) >= kgMaxNodes {
		return Result{IsError: true, Content: fmt.Sprintf("graph full (%d/%d nodes)", len(s.Nodes), kgMaxNodes)}, nil
	}

	id := p.ID
	if id == "" {
		id = slugify(p.Title)
	}
	if len(id) > 80 {
		id = id[:80]
	}

	now := time.Now()
	if ex, ok := s.Nodes[id]; ok {
		// #1327: Type had no non-empty guard (Title/Content/Tags/Status all
		// do) - a partial update carrying id+title but no type silently
		// reclassified decision/entity nodes as note, and the next save
		// persisted the demotion.
		if p.Type != "" {
			ex.Type = nt
		}
		if p.Title != "" {
			ex.Title = p.Title
		}
		// #844: Content was unconditionally assigned - an update carrying only
		// status=superseded erased the node's accumulated text. Guard like
		// Tags/Status so partial updates preserve prior content.
		if p.Content != "" {
			ex.Content = p.Content
		}
		if p.Tags != nil {
			ex.Tags = p.Tags
		}
		if p.Status != "" {
			ex.Status = p.Status
		}
		ex.UpdatedAt = now
	} else {
		s.Nodes[id] = &kgNode{ID: id, Type: nt, Title: p.Title, Content: p.Content, Tags: p.Tags, Status: p.Status, CreatedAt: now, UpdatedAt: now}
	}

	if err := t.save(s); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("failed to save: %v", err)}, nil
	}
	return Result{Content: fmt.Sprintf("Node '%s' (%s) saved. Total: %d nodes.", id, nt, len(s.Nodes))}, nil
}

func (t *KnowledgeGraphTool) doLink(s *kgStore, p *kgParams) (Result, error) {
	if p.ID == "" || p.To == "" {
		return Result{IsError: true, Content: "both id (source) and to (target) required"}, nil
	}
	if !kgEdgeTypes[p.Type] {
		return Result{IsError: true, Content: fmt.Sprintf("invalid edge type %q", p.Type)}, nil
	}
	if _, ok := s.Nodes[p.ID]; !ok {
		return Result{IsError: true, Content: fmt.Sprintf("source %q not found", p.ID)}, nil
	}
	if _, ok := s.Nodes[p.To]; !ok {
		return Result{IsError: true, Content: fmt.Sprintf("target %q not found", p.To)}, nil
	}
	for _, e := range s.Edges {
		if e.From == p.ID && e.To == p.To && e.Type == p.Type {
			return Result{Content: fmt.Sprintf("Edge %s --[%s]--> %s already exists.", p.ID, p.Type, p.To)}, nil
		}
	}
	if len(s.Edges) >= kgMaxEdges {
		return Result{IsError: true, Content: fmt.Sprintf("edge limit (%d)", kgMaxEdges)}, nil
	}
	s.Edges = append(s.Edges, kgEdge{From: p.ID, To: p.To, Type: p.Type})
	if err := t.save(s); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("failed to save: %v", err)}, nil
	}
	return Result{Content: fmt.Sprintf("Linked: %s --[%s]--> %s. Total edges: %d.", p.ID, p.Type, p.To, len(s.Edges))}, nil
}

func (t *KnowledgeGraphTool) doQuery(s *kgStore, p *kgParams) (Result, error) {
	q := strings.ToLower(p.Query)
	var matches []*kgNode
	for _, n := range s.Nodes {
		if p.Type != "" && n.Type != p.Type {
			continue
		}
		if q == "" {
			matches = append(matches, n)
		} else {
			hay := strings.ToLower(n.Title + " " + n.Content + " " + strings.Join(n.Tags, " "))
			if strings.Contains(hay, q) {
				matches = append(matches, n)
			}
		}
	}
	if len(matches) == 0 {
		return Result{Content: "No matching nodes found."}, nil
	}
	sort.Slice(matches, func(i, j int) bool { return matches[i].UpdatedAt.After(matches[j].UpdatedAt) })
	if len(matches) > kgQueryResults {
		matches = matches[:kgQueryResults]
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Found %d node(s):\n\n", len(matches)))
	for _, n := range matches {
		formatNodeShort(&sb, n)
	}
	return Result{Content: sb.String()}, nil
}

func (t *KnowledgeGraphTool) doList(s *kgStore) (Result, error) {
	if len(s.Nodes) == 0 {
		return Result{Content: "Knowledge graph is empty. Use action='add' to create your first node."}, nil
	}
	byType := make(map[string][]*kgNode)
	for _, n := range s.Nodes {
		byType[n.Type] = append(byType[n.Type], n)
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Knowledge Graph: %d nodes, %d edges\n\n", len(s.Nodes), len(s.Edges)))
	for _, typ := range []string{"decision", "pattern", "entity", "issue", "note"} {
		nodes := byType[typ]
		if len(nodes) == 0 {
			continue
		}
		sort.Slice(nodes, func(i, j int) bool { return nodes[i].UpdatedAt.After(nodes[j].UpdatedAt) })
		sb.WriteString(fmt.Sprintf("[%s] (%d)\n", typ, len(nodes)))
		for _, n := range nodes {
			st := ""
			if n.Status != "" {
				st = fmt.Sprintf(" (%s)", n.Status)
			}
			sb.WriteString(fmt.Sprintf("  - %s%s: %s\n", n.ID, st, n.Title))
		}
		sb.WriteString("\n")
	}
	return Result{Content: sb.String()}, nil
}

func (t *KnowledgeGraphTool) doDelete(s *kgStore, p *kgParams) (Result, error) {
	if p.ID == "" {
		return Result{IsError: true, Content: "id is required for delete"}, nil
	}
	if _, ok := s.Nodes[p.ID]; !ok {
		return Result{IsError: true, Content: fmt.Sprintf("node %q not found", p.ID)}, nil
	}
	delete(s.Nodes, p.ID)
	newEdges := s.Edges[:0]
	rm := 0
	for _, edge := range s.Edges {
		if edge.From == p.ID || edge.To == p.ID {
			rm++
			continue
		}
		newEdges = append(newEdges, edge)
	}
	s.Edges = newEdges
	if err := t.save(s); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("failed to save: %v", err)}, nil
	}
	return Result{Content: fmt.Sprintf("Deleted '%s' + %d edges. Remaining: %d nodes, %d edges.", p.ID, rm, len(s.Nodes), len(s.Edges))}, nil
}

func (t *KnowledgeGraphTool) doTrace(s *kgStore, p *kgParams) (Result, error) {
	if p.ID == "" {
		return Result{IsError: true, Content: "id is required for trace"}, nil
	}
	if _, ok := s.Nodes[p.ID]; !ok {
		return Result{IsError: true, Content: fmt.Sprintf("node %q not found", p.ID)}, nil
	}
	visited := map[string]bool{p.ID: true}
	type fi struct {
		id    string
		depth int
	}
	queue := []fi{{p.ID, 0}}
	var lines []string
	count := 0
	for len(queue) > 0 && count < kgTraceMaxNodes {
		item := queue[0]
		queue = queue[1:]
		node := s.Nodes[item.id]
		if node == nil {
			continue
		}
		if item.depth == 0 {
			lines = append(lines, fmt.Sprintf("[%s] %s", node.Type, node.Title))
		}
		if item.depth >= kgTraceMaxDepth {
			continue
		}
		for _, e := range s.Edges {
			if e.From != item.id || visited[e.To] {
				continue
			}
			visited[e.To] = true
			tgt := s.Nodes[e.To]
			if tgt == nil {
				continue
			}
			ind := strings.Repeat("  ", item.depth+1)
			lines = append(lines, fmt.Sprintf("%s--[%s]--> [%s] %s", ind, e.Type, tgt.Type, tgt.Title))
			queue = append(queue, fi{e.To, item.depth + 1})
			count++
		}
	}
	if len(lines) <= 1 {
		return Result{Content: fmt.Sprintf("Node '%s' has no outgoing relationships.", p.ID)}, nil
	}
	return Result{Content: fmt.Sprintf("Trace from '%s':\n%s", p.ID, strings.Join(lines, "\n"))}, nil
}

func (t *KnowledgeGraphTool) doStats(s *kgStore) (Result, error) {
	bt := map[string]int{}
	bs := map[string]int{}
	for _, n := range s.Nodes {
		bt[n.Type]++
		if n.Status != "" {
			bs[n.Status]++
		}
	}
	be := map[string]int{}
	for _, e := range s.Edges {
		be[e.Type]++
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Knowledge Graph: %d/%d nodes, %d/%d edges\n", len(s.Nodes), kgMaxNodes, len(s.Edges), kgMaxEdges))
	for _, typ := range []string{"decision", "pattern", "entity", "issue", "note"} {
		if bt[typ] > 0 {
			sb.WriteString(fmt.Sprintf("  %s: %d\n", typ, bt[typ]))
		}
	}
	if len(bs) > 0 {
		sb.WriteString("Statuses:\n")
		for _, st := range []string{"proposed", "accepted", "superseded", "rejected"} {
			if bs[st] > 0 {
				sb.WriteString(fmt.Sprintf("  %s: %d\n", st, bs[st]))
			}
		}
	}
	if len(be) > 0 {
		sb.WriteString("Edges:\n")
		for _, et := range []string{"depends-on", "supersedes", "relates-to", "implements", "contradicts", "evolves-from"} {
			if be[et] > 0 {
				sb.WriteString(fmt.Sprintf("  %s: %d\n", et, be[et]))
			}
		}
	}
	return Result{Content: sb.String()}, nil
}

// --- Persistence ---

func (t *KnowledgeGraphTool) kgPath() string {
	if t.filePath != "" {
		return t.filePath
	}
	t.filePath = filepath.Join(t.WorkingDir, ".ggcode", kgFileName)
	return t.filePath
}

func (t *KnowledgeGraphTool) load() (*kgStore, error) {
	if t.loaded {
		return t.cache, nil
	}
	data, err := os.ReadFile(t.kgPath())
	if err != nil {
		if os.IsNotExist(err) {
			t.cache = &kgStore{Nodes: make(map[string]*kgNode), Edges: []kgEdge{}}
			t.loaded = true
			return t.cache, nil
		}
		return nil, err
	}
	var s kgStore
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("corrupt knowledge graph: %w", err)
	}
	if s.Nodes == nil {
		s.Nodes = make(map[string]*kgNode)
	}
	if s.Edges == nil {
		s.Edges = []kgEdge{}
	}
	t.cache = &s
	t.loaded = true
	return t.cache, nil
}

func (t *KnowledgeGraphTool) save(s *kgStore) error {
	dir := filepath.Dir(t.kgPath())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := t.kgPath() + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	t.cache = s
	return os.Rename(tmp, t.kgPath())
}

// --- Helpers ---

func slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	for _, r := range []string{" ", "_", "/"} {
		s = strings.ReplaceAll(s, r, "-")
	}
	for strings.Contains(s, "--") {
		s = strings.ReplaceAll(s, "--", "-")
	}
	var sb strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			sb.WriteRune(r)
		}
	}
	res := strings.Trim(sb.String(), "-")
	if res == "" {
		res = fmt.Sprintf("node-%d", time.Now().UnixNano()%100000)
	}
	return res
}

func formatNodeShort(sb *strings.Builder, n *kgNode) {
	sb.WriteString(fmt.Sprintf("[%s] %s (id=%s)\n", n.Type, n.Title, n.ID))
	if n.Content != "" {
		pv := n.Content
		// #850: rune-safe preview - byte slicing split multibyte characters.
		if utf8.RuneCountInString(pv) > 200 {
			pv = string([]rune(pv)[:200]) + "..."
		}
		sb.WriteString(fmt.Sprintf("  %s\n", pv))
	}
	if len(n.Tags) > 0 {
		sb.WriteString(fmt.Sprintf("  tags: %s\n", strings.Join(n.Tags, ", ")))
	}
}
