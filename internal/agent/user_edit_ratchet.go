package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
)

// r444 user-edit ratchet: learn from user rewrites, not just errors.
//
// The rule ratchet's only extraction source is tool ERROR output
// (ratchet.go Rule.MatchPattern). But the most common teaching signal a
// user gives - manually editing an agent's output between turns - never
// surfaces as an error: the agent's file was fine, the user just preferred
// it differently. Teach-by-demonstration memory (2025-2026 agent memory
// systems: Claude Code memories, learn-from-edits) treats those rewrites
// as first-class learning data. This observer closes that gap.
//
// Mechanism (deliberately conservative, zero LLM):
//   - NoteAgentWrite records (path, mtime) after every successful
//     edit_file/write_file/multi_edit_file call.
//   - CheckTurnBoundary runs when the next user message arrives: any
//     tracked file whose mtime moved since the agent's write was changed
//     by someone other than this agent's tool loop (the user, an IDE, an
//     external formatter) during the idle window. That is one observation.
//   - A file needs userEditPromoteTurns INDEPENDENT turn-gap observations
//     before a rule is created - one-off tweaks (experiments, fmt runs the
//     user did by hand) must not be learned. This mirrors the ratchet's
//     staleRuleMinHits=3 philosophy: repetition is the truth filter.
//   - Promoted rules go through the normal AddRule path, so semantic
//     dedup (>=0.75 similarity merge), the 60-slot LRU, staleness
//     recycling and TopRulesForPrompt injection all apply unchanged.
//
// False-positive surface: an external command the user runs (gofmt, sed)
// is indistinguishable from a hand edit at mtime granularity. That is
// acceptable in both effect and direction: the promoted rule only says
// "the user adjusts this file; re-read before editing" - harmless advice
// in either case, and it still requires cross-turn repetition to fire.

const (
	// userEditPromoteTurns: independent turn-gap observations required.
	userEditPromoteTurns = 2
	// userEditObsTTL: forget pending observations after this idle span.
	userEditObsTTL = 14 * 24 * time.Hour
	// userEditMaxTracked: cap files tracked per turn (flood guard).
	userEditMaxTracked = 40
)

// ruleSourceUserEdit marks rules learned from user rewrites. The zero
// value ("" / "error") keeps pre-r444 agent-rules.json backward
// compatible - existing files load unchanged.
const ruleSourceUserEdit = "user_edit"

type userEditObservation struct {
	turns    int
	lastSeen time.Time
}

// UserEditObserver tracks agent-written files across turn gaps and
// promotes repeated user rewrites into ratchet rules.
type UserEditObserver struct {
	mu      sync.Mutex
	store   *RuleStore
	wrote   map[string]time.Time // file -> mtime right after agent's write (this turn)
	pending map[string]*userEditObservation
	// negHits tracks undo signals per file (r447 negative channel).
	negHits map[string]*userEditObservation
}

func newUserEditObserver(store *RuleStore) *UserEditObserver {
	return &UserEditObserver{
		store:   store,
		wrote:   make(map[string]time.Time),
		pending: make(map[string]*userEditObservation),
		negHits: make(map[string]*userEditObservation),
	}
}

// userWriteTools: the tools whose successful calls count as "the agent
// authored this file this turn". file_ops moves are tracked too because a
// move makes the destination agent-authored (#3214: only action=="move"
// destinations count - mkdir creates no file content, delete has no
// destination, and recursive moves fan out per-operation destination).
var userWriteTools = map[string]bool{
	"edit_file":       true,
	"multi_edit_file": true,
	"write_file":      true,
	"multi_file_edit": true,
	"file_ops":        true,
}

// NoteAgentWrite records a file the agent just wrote successfully. mtime
// is sampled once here; unknown paths (deletions racing us) are skipped.
func (o *UserEditObserver) NoteAgentWrite(path string) {
	if o == nil || o.store == nil || path == "" {
		return
	}
	mt, err := mtimeOf(path)
	if err != nil {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if len(o.wrote) >= userEditMaxTracked {
		if _, ok := o.wrote[path]; !ok {
			return // flood guard: only track re-writes of known files
		}
	}
	o.wrote[path] = mt
}

// negSignalRecycle: undo signals required to retire a promoted user-edit
// rule. Mirrors userEditPromoteTurns - repetition is the truth filter in
// both directions (r447).
const negSignalRecycle = 2

// NoteNegativeSignal (r447): the user undid the agent's edit to path.
// One signal cancels any pending positive observation for that file - the
// "user rewrites this file" reading was a misread; the user was rejecting
// the edit, not adjusting it. At negSignalRecycle signals an
// already-promoted user_edit rule for the file is retired: repeated undos
// mean the rule's advice is pointing the wrong direction.
func (o *UserEditObserver) NoteNegativeSignal(path string) {
	if o == nil || o.store == nil || path == "" {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	delete(o.pending, path)
	delete(o.wrote, path)
	neg := o.negHits[path]
	if neg == nil {
		neg = &userEditObservation{}
		o.negHits[path] = neg
	}
	neg.turns++
	neg.lastSeen = time.Now()
	if neg.turns >= negSignalRecycle {
		if n := o.store.RemoveUserEditRules(filepath.Base(path)); n > 0 {
			debug.Log("userEditRatchet", "retired %d user-edit rule(s) for %s after %d undo signals", n, filepath.Base(path), neg.turns)
		}
		delete(o.negHits, path)
	}
}

// CheckTurnBoundary is called when a new user message arrives. Files the
// agent wrote last turn whose mtime has since moved were rewritten
// externally during the idle gap; each is one observation. Files observed
// userEditPromoteTurns times (across distinct gaps) are promoted into a
// ratchet rule via the normal AddRule path.
func (o *UserEditObserver) CheckTurnBoundary() {
	if o == nil || o.store == nil {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()

	now := time.Now()
	var promoted []string
	for path, wroteAt := range o.wrote {
		mt, err := mtimeOf(path)
		if err != nil || mt.Equal(wroteAt) {
			continue // gone or untouched during the gap
		}
		obs := o.pending[path]
		if obs == nil {
			obs = &userEditObservation{}
			o.pending[path] = obs
		}
		obs.turns++
		obs.lastSeen = now
		if obs.turns >= userEditPromoteTurns {
			o.store.AddRule(userEditRule(path, obs.turns))
			delete(o.pending, path)
			promoted = append(promoted, filepath.Base(path))
		}
	}
	// New turn: the write map restarts; stale pending observations decay.
	o.wrote = make(map[string]time.Time)
	for path, obs := range o.pending {
		if now.Sub(obs.lastSeen) > userEditObsTTL {
			delete(o.pending, path)
		}
	}
	for path, obs := range o.negHits {
		if now.Sub(obs.lastSeen) > userEditObsTTL {
			delete(o.negHits, path)
		}
	}
	if len(promoted) > 0 {
		debug.Log("userEditRatchet", "promoted %d user-edit rule(s): %v", len(promoted), promoted)
	}
}

// userEditRule builds the ratchet rule for a repeatedly-rewritten file.
// ToolPattern matches the basename so the rule injects when the agent is
// about to touch that file again.
func userEditRule(path string, turns int) Rule {
	base := filepath.Base(path)
	return Rule{
		Category:    "convention",
		Rule:        "The user manually adjusts agent output in " + base + " (observed " + strconv.Itoa(turns) + " times across turns): re-read the file and match the user's current version before editing it.",
		ToolPattern: regexp.QuoteMeta(base),
		FixHint:     "Read " + base + " first; diff your plan against the user's edits.",
		Source:      ruleSourceUserEdit,
	}
}

// getUserEditObserver lazily builds the observer on the agent's rule store
// (nil-safe when there is no working dir / rule store).
func (a *Agent) getUserEditObserver() *UserEditObserver {
	if a.userEditObs != nil {
		return a.userEditObs
	}
	o := newUserEditObserver(a.getRuleStore())
	a.userEditObs = o
	return o
}

// extractUserEditPaths pulls every agent-authored file path out of a
// successful write-tool call's JSON arguments (raw JSON bytes). Returns
// the paths in argument order; an empty slice means nothing to track.
//
// #3214: file_ops move operations contribute their destination (a move
// makes the destination agent-authored); other file_ops actions (mkdir/
// delete) contribute nothing. multi_file_edit now returns ALL files'
// paths - the per-file observer is fanned out per path, closing the
// "deliberately deferred" gap the single-path API used to have.
func extractUserEditPaths(toolName string, rawArgs []byte) []string {
	var parsed struct {
		FilePath string `json:"file_path"`
		Path     string `json:"path"`
		Files    []struct {
			Path string `json:"path"`
		} `json:"files"`
		Edits []struct {
			Path string `json:"path"`
		} `json:"edits"`
		Operations []struct {
			Action      string `json:"action"`
			Destination string `json:"destination"`
		} `json:"operations"`
	}
	if err := json.Unmarshal(rawArgs, &parsed); err != nil {
		return nil
	}
	var out []string
	add := func(p string) {
		if p != "" {
			out = append(out, p)
		}
	}
	switch toolName {
	case "file_ops":
		for _, op := range parsed.Operations {
			if op.Action == "move" {
				add(op.Destination)
			}
		}
		return out
	case "multi_file_edit":
		for _, f := range parsed.Files {
			add(f.Path)
		}
		return out
	}
	// Single-file tools: first populated field wins (legacy semantics).
	add(parsed.FilePath)
	add(parsed.Path)
	if len(out) == 0 && len(parsed.Files) > 0 {
		add(parsed.Files[0].Path)
	}
	if len(out) == 0 && len(parsed.Edits) > 0 {
		add(parsed.Edits[0].Path)
	}
	return out
}

// mtimeOf isolates os.Stat for testability.
var mtimeOf = func(path string) (time.Time, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return time.Time{}, err
	}
	return fi.ModTime(), nil
}
