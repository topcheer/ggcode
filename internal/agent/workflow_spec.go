package agent

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
)

// r26 (Lean4Agent-inspired, arXiv:2606.06523): stateful per-workflow
// declaration on top of the stateless per-call invariants engine (r454).
//
// Frontier claim: agents need machine-checkable WORKFLOW specifications -
// step ordering (preconditions: "release requires tests-passed"), artifact
// grounding (which file(s) prove a step happened) and end-of-run
// postcondition audits - so "I finished, steps 1-4 done" can be checked
// against declared artifacts instead of trusted. The invariants engine
// (r454) already gives stateless per-call rules; this adds the missing
// stateful layer: WHICH step a call belongs to and whether its declared
// prerequisites produced their artifacts this run.
//
// Like invariants, everything is opt-in: with no workflow-spec.json (the
// default) the engine is inert and behavior is byte-for-byte unchanged.
// Theorem-prover-grade formal verification (Lean/TLA+) stays BACKLOG by
// design - this is the lightweight Go-native subset.

// WorkflowStep is one declared step. A step is COMPLETE this run when a
// tool call produced a path matching ArtifactGlob (grounded completion -
// no "trust me" completion flag). A step with OnCommands additionally
// guards commands: executing a matching command before every step in
// Requires is complete is a violation (block or warn).
type WorkflowStep struct {
	ID           string   `json:"id"`
	ArtifactGlob string   `json:"artifact_glob,omitempty"` // product(s) proving completion
	OnCommands   []string `json:"on_commands,omitempty"`   // command globs (prefix*/suffix*/*infix*) this step guards; empty = artifact-only node
	Requires     []string `json:"requires,omitempty"`      // step IDs that must be complete first
	Mode         string   `json:"mode"`                    // warn | block (default block: step ordering is usually a hard contract)
	Message      string   `json:"message,omitempty"`
}

// WorkflowViolation reports a precondition breach with the counterexample
// step (the Lean4Agent "debuggable violation" property: not just "denied"
// but WHICH prerequisite is missing and what would prove it).
type WorkflowViolation struct {
	Step     WorkflowStep
	Missing  string // unmet prerequisite step ID
	WantGlob string // artifact that would have proven it
	Command  string // the guarded command that triggered the check
	// sa-85 structural attribution: recent attempts (outcome + artifact
	// grounding) of the MISSING step's guarded commands, so the rejection can
	// say where execution broke (never ran / failed / no artifact).
	Attempts    []StepAttempt
	LastAttempt *StepAttempt
}

// workflowSpecFileDoc is the user-facing contract (mirrors invariantFile).
type workflowSpecFileDoc struct {
	Steps []WorkflowStep `json:"steps"`
}

// workflowEngine holds merged steps + the run's completed-step registry.
// Lazily built on first use (nil-safe via wfEngineLazy).
type workflowEngine struct {
	mu        sync.RWMutex
	steps     map[string]WorkflowStep
	order     []string // declaration order for deterministic iteration
	loaded    bool
	loadDir   string    // dir whose .ggcode/workflow-spec.json to read
	startedAt time.Time // #3414: freshness anchor for on-disk artifact probing
	completed sync.Map  // step ID -> struct{} (grounded by artifact this run)
	// sa-85 failure ledger: outcomes of executed guarded commands, kept on a
	// dedicated lock (never taken while holding traceMu) so attribution reads
	// stay safe inside checkPreconditions' e.mu critical section.
	traceMu sync.Mutex
	trace   []StepAttempt
}

const workflowSpecFileName = "workflow-spec.json"

// loadWorkflowSpec merges ~/.ggcode/workflow-spec.json (user scope) with
// <dir>/.ggcode/workflow-spec.json (project scope); same-ID project steps
// override user steps. Parse failure degrades to the other scope (same
// fail-open policy as invariants - a guardrail, not a jail).
func (e *workflowEngine) loadWorkflowSpec() {
	if e.loaded {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.loaded {
		return
	}
	e.loaded = true
	e.steps = map[string]WorkflowStep{}
	for _, scope := range []struct{ dir, label string }{
		{userGGCodeDir(), "user"},
		{e.loadDir, "project"},
	} {
		if scope.dir == "" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(scope.dir, workflowSpecFileName))
		if err != nil {
			continue // absent = no steps from this scope
		}
		var f workflowSpecFileDoc
		if err := json.Unmarshal(data, &f); err != nil {
			debug.Log("agent", "[workflow-spec] %s scope %s failed to parse: %v", scope.label, workflowSpecFileName, err)
			continue
		}
		for _, st := range f.Steps {
			if st.ID == "" {
				continue
			}
			switch strings.ToLower(st.Mode) {
			case "warn":
				st.Mode = "warn"
			default:
				st.Mode = "block"
			}
			if _, dup := e.steps[st.ID]; !dup {
				e.order = append(e.order, st.ID)
			}
			e.steps[st.ID] = st // project scope runs last: overrides user
		}
	}
}

// isComplete reports whether a step's artifacts were produced this run.
func (e *workflowEngine) isComplete(id string) bool {
	if id == "" {
		return false
	}
	_, ok := e.completed.Load(id)
	return ok
}

// recordCompletion grounds a step as complete when a produced path matches
// its artifact glob (called with every write-class product of this run).
func (e *workflowEngine) recordCompletion(path string) {
	if path == "" {
		return
	}
	e.loadWorkflowSpec()
	e.mu.RLock()
	defer e.mu.RUnlock()
	for id, st := range e.steps {
		if _, done := e.completed.Load(id); done {
			continue
		}
		if st.ArtifactGlob != "" && invariantGlobMatch(st.ArtifactGlob, path) {
			e.completed.Store(id, struct{}{})
			debug.Log("agent", "[workflow-spec] step %s complete (artifact %q)", id, path)
		}
	}
}

// wfClockSkew is the wall-clock tolerance applied when comparing artifact
// mtime against the engine anchor. Both timestamps come from CLOCK_REALTIME
// (no monotonic component), and CI runners / NTP-adjusted hosts can step the
// clock backwards between `time.Now()` (anchor) and the kernel stamping the
// file write, making a genuinely fresh artifact look stale - which deadlocks
// block mode on a step that actually completed (observed twice consecutively
// on GitHub Actions runners, PR #3490). A 2s grace window absorbs that skew
// while keeping pre-run artifacts (minutes old) correctly stale.
const wfClockSkew = 2 * time.Second

// globFreshOnDisk reports whether pattern resolves to at least one file
// on disk whose mtime is at or after the engine's freshness anchor (#3414),
// modulo the wfClockSkew wall-clock tolerance.
// Patterns are interpreted relative to the working dir (the parent of
// loadDir); absolute patterns are used as-is.
func (e *workflowEngine) globFreshOnDisk(pattern string) bool {
	if pattern == "" || e.startedAt.IsZero() {
		// Zero anchor (engine built outside workflowEngineLazy, e.g. tests):
		// freshness is unprovable - conservatively do not ground.
		return false
	}
	// #3425: pattern language MUST be invariantGlobMatch - the same one the
	// write path (recordCompletion) uses. The previous filepath.Glob probe
	// had no ** superglob: "**/coverage.out" matched neither a top-level
	// coverage.out nor a/b/coverage.out, so exec-produced artifacts with
	// superglob specs never grounded and the block safety net (groundIfFresh)
	// failed the same way - both guards dead at once. Walk the working dir
	// and match RELATIVE paths, exactly the shape invariantGlobMatch expects.
	// Only .git is pruned (write-path products inside .git are covered by
	// recordCompletion; exec artifacts do not materialize there).
	base := filepath.Dir(e.loadDir) // == working dir
	found := false
	_ = filepath.WalkDir(base, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if d != nil && d.IsDir() {
				return fs.SkipDir // unreadable subtree: skip, keep scanning
			}
			return nil
		}
		if found {
			return fs.SkipAll
		}
		if d.IsDir() {
			if p != base && d.Name() == ".git" {
				return fs.SkipDir
			}
			return nil
		}
		rel, relErr := filepath.Rel(base, p)
		// #3780 A: absolute patterns are used AS-IS (the comment contract
		// above and the write path's recordCompletion match absolute tool
		// paths directly). The old code always matched against the RELATIVE
		// path, so an absolute artifact_glob compiled to a leading-"/"
		// literal regex that a relative path can never satisfy - probe and
		// groundIfFresh silently dead for absolute specs, and block mode
		// rejected already-completed steps.
		cand := filepath.ToSlash(rel)
		if filepath.IsAbs(pattern) {
			cand = filepath.ToSlash(p)
		}
		if relErr != nil || !invariantGlobMatch(pattern, cand) {
			return nil
		}
		info, infoErr := d.Info()
		if infoErr == nil && !info.IsDir() && !info.ModTime().Before(e.startedAt.Add(-wfClockSkew)) {
			found = true
			return fs.SkipAll
		}
		return nil
	})
	return found
}

// probeArtifactsOnDisk grounds every incomplete step whose artifact_glob
// now matches a file produced since the engine anchor (#3414). Called
// after successful run_command calls: exec products (go test
// -coverprofile=..., make bin/*) never flow through invariantOpTargets,
// so without this probe the flagship spec shape deadlocked block mode on
// steps that had actually completed. Grounding still requires a REAL,
// FRESH artifact on disk - no trust-me completion flag.
func (e *workflowEngine) probeArtifactsOnDisk() {
	e.loadWorkflowSpec()
	e.mu.RLock()
	defer e.mu.RUnlock()
	for _, id := range e.order {
		st := e.steps[id]
		if st.ArtifactGlob == "" {
			continue
		}
		if _, done := e.completed.Load(id); done {
			continue
		}
		if e.globFreshOnDisk(st.ArtifactGlob) {
			e.completed.Store(id, struct{}{})
			debug.Log("agent", "[workflow-spec] step %s complete (artifact %q observed on disk after command)", id, st.ArtifactGlob)
		}
	}
}

// groundIfFresh is the targeted, block-path self-heal (#3414 option 3 as
// a safety net): before a run_command is rejected for an unmet
// prerequisite, the engine re-checks the disk for that step's artifact.
// Returns true when the requirement got grounded by the check.
func (e *workflowEngine) groundIfFresh(id string) bool {
	if id == "" {
		return false
	}
	if _, done := e.completed.Load(id); done {
		return true
	}
	st, ok := e.steps[id]
	if !ok || st.ArtifactGlob == "" {
		return false
	}
	if !e.globFreshOnDisk(st.ArtifactGlob) {
		return false
	}
	e.completed.Store(id, struct{}{})
	debug.Log("agent", "[workflow-spec] step %s complete (artifact %q observed on disk during precondition check)", id, st.ArtifactGlob)
	return true
}

// commandMatches reports whether cmd matches any OnCommands glob of the
// step. Globs use the same semantics as invariant tool matching: a leading
// or trailing "*" (prefix/suffix match); a bare "*" matches everything.
func commandMatches(patterns []string, cmd string) bool {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return false
	}
	for _, p := range patterns {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if p == "*" {
			return true
		}
		switch {
		case strings.HasPrefix(p, "*") && strings.HasSuffix(p, "*"):
			if strings.Contains(cmd, strings.Trim(p, "*")) {
				return true
			}
		case strings.HasPrefix(p, "*"):
			if strings.HasSuffix(cmd, strings.TrimPrefix(p, "*")) {
				return true
			}
		case strings.HasSuffix(p, "*"):
			if strings.HasPrefix(cmd, strings.TrimSuffix(p, "*")) {
				return true
			}
		default:
			if cmd == p {
				return true
			}
		}
	}
	return false
}

// checkPreconditions evaluates a run_command call: when it matches a
// guarded step's OnCommands, every Requires step must already be complete.
// Returns the FIRST block-mode violation (strongest action wins), else the
// first warn-mode one - the counterexample names the unmet prerequisite
// and the artifact that would prove it (debuggable violation, not a bare
// denial).
func (e *workflowEngine) checkPreconditions(toolName string, args json.RawMessage) *WorkflowViolation {
	if toolName != "run_command" {
		return nil
	}
	e.loadWorkflowSpec()
	e.mu.RLock()
	defer e.mu.RUnlock()
	if len(e.steps) == 0 {
		return nil
	}
	command, _ := parseRunCommandArgs(args)
	if command == "" {
		return nil
	}
	var warnHit *WorkflowViolation
	for _, id := range e.order {
		st := e.steps[id]
		if !commandMatches(st.OnCommands, command) {
			continue
		}
		for _, req := range st.Requires {
			if e.isComplete(req) {
				continue
			}
			// #3414 safety net: before rejecting, re-check the disk for
			// the prerequisite's artifact - a successful command may have
			// produced it without a write-class tool call (exec products
			// never flow through invariantOpTargets). Fresh file on disk
			// grounds the requirement instead of blocking.
			if e.groundIfFresh(req) {
				continue
			}
			want := ""
			if rs, ok := e.steps[req]; ok {
				want = rs.ArtifactGlob
			}
			v := &WorkflowViolation{Step: st, Missing: req, WantGlob: want, Command: command}
			if att := e.recentAttempts(req, wfTraceMaxPerViolation); len(att) > 0 {
				v.Attempts = att
				last := att[len(att)-1]
				v.LastAttempt = &last
			}
			if st.Mode == "block" {
				return v
			}
			if warnHit == nil {
				warnHit = v
			}
			break // first unmet prerequisite is the counterexample
		}
	}
	return warnHit
}

// outstandingSteps lists declared steps not yet grounded by artifacts this
// run (end-of-run postcondition audit input).
func (e *workflowEngine) outstandingSteps() []WorkflowStep {
	e.loadWorkflowSpec()
	e.mu.RLock()
	defer e.mu.RUnlock()
	var out []WorkflowStep
	for _, id := range e.order {
		st := e.steps[id]
		if st.ArtifactGlob != "" && !e.isComplete(id) {
			out = append(out, st)
		}
	}
	return out
}

// outstandingMessage renders the end-of-run audit: which declared steps
// have no grounded artifacts yet. Empty when the spec is absent or fully
// complete (inert by default).
func (e *workflowEngine) outstandingMessage() string {
	if e == nil {
		return ""
	}
	out := e.outstandingSteps()
	if len(out) == 0 {
		return ""
	}
	ids := make([]string, len(out))
	for i, st := range out {
		ids[i] = st.ID + " (expects " + st.ArtifactGlob + ")"
	}
	return fmt.Sprintf("[workflow-spec] Declared steps with no produced artifacts yet this run: %s. If you are about to declare the task complete, verify these steps actually happened or state which ones were skipped.", strings.Join(ids, "; "))
}

// workflowEngineLazy mirrors invariantEngineLazy: anchored to the agent's
// working dir, nil when inert (no working dir).
func (a *Agent) workflowEngineLazy() *workflowEngine {
	a.mu.RLock()
	e := a.wfEngine
	wd := a.workingDir
	a.mu.RUnlock()
	if e != nil {
		return e
	}
	if wd == "" {
		return nil
	}
	newE := &workflowEngine{loadDir: filepath.Join(wd, ".ggcode"), startedAt: time.Now()}
	a.mu.Lock()
	if a.wfEngine == nil {
		a.wfEngine = newE
	}
	a.mu.Unlock()
	return a.wfEngine
}
