package tool

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

var defineToolNameRe = regexp.MustCompile(`^[a-z][a-z0-9_-]{2,40}$`)

var paramPlaceholderRe = regexp.MustCompile(`\{\{\s*([a-zA-Z_][a-zA-Z0-9_]*)\s*\}\}`)

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

type DefineTool struct {
	WorkingDir string
	Registry   *Registry

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
		// #P0 step-output refs: {{steps[N].key}} may only look backward, so a
		// definition can never contain a forward reference or a cycle.
		for _, m := range stepOutRefRe.FindAllStringSubmatch(string(s.Args), -1) {
			refIdx := -1
			fmt.Sscanf(m[1], "%d", &refIdx)
			if refIdx < 0 || refIdx >= i {
				return Result{IsError: true, Content: fmt.Sprintf("step %d: step reference {{steps[%s].%s}} must point to an earlier step (0..%d) - forward/self references are rejected at define time", i+1, m[1], m[2], i-1)}, nil
			}
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
		now = old.CreatedAt
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
	var schemaDoc struct {
		Properties map[string]interface{} `json:"properties"`
		Required   []string               `json:"required"`
	}
	if len(def.ParamsSchema) > 0 {

		if err := json.Unmarshal(def.ParamsSchema, &schemaDoc); err != nil {
			return Result{IsError: true, Content: fmt.Sprintf("tool %q has an invalid params schema (corrupted definition?): %v", def.Name, err)}, nil
		}
	}
	for _, req := range schemaDoc.Required {

		v, ok := params[req]
		if !ok || v == nil {
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
	stepOutputs := make([]map[string]interface{}, len(def.Steps)) // captured per step for {{steps[N].key}} refs
	for i, s := range def.Steps {

		if _, ok := readOnlyToolNames[s.Tool]; !ok {
			return Result{IsError: true, Content: fmt.Sprintf("step %d: tool %q is not read-only. This definition was not created via the define action (the on-disk store allows arbitrary content) - refusing to execute a write-capable nested step without the normal per-call approval flow.", i+1, s.Tool)}, nil
		}
		resolved, err := resolveStepRefs(s.Args, stepOutputs, i)
		if err != nil {
			return Result{IsError: true, Content: fmt.Sprintf("step %d: %v", i+1, err)}, nil
		}
		resolved, err = substituteParamPlaceholders(resolved, params)
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
		stepOutputs[i] = captureStepOutput(out.Content, stepOut)
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

				if s, ok := val.(string); ok {
					return s
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

// stepOutRefRe matches backward step-output references: {{steps[0].path}}.
// Validated at define time (backward-only) and resolved at call time.
var stepOutRefRe = regexp.MustCompile(`\{\{\s*steps\[(\d+)\]\.([a-zA-Z0-9_.]+)\s*\}\}`)

// captureStepOutput stores one step's output for later {{steps[N].key}}
// lookup: "raw" keeps the clamped text, JSON-object outputs additionally
// expose their top-level (and nested, via dotted paths) keys.
func captureStepOutput(rawContent, clamped string) map[string]interface{} {
	m := map[string]interface{}{"raw": clamped}
	var obj map[string]interface{}
	if err := json.Unmarshal([]byte(rawContent), &obj); err == nil && obj != nil {
		for k, v := range obj {
			m[k] = v
		}
	}
	return m
}

// lookupStepKey resolves a dotted key path ("items.0.name") against a step
// output map; maps are traversed by string key, slices by decimal index.
func lookupStepKey(out map[string]interface{}, key string) (interface{}, bool) {
	cur := interface{}(out)
	for _, part := range strings.Split(key, ".") {
		switch node := cur.(type) {
		case map[string]interface{}:
			var ok bool
			cur, ok = node[part]
			if !ok {
				return nil, false
			}
		case []interface{}:
			idx := -1
			fmt.Sscanf(part, "%d", &idx)
			if idx < 0 || idx >= len(node) {
				return nil, false
			}
			cur = node[idx]
		default:
			return nil, false
		}
	}
	return cur, true
}

// resolveStepRefs replaces {{steps[N].key}} placeholders in a step's args
// with outputs captured from earlier steps. N must be < curStep (define
// time already rejects forward/self references; this is the belt to the
// suspenders).
func resolveStepRefs(args json.RawMessage, stepOutputs []map[string]interface{}, curStep int) (json.RawMessage, error) {
	s := string(args)
	if !strings.Contains(s, "{{steps[") {
		return args, nil
	}
	resolved := stepOutRefRe.ReplaceAllStringFunc(s, func(match string) string {
		m := stepOutRefRe.FindStringSubmatch(match)
		refIdx := -1
		fmt.Sscanf(m[1], "%d", &refIdx)
		if refIdx < 0 || refIdx >= curStep || refIdx >= len(stepOutputs) || stepOutputs[refIdx] == nil {
			return match // leave marker; flagged below
		}
		v, ok := lookupStepKey(stepOutputs[refIdx], m[2])
		if !ok {
			return match
		}
		switch val := v.(type) {
		case string:
			return val
		default:
			b, _ := json.Marshal(val)
			return string(b)
		}
	})
	if strings.Contains(resolved, "{{steps[") {
		return nil, fmt.Errorf("unresolvable step reference(s): outputs from earlier steps do not contain the referenced key (or the reference does not point backward)")
	}
	return json.RawMessage(resolved), nil
}
