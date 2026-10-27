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

// kgEdge carries bi-temporal validity (Zep/Graphiti model, arXiv:2501.13956):
// RecordedAt is system time (when the agent recorded the fact), while
// ValidFrom/ValidUntil span the fact's real-world validity interval. Edges
// are never hard-deleted on invalidation - they are soft-invalidated so
// historical point-in-time views (trace as_of) remain queryable.
type kgEdge struct {
	From       string     `json:"from"`
	To         string     `json:"to"`
	Type       string     `json:"type"`
	RecordedAt time.Time  `json:"recorded_at,omitempty"`
	ValidFrom  *time.Time `json:"valid_from,omitempty"`
	ValidUntil *time.Time `json:"valid_until,omitempty"`
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
	// Bi-temporal params (Zep/Graphiti model):
	ValidFrom string `json:"valid_from"`
	AsOf      string `json:"as_of"`
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
	return `Manage a persistent knowledge graph for the codebase. Records structured facts (decisions, patterns, entities, issues) and their relationships (depends-on, supersedes, relates-to) that accumulate across sessions. Different from save_memory: this is structured domain knowledge about the codebase, not behavioral rules for the agent. Edges carry bi-temporal validity: invalidate marks a fact as no-longer-true while history stays queryable via trace as_of=<RFC3339>. Actions: add, link, invalidate, query, list, delete, trace, stats.`
}

func (t *KnowledgeGraphTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"action": {"type": "string", "enum": ["add", "link", "invalidate", "query", "list", "delete", "trace", "stats"], "description": "Action to perform"},
			"id": {"type": "string", "description": "Node ID (for delete, trace, link). Auto-generated from title if omitted on add."},
			"type": {"type": "string", "description": "Node type (add) or edge type (link). Nodes: decision, pattern, entity, issue, note. Edges: depends-on, supersedes, relates-to, implements, contradicts, evolves-from"},
			"title": {"type": "string", "description": "Node title (add, query)"},
			"content": {"type": "string", "description": "Node content/detail (add)"},
			"tags": {"type": "array", "items": {"type": "string"}, "description": "Tags for categorization (add, query filter)"},
			"status": {"type": "string", "description": "Lifecycle status for decisions: proposed, accepted, superseded, rejected (add)"},
			"to": {"type": "string", "description": "Target node ID (link)"},
			"query": {"type": "string", "description": "Search text matching title/content/tags (query)"},
			"valid_from": {"type": "string", "description": "RFC3339 fact-validity start (link, optional; defaults to now). A fact whose prior interval was invalidated may be re-asserted."},
			"as_of": {"type": "string", "description": "RFC3339 point-in-time view (trace): only edges whose validity interval covers as_of are traversed"}
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
	case "invalidate":
		return t.doInvalidate(store, &p)
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
	idWasAuto := false
	if id == "" {
		id = slugify(p.Title)
		idWasAuto = true
	}
	if len(id) > 80 {
		id = kgTruncateID(id, 80) // #4479: rune-safe cut, never mid-rune
	}
	// #1692 case 4: slugify is lowercase + strip-non-[a-z0-9-], so "C++ API"
	// and "c api" both become "c-api" - the second add hit the partial-
	// update branch and silently OVERWROTE the first node's Content while
	// reporting plain "saved". When the id was auto-generated and the
	// existing node has a different title, disambiguate with a numeric
	// suffix instead. Same title → legitimate update path.
	if idWasAuto {
		for i := 2; ; i++ {
			ex, exists := s.Nodes[id]
			if !exists || strings.EqualFold(ex.Title, p.Title) {
				break
			}
			suffix := fmt.Sprintf("-%d", i)
			base := kgTruncateID(id, 80-len(suffix)) // #4479: rune-safe
			id = base + suffix
		}
	}

	now := time.Now()
	// #4098: capture the pre-update title BEFORE the field assignment
	// below - the disclosure at the end re-reads s.Nodes[id] (the same
	// pointer, already carrying p.Title), so comparing against ex.Title
	// there was always equal and the #1692 case-4 disclosure never fired
	// (silent overwrite of a different-title node under an explicit id).
	prevTitle := ""
	if ex, ok := s.Nodes[id]; ok {
		prevTitle = ex.Title
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
	// #1692 case 4: distinct titles can collide on the slug ("C++ API" vs
	// "c api" -> c-api) and the second save silently OVERWROTE the first
	// node's fields while reporting a plain "saved".
	note := ""
	if prevTitle != "" && p.Title != "" && !strings.EqualFold(prevTitle, p.Title) {
		note = fmt.Sprintf(" (note: slug '%s' already existed with title %q - fields were overwritten)", id, prevTitle)
	}
	return Result{Content: fmt.Sprintf("Node '%s' (%s) saved%s. Total: %d nodes.", id, nt, note, len(s.Nodes))}, nil
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
	now := time.Now()
	var vf *time.Time
	if p.ValidFrom != "" && !strings.EqualFold(strings.TrimSpace(p.ValidFrom), "now") {
		tm, err := kgParseTime(p.ValidFrom)
		if err != nil {
			return Result{IsError: true, Content: fmt.Sprintf("invalid valid_from: %v", err)}, nil
		}
		vf = &tm
	}
	// Temporal dedup: a currently-valid duplicate is rejected, but a fact
	// whose prior interval was invalidated may become true again (Zep-style
	// re-assertion) - append a fresh validity interval instead of refusing.
	relink := false
	for _, e := range s.Edges {
		if e.From == p.ID && e.To == p.To && e.Type == p.Type {
			if kgEdgeValidAt(&e, now) {
				return Result{Content: fmt.Sprintf("Edge %s --[%s]--> %s already exists.", p.ID, p.Type, p.To)}, nil
			}
			relink = true
		}
	}
	if len(s.Edges) >= kgMaxEdges {
		return Result{IsError: true, Content: fmt.Sprintf("edge limit (%d)", kgMaxEdges)}, nil
	}
	s.Edges = append(s.Edges, kgEdge{From: p.ID, To: p.To, Type: p.Type, RecordedAt: now, ValidFrom: defaultValidFrom(vf, now)})
	if err := t.save(s); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("failed to save: %v", err)}, nil
	}
	prefix := ""
	if relink {
		prefix = "Re-validated (prior interval invalidated). "
	}
	return Result{Content: fmt.Sprintf("Linked: %s --[%s]--> %s. %sTotal edges: %d.", p.ID, p.Type, p.To, prefix, len(s.Edges))}, nil
}

// doInvalidate soft-invalidates currently-valid edges matching (id, to,
// optional type): the fact stops being true at now, but the historical
// interval stays queryable via trace as_of (Zep-style - edges are never
// deleted, history is preserved).
func (t *KnowledgeGraphTool) doInvalidate(s *kgStore, p *kgParams) (Result, error) {
	if p.ID == "" || p.To == "" {
		return Result{IsError: true, Content: "both id (source) and to (target) required"}, nil
	}
	if p.Type != "" && !kgEdgeTypes[p.Type] {
		return Result{IsError: true, Content: fmt.Sprintf("invalid edge type %q", p.Type)}, nil
	}
	now := time.Now()
	n := 0
	for i := range s.Edges {
		e := &s.Edges[i]
		if e.From != p.ID || e.To != p.To {
			continue
		}
		if p.Type != "" && e.Type != p.Type {
			continue
		}
		if !kgEdgeValidAt(e, now) {
			continue
		}
		until := now
		e.ValidUntil = &until
		n++
	}
	if n == 0 {
		return Result{Content: fmt.Sprintf("No currently-valid edge %s --> %s found.", p.ID, p.To)}, nil
	}
	if err := t.save(s); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("failed to save: %v", err)}, nil
	}
	return Result{Content: fmt.Sprintf("Invalidated %d edge(s) %s --> %s (valid until %s). Historical view: trace with as_of. Total edges: %d.", n, p.ID, p.To, now.Format(time.RFC3339), len(s.Edges))}, nil
}

func (t *KnowledgeGraphTool) doQuery(s *kgStore, p *kgParams) (Result, error) {
	q := strings.ToLower(p.Query)
	var matches []*kgNode
	for _, n := range s.Nodes {
		if p.Type != "" && n.Type != p.Type {
			continue
		}
		// #1692 case 3: the parameter doc promises "query filter" for tags -
		// doQuery never read p.Tags, so a tags-filtered query returned the
		// UNFILTERED set. Node tags are lowercase (slugify); match case-
		// insensitively, node must carry EVERY requested tag (AND).
		if len(p.Tags) > 0 {
			nodeTags := make(map[string]bool, len(n.Tags))
			for _, t := range n.Tags {
				nodeTags[strings.ToLower(t)] = true
			}
			ok := true
			for _, want := range p.Tags {
				if !nodeTags[strings.ToLower(want)] {
					ok = false
					break
				}
			}
			if !ok {
				continue
			}
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
	var asOf time.Time
	asOfSet := false
	if p.AsOf != "" {
		tm, err := kgParseTime(p.AsOf)
		if err != nil {
			return Result{IsError: true, Content: fmt.Sprintf("invalid as_of: %v", err)}, nil
		}
		asOf = tm
		asOfSet = true
	}
	now := time.Now()
	hidden := 0
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
		at := now
		if asOfSet {
			at = asOf
		}
		for _, e := range s.Edges {
			if e.From != item.id || visited[e.To] {
				continue
			}
			if !kgEdgeValidAt(&e, at) {
				hidden++
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
		msg := fmt.Sprintf("Node '%s' has no outgoing relationships.", p.ID)
		if hidden > 0 {
			msg = fmt.Sprintf("Node '%s' has no outgoing relationships within the validity window (%d edge(s) hidden; pass as_of=<RFC3339> for a historical view).", p.ID, hidden)
		}
		return Result{Content: msg}, nil
	}
	if hidden > 0 {
		lines = append(lines, fmt.Sprintf("(%d edge(s) outside the validity window hidden; pass as_of=<RFC3339> for a historical view)", hidden))
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
	now := time.Now()
	current, invalidated := 0, 0
	for _, e := range s.Edges {
		be[e.Type]++
		if kgEdgeValidAt(&e, now) {
			current++
		} else {
			invalidated++
		}
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
		sb.WriteString(fmt.Sprintf("  edge validity: %d current, %d invalidated\n", current, invalidated))
	}
	return Result{Content: sb.String()}, nil
}

// --- Persistence ---

// defaultValidFrom pins #2595: an omitted (or explicit "now") valid_from
// means the edge starts at the link moment, NOT at negative infinity. The
// parameter schema documents "defaults to now"; a nil ValidFrom made the
// edge visible at ANY as_of point, even well before the fact was asserted
// (trace as_of=2000-01-01 showed edges linked in 2026).
func defaultValidFrom(vf *time.Time, now time.Time) *time.Time {
	if vf != nil {
		return vf
	}
	return &now
}

// Clone implements the Cloner contract (tool.go): this tool holds mutable
// state (WorkingDir) plus a resolution cache pinned to that directory, so
// every agent/worktree must get its own instance. #2596: without this, the
// first agent to touch the graph pinned filePath/cache/loaded forever; every
// later syncToolWorkingDir override landed the data in the wrong workspace
// with a silent fake success.
func (t *KnowledgeGraphTool) Clone() Tool {
	return &KnowledgeGraphTool{WorkingDir: t.WorkingDir}
}

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
	// #4479: every agent/worktree holds its own cloned instance with a
	// cache that never re-reads the file, so a blind overwrite here
	// silently dropped the other instance's writes (lost update). Re-read
	// the on-disk store and union it into s before persisting: nodes by
	// ID (ours wins on conflict - it is the newer edit), edges by full
	// value. Concurrent delete-vs-add still resolves to "kept", which is
	// the no-loss side of the trade; the alternative (version check)
	// would reject legitimate writes instead.
	if data, err := os.ReadFile(t.kgPath()); err == nil {
		var disk kgStore
		if json.Unmarshal(data, &disk) == nil {
			mergeKgStores(s, &disk)
		}
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

// kgTruncateID cuts s to at most maxBytes without splitting a multi-byte
// rune (#4479: a byte-level cut produced invalid UTF-8 IDs that json.Marshal
// then rewrote to U+FFFD, breaking lookups after a save/load round-trip).
func kgTruncateID(s string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len(s) <= maxBytes {
		return s
	}
	cut := maxBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}

// mergeKgStores unions disk into s (the in-memory store about to be saved).
// Nodes: ours wins on ID conflict (it is the newer edit); nodes only present
// on disk (written concurrently by another agent instance) are carried over.
// Edges are keyed by (from, to, type): when memory holds ANY edge with that
// key - including a soft-invalidated one - the memory version(s) win and the
// disk copy is skipped, so an invalidation is never resurrected by the merge.
// Disk edges with a key memory has never seen are appended unchanged,
// preserving the other writer's bi-temporal intervals.
func mergeKgStores(s, disk *kgStore) {
	if s.Nodes == nil {
		s.Nodes = make(map[string]*kgNode)
	}
	for id, n := range disk.Nodes {
		if _, ok := s.Nodes[id]; !ok {
			copied := *n
			s.Nodes[id] = &copied
		}
	}
	if len(disk.Edges) == 0 {
		return
	}
	have := make(map[string]struct{}, len(s.Edges))
	for _, e := range s.Edges {
		have[e.From+"\x00"+e.To+"\x00"+e.Type] = struct{}{}
	}
	for _, e := range disk.Edges {
		key := e.From + "\x00" + e.To + "\x00" + e.Type
		if _, dup := have[key]; !dup {
			s.Edges = append(s.Edges, e)
			have[key] = struct{}{}
		}
	}
}

// --- Helpers ---

// kgParseTime parses an RFC3339 timestamp (or "now") for temporal params.
func kgParseTime(s string) (time.Time, error) {
	if strings.EqualFold(strings.TrimSpace(s), "now") {
		return time.Now(), nil
	}
	tm, err := time.Parse(time.RFC3339, strings.TrimSpace(s))
	if err != nil {
		return time.Time{}, fmt.Errorf("want RFC3339 (e.g. 2026-01-02T15:04:05Z), got %q", s)
	}
	return tm, nil
}

// kgEdgeValidAt reports whether the edge's fact is true at instant at.
// Legacy edges without temporal fields are treated as always-valid, so
// pre-temporal graph files keep their existing trace/list behavior.
func kgEdgeValidAt(e *kgEdge, at time.Time) bool {
	if e.ValidFrom != nil && at.Before(*e.ValidFrom) {
		return false
	}
	if e.ValidUntil != nil && !at.Before(*e.ValidUntil) {
		return false
	}
	return true
}

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
