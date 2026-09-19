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

// CmdSnippetTool provides a persistent, project-scoped library of reusable
// shell commands. The agent saves commands it discovers during a session
// (build, test, deploy, debug) and retrieves them in future sessions,
// avoiding the need to re-discover project-specific commands from scratch.
//
// Storage: <workingDir>/.ggcode/cmd-snippets.json
// Competitor parallel: Claude Code's .claude/commands/, Cursor's rules,
// Aider's aider.conf.yml aliases - but focused specifically on shell commands
// the agent runs, with tags and usage counts for relevance ranking.
//
// Actions: save, list, get, delete, search

const (
	cmdSnippetMaxName       = 120
	cmdSnippetMaxCommand    = 2000
	cmdSnippetMaxDesc       = 500
	cmdSnippetMaxTags       = 8
	cmdSnippetMaxEntries    = 200
	cmdSnippetFileName      = "cmd-snippets.json"
	cmdSnippetSearchResults = 20
)

type cmdSnippetEntry struct {
	Name        string    `json:"name"`
	Command     string    `json:"command"`
	Description string    `json:"description,omitempty"`
	Tags        []string  `json:"tags,omitempty"`
	UseCount    int       `json:"use_count"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type cmdSnippetStore struct {
	Entries []cmdSnippetEntry `json:"entries"`
}

type CmdSnippetTool struct {
	WorkingDir string

	mu       sync.Mutex
	filePath string
	cache    *cmdSnippetStore
	loaded   bool
}

func (t *CmdSnippetTool) Name() string { return "cmd_snippet" }

func (t *CmdSnippetTool) Description() string {
	return "Manage a persistent library of reusable shell commands (build, test, deploy, debug). Saves commands per-project so future sessions can recall them without re-discovery. Actions: save, list, get, delete, search."
}

func (t *CmdSnippetTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"action": {
				"type": "string",
				"enum": ["save", "list", "get", "delete", "search"],
				"description": "save: store a new or updated command. list: show all. get: retrieve by name. delete: remove by name. search: find by keyword."
			},
			"name": {
				"type": "string",
				"description": "Unique name for the command (required for save, get, delete). E.g. 'build-go', 'test-unit'."
			},
			"command": {
				"type": "string",
				"description": "The shell command to store (required for save)."
			},
			"description_field": {
				"type": "string",
				"description": "Optional human-readable description of what the command does."
			},
			"tags": {
				"type": "array",
				"items": {"type": "string"},
				"description": "Optional tags for categorization (e.g. 'build', 'test'). Max 8."
			},
			"query": {
				"type": "string",
				"description": "Search query for the 'search' action."
			},
			"description": {
				"type": "string",
				"description": "REQUIRED. Brief activity label shown in the UI."
			}
		},
		"required": ["action", "description"]
	}`)
}

func (t *CmdSnippetTool) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	var args struct {
		Action      string   `json:"action"`
		Name        string   `json:"name"`
		Command     string   `json:"command"`
		Description string   `json:"description_field"`
		Tags        []string `json:"tags"`
		Query       string   `json:"query"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}

	switch args.Action {
	case "save":
		return t.doSave(args.Name, args.Command, args.Description, args.Tags)
	case "list":
		return t.doList()
	case "get":
		return t.doGet(args.Name)
	case "delete":
		return t.doDelete(args.Name)
	case "search":
		return t.doSearch(args.Query)
	default:
		return Result{IsError: true, Content: fmt.Sprintf("unknown action %q: must be save, list, get, delete, or search", args.Action)}, nil
	}
}

// ---- Storage helpers ----

func (t *CmdSnippetTool) storePath() string {
	if t.filePath == "" {
		dir := t.WorkingDir
		if dir == "" {
			dir, _ = os.Getwd()
		}
		t.filePath = filepath.Join(dir, ".ggcode", cmdSnippetFileName)
	}
	return t.filePath
}

// load returns a READ-ONLY snapshot: the cached Entries slice is copied
// under the lock so callers can never race a concurrent doSave/doGet/
// doDelete mutation (#1644 case 3: load used to hand out t.cache itself,
// then released the lock while callers mutated the shared struct).
func (t *CmdSnippetTool) load() (*cmdSnippetStore, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.loadLocked()
}

// loadLocked is load without locking - callers must hold t.mu.
func (t *CmdSnippetTool) loadLocked() (*cmdSnippetStore, error) {
	if t.loaded && t.cache != nil {
		return t.cloneLocked(), nil
	}
	path := t.storePath()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			t.cache = &cmdSnippetStore{}
			t.loaded = true
			return t.cloneLocked(), nil
		}
		return nil, err
	}
	var store cmdSnippetStore
	if err := json.Unmarshal(data, &store); err != nil {
		return nil, fmt.Errorf("corrupt snippet store: %w", err)
	}
	t.cache = &store
	t.loaded = true
	return t.cloneLocked(), nil
}

// cloneLocked deep-copies the entries slice so the snapshot and the cache
// diverge safely. Callers must hold t.mu.
func (t *CmdSnippetTool) cloneLocked() *cmdSnippetStore {
	if t.cache == nil {
		return &cmdSnippetStore{}
	}
	clone := &cmdSnippetStore{Entries: make([]cmdSnippetEntry, len(t.cache.Entries))}
	copy(clone.Entries, t.cache.Entries)
	return clone
}

// mutate runs fn against the store with the lock held for the whole
// read-modify-write sequence (#1644 case 3: load and persist each took the
// lock separately, so two interleaved actions could load the same snapshot
// and the second persist silently clobbered the first). fn receives the
// authoritative store; persisting is skipped when persist is false.
func (t *CmdSnippetTool) mutate(persist bool, fn func(store *cmdSnippetStore) error) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.cache == nil || !t.loaded {
		if _, err := t.loadForMutationLocked(); err != nil {
			return err
		}
	}
	if err := fn(t.cache); err != nil {
		return err
	}
	if !persist {
		return nil
	}
	return t.persistLocked(t.cache)
}

// loadForMutationLocked primes the cache from disk without copying -
// mutate works on the authoritative store, not a snapshot.
func (t *CmdSnippetTool) loadForMutationLocked() (*cmdSnippetStore, error) {
	path := t.storePath()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			t.cache = &cmdSnippetStore{}
			t.loaded = true
			return t.cache, nil
		}
		return nil, err
	}
	var store cmdSnippetStore
	if err := json.Unmarshal(data, &store); err != nil {
		return nil, fmt.Errorf("corrupt snippet store: %w", err)
	}
	t.cache = &store
	t.loaded = true
	return t.cache, nil
}

// persistLocked is persist without locking - callers must hold t.mu.
func (t *CmdSnippetTool) persistLocked(store *cmdSnippetStore) error {
	path := t.storePath()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		return err
	}
	t.cache = store
	t.loaded = true
	return os.WriteFile(path, data, 0644)
}

// ---- Actions ----

func (t *CmdSnippetTool) doSave(name, command, desc string, tags []string) (Result, error) {
	name = strings.TrimSpace(name)
	command = strings.TrimSpace(command)
	if name == "" {
		return Result{IsError: true, Content: "name is required"}, nil
	}
	if command == "" {
		return Result{IsError: true, Content: "command is required"}, nil
	}
	if utf8.RuneCountInString(name) > cmdSnippetMaxName {
		return Result{IsError: true, Content: fmt.Sprintf("name too long: %d chars (max %d)", utf8.RuneCountInString(name), cmdSnippetMaxName)}, nil
	}
	if utf8.RuneCountInString(command) > cmdSnippetMaxCommand {
		return Result{IsError: true, Content: fmt.Sprintf("command too long: %d chars (max %d)", utf8.RuneCountInString(command), cmdSnippetMaxCommand)}, nil
	}
	if utf8.RuneCountInString(desc) > cmdSnippetMaxDesc {
		desc = truncateRunes(desc, cmdSnippetMaxDesc-3) + "..."
	}
	tagNote := ""
	if len(tags) > cmdSnippetMaxTags {
		// #1644 case 6: silent truncation - the caller must know the save
		// kept fewer tags than submitted.
		tags = tags[:cmdSnippetMaxTags]
		tagNote = fmt.Sprintf(" [note: tags truncated to first %d]", cmdSnippetMaxTags)
	}

	verb := "saved"
	var total int
	err := t.mutate(true, func(store *cmdSnippetStore) error {
		now := time.Now()
		updated := false
		for idx := range store.Entries {
			if strings.EqualFold(store.Entries[idx].Name, name) {
				store.Entries[idx].Command = command
				store.Entries[idx].Description = desc
				store.Entries[idx].Tags = tags
				store.Entries[idx].UpdatedAt = now
				store.Entries[idx].UseCount++
				updated = true
				break
			}
		}
		if !updated {
			if len(store.Entries) >= cmdSnippetMaxEntries {
				// Evict least recently used entry (lowest UpdatedAt).
				oldest := 0
				for idx := range store.Entries {
					if store.Entries[idx].UpdatedAt.Before(store.Entries[oldest].UpdatedAt) {
						oldest = idx
					}
				}
				store.Entries = append(store.Entries[:oldest], store.Entries[oldest+1:]...)
			}
			store.Entries = append(store.Entries, cmdSnippetEntry{
				Name:        name,
				Command:     command,
				Description: desc,
				Tags:        tags,
				UseCount:    1,
				CreatedAt:   now,
				UpdatedAt:   now,
			})
		}
		// Keep sorted by name for readability.
		sort.SliceStable(store.Entries, func(a, b int) bool {
			return strings.ToLower(store.Entries[a].Name) < strings.ToLower(store.Entries[b].Name)
		})
		total = len(store.Entries)
		if updated {
			verb = "updated"
		}
		return nil
	})
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("failed to save snippet: %v", err)}, nil
	}
	return Result{Content: fmt.Sprintf("Snippet %q %s (%d total).%s", name, verb, total, tagNote)}, nil
}

func (t *CmdSnippetTool) doList() (Result, error) {
	store, err := t.load()
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("failed to load snippet store: %v", err)}, nil
	}
	if len(store.Entries) == 0 {
		return Result{Content: "No command snippets stored yet. Use action='save' to store one."}, nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Command snippets (%d):\n", len(store.Entries)))
	for _, entry := range store.Entries {
		sb.WriteString(fmt.Sprintf("\n## %s", entry.Name))
		if entry.UseCount > 1 {
			sb.WriteString(fmt.Sprintf(" (used %dx)", entry.UseCount))
		}
		sb.WriteString("\n")
		sb.WriteString(fmt.Sprintf("  Command: `%s`\n", entry.Command))
		if entry.Description != "" {
			sb.WriteString(fmt.Sprintf("  Description: %s\n", entry.Description))
		}
		if len(entry.Tags) > 0 {
			sb.WriteString(fmt.Sprintf("  Tags: %s\n", strings.Join(entry.Tags, ", ")))
		}
	}
	return Result{Content: strings.TrimSpace(sb.String())}, nil
}

func (t *CmdSnippetTool) doGet(name string) (Result, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Result{IsError: true, Content: "name is required"}, nil
	}
	var found *cmdSnippetEntry
	err := t.mutate(true, func(store *cmdSnippetStore) error {
		for idx := range store.Entries {
			if strings.EqualFold(store.Entries[idx].Name, name) {
				// Increment use count and persist - whole read-modify-write
				// under the lock (#1644 case 3).
				store.Entries[idx].UseCount++
				store.Entries[idx].UpdatedAt = time.Now()
				entry := store.Entries[idx]
				found = &entry
				return nil
			}
		}
		return nil
	})
	if err != nil {
		// #1644 case 5: the use-count persist failure used to be silently
		// discarded (`_ = t.persist(store)`) - surface it.
		return Result{IsError: true, Content: fmt.Sprintf("failed to persist snippet use count: %v", err)}, nil
	}
	if found == nil {
		return Result{IsError: true, Content: fmt.Sprintf("snippet %q not found", name)}, nil
	}
	entry := *found
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Name: %s\n", entry.Name))
	sb.WriteString(fmt.Sprintf("Command: %s\n", entry.Command))
	if entry.Description != "" {
		sb.WriteString(fmt.Sprintf("Description: %s\n", entry.Description))
	}
	if len(entry.Tags) > 0 {
		sb.WriteString(fmt.Sprintf("Tags: %s\n", strings.Join(entry.Tags, ", ")))
	}
	return Result{Content: strings.TrimSpace(sb.String())}, nil
}

func (t *CmdSnippetTool) doDelete(name string) (Result, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Result{IsError: true, Content: "name is required"}, nil
	}
	deleted := false
	remaining := 0
	err := t.mutate(true, func(store *cmdSnippetStore) error {
		for idx := range store.Entries {
			if strings.EqualFold(store.Entries[idx].Name, name) {
				store.Entries = append(store.Entries[:idx], store.Entries[idx+1:]...)
				deleted = true
				break
			}
		}
		remaining = len(store.Entries)
		return nil
	})
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("failed to persist deletion: %v", err)}, nil
	}
	if !deleted {
		return Result{IsError: true, Content: fmt.Sprintf("snippet %q not found", name)}, nil
	}
	return Result{Content: fmt.Sprintf("Snippet %q deleted (%d remaining).", name, remaining)}, nil
}

func (t *CmdSnippetTool) doSearch(query string) (Result, error) {
	query = strings.TrimSpace(strings.ToLower(query))
	if query == "" {
		return Result{IsError: true, Content: "query is required for search"}, nil
	}
	store, err := t.load()
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("failed to load snippet store: %v", err)}, nil
	}

	type scored struct {
		entry cmdSnippetEntry
		score int
	}
	var results []scored
	for _, entry := range store.Entries {
		s := 0
		if strings.Contains(strings.ToLower(entry.Name), query) {
			s += 10
		}
		if strings.Contains(strings.ToLower(entry.Command), query) {
			s += 5
		}
		if strings.Contains(strings.ToLower(entry.Description), query) {
			s += 3
		}
		for _, tag := range entry.Tags {
			if strings.Contains(strings.ToLower(tag), query) {
				s += 4
			}
		}
		if s > 0 {
			results = append(results, scored{entry: entry, score: s})
		}
	}

	if len(results) == 0 {
		return Result{Content: fmt.Sprintf("No snippets matching %q.", query)}, nil
	}

	sort.SliceStable(results, func(a, b int) bool {
		return results[a].score > results[b].score
	})
	if len(results) > cmdSnippetSearchResults {
		results = results[:cmdSnippetSearchResults]
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Found %d snippet(s) matching %q:\n", len(results), query))
	for _, r := range results {
		sb.WriteString(fmt.Sprintf("\n  %s: `%s`", r.entry.Name, r.entry.Command))
		if r.entry.Description != "" {
			sb.WriteString(fmt.Sprintf(" - %s", r.entry.Description))
		}
	}
	return Result{Content: strings.TrimSpace(sb.String())}, nil
}

// Clone returns an independent copy for use by a different agent context.
func (t *CmdSnippetTool) Clone() Tool {
	return &CmdSnippetTool{WorkingDir: t.WorkingDir}
}
