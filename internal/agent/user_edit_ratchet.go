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

const (
	userEditPromoteTurns = 2
	userEditObsTTL       = 14 * 24 * time.Hour
	userEditMaxTracked   = 40
)

const ruleSourceUserEdit = "user_edit"

type userEditObservation struct {
	turns    int
	lastSeen time.Time
}

type UserEditObserver struct {
	mu      sync.Mutex
	store   *RuleStore
	wrote   map[string]time.Time
	pending map[string]*userEditObservation
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

var userWriteTools = map[string]bool{
	"edit_file":       true,
	"multi_edit_file": true,
	"write_file":      true,
	"multi_file_edit": true,
	"file_ops":        true,
}

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
			return
		}
	}
	o.wrote[path] = mt
}

const negSignalRecycle = 2

func (o *UserEditObserver) NoteAgentUndo(path string) {
	if o == nil || o.store == nil || path == "" {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	delete(o.wrote, path)
}

func (o *UserEditObserver) RestampBaseline() {
	if o == nil || o.store == nil {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	for path := range o.wrote {
		mt, err := mtimeOf(path)
		if err != nil {
			continue
		}
		o.wrote[path] = mt
	}
}

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
			continue
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

func (a *Agent) getUserEditObserver() *UserEditObserver {
	a.mu.RLock()
	o := a.userEditObs
	a.mu.RUnlock()
	if o != nil {
		return o
	}
	rs := a.getRuleStore()
	o = newUserEditObserver(rs)
	a.mu.Lock()
	if a.userEditObs == nil {
		a.userEditObs = o
		a.mu.Unlock()
		return o
	}
	existing := a.userEditObs
	a.mu.Unlock()
	return existing
}

func (a *Agent) RecordUserUndo(path string) {
	a.getUserEditObserver().NoteNegativeSignal(path)
}

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

var mtimeOf = func(path string) (time.Time, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return time.Time{}, err
	}
	return fi.ModTime(), nil
}
