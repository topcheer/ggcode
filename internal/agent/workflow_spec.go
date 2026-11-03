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

type WorkflowStep struct {
	ID           string   `json:"id"`
	ArtifactGlob string   `json:"artifact_glob,omitempty"`
	OnCommands   []string `json:"on_commands,omitempty"`
	Requires     []string `json:"requires,omitempty"`
	Mode         string   `json:"mode"`
	Message      string   `json:"message,omitempty"`
}

type WorkflowViolation struct {
	Step            WorkflowStep
	Missing         string
	WantGlob        string
	Command         string
	Attempts        []StepAttempt
	LastAttempt     *StepAttempt
	AttemptedEver   bool
	UnknownRequires bool
}

type workflowSpecFileDoc struct {
	Steps []WorkflowStep `json:"steps"`
}

type workflowEngine struct {
	mu            sync.RWMutex
	steps         map[string]WorkflowStep
	order         []string
	loaded        bool
	loadDir       string
	startedAt     time.Time
	completed     sync.Map
	unknownReq    map[string][]string
	traceMu       sync.Mutex
	trace         []StepAttempt
	attemptCounts map[string]int
	auditFired    bool
}

const workflowSpecFileName = "workflow-spec.json"

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
			continue
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
			e.steps[st.ID] = st
		}
	}

	e.unknownReq = nil
	for _, st := range e.steps {
		for _, req := range st.Requires {
			if _, ok := e.steps[req]; ok {
				continue
			}
			if e.unknownReq == nil {
				e.unknownReq = make(map[string][]string)
			}
			e.unknownReq[st.ID] = append(e.unknownReq[st.ID], req)
		}
	}
}

func (e *workflowEngine) isComplete(id string) bool {
	if id == "" {
		return false
	}
	_, ok := e.completed.Load(id)
	return ok
}

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

const wfClockSkew = 2 * time.Second

func (e *workflowEngine) globFreshOnDisk(pattern string) bool {
	if pattern == "" || e.startedAt.IsZero() {

		return false
	}

	base := filepath.Dir(e.loadDir)
	found := false
	_ = filepath.WalkDir(base, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if d != nil && d.IsDir() {
				return fs.SkipDir
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

var benignReportVerbs = map[string]bool{
	"echo": true, "printf": true, "true": true, "false": true, ":": true, "#": true,
}

func commandMatches(patterns []string, cmd string) bool {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return false
	}
	segments := splitCompoundCommand(cmd)
	if len(segments) == 0 {
		segments = []string{cmd}
	}
	for _, p := range patterns {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if p == "*" {
			return true
		}
		for _, segRaw := range segments {
			seg := strings.TrimSpace(stripEnvAssignments(segRaw))
			if seg == "" {
				continue
			}
			switch {
			case strings.HasPrefix(p, "*") && strings.HasSuffix(p, "*"):

				if benignReportVerbs[strings.Fields(seg)[0]] {
					continue
				}
				core := strings.Trim(p, "*")
				for _, f := range strings.Fields(seg) {
					if f == core {
						return true
					}
				}
			case strings.HasPrefix(p, "*"):
				if strings.HasSuffix(seg, strings.TrimPrefix(p, "*")) {
					return true
				}
			case strings.HasSuffix(p, "*"):
				if strings.HasPrefix(seg, strings.TrimSuffix(p, "*")) {
					return true
				}
			default:
				if seg == p {
					return true
				}
			}
		}
	}
	return false
}

func (e *workflowEngine) checkPreconditions(toolName string, args json.RawMessage) *WorkflowViolation {
	if !isCommandExecTool(toolName) {
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

			if e.groundIfFresh(req) {
				continue
			}
			want := ""
			_, knownReq := e.steps[req]
			if rs, ok := e.steps[req]; ok {
				want = rs.ArtifactGlob
			}
			v := &WorkflowViolation{Step: st, Missing: req, WantGlob: want, Command: command, UnknownRequires: !knownReq}
			if att := e.recentAttempts(req, wfTraceMaxPerViolation); len(att) > 0 {
				v.Attempts = att
				last := att[len(att)-1]
				v.LastAttempt = &last
			}
			v.AttemptedEver = e.stepAttemptedEver(req)
			if st.Mode == "block" {
				return v
			}
			if warnHit == nil {
				warnHit = v
			}
			break
		}
	}
	return warnHit
}

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

func (e *workflowEngine) outstandingMessageOnce() string {
	if e == nil {
		return ""
	}
	m := e.outstandingMessage()
	if m == "" {
		return ""
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.auditFired {
		return ""
	}
	e.auditFired = true
	return m
}

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
