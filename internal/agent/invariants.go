package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/topcheer/ggcode/internal/debug"
)

// r454: Declarative behavior invariants (AgentSpec-style, arXiv 2505.04447).
//
// Frontier claim: users should be able to DECLARE behavior invariants
// ("never delete files outside internal/", "only delete files this run
// created") in a file, and the runtime enforces them with DETERMINISTIC
// assertions at the tool-call choke point - distinct from prompt-level
// constraints (probabilistic, e.g. constraint_amnesia.go) and from
// hard-coded detectors (not user-declarable, e.g. permission/zz_* pins).
//
// Everything here is opt-in: with no invariants file (the default) the
// engine is inert and behavior is byte-for-byte unchanged.

// Invariant is one declared rule. A tool call VIOLATES the invariant when
// the tool matches OnTools (empty = all) AND every set predicate matches
// (Op, PathGlob, CreatedByRun). Predicates combine conjunctively so a
// rule stays simple to reason about.
type Invariant struct {
	ID           string   `json:"id"`
	OnTools      []string `json:"on_tools"`                 // tool names; supports trailing * globs; empty = all tools
	Op           string   `json:"op,omitempty"`             // delete|write|mkdir|move|exec; empty = any
	PathGlob     string   `json:"path_glob,omitempty"`      // target path must match (supports **); empty = any target
	CreatedByRun *bool    `json:"created_by_run,omitempty"` // target must (true) / must not (false) be a product of this run
	Mode         string   `json:"mode"`                     // warn | block (default warn)
	Message      string   `json:"message,omitempty"`
}

// Violation is one detected breach.
type Violation struct {
	Inv    Invariant
	Target string // best-effort path the rule evaluated against
	Op     string
}

// invariantEngine holds the merged rule set and the run-product registry.
// Lazily built on first use (nil-safe via checkInvariants wrapper).
type invariantEngine struct {
	mu         sync.RWMutex
	invariants []Invariant
	loaded     bool
	loadDir    string // dir whose .ggcode/invariants.json to read

	// runProducts: absolute paths created/written by tool calls in THIS
	// agent run (feeds the created_by_run predicate).
	runProducts sync.Map
}

// invariantFileName is looked up as <dir>/.ggcode/invariants.json.
const invariantFileName = "invariants.json"

// invariantFileDoc is the user-facing contract, returned by the loader's
// error path when the file exists but does not parse.
type invariantFile struct {
	Invariants []Invariant `json:"invariants"`
}

// loadInvariants merges ~/.ggcode/invariants.json (user scope) with
// <dir>/.ggcode/invariants.json (project scope); same-ID project rules
// override user rules. Parse failure degrades to the OTHER scope with a
// debug log - a broken user file must not brick the project rules and
// vice versa. Unknown modes are normalized to warn (fail-open: the
// framework is a guardrail, not a jail).
func (e *invariantEngine) loadInvariants() {
	if e.loaded {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.loaded {
		return
	}
	e.loaded = true
	byID := map[string]Invariant{}
	for _, scope := range []struct{ dir, label string }{
		{userGGCodeDir(), "user"},
		{e.loadDir, "project"},
	} {
		if scope.dir == "" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(scope.dir, invariantFileName))
		if err != nil {
			continue // absent = no rules from this scope
		}
		var f invariantFile
		if err := json.Unmarshal(data, &f); err != nil {
			debug.Log("agent", "[invariants] %s scope %s failed to parse: %v", scope.label, invariantFileName, err)
			continue
		}
		for _, inv := range f.Invariants {
			if inv.ID == "" {
				continue
			}
			switch strings.ToLower(inv.Mode) {
			case "block":
				inv.Mode = "block"
			default:
				inv.Mode = "warn"
			}
			inv.Op = strings.ToLower(strings.TrimSpace(inv.Op))
			byID[inv.ID] = inv // project scope runs last: overrides user
		}
	}
	for _, inv := range byID {
		e.invariants = append(e.invariants, inv)
	}
}

// userGGCodeDir returns ~/.ggcode (best-effort; empty on failure).
func userGGCodeDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".ggcode")
}

// check evaluates all invariants against one tool call. Returns the FIRST
// violation (block-mode rules are checked first so the strongest action
// wins) or nil.
func (e *invariantEngine) check(toolName string, args json.RawMessage) *Violation {
	e.loadInvariants()
	e.mu.RLock()
	defer e.mu.RUnlock()
	if len(e.invariants) == 0 {
		return nil
	}
	op := invariantOpOf(toolName, args)
	target := invariantTargetPath(toolName, args)
	var warnHit *Violation
	for i := range e.invariants {
		inv := e.invariants[i]
		if !invariantToolMatches(inv.OnTools, toolName) {
			continue
		}
		if inv.Op != "" && inv.Op != op {
			continue
		}
		if inv.PathGlob != "" && !invariantGlobMatch(inv.PathGlob, target) {
			continue
		}
		if inv.CreatedByRun != nil {
			_, created := e.runProducts.Load(absInvariantPath(target))
			// created_by_run is a REQUIREMENT predicate, not a conjunctive
			// match: the invariant holds when the target's created-state
			// equals the declared requirement. A mismatch IS the violation.
			if *inv.CreatedByRun == created {
				continue // requirement satisfied, no violation
			}
		}
		v := &Violation{Inv: inv, Target: target, Op: op}
		if inv.Mode == "block" {
			return v
		}
		if warnHit == nil {
			warnHit = v
		}
	}
	return warnHit
}

// recordProduct registers a path as a product of this run (called after a
// successful write-class tool call).
func (e *invariantEngine) recordProduct(path string) {
	if path == "" {
		return
	}
	e.runProducts.Store(absInvariantPath(path), struct{}{})
}

// absInvariantPath normalizes for registry lookups.
func absInvariantPath(p string) string {
	if p == "" {
		return ""
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return filepath.Clean(p)
	}
	return abs
}

// invariantToolMatches: empty list = all tools; entries support a
// leading or trailing "*" (e.g. "file_*", "*_file", "multi_*").
func invariantToolMatches(on []string, name string) bool {
	if len(on) == 0 {
		return true
	}
	for _, pat := range on {
		if pat == name {
			return true
		}
		if strings.HasSuffix(pat, "*") && strings.HasPrefix(name, strings.TrimSuffix(pat, "*")) {
			return true
		}
		if strings.HasPrefix(pat, "*") && strings.HasSuffix(name, strings.TrimPrefix(pat, "*")) {
			return true
		}
	}
	return false
}

// invariantOpOf classifies the operation class of a tool call:
// delete | mkdir | move | write | exec | "" (non-mutating).
func invariantOpOf(name string, args json.RawMessage) string {
	switch name {
	case "write_file", "edit_file", "multi_edit_file", "multi_file_write", "batch_replace":
		return "write"
	case "run_command", "start_command", "write_command_input", "desktop_control":
		return "exec"
	case "file_ops":
		var a struct {
			Operations []struct {
				Action string `json:"action"`
			} `json:"operations"`
		}
		if json.Unmarshal(args, &a) == nil {
			for _, op := range a.Operations {
				switch strings.ToLower(op.Action) {
				case "delete":
					return "delete"
				case "mkdir":
					return "mkdir"
				case "move":
					return "move"
				}
			}
		}
		return ""
	default:
		return ""
	}
}

// invariantTargetPath extracts the primary target path from well-known
// write/delete tools (best-effort; empty when none).
func invariantTargetPath(name string, args json.RawMessage) string {
	if len(args) == 0 {
		return ""
	}
	str := func(key string) string {
		var a map[string]json.RawMessage
		if json.Unmarshal(args, &a) != nil {
			return ""
		}
		raw, ok := a[key]
		if !ok {
			return ""
		}
		var s string
		if json.Unmarshal(raw, &s) == nil {
			return s
		}
		return ""
	}
	switch name {
	case "write_file", "edit_file", "multi_edit_file":
		return str("file_path")
	case "batch_replace":
		var a struct {
			Files []string `json:"files"`
		}
		if json.Unmarshal(args, &a) == nil && len(a.Files) > 0 {
			return a.Files[0]
		}
		return ""
	case "file_ops":
		var a struct {
			Operations []struct {
				Action      string `json:"action"`
				Source      string `json:"source"`
				Destination string `json:"destination"`
			} `json:"operations"`
		}
		if json.Unmarshal(args, &a) != nil {
			return ""
		}
		for _, op := range a.Operations {
			switch strings.ToLower(op.Action) {
			case "delete":
				return op.Source
			case "mkdir":
				return op.Destination
			case "move":
				return op.Destination // moving INTO a path is the mutation
			}
		}
		return ""
	default:
		return ""
	}
}

// invariantGlobMatch matches pattern against path with ** support:
// "**" crosses directory boundaries (a trailing "**" also matches the
// empty remainder), plain segments match one level, and a bare pattern
// also matches the base name (so ".env" hits any ".env" in the tree,
// matching user intent).
func invariantGlobMatch(pattern, path string) bool {
	if pattern == "" || path == "" {
		return false
	}
	segs := strings.Split(filepath.ToSlash(pattern), "/")
	var b strings.Builder
	b.WriteString("^(?:.*/)?") // any pattern may be relative to any root
	for i, seg := range segs {
		last := i == len(segs)-1
		if seg == "**" {
			if last {
				b.WriteString(".*")
			} else {
				b.WriteString(`(?:.*/)?`)
			}
			continue
		}
		for _, ch := range seg {
			switch ch {
			case '*':
				b.WriteString("[^/]*")
			case '?':
				b.WriteString("[^/]")
			default:
				b.WriteString(regexp.QuoteMeta(string(ch)))
			}
		}
		if !last {
			b.WriteString("/")
		}
	}
	b.WriteString("$")
	re, err := regexp.Compile(b.String())
	if err != nil {
		return false
	}
	p := filepath.ToSlash(path)
	return re.MatchString(p) || re.MatchString(filepath.Base(p))
}
