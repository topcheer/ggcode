package tool

// Tool macros (sa-148, tool composition): record a reusable sequence of
// read-only tool calls once, replay it by name forever.
//
// Gap context: the strategy playbook (internal/agent/playbook.go, ACE
// arXiv:2510.04618) learns successful tool sequences but only injects them
// as system-prompt hints - the model must re-derive every call each run.
// Skills are hand-written markdown. Nothing bridges "learned pattern" to
// "callable asset". A macro is that bridge: define_macro persists the
// sequence, run_macro replays it in one call.
//
// Security model (mirrors code_execution): macros may only compose
// read-only tools (readOnlyToolNames). Write operations (edit_file,
// run_command, ...) are rejected at define time so a macro can never
// bypass the normal per-call permission flow with UI diff preview. Macros
// cannot call macros ("macro" itself is not read-only), so recursion is
// structurally impossible.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	macroFileName = "macros.json"

	// macroMaxSteps bounds one macro's length; a macro needing more than 8
	// tool calls is a workflow (workflow_run), not a macro.
	macroMaxSteps = 8

	// macroMaxEntries caps the store so the file stays small and the list
	// action stays readable.
	macroMaxEntries = 50

	// macroMaxArgs bounds run_macro positional arguments ({{1}}..{{9}}).
	macroMaxArgs = 9

	// macroStepOutMax / macroTotalOutMax bound replay output per step and
	// overall, keeping a chatty macro from flooding the context window.
	macroStepOutMax  = 2048
	macroTotalOutMax = 16 * 1024
)

// macroNameRe constrains macro names to a stable, shell- and path-safe set.
var macroNameRe = regexp.MustCompile(`^[a-z][a-z0-9_-]{2,40}$`)

// macroStep is one tool invocation inside a macro. Args is a JSON object
// template whose string leaves may reference positional run-time
// arguments via {{1}}..{{9}}.
type macroStep struct {
	Tool string          `json:"tool"`
	Args json.RawMessage `json:"args"`
}

type macroDef struct {
	Name        string      `json:"name"`
	Description string      `json:"description,omitempty"`
	Steps       []macroStep `json:"steps"`
	CreatedAt   time.Time   `json:"created_at"`
	UpdatedAt   time.Time   `json:"updated_at"`
}

type macroStore struct {
	Entries map[string]*macroDef `json:"entries"`
}

// MacroTool implements the "macro" builtin: define / run / list / get /
// delete reusable read-only tool-call sequences. Storage follows the
// cmd_snippet convention: <workingDir>/.ggcode/macros.json, mutex-guarded
// cache, atomic-ish persist, read-only snapshots for callers.
type MacroTool struct {
	WorkingDir string
	// Registry is used at run time to look up sub-tools by name (same
	// late-binding contract as CodeExecution).
	Registry *Registry

	mu       sync.Mutex
	filePath string
	cache    *macroStore
}

func (t *MacroTool) Name() string { return "macro" }

func (t *MacroTool) Description() string {
	return `Record and replay reusable sequences of read-only tool calls (tool macros).

Actions:
  define {name, description, steps:[{tool, args}]} - persist a macro. steps: 1-8 entries; tool must be a read-only tool; args is a JSON object whose string values may contain {{1}}..{{9}} placeholders substituted at run time. Overwrites an existing macro of the same name.
  run {name, args:[...]} - replay: substitutes args into placeholders, executes each step in order, stops at the first error. Only read-only tools run; write tools can never be part of a macro.
  list - show saved macros (name, description, step count).
  get {name} - show one macro's full definition.
  delete {name} - remove a macro.

Use when the same multi-step read-only pattern recurs across runs (e.g. pre-release checks: git_status + git_log + grep of version files). Persisted to .ggcode/macros.json, shared per workspace.`
}

func (t *MacroTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"action": {
				"type": "string",
				"enum": ["define", "run", "list", "get", "delete"],
				"description": "REQUIRED. Brief activity label shown in the UI in the user's language. Macro operation to perform."
			},
			"name": {
				"type": "string",
				"description": "Macro name (define/run/get/delete). Lowercase letters, digits, hyphen, underscore; 3-41 chars."
			},
			"description": {
				"type": "string",
				"description": "define only. What this macro does, shown in list output."
			},
			"steps": {
				"type": "array",
				"description": "define only. Ordered tool calls: [{tool: 'git_status', args: {path: '.'}}]. Tool must be read-only; 1-8 steps.",
				"items": {
					"type": "object",
					"properties": {
						"tool": {"type": "string"},
						"args": {"type": "object"}
					},
					"required": ["tool"]
				}
			},
			"args": {
				"type": "array",
				"description": "run only. Positional values substituted for {{1}}..{{9}} placeholders, max 9.",
				"items": {"type": ["string", "number", "boolean"]}
			}
		},
		"required": ["action"]
	}`)
}

func (t *MacroTool) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	var args struct {
		Action      string          `json:"action"`
		Name        string          `json:"name"`
		Description string          `json:"description"`
		Steps       []macroStep     `json:"steps"`
		RunArgs     json.RawMessage `json:"args"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}
	switch args.Action {
	case "define":
		return t.doDefine(args.Name, args.Description, args.Steps)
	case "run":
		return t.doRun(ctx, args.Name, args.RunArgs)
	case "list":
		return t.doList()
	case "get":
		return t.doGet(args.Name)
	case "delete":
		return t.doDelete(args.Name)
	default:
		return Result{IsError: true, Content: fmt.Sprintf("unknown action %q (want define|run|list|get|delete)", args.Action)}, nil
	}
}

func (t *MacroTool) doDefine(name, desc string, steps []macroStep) (Result, error) {
	if !macroNameRe.MatchString(name) {
		return Result{IsError: true, Content: fmt.Sprintf("invalid macro name %q: must match %s", name, macroNameRe.String())}, nil
	}
	if len(steps) == 0 || len(steps) > macroMaxSteps {
		return Result{IsError: true, Content: fmt.Sprintf("steps must be 1..%d, got %d", macroMaxSteps, len(steps))}, nil
	}
	for i, s := range steps {
		if _, ok := readOnlyToolNames[s.Tool]; !ok {
			return Result{IsError: true, Content: fmt.Sprintf("step %d: tool %q is not in the read-only set - macros may only compose read-only tools so write operations keep their normal per-call approval flow", i+1, s.Tool)}, nil
		}
		if len(s.Args) > 0 {
			var probe any
			if err := json.Unmarshal(s.Args, &probe); err != nil {
				return Result{IsError: true, Content: fmt.Sprintf("step %d: args must be a JSON object: %v", i+1, err)}, nil
			}
		}
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	store, err := t.loadLocked()
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("load macro store: %v", err)}, nil
	}
	if _, exists := store.Entries[name]; !exists && len(store.Entries) >= macroMaxEntries {
		return Result{IsError: true, Content: fmt.Sprintf("macro store full (%d/%d): delete an unused macro first", len(store.Entries), macroMaxEntries)}, nil
	}
	now := time.Now()
	old := store.Entries[name]
	def := &macroDef{Name: name, Description: desc, Steps: steps, UpdatedAt: now}
	if old != nil {
		def.CreatedAt = old.CreatedAt
	} else {
		def.CreatedAt = now
	}
	store.Entries[name] = def
	if err := t.persistLocked(store); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("persist macro store: %v", err)}, nil
	}
	return Result{Content: fmt.Sprintf("macro %q defined: %d step(s)%s", name, len(steps), overwriteNote(old))}, nil
}

func overwriteNote(old *macroDef) string {
	if old == nil {
		return ""
	}
	return " (overwrote previous definition)"
}

func (t *MacroTool) doRun(ctx context.Context, name string, runArgsRaw json.RawMessage) (Result, error) {
	if t.Registry == nil {
		return Result{IsError: true, Content: "tool registry not available"}, nil
	}
	var runArgs []string
	if len(runArgsRaw) > 0 {
		if err := json.Unmarshal(runArgsRaw, &runArgs); err != nil {
			return Result{IsError: true, Content: fmt.Sprintf("args must be an array of scalars: %v", err)}, nil
		}
	}
	if len(runArgs) > macroMaxArgs {
		return Result{IsError: true, Content: fmt.Sprintf("too many args: max %d, got %d", macroMaxArgs, len(runArgs))}, nil
	}

	t.mu.Lock()
	store, err := t.loadLocked()
	var def *macroDef
	if err == nil {
		def = store.Entries[name]
	}
	t.mu.Unlock()
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("load macro store: %v", err)}, nil
	}
	if def == nil {
		return Result{IsError: true, Content: fmt.Sprintf("macro %q not found (action=list to see saved macros)", name)}, nil
	}

	var out strings.Builder
	fmt.Fprintf(&out, "macro %q: %d step(s)\n", name, len(def.Steps))
	total := 0
	for i, s := range def.Steps {
		expanded, err := expandMacroArgs(s.Args, runArgs)
		if err != nil {
			return Result{IsError: true, Content: fmt.Sprintf("step %d: %v", i+1, err)}, nil
		}
		tool, ok := t.Registry.Get(s.Tool)
		if !ok {
			return Result{IsError: true, Content: fmt.Sprintf("step %d: tool %q not registered", i+1, s.Tool)}, nil
		}
		res, err := tool.Execute(ctx, expanded)
		stepHead := fmt.Sprintf("--- step %d/%d %s: ", i+1, len(def.Steps), s.Tool)
		body := res.Content
		if err != nil {
			return Result{IsError: true, Content: fmt.Sprintf("%serror: %v\n%s(stopped at step %d)", stepHead, err, out.String(), i+1)}, nil
		}
		status := "ok"
		if res.IsError {
			status = "error"
		}
		body = clampRunes(body, macroStepOutMax)
		total += len(stepHead) + len(status) + len(body)
		fmt.Fprintf(&out, "%s%s\n%s\n", stepHead, status, body)
		if total > macroTotalOutMax {
			out.WriteString("... (output budget reached, remaining steps suppressed)\n")
			break
		}
		if res.IsError {
			out.WriteString(fmt.Sprintf("(stopped at step %d: step returned an error)", i+1))
			return Result{IsError: true, Content: out.String()}, nil
		}
	}
	return Result{Content: clampRunes(out.String(), macroTotalOutMax)}, nil
}

// expandMacroArgs substitutes {{1}}..{{9}} placeholders in the serialized
// args template. Placeholders may appear inside string values; each
// substituted value is JSON-escaped so injection cannot break out of the
// string literal.
func expandMacroArgs(template json.RawMessage, runArgs []string) (json.RawMessage, error) {
	if len(template) == 0 {
		return json.RawMessage("{}"), nil
	}
	out := string(template)
	for i, v := range runArgs {
		ph := fmt.Sprintf("{{%d}}", i+1)
		esc, err := json.Marshal(v)
		if err != nil {
			return nil, err
		}
		out = strings.ReplaceAll(out, ph, strings.Trim(string(esc), `"`))
	}
	// Reject leftover placeholders beyond supplied args? No: a template
	// may intentionally keep a literal {{7}} for a later arg that this run
	// did not supply; the tool will see it as a literal string.
	return json.RawMessage(out), nil
}

func (t *MacroTool) doList() (Result, error) {
	t.mu.Lock()
	store, err := t.loadLocked()
	t.mu.Unlock()
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("load macro store: %v", err)}, nil
	}
	if len(store.Entries) == 0 {
		return Result{Content: "no macros saved yet (action=define to create one)"}, nil
	}
	names := make([]string, 0, len(store.Entries))
	for n := range store.Entries {
		names = append(names, n)
	}
	sort.Strings(names)
	var b strings.Builder
	fmt.Fprintf(&b, "%d macro(s):\n", len(names))
	for _, n := range names {
		d := store.Entries[n]
		desc := d.Description
		if desc == "" {
			desc = "(no description)"
		}
		fmt.Fprintf(&b, "  %s - %s [%d step(s)]\n", n, desc, len(d.Steps))
	}
	return Result{Content: b.String()}, nil
}

func (t *MacroTool) doGet(name string) (Result, error) {
	t.mu.Lock()
	store, err := t.loadLocked()
	var def *macroDef
	if err == nil {
		def = store.Entries[name]
	}
	t.mu.Unlock()
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("load macro store: %v", err)}, nil
	}
	if def == nil {
		return Result{IsError: true, Content: fmt.Sprintf("macro %q not found", name)}, nil
	}
	data, _ := json.MarshalIndent(def, "", "  ")
	return Result{Content: string(data)}, nil
}

func (t *MacroTool) doDelete(name string) (Result, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	store, err := t.loadLocked()
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("load macro store: %v", err)}, nil
	}
	if _, ok := store.Entries[name]; !ok {
		return Result{IsError: true, Content: fmt.Sprintf("macro %q not found", name)}, nil
	}
	delete(store.Entries, name)
	if err := t.persistLocked(store); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("persist macro store: %v", err)}, nil
	}
	return Result{Content: fmt.Sprintf("macro %q deleted", name)}, nil
}

// ---- Storage helpers (cmd_snippet convention) ----

func (t *MacroTool) storePath() string {
	if t.filePath == "" {
		dir := t.WorkingDir
		if dir == "" {
			dir, _ = os.Getwd()
		}
		t.filePath = filepath.Join(dir, ".ggcode", macroFileName)
	}
	return t.filePath
}

// loadLocked returns the cached store, loading from disk on first use.
// Caller must hold t.mu; the returned map is shared, treat as read-only.
func (t *MacroTool) loadLocked() (*macroStore, error) {
	if t.cache != nil {
		return t.cache, nil
	}
	store := &macroStore{Entries: map[string]*macroDef{}}
	data, err := os.ReadFile(t.storePath())
	if err != nil {
		if os.IsNotExist(err) {
			t.cache = store
			return store, nil
		}
		return nil, err
	}
	if err := json.Unmarshal(data, store); err != nil {
		// Corrupt store: quarantine by renaming, start fresh (same
		// fail-open contract as other .ggcode stores; never wedge the
		// tool on a bad file).
		_ = os.Rename(t.storePath(), t.storePath()+".corrupt")
		t.cache = &macroStore{Entries: map[string]*macroDef{}}
		return t.cache, nil
	}
	if store.Entries == nil {
		store.Entries = map[string]*macroDef{}
	}
	t.cache = store
	return store, nil
}

func (t *MacroTool) persistLocked(store *macroStore) error {
	if err := os.MkdirAll(filepath.Dir(t.storePath()), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(t.storePath(), data, 0o644); err != nil {
		return err
	}
	t.cache = store
	return nil
}

// clampRunes truncates s to at most max runes without cutting mid-rune.
func clampRunes(s string, max int) string {
	if len(s) <= max {
		return s
	}
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max]) + "\n... (truncated)"
}
