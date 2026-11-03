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

type Invariant struct {
	ID           string   `json:"id"`
	OnTools      []string `json:"on_tools"`
	Op           string   `json:"op,omitempty"`
	PathGlob     string   `json:"path_glob,omitempty"`
	CreatedByRun *bool    `json:"created_by_run,omitempty"`
	Mode         string   `json:"mode"`
	Message      string   `json:"message,omitempty"`
}

type Violation struct {
	Inv    Invariant
	Target string
	Op     string
}

type invariantEngine struct {
	mu         sync.RWMutex
	invariants []Invariant
	loaded     bool
	loadDir    string

	runProducts sync.Map
}

const invariantFileName = "invariants.json"

type invariantFile struct {
	Invariants []Invariant `json:"invariants"`
}

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
			continue
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
			byID[inv.ID] = inv
		}
	}
	for _, inv := range byID {
		e.invariants = append(e.invariants, inv)
	}
}

func userGGCodeDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".ggcode")
}

func (e *invariantEngine) check(toolName string, args json.RawMessage) *Violation {
	e.loadInvariants()
	e.mu.RLock()
	defer e.mu.RUnlock()
	if len(e.invariants) == 0 {
		return nil
	}
	var warnHit *Violation
	for _, ot := range invariantOpTargets(toolName, args) {
		for i := range e.invariants {
			inv := e.invariants[i]
			if !invariantToolMatches(inv.OnTools, toolName) {
				continue
			}
			if inv.Op != "" && inv.Op != ot.Op {
				continue
			}
			if inv.PathGlob != "" && !invariantGlobMatch(inv.PathGlob, ot.Target) {
				continue
			}
			if inv.CreatedByRun != nil {
				_, created := e.runProducts.Load(absInvariantPath(ot.Target))

				if *inv.CreatedByRun == created {
					continue
				}
			}
			v := &Violation{Inv: inv, Target: ot.Target, Op: ot.Op}
			if inv.Mode == "block" {
				return v
			}
			if warnHit == nil {
				warnHit = v
			}
		}
	}
	return warnHit
}

func (e *invariantEngine) recordProduct(path string) {
	if path == "" {
		return
	}
	e.runProducts.Store(absInvariantPath(path), struct{}{})
}

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

type invariantOpTarget struct {
	Op     string
	Target string
}

func invariantOpTargets(name string, args json.RawMessage) []invariantOpTarget {

	switch name {
	case "multi_file_write":
		var a struct {
			Files []struct {
				Path string `json:"path"`
			} `json:"files"`
		}
		if json.Unmarshal(args, &a) == nil && len(a.Files) > 0 {
			out := make([]invariantOpTarget, 0, len(a.Files))
			for _, f := range a.Files {
				out = append(out, invariantOpTarget{Op: "write", Target: f.Path})
			}
			return out
		}
	case "batch_replace":
		var a struct {
			Files []string `json:"files"`
		}
		if json.Unmarshal(args, &a) == nil && len(a.Files) > 0 {
			out := make([]invariantOpTarget, 0, len(a.Files))
			for _, p := range a.Files {
				out = append(out, invariantOpTarget{Op: "write", Target: p})
			}
			return out
		}
	}
	if name != "file_ops" {
		return []invariantOpTarget{{Op: invariantOpOf(name, args), Target: invariantTargetPath(name, args)}}
	}
	var a struct {
		Operations []struct {
			Action      string `json:"action"`
			Source      string `json:"source"`
			Destination string `json:"destination"`
		} `json:"operations"`
	}
	if json.Unmarshal(args, &a) != nil {
		return []invariantOpTarget{{Op: "", Target: ""}}
	}
	var out []invariantOpTarget
	for _, op := range a.Operations {
		switch strings.ToLower(op.Action) {
		case "delete":
			out = append(out, invariantOpTarget{Op: "delete", Target: op.Source})
		case "mkdir":
			out = append(out, invariantOpTarget{Op: "mkdir", Target: op.Source})
		case "move":
			out = append(out, invariantOpTarget{Op: "move", Target: op.Destination})
		}
	}
	return out
}

func invariantOpOf(name string, args json.RawMessage) string {
	switch name {
	case "write_file", "edit_file", "multi_edit_file", "multi_file_write", "batch_replace", "notebook_edit":
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
	case "notebook_edit":
		return str("notebook_path")
	case "multi_file_write":
		var a struct {
			Files []struct {
				Path string `json:"path"`
			} `json:"files"`
		}
		if json.Unmarshal(args, &a) == nil && len(a.Files) > 0 {
			return a.Files[0].Path
		}
		return ""
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
				return op.Destination
			}
		}
		return ""
	default:
		return ""
	}
}

func invariantGlobMatch(pattern, path string) bool {
	if pattern == "" || path == "" {
		return false
	}
	segs := strings.Split(filepath.ToSlash(pattern), "/")
	var b strings.Builder
	b.WriteString("^(?:.*/)?")
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
