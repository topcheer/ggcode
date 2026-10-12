package tool

// Agent-defined parameterized tools (r13, Voyager-style dynamic skill
// synthesis, arXiv:2607.10113 taxonomy): the agent defines a named tool
// with a JSON-Schema parameter contract and a handler expressed as the
// macro step DSL, then calls it as a first-class parametrized tool.
//
// Layering vs existing assets:
//   - macro (sa-148): read-only step SEQUENCES with positional {{1}}..{{9}}
//     substitution - replay convenience, no parameter contract.
//   - define_tool (this): same step DSL but with a declared
//     params_schema (JSON Schema, named {{param}} placeholders), so a
//     defined tool is a reusable parameterized abstraction ("check_pat",
//     "find_usage") rather than a fixed sequence.
//   - skills (.ggskill): prompt-layer knowledge; define_tool is
//     tool-layer behavior.
//
// Security model (parity with macro.go): steps may only compose
// read-only tools. The r13 backlog sketch suggested opening the
// whitelist to edit_file/run_command, but tool-layer dispatch executes
// Registry tools directly - the agent's per-call permission gating (UI
// diff preview, approval flow) lives ABOVE the tool layer, so write
// steps would silently bypass approvals. That is rejected at define
// time with an explanatory error; a write-capable define_tool needs
// agent-layer integration first. define_tool cannot call define_tool or
// macro (not read-only), so recursion is structurally impossible.

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
	defineToolsFileName  = "define_tools.json"
	defineToolMaxSteps   = 8
	defineToolMaxParams  = 9
	defineToolMaxEntries = 50
)

// defineToolNameRe: distinct namespace from macros avoids confusion in
// lists; allows a trailing "?" hint convention? No - keep the same
// conservative charset as macro names.
var defineToolNameRe = regexp.MustCompile(`^[a-z][a-z0-9_-]{2,40}$`)

// paramPlaceholderRe matches {{name}} in step-arg string templates.
var paramPlaceholderRe = regexp.MustCompile(`\{\{\s*([a-zA-Z_][a-zA-Z0-9_]*)\s*\}\}`)

// definedToolDef is one agent-defined tool: a name, a JSON-Schema
// parameter contract, and a handler of read-only steps whose string
// leaves may reference {{param}} placeholders.
type definedToolDef struct {
	Name         string          `json:"name"`
	Description  string          `json:"description,omitempty"`
	ParamsSchema json.RawMessage `json:"params_schema,omitempty"`
	Steps        []macroStep     `json:"steps"`
	CreatedAt    time.Time       `json:"created_at"`
	UpdatedAt    time.Time       `json:"updated_at"`
}

type definedToolStore struct {
	Entries map[string]*definedToolDef `json:"entries"`
}

// DefineTool implements the "define_tool" builtin: define / call / list /
// get / delete agent-defined parametrized tools. Storage follows the
// cmd_snippet/macro convention: <workingDir>/.ggcode/define_tools.json.
type DefineTool struct {
	WorkingDir string
	// Registry is used at call time to look up step tools by name (same
	// late-binding contract as MacroTool / CodeExecution).
	Registry *Registry

	mu       sync.Mutex
	filePath string
	cache    *definedToolStore
}

func (t *DefineTool) Name() string { return "define_tool" }

func (t *DefineTool) Description() string {
	return `Define and call first-class parameterized tools (agent-defined tools).

Actions:
  define {name, description, params_schema, steps:[{tool, args}]} - persist a parameterized tool. params_schema is a JSON Schema object; step args string values may contain {{param}} placeholders substituted at call time. Steps may only use read-only tools (write tools would bypass per-call approval and are rejected). Overwrites an existing definition of the same name.
  call {name, params:{...}} - validate params against the schema's required list, substitute into steps, execute in order, stop at first error.
  list - show defined tools (name, description, param count, step count).
  get {name} - show one definition (schema + steps).
  delete {name} - remove a definition.

Use when a parameterized multi-step pattern recurs (e.g. "find symbol X usages" = lsp_symbols + grep with {{symbol}}). Persisted to .ggcode/define_tools.json, shared per workspace.`
}

func (t *DefineTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"action": {
				"type": "string",
				"enum": ["define", "call", "list", "get", "delete"],
				"description": "REQUIRED. Operation to perform."
			},
			"name": {
				"type": "string",
				"description": "Tool name (define/call/get/delete). Lowercase letters, digits, hyphen, underscore; 3-41 chars."
			},
			"description": {
				"type": "string",
				"description": "define only. What this tool does, shown in list output."
			},
			"params_schema": {
				"type": "object",
				"description": "define only. JSON Schema object for the call-time params, e.g. {\"type\":\"object\",\"properties\":{\"symbol\":{\"type\":\"string\"}},\"required\":[\"symbol\"]}. Max 9 properties."
			},
			"steps": {
				"type": "array",
				"description": "define only. Handler: 1-8 read-only tool calls [{tool:'grep', args:{pattern:'{{symbol}}'}}]. String values may contain {{param}} placeholders.",
				"items": {
					"type": "object",
					"properties": {
						"tool": {"type": "string"},
						"args": {"type": "object"}
					},
					"required": ["tool"]
				}
			},
			"params": {
				"type": "object",
				"description": "call only. Values substituted for {{param}} placeholders."
			}
		},
		"required": ["action"]
	}`)
}

func (t *DefineTool) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	var args struct {
		Action       string                 `json:"action"`
		Name         string                 `json:"name"`
		Description  string                 `json:"description"`
		ParamsSchema json.RawMessage        `json:"params_schema"`
		Steps        []macroStep            `json:"steps"`
		Params       map[string]interface{} `json:"params"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}
	switch args.Action {
	case "define":
		return t.doDefine(args.Name, args.Description, args.ParamsSchema, args.Steps)
	case "call":
		return t.doCall(ctx, args.Name, args.Params)
	case "list":
		return t.doList()
	case "get":
		return t.doGet(args.Name)
	case "delete":
		return t.doDelete(args.Name)
	default:
		return Result{IsError: true, Content: fmt.Sprintf("unknown action %q (want define|call|list|get|delete)", args.Action)}, nil
	}
}

func (t *DefineTool) doDefine(name, description string, schema json.RawMessage, steps []macroStep) (Result, error) {
	if !defineToolNameRe.MatchString(name) {
		return Result{IsError: true, Content: fmt.Sprintf("invalid tool name %q: must match %s", name, defineToolNameRe.String())}, nil
	}
	if len(steps) < 1 || len(steps) > defineToolMaxSteps {
		return Result{IsError: true, Content: fmt.Sprintf("steps must be 1..%d, got %d", defineToolMaxSteps, len(steps))}, nil
	}
	// Schema: optional but must be an object; cap properties.
	var schemaDoc struct {
		Type       string                 `json:"type"`
		Properties map[string]interface{} `json:"properties"`
		Required   []string               `json:"required"`
	}
	if len(schema) > 0 {
		if err := json.Unmarshal(schema, &schemaDoc); err != nil {
			return Result{IsError: true, Content: fmt.Sprintf("params_schema must be a JSON Schema object: %v", err)}, nil
		}
		if len(schemaDoc.Properties) > defineToolMaxParams {
			return Result{IsError: true, Content: fmt.Sprintf("params_schema may declare at most %d properties, got %d", defineToolMaxParams, len(schemaDoc.Properties))}, nil
		}
		for _, req := range schemaDoc.Required {
			if _, ok := schemaDoc.Properties[req]; !ok {
				return Result{IsError: true, Content: fmt.Sprintf("params_schema required param %q has no property definition", req)}, nil
			}
		}
	}
	for i, s := range steps {
		if _, ok := readOnlyToolNames[s.Tool]; !ok {
			return Result{IsError: true, Content: fmt.Sprintf("step %d: tool %q is not in the read-only set - defined tools may only compose read-only tools so write operations keep their normal per-call approval flow", i+1, s.Tool)}, nil
		}
		if len(s.Args) > 0 && !jsonIsObject(s.Args) {
			return Result{IsError: true, Content: fmt.Sprintf("step %d: args must be a JSON object", i+1)}, nil
		}
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	store, err := t.loadLocked()
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("load tool store: %v", err)}, nil
	}
	if _, exists := store.Entries[name]; !exists && len(store.Entries) >= defineToolMaxEntries {
		return Result{IsError: true, Content: fmt.Sprintf("tool store full (%d/%d): delete an unused definition first", len(store.Entries), defineToolMaxEntries)}, nil
	}
	now := time.Now().UTC()
	if old, ok := store.Entries[name]; ok {
		now = old.CreatedAt // preserve original creation on overwrite
	}
	store.Entries[name] = &definedToolDef{
		Name: name, Description: description,
		ParamsSchema: schema, Steps: steps,
		CreatedAt: now, UpdatedAt: time.Now().UTC(),
	}
	if err := t.persistLocked(store); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("persist tool store: %v", err)}, nil
	}
	params := len(schemaDoc.Properties)
	return Result{Content: fmt.Sprintf("defined tool %q (%d param(s), %d step(s)) - call it with action=call", name, params, len(steps))}, nil
}

func (t *DefineTool) doCall(ctx context.Context, name string, params map[string]interface{}) (Result, error) {
	if t.Registry == nil {
		return Result{IsError: true, Content: "tool registry not available"}, nil
	}
	t.mu.Lock()
	store, err := t.loadLocked()
	var def *definedToolDef
	if err == nil {
		def = store.Entries[name]
	}
	t.mu.Unlock()
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("load tool store: %v", err)}, nil
	}
	if def == nil {
		return Result{IsError: true, Content: fmt.Sprintf("defined tool %q not found (action=list to see names)", name)}, nil
	}
	// Validate required params, then reject unknown params (typo guard).
	var schemaDoc struct {
		Properties map[string]interface{} `json:"properties"`
		Required   []string               `json:"required"`
	}
	if len(def.ParamsSchema) > 0 {
		_ = json.Unmarshal(def.ParamsSchema, &schemaDoc)
	}
	for _, req := range schemaDoc.Required {
		if _, ok := params[req]; !ok {
			return Result{IsError: true, Content: fmt.Sprintf("missing required param %q", req)}, nil
		}
	}
	for k := range params {
		if _, ok := schemaDoc.Properties[k]; !ok {
			return Result{IsError: true, Content: fmt.Sprintf("unknown param %q (declared: %s)", k, declaredParamNames(schemaDoc.Properties))}, nil
		}
	}

	var b strings.Builder
	total := 0
	for i, s := range def.Steps {
		resolved, err := substituteParamPlaceholders(s.Args, params)
		if err != nil {
			return Result{IsError: true, Content: fmt.Sprintf("step %d: %v", i+1, err)}, nil
		}
		sub, ok := t.Registry.Get(s.Tool)
		if !ok {
			return Result{IsError: true, Content: fmt.Sprintf("step %d: tool %q not found in registry", i+1, s.Tool)}, nil
		}
		out, err := sub.Execute(ctx, resolved)
		if err != nil {
			return Result{IsError: true, Content: fmt.Sprintf("step %d (%s) failed: %v", i+1, s.Tool, err)}, nil
		}
		stepOut := clampRunes(out.Content, macroStepOutMax)
		fmt.Fprintf(&b, "--- step %d: %s ---\n%s\n", i+1, s.Tool, stepOut)
		total += len(stepOut)
		if out.IsError {
			b.WriteString("(step reported an error; stopping)\n")
			return Result{IsError: true, Content: clampRunes(b.String(), macroTotalOutMax)}, nil
		}
		if total >= macroTotalOutMax {
			b.WriteString("... (output budget reached, later steps skipped)\n")
			break
		}
	}
	return Result{Content: clampRunes(b.String(), macroTotalOutMax)}, nil
}

func (t *DefineTool) doList() (Result, error) {
	t.mu.Lock()
	store, err := t.loadLocked()
	t.mu.Unlock()
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("load tool store: %v", err)}, nil
	}
	if len(store.Entries) == 0 {
		return Result{Content: "no defined tools yet (action=define to create one)"}, nil
	}
	names := make([]string, 0, len(store.Entries))
	for n := range store.Entries {
		names = append(names, n)
	}
	sort.Strings(names)
	var b strings.Builder
	fmt.Fprintf(&b, "%d defined tool(s):\n", len(names))
	for _, n := range names {
		d := store.Entries[n]
		desc := d.Description
		if desc == "" {
			desc = "(no description)"
		}
		var schemaDoc struct {
			Properties map[string]interface{} `json:"properties"`
		}
		_ = json.Unmarshal(d.ParamsSchema, &schemaDoc)
		fmt.Fprintf(&b, "  %s(%s) - %s [%d step(s)]\n", n, strings.Join(sortedKeys(schemaDoc.Properties), ", "), desc, len(d.Steps))
	}
	return Result{Content: b.String()}, nil
}

func (t *DefineTool) doGet(name string) (Result, error) {
	t.mu.Lock()
	store, err := t.loadLocked()
	var def *definedToolDef
	if err == nil {
		def = store.Entries[name]
	}
	t.mu.Unlock()
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("load tool store: %v", err)}, nil
	}
	if def == nil {
		return Result{IsError: true, Content: fmt.Sprintf("defined tool %q not found", name)}, nil
	}
	data, _ := json.MarshalIndent(def, "", "  ")
	return Result{Content: string(data)}, nil
}

func (t *DefineTool) doDelete(name string) (Result, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	store, err := t.loadLocked()
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("load tool store: %v", err)}, nil
	}
	if _, ok := store.Entries[name]; !ok {
		return Result{IsError: true, Content: fmt.Sprintf("defined tool %q not found", name)}, nil
	}
	delete(store.Entries, name)
	if err := t.persistLocked(store); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("persist tool store: %v", err)}, nil
	}
	return Result{Content: fmt.Sprintf("defined tool %q deleted", name)}, nil
}

// substituteParamPlaceholders replaces {{param}} references in any string
// leaf of the args template with the JSON-encoded param value (trimmed of
// the enclosing quotes so the leaf stays a plain string; numeric and
// boolean params keep their literal form). An unknown placeholder name is
// an error - silent leftover {{x}} in a grep pattern wastes a run.
func substituteParamPlaceholders(template json.RawMessage, params map[string]interface{}) (json.RawMessage, error) {
	if len(template) == 0 {
		return json.RawMessage("{}"), nil
	}
	var doc map[string]interface{}
	if err := json.Unmarshal(template, &doc); err != nil {
		return nil, fmt.Errorf("args must be a JSON object: %v", err)
	}
	var walk func(v interface{}) (interface{}, error)
	walk = func(v interface{}) (interface{}, error) {
		switch tv := v.(type) {
		case string:
			var err error
			out := paramPlaceholderRe.ReplaceAllStringFunc(tv, func(m string) string {
				if err != nil {
					return m
				}
				name := paramPlaceholderRe.FindStringSubmatch(m)[1]
				val, ok := params[name]
				if !ok {
					err = fmt.Errorf("placeholder {{%s}} has no matching param", name)
					return m
				}
				esc, e := json.Marshal(val)
				if e != nil {
					err = e
					return m
				}
				return strings.Trim(string(esc), `"`)
			})
			if err != nil {
				return nil, err
			}
			return out, nil
		case map[string]interface{}:
			for k, ev := range tv {
				nv, err := walk(ev)
				if err != nil {
					return nil, err
				}
				tv[k] = nv
			}
			return tv, nil
		case []interface{}:
			for i, ev := range tv {
				nv, err := walk(ev)
				if err != nil {
					return nil, err
				}
				tv[i] = nv
			}
			return tv, nil
		default:
			return v, nil
		}
	}
	if _, err := walk(doc); err != nil {
		return nil, err
	}
	out, err := json.Marshal(doc)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// jsonIsObject reports whether raw decodes to a JSON object.
func jsonIsObject(raw json.RawMessage) bool {
	var doc map[string]interface{}
	return json.Unmarshal(raw, &doc) == nil
}

func declaredParamNames(props map[string]interface{}) string {
	if len(props) == 0 {
		return "none declared - define the tool with a params_schema first"
	}
	return strings.Join(sortedKeys(props), ", ")
}

func sortedKeys(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// ---- Storage helpers (cmd_snippet/macro convention) ----

func (t *DefineTool) storePath() string {
	if t.filePath == "" {
		dir := t.WorkingDir
		if dir == "" {
			dir, _ = os.Getwd()
		}
		t.filePath = filepath.Join(dir, ".ggcode", defineToolsFileName)
	}
	return t.filePath
}

func (t *DefineTool) loadLocked() (*definedToolStore, error) {
	if t.cache != nil {
		return t.cache, nil
	}
	store := &definedToolStore{Entries: map[string]*definedToolDef{}}
	data, err := os.ReadFile(t.storePath())
	if err != nil {
		if os.IsNotExist(err) {
			t.cache = store
			return store, nil
		}
		return nil, err
	}
	if err := json.Unmarshal(data, store); err != nil {
		// Corrupt store: quarantine, start fresh (same fail-open contract
		// as the macro store; never wedge the tool on a bad file).
		_ = os.Rename(t.storePath(), t.storePath()+".corrupt")
		t.cache = &definedToolStore{Entries: map[string]*definedToolDef{}}
		return t.cache, nil
	}
	if store.Entries == nil {
		store.Entries = map[string]*definedToolDef{}
	}
	t.cache = store
	return store, nil
}

func (t *DefineTool) persistLocked(store *definedToolStore) error {
	if err := os.MkdirAll(filepath.Dir(t.storePath()), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(t.storePath(), data, 0o644)
}
